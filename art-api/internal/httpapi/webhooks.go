package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/google/uuid"
)

var webhookEventTypes = []string{"*", events.UserUpdated, events.UserDisabled, events.SessionCreated, events.SessionRevoked, events.SessionRevokedAll, events.ACLUpdated, events.StrategyUpdated, events.UserGroupMembershipUpdated, events.RelayUpdated, events.DeviceUpdated, events.AuditRecorded}

type webhookRequest struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
}

func normalizeWebhook(input *webhookRequest) bool {
	input.Name, input.URL = strings.TrimSpace(input.Name), strings.TrimSpace(input.URL)
	if len(input.Name) < 2 || len(input.Name) > 128 || len(input.URL) > 2048 || len(input.Events) < 1 || len(input.Events) > len(webhookEventTypes) {
		return false
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(input.Events))
	for _, eventType := range input.Events {
		eventType = strings.TrimSpace(eventType)
		if !slices.Contains(webhookEventTypes, eventType) {
			return false
		}
		if !seen[eventType] {
			seen[eventType] = true
			normalized = append(normalized, eventType)
		}
	}
	slices.Sort(normalized)
	input.Events = normalized
	return true
}

func (s *Server) listWebhooks(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListWebhooks(request.Context())
	if err != nil {
		writeError(response, 500, "webhooks unavailable")
		return
	}
	writeJSON(response, 200, map[string]any{"webhooks": values, "event_types": webhookEventTypes})
}

func (s *Server) createWebhook(response http.ResponseWriter, request *http.Request) {
	if s.webhooks == nil {
		writeError(response, 503, "webhooks unavailable")
		return
	}
	var input webhookRequest
	if decodeJSON(request, &input, 16<<10) != nil || !normalizeWebhook(&input) || s.webhooks.ValidateURL(input.URL) != nil {
		writeError(response, 400, "invalid webhook")
		return
	}
	now := time.Now().UTC()
	value := domain.Webhook{ID: uuid.NewString(), Name: input.Name, URL: input.URL, Events: input.Events, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateWebhook(request.Context(), value); err != nil {
		writeError(response, 500, "cannot create webhook")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "webhook_created", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"webhook_id": value.ID, "name": value.Name}})
	writeJSON(response, 201, map[string]any{"webhook": value, "secret": s.webhooks.Secret(value.ID)})
}

func (s *Server) updateWebhook(response http.ResponseWriter, request *http.Request) {
	if s.webhooks == nil {
		writeError(response, 503, "webhooks unavailable")
		return
	}
	existing, err := s.repository.FindWebhookByID(request.Context(), request.PathValue("webhookID"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "webhook not found")
		return
	} else if err != nil {
		writeError(response, 500, "webhook unavailable")
		return
	}
	var input webhookRequest
	if decodeJSON(request, &input, 16<<10) != nil || !normalizeWebhook(&input) || s.webhooks.ValidateURL(input.URL) != nil {
		writeError(response, 400, "invalid webhook")
		return
	}
	existing.Name, existing.URL, existing.Events, existing.Enabled, existing.UpdatedAt = input.Name, input.URL, input.Events, input.Enabled, time.Now().UTC()
	if err = s.repository.UpdateWebhook(request.Context(), existing); err != nil {
		writeError(response, 500, "cannot update webhook")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "webhook_updated", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"webhook_id": existing.ID}})
	writeJSON(response, 200, existing)
}

func (s *Server) deleteWebhook(response http.ResponseWriter, request *http.Request) {
	if err := s.repository.DeleteWebhook(request.Context(), request.PathValue("webhookID")); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "webhook not found")
		return
	} else if err != nil {
		writeError(response, 500, "cannot delete webhook")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "webhook_deleted", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"webhook_id": request.PathValue("webhookID")}})
	response.WriteHeader(204)
}

func (s *Server) listWebhookDeliveries(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListWebhookDeliveries(request.Context(), request.PathValue("webhookID"), 100)
	if err != nil {
		writeError(response, 500, "deliveries unavailable")
		return
	}
	writeJSON(response, 200, values)
}
