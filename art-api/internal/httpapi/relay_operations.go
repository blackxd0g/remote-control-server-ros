package httpapi

import (
	"context"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"net/http"
)

type relayOperationsRepository interface {
	RelayDeliveryCounts(context.Context, string) (relaygateway.DeliveryCounts, error)
	CompactRelayDelivery(context.Context, string) (int, error)
}

func (s *Server) relayDeliveryStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("relayID")
	if _, err := s.relayByID(r.Context(), id); err != nil {
		writeError(w, 404, "relay not found")
		return
	}
	repository, ok := s.repository.(relayOperationsRepository)
	if !ok {
		writeError(w, 503, "relay operations unavailable")
		return
	}
	counts, err := repository.RelayDeliveryCounts(r.Context(), id)
	if err != nil {
		writeError(w, 503, "relay history unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"runtime": s.relayGateway.DeliveryObservation(id), "history": counts, "retention_days": 30, "batch_limit": 1000})
}
func (s *Server) compactRelayDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("relayID")
	if _, err := s.relayByID(r.Context(), id); err != nil {
		writeError(w, 404, "relay not found")
		return
	}
	repository, ok := s.repository.(relayOperationsRepository)
	if !ok {
		writeError(w, 503, "relay operations unavailable")
		return
	}
	n, err := repository.CompactRelayDelivery(r.Context(), id)
	if err != nil {
		writeError(w, 503, "relay history compaction failed")
		return
	}
	principal, _ := principalFrom(r.Context())
	_ = s.audit.Record(r.Context(), domain.AuditEvent{Type: "relay_history_compacted", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"relay_id": id, "archived": n}})
	writeJSON(w, 200, map[string]int{"archived": n})
}
