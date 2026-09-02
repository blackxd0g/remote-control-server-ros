package httpapi

import (
	"context"
	"net/http"
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
