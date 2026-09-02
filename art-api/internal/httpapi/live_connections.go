package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/connections"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaycontrol"
)

func (s *Server) liveConnections(response http.ResponseWriter, request *http.Request) {
	from := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if repository, ok := s.repository.(interface {
		ListConnectionRecords(context.Context, time.Time, int) ([]domain.ConnectionRecord, error)
	}); ok {
		records, err := repository.ListConnectionRecords(request.Context(), from, 5000)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "connection telemetry unavailable")
			return
		}
		writeJSON(response, http.StatusOK, connections.BuildRecords(records, time.Now().UTC()))
		return
	}
	repository, ok := s.repository.(interface {
		QueryAudit(context.Context, domain.AuditQuery) (domain.AuditPage, error)
	})
	if !ok {
		writeError(response, http.StatusNotImplemented, "connection telemetry unavailable")
		return
	}
	events := make([]domain.AuditEvent, 0, 1500)
	for _, eventType := range []string{"connection_started", "connection_updated", "connection_closed"} {
		page, err := repository.QueryAudit(request.Context(), domain.AuditQuery{Type: eventType, From: &from, Limit: 500})
		if err != nil {
			writeError(response, http.StatusInternalServerError, "connection telemetry unavailable")
			return
		}
		events = append(events, page.Events...)
	}
	writeJSON(response, http.StatusOK, connections.Build(events, time.Now().UTC()))
}

func (s *Server) relayConnectionState(response http.ResponseWriter, request *http.Request) {
	var input struct {
		UUID  string `json:"uuid"`
		State string `json:"state"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid relay connection state")
		return
	}
	input.UUID, input.State = strings.TrimSpace(input.UUID), strings.TrimSpace(input.State)
	if len(input.UUID) < 8 || len(input.UUID) > 128 || (input.State != "active" && input.State != "closed") {
		writeError(response, http.StatusBadRequest, "invalid relay connection state")
		return
	}
	repository, ok := s.repository.(interface {
		ConnectionRecord(context.Context, string) (domain.ConnectionRecord, error)
		UpsertConnection(context.Context, domain.ConnectionRecord) error
	})
	if !ok {
		writeError(response, http.StatusNotImplemented, "connection telemetry unavailable")
		return
	}
	record, err := repository.ConnectionRecord(request.Context(), "relay:"+input.UUID)
	if errors.Is(err, domain.ErrNotFound) {
		now := time.Now().UTC()
		record = domain.ConnectionRecord{Key: "relay:" + input.UUID, Transport: "relay", RelayUUID: input.UUID, StartedAt: now, LastSeenAt: now}
		err = nil
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "connection telemetry unavailable")
		return
	}
	now := time.Now().UTC()
	record.LastSeenAt = now
	if input.State == "closed" {
		record.ClosedAt = &now
	}
	if err = repository.UpsertConnection(request.Context(), record); err != nil {
		writeError(response, http.StatusInternalServerError, "connection telemetry unavailable")
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"status": input.State})
}

func (s *Server) containLiveConnection(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Key string `json:"key"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid connection")
		return
	}
	input.Key = strings.TrimSpace(input.Key)
	if input.Key == "" || len(input.Key) > 512 {
		writeError(response, http.StatusBadRequest, "invalid connection")
		return
	}
	repository, ok := s.repository.(interface {
		ConnectionRecord(context.Context, string) (domain.ConnectionRecord, error)
		UpsertConnection(context.Context, domain.ConnectionRecord) error
	})
	if !ok {
		writeError(response, http.StatusNotImplemented, "connection control unavailable")
		return
	}
	record, err := repository.ConnectionRecord(request.Context(), input.Key)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusNotFound, "connection not found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "connection control unavailable")
		return
	}
	if record.ClosedAt != nil {
		writeJSON(response, http.StatusOK, map[string]any{"status": "already_closed", "connection": record})
		return
	}
	if record.ActorSessionID == "" {
		writeError(response, http.StatusConflict, "connection has no attributable server session")
		return
	}
	transportInterrupted := false
	transportStatus := "not_available"
	if record.Transport == "relay" && record.RelayUUID != "" && record.RelayServer != "" && s.relayControl != nil {
		terminateContext, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		terminateErr := s.relayControl.Terminate(terminateContext, record.RelayServer, record.RelayUUID)
		cancel()
		switch {
		case terminateErr == nil:
			transportInterrupted, transportStatus = true, "terminated"
		case errors.Is(terminateErr, relaycontrol.ErrNotFound):
			transportStatus = "not_found"
		default:
			transportStatus = "unconfirmed"
		}
	}
	if err = s.auth.RevokeSession(request.Context(), record.ActorSessionID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "session revoke failed")
		return
	}
	now := time.Now().UTC()
	record.LastSeenAt, record.ClosedAt = now, &now
	if err = repository.UpsertConnection(request.Context(), record); err != nil {
		writeError(response, http.StatusInternalServerError, "connection state update failed")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "connection_contained", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, ControllerDevice: record.ControllerDevice, TargetRustDeskID: record.TargetRustDeskID,
		Result: "success", Metadata: map[string]any{"connection_key": record.Key, "revoked_session_id": record.ActorSessionID,
			"effect": "new_connections_blocked", "transport_interrupt": transportStatus, "relay_uuid": record.RelayUUID,
			"relay_server": record.RelayServer}})
	writeJSON(response, http.StatusOK, map[string]any{
		"status": "contained", "session_revoked": true, "new_connections_blocked": true,
		"transport_interrupted": transportInterrupted, "transport_status": transportStatus, "connection": record,
	})
}
