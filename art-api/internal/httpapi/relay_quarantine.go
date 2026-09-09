package httpapi

import (
	"context"
	"encoding/json"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) relayQuarantine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 || offset > 4096 {
		writeError(w, 400, "invalid offset")
		return
	}
	page, err := s.relayGateway.Quarantine(r.Context(), r.PathValue("relayID"), "list", r.URL.Query().Get("revision"), offset)
	if err != nil {
		writeError(w, 409, "quarantine changed or relay unavailable; refresh")
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) exportRelayQuarantine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	id := r.PathValue("relayID")
	revision := r.URL.Query().Get("revision")
	if _, err := uuid.Parse(revision); err != nil {
		writeError(w, 400, "revision required")
		return
	}
	principal, _ := principalFrom(ctx)
	if err := s.audit.Record(ctx, domain.AuditEvent{Type: "relay_quarantine_export_requested", ActorUserID: principal.User.ID, Result: "requested", Metadata: map[string]any{"relay_id": id, "revision": revision}}); err != nil {
		writeError(w, 503, "audit unavailable")
		return
	}
	events := []relaygateway.QuarantineEvent{}
	total := -1
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			writeError(w, 504, "export timed out")
			return
		case <-ticker.C:
		}
		page, err := s.relayGateway.Quarantine(ctx, id, "list", revision, len(events))
		if err != nil || (total >= 0 && page.Total != total) {
			writeError(w, 409, "quarantine changed or relay unavailable; refresh")
			return
		}
		total = page.Total
		events = append(events, page.Events...)
		if len(events) == total {
			break
		}
	}
	if err := s.audit.Record(ctx, domain.AuditEvent{Type: "relay_quarantine_exported", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"relay_id": id, "revision": revision, "count": total}}); err != nil {
		writeError(w, 503, "audit unavailable")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="relay-quarantine.json"`)
	writeJSON(w, 200, map[string]any{"relay_id": id, "revision": revision, "total": total, "events": events})
}

func (s *Server) archiveRelayQuarantine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Revision string `json:"revision"`
		Confirm  bool   `json:"confirm"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || !input.Confirm {
		writeError(w, 400, "explicit confirmation required")
		return
	}
	if _, err := uuid.Parse(input.Revision); err != nil {
		writeError(w, 400, "invalid revision")
		return
	}
	id := r.PathValue("relayID")
	principal, _ := principalFrom(r.Context())
	operation := uuid.NewString()
	metadata := map[string]any{"relay_id": id, "revision": input.Revision, "operation_id": operation}
	if err := s.audit.Record(r.Context(), domain.AuditEvent{ID: operation, Type: "relay_quarantine_archive_requested", ActorUserID: principal.User.ID, Result: "requested", Metadata: metadata}); err != nil {
		writeError(w, 503, "audit unavailable; command not sent")
		return
	}
	_, err := s.relayGateway.Quarantine(r.Context(), id, "archive", input.Revision, 0)
	result := "success"
	if err != nil {
		result = "unknown"
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	auditErr := s.audit.Record(auditCtx, domain.AuditEvent{Type: "relay_quarantine_archived", ActorUserID: principal.User.ID, Result: result, Metadata: metadata})
	if err != nil {
		writeError(w, 409, "outcome uncertain or quarantine changed; refresh, retry same revision only")
		return
	}
	if auditErr != nil {
		writeError(w, 503, "relay archived; result audit unavailable; request audit retained")
		return
	}
	writeJSON(w, 200, map[string]string{"revision": input.Revision, "backup": "quarantine-" + input.Revision + ".json"})
}
