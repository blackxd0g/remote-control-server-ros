package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type sessionQueryRepository interface {
	QuerySessions(context.Context, domain.SessionQuery) (domain.SessionPage, error)
	SessionSummary(context.Context, time.Time) (domain.SessionSummary, error)
}

func (s *Server) querySessions(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.repository.(sessionQueryRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "session center unavailable")
		return
	}
	query, err := parseSessionQuery(request)
	if err != "" {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	page, queryErr := repository.QuerySessions(request.Context(), query)
	if queryErr != nil {
		writeError(response, http.StatusInternalServerError, "sessions unavailable")
		return
	}
	principal, _ := principalFrom(request.Context())
	for index := range page.Sessions {
		page.Sessions[index].Current = page.Sessions[index].ID == principal.Session.ID
	}
	writeJSON(response, http.StatusOK, page)
}

func (s *Server) sessionSummary(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.repository.(sessionQueryRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "session center unavailable")
		return
	}
	result, err := repository.SessionSummary(request.Context(), time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session summary unavailable")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) bulkRevokeSessions(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input struct {
		IDs []string `json:"ids"`
	}
	if decodeJSON(request, &input, 64<<10) != nil || len(input.IDs) < 1 || len(input.IDs) > 500 {
		writeError(response, http.StatusBadRequest, "invalid session selection")
		return
	}
	seen := map[string]bool{}
	for index, id := range input.IDs {
		id = strings.TrimSpace(id)
		if len(id) > 128 || id == "" || seen[id] || id == principal.Session.ID {
			writeError(response, http.StatusConflict, "current session cannot be revoked by a bulk operation")
			return
		}
		if _, err := s.repository.FindSession(request.Context(), id); err != nil {
			writeError(response, http.StatusNotFound, "session not found")
			return
		}
		seen[id] = true
		input.IDs[index] = id
	}
	for _, id := range input.IDs {
		if err := s.auth.RevokeSession(request.Context(), id); err != nil {
			writeError(response, http.StatusInternalServerError, "session revoke failed")
			return
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "session_bulk_revoke", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"session_ids": input.IDs, "count": len(input.IDs)}})
	writeJSON(response, http.StatusOK, map[string]any{"revoked": len(input.IDs)})
}

func parseSessionQuery(request *http.Request) (domain.SessionQuery, string) {
	values := request.URL.Query()
	limit, _ := strconv.Atoi(values.Get("limit"))
	offset, _ := strconv.Atoi(values.Get("offset"))
	query := domain.SessionQuery{Limit: limit, Offset: offset, Status: strings.TrimSpace(values.Get("status")), UserID: strings.TrimSpace(values.Get("user_id")), Search: strings.TrimSpace(values.Get("search")), Now: time.Now().UTC()}
	if limit < 0 || limit > 500 || offset < 0 || offset > 10_000_000 || len(query.Search) > 256 || len(query.UserID) > 128 {
		return domain.SessionQuery{}, "invalid session query"
	}
	if query.Status != "" && query.Status != "active" && query.Status != "revoked" && query.Status != "expired" {
		return domain.SessionQuery{}, "invalid session status"
	}
	return query, ""
}
