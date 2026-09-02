package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const dashboardPreferenceKey = "console.dashboard.layout"

var dashboardWidgetIDs = []string{"online-devices", "offline-devices", "users", "users-online", "active-connections", "rendezvous-peers", "relay-load", "diagnostics", "hbbs-sync", "status-strip"}

type dashboardLayout struct {
	Version int      `json:"version"`
	Order   []string `json:"order"`
	Hidden  []string `json:"hidden"`
}

func defaultDashboardLayout() dashboardLayout {
	return dashboardLayout{Version: 1, Order: append([]string(nil), dashboardWidgetIDs...), Hidden: []string{}}
}

func normalizeDashboardLayout(input dashboardLayout) (dashboardLayout, bool) {
	known, seen := map[string]bool{}, map[string]bool{}
	for _, id := range dashboardWidgetIDs {
		known[id] = true
	}
	result := dashboardLayout{Version: 1, Hidden: []string{}}
	for _, id := range input.Order {
		if known[id] && !seen[id] {
			result.Order = append(result.Order, id)
			seen[id] = true
		}
	}
	for _, id := range dashboardWidgetIDs {
		if !seen[id] {
			result.Order = append(result.Order, id)
		}
	}
	hidden := map[string]bool{}
	for _, id := range input.Hidden {
		if known[id] && !hidden[id] {
			result.Hidden = append(result.Hidden, id)
			hidden[id] = true
		}
	}
	return result, len(result.Hidden) < len(dashboardWidgetIDs)
}

func (s *Server) getDashboardLayout(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	raw, err := s.repository.GetUserPreference(request.Context(), principal.User.ID, dashboardPreferenceKey)
	if errors.Is(err, domain.ErrNotFound) {
		writeJSON(response, http.StatusOK, defaultDashboardLayout())
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "dashboard preferences unavailable")
		return
	}
	var layout dashboardLayout
	if json.Unmarshal([]byte(raw), &layout) != nil {
		writeJSON(response, http.StatusOK, defaultDashboardLayout())
		return
	}
	layout, _ = normalizeDashboardLayout(layout)
	writeJSON(response, http.StatusOK, layout)
}

func (s *Server) putDashboardLayout(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input dashboardLayout
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid dashboard layout")
		return
	}
	layout, valid := normalizeDashboardLayout(input)
	if !valid {
		writeError(response, http.StatusBadRequest, "at least one dashboard widget must remain visible")
		return
	}
	raw, _ := json.Marshal(layout)
	if err := s.repository.UpsertUserPreference(request.Context(), principal.User.ID, dashboardPreferenceKey, string(raw), time.Now().UTC()); err != nil {
		writeError(response, http.StatusInternalServerError, "dashboard preferences unavailable")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "dashboard_layout_update", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	writeJSON(response, http.StatusOK, layout)
}
