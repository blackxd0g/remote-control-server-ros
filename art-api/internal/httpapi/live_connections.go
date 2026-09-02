package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/connections"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
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
			"effect": "new_connections_blocked", "transport_interrupt": "not_guaranteed"}})
	writeJSON(response, http.StatusOK, map[string]any{
		"status": "contained", "session_revoked": true, "new_connections_blocked": true,
		"transport_interrupted": false, "connection": record,
	})
}
