package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/google/uuid"
)

const maxAttempts = 6

type Service struct {
	repository   domain.Repository
	hub          *events.Hub
	masterSecret []byte
	allowPrivate bool
	client       *http.Client
}

func New(repository domain.Repository, hub *events.Hub, masterSecret []byte, allowPrivate bool) *Service {
	service := &Service{repository: repository, hub: hub, masterSecret: append([]byte(nil), masterSecret...), allowPrivate: allowPrivate}
	service.client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: service.dialContext, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}}
	return service
}

func (s *Service) ValidateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("webhook URL must be an absolute HTTPS URL without credentials")
	}
	if !s.allowPrivate {
		addresses, resolveErr := net.DefaultResolver.LookupIPAddr(context.Background(), parsed.Hostname())
		if resolveErr != nil || len(addresses) == 0 {
			return errors.New("webhook hostname cannot be resolved")
		}
		for _, address := range addresses {
			if isPrivate(address.IP) {
				return errors.New("private and local webhook destinations are disabled")
			}
		}
	}
	return nil
}

func (s *Service) Secret(id string) string {
	mac := hmac.New(sha256.New, s.masterSecret)
	_, _ = mac.Write([]byte("art-webhook-v1:" + id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) Run(ctx context.Context) {
	go s.RunEvents(ctx)
	s.RunDelivery(ctx)
}

func (s *Service) RunEvents(ctx context.Context) {
	channel, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-channel:
			if !ok {
				return
			}
			s.enqueue(ctx, event)
		}
	}
}

func (s *Service) RunDelivery(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.deliverDue(ctx)
		}
	}
}

func (s *Service) enqueue(ctx context.Context, event events.Event) {
	hooks, err := s.repository.ListWebhooks(ctx)
	if err != nil {
		slog.Error("list webhooks", "error", err)
		return
	}
	payload, _ := json.Marshal(event)
	for _, hook := range hooks {
		if !hook.Enabled || (!slices.Contains(hook.Events, "*") && !slices.Contains(hook.Events, event.Type)) {
			continue
		}
		now := time.Now().UTC()
		if err = s.repository.CreateWebhookDelivery(ctx, domain.WebhookDelivery{ID: uuid.NewString(), WebhookID: hook.ID, EventType: event.Type, Payload: string(payload), Status: "pending", NextAttempt: now, CreatedAt: now}); err != nil {
			slog.Error("enqueue webhook", "webhook_id", hook.ID, "error", err)
		}
	}
}

func (s *Service) deliverDue(ctx context.Context) {
	values, err := s.repository.ListDueWebhookDeliveries(ctx, time.Now().UTC(), 25)
	if err != nil {
		slog.Error("list webhook deliveries", "error", err)
		return
	}
	for _, delivery := range values {
		s.deliver(ctx, delivery)
	}
}

func (s *Service) deliver(ctx context.Context, delivery domain.WebhookDelivery) {
	hook, err := s.repository.FindWebhookByID(ctx, delivery.WebhookID)
	if err != nil || !hook.Enabled {
		delivery.Status = "cancelled"
		delivery.LastError = "webhook disabled or removed"
		_ = s.repository.UpdateWebhookDelivery(ctx, delivery)
		return
	}
	if err = s.ValidateURL(hook.URL); err != nil {
		delivery.Status = "failed"
		delivery.LastError = err.Error()
		_ = s.repository.UpdateWebhookDelivery(ctx, delivery)
		return
	}
	delivery.Attempts++
	timestamp := fmt.Sprintf("%d", time.Now().UTC().Unix())
	signed := timestamp + "." + delivery.Payload
	mac := hmac.New(sha256.New, []byte(s.Secret(hook.ID)))
	_, _ = mac.Write([]byte(signed))
	request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewBufferString(delivery.Payload))
	if requestErr == nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "Remote-Control-Server-Webhooks/1")
		request.Header.Set("X-ART-Event", delivery.EventType)
		request.Header.Set("X-ART-Delivery", delivery.ID)
		request.Header.Set("X-ART-Timestamp", timestamp)
		signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		request.Header.Set("X-RDS-Signature", signature)
		request.Header.Set("X-ART-Signature", signature)
	}
	var response *http.Response
	if requestErr == nil {
		response, err = s.client.Do(request)
	} else {
		err = requestErr
	}
	if response != nil {
		delivery.ResponseCode = response.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
	}
	now := time.Now().UTC()
	if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		delivery.Status = "delivered"
		delivery.LastError = ""
		delivery.DeliveredAt = &now
	} else {
		if err != nil {
			delivery.LastError = truncate(err.Error(), 512)
		} else {
			delivery.LastError = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		if delivery.Attempts >= maxAttempts {
			delivery.Status = "failed"
		} else {
			delivery.Status = "pending"
			delivery.NextAttempt = now.Add(time.Duration(1<<min(delivery.Attempts, 8)) * time.Second)
		}
	}
	if err = s.repository.UpdateWebhookDelivery(ctx, delivery); err != nil {
		slog.Error("update webhook delivery", "delivery_id", delivery.ID, "error", err)
	}
}

func (s *Service) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, resolved := range addresses {
		if !s.allowPrivate && isPrivate(resolved.IP) {
			continue
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
	}
	return nil, errors.New("webhook destination is not allowed")
}

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
