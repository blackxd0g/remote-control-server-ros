package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/relaycontrol"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"github.com/google/uuid"
)

func (s *Server) relayByID(ctx context.Context, id string) (domain.RelayServer, error) {
	values, err := s.repository.ListRelayServers(ctx)
	if err != nil {
		return domain.RelayServer{}, err
	}
	for _, v := range values {
		if v.ID == id {
			return v, nil
		}
	}
	return domain.RelayServer{}, domain.ErrNotFound
}
func (s *Server) relayCredentialRepository() (relaygateway.Repository, error) {
	r, ok := s.repository.(relaygateway.Repository)
	if !ok {
		return nil, domain.ErrUnsupported
	}
	return r, nil
}
func (s *Server) checkRelayCredential(ctx context.Context, id, token string) error {
	r, err := s.relayCredentialRepository()
	if err != nil {
		return err
	}
	v, err := s.relayByID(ctx, id)
	if err != nil || !v.Enabled {
		return relaygateway.ErrUnauthorized
	}
	c, err := r.RelayCredential(ctx, id)
	if err != nil || !c.Matches(token) {
		return relaygateway.ErrUnauthorized
	}
	return nil
}
func secureRelayRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) createRelayEnrollment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("relayID")
	relay, err := s.relayByID(r.Context(), id)
	if err != nil || !relay.Enabled {
		writeError(w, 409, "enabled relay required")
		return
	}
	repository, err := s.relayCredentialRepository()
	if err != nil {
		writeError(w, 503, "relay credentials unavailable")
		return
	}
	token, err := relaygateway.NewToken()
	if err != nil {
		writeError(w, 500, "credential generation failed")
		return
	}
	now := time.Now().UTC()
	c := relaygateway.Credential{RelayID: id, EnrollmentHash: relaygateway.Hash(token), EnrollmentExpires: now.Add(10 * time.Minute), Generation: uuid.NewString(), UpdatedAt: now}
	if err = repository.PutRelayEnrollment(r.Context(), c); err != nil {
		writeError(w, 500, "enrollment unavailable")
		return
	}
	s.relayGateway.Disconnect(id)
	principal, _ := principalFrom(r.Context())
	_ = s.audit.Record(r.Context(), domain.AuditEvent{Type: "relay_enrollment_created", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"relay_id": id}})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"relay_id": id, "enrollment_token": token, "expires_at": c.EnrollmentExpires})
}
func (s *Server) enrollRelay(w http.ResponseWriter, r *http.Request) {
	if !secureRelayRequest(r) {
		writeError(w, 400, "HTTPS required")
		return
	}
	var in struct {
		RelayID string `json:"relay_id"`
		Token   string `json:"enrollment_token"`
	}
	if decodeJSON(r, &in, 4096) != nil || len(in.RelayID) > 128 || len(in.Token) != 43 {
		writeError(w, 401, "invalid enrollment")
		return
	}
	repository, err := s.relayCredentialRepository()
	if err != nil {
		writeError(w, 503, "relay credentials unavailable")
		return
	}
	token, err := relaygateway.NewToken()
	if err != nil {
		writeError(w, 500, "credential generation failed")
		return
	}
	if err = repository.ConsumeRelayEnrollment(r.Context(), in.RelayID, relaygateway.Hash(in.Token), relaygateway.Hash(token), time.Now().UTC()); err != nil {
		writeError(w, 401, "invalid enrollment")
		return
	}
	_ = s.audit.Record(r.Context(), domain.AuditEvent{Type: "relay_enrolled", Result: "success", Metadata: map[string]any{"relay_id": in.RelayID}})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]string{"relay_id": in.RelayID, "credential": token})
}
func (s *Server) revokeRelayCredential(w http.ResponseWriter, r *http.Request) {
	repository, err := s.relayCredentialRepository()
	if err != nil {
		writeError(w, 503, "relay credentials unavailable")
		return
	}
	id := r.PathValue("relayID")
	if err = repository.RevokeRelayCredential(r.Context(), id, time.Now().UTC()); err != nil {
		writeError(w, statusForStore(err), "relay credential not found")
		return
	}
	s.relayGateway.Disconnect(id)
	principal, _ := principalFrom(r.Context())
	_ = s.audit.Record(r.Context(), domain.AuditEvent{Type: "relay_credential_revoked", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"relay_id": id}})
	w.WriteHeader(204)
}
func (s *Server) connectRelay(w http.ResponseWriter, r *http.Request) {
	if !secureRelayRequest(r) || r.Header.Get("Origin") != "" {
		writeError(w, 400, "secure non-browser control channel required")
		return
	}
	id := r.PathValue("relayID")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := s.checkRelayCredential(r.Context(), id, token); err != nil {
		writeError(w, 401, "invalid relay credential")
		return
	}
	s.relayGateway.Serve(w, r, id, token)
}

func (s *Server) relayControlState(ctx context.Context, values []domain.RelayServer) {
	repository, err := s.relayCredentialRepository()
	if err != nil {
		return
	}
	for i := range values {
		credential, err := repository.RelayCredential(ctx, values[i].ID)
		if errors.Is(err, domain.ErrNotFound) {
			values[i].ControlMode = "legacy"
			continue
		}
		values[i].ControlMode = "wss"
		values[i].ControlConnected = err == nil && credential.Hash != "" && s.relayGateway.Online(values[i].ID)
		if !values[i].ControlConnected {
			values[i].Health = "offline"
		}
	}
}

func (s *Server) relayCommand(ctx context.Context, address, id, action string) (string, error) {
	repository, err := s.relayCredentialRepository()
	if err != nil {
		return "", err
	}
	values, err := s.repository.ListRelayServers(ctx)
	if err != nil {
		return "", err
	}
	for _, v := range values {
		if net.JoinHostPort(v.Hostname, strconv.Itoa(v.Port)) != address {
			continue
		}
		if !v.Enabled {
			return "", relaygateway.ErrOffline
		}
		_, err := repository.RelayCredential(ctx, v.ID)
		if err == nil {
			return s.relayGateway.Command(ctx, v.ID, action, id)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return "", err
		}
		if action == "permit" {
			return "legacy_sent", s.relayControl.Permit(ctx, address, id)
		}
		err = s.relayControl.Terminate(ctx, address, id)
		if errors.Is(err, relaycontrol.ErrNotFound) {
			return "not_found", nil
		}
		return "terminated", err
	}
	return "", domain.ErrNotFound
}
func (s *Server) permitRelay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Address string `json:"address"`
		UUID    string `json:"uuid"`
	}
	if decodeJSON(r, &in, 4096) != nil || len(in.UUID) < 8 || len(in.UUID) > 128 || len(in.Address) > 300 {
		writeError(w, 400, "invalid relay permit")
		return
	}
	status, err := s.relayCommand(r.Context(), in.Address, in.UUID, "permit")
	if err != nil {
		writeError(w, 503, "relay permit not confirmed")
		return
	}
	writeJSON(w, 200, map[string]string{"status": status})
}

func (s *Server) reportRelay(ctx context.Context, id string, m relaygateway.Message) error {
	relay, err := s.relayByID(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if m.Type == "telemetry" {
		relay.Connections, relay.Bandwidth, relay.UpdatedAt = m.Connections, int64(m.Bandwidth), now
		relay, err = s.repository.UpsertRelayTelemetry(ctx, relay)
		if err != nil {
			return err
		}
		s.hub.Publish(events.RelayUpdated, relay)
		return s.repository.AppendRelayMetric(ctx, domain.RelayMetric{RelayID: id, RecordedAt: now, Health: "healthy", Connections: m.Connections, Bandwidth: int64(m.Bandwidth)})
	}
	repository, ok := s.repository.(interface {
		ConnectionRecord(context.Context, string) (domain.ConnectionRecord, error)
		UpsertConnection(context.Context, domain.ConnectionRecord) error
	})
	if !ok {
		return domain.ErrUnsupported
	}
	record, err := repository.ConnectionRecord(ctx, "relay:"+m.UUID)
	if errors.Is(err, domain.ErrNotFound) {
		record = domain.ConnectionRecord{Key: "relay:" + m.UUID, Transport: "relay", RelayUUID: m.UUID, StartedAt: now}
		err = nil
	}
	if err != nil {
		return err
	}
	address := net.JoinHostPort(relay.Hostname, strconv.Itoa(relay.Port))
	if record.RelayServer != "" && record.RelayServer != address {
		return relaygateway.ErrUnauthorized
	}
	record.RelayServer, record.LastSeenAt = address, now
	if m.Status == "closed" {
		record.ClosedAt = &now
	}
	return repository.UpsertConnection(ctx, record)
}
