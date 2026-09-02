package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type auditQueryRepository interface {
	QueryAudit(context.Context, domain.AuditQuery) (domain.AuditPage, error)
	AuditSummary(context.Context, domain.AuditQuery) (domain.AuditSummary, error)
}

func (s *Server) queryAudit(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.repository.(auditQueryRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "audit explorer unavailable")
		return
	}
	query, err := parseAuditQuery(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	page, err := repository.QueryAudit(request.Context(), query)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "audit unavailable")
		return
	}
	writeJSON(response, http.StatusOK, page)
}

func (s *Server) auditSummary(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.repository.(auditQueryRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "audit explorer unavailable")
		return
	}
	query, err := parseAuditQuery(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	summary, err := repository.AuditSummary(request.Context(), query)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "audit summary unavailable")
		return
	}
	writeJSON(response, http.StatusOK, summary)
}

func (s *Server) exportAuditCSV(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.repository.(auditQueryRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "audit explorer unavailable")
		return
	}
	query, err := parseAuditQuery(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	response.Header().Set("Content-Type", "text/csv; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="rustdesk-audit.csv"`)
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(response)
	writer.Comma = ';'
	_ = writer.Write([]string{"occurred_at", "type", "actor_user_id", "actor_session_id", "controller_device_id", "target_rustdesk_id", "ip", "result", "reason", "metadata"})
	query.Limit, query.Offset = 500, 0
	for exported := 0; exported < 100_000; {
		page, queryErr := repository.QueryAudit(request.Context(), query)
		if queryErr != nil {
			return
		}
		for _, event := range page.Events {
			metadata, _ := json.Marshal(event.Metadata)
			_ = writer.Write([]string{event.OccurredAt.UTC().Format(timeLayout), spreadsheetSafe(event.Type), spreadsheetSafe(event.ActorUserID), spreadsheetSafe(event.ActorSessionID), spreadsheetSafe(event.ControllerDevice), spreadsheetSafe(event.TargetRustDeskID), spreadsheetSafe(event.IP), spreadsheetSafe(event.Result), spreadsheetSafe(event.Reason), spreadsheetSafe(string(metadata))})
		}
		exported += len(page.Events)
		query.Offset += len(page.Events)
		if len(page.Events) == 0 || int64(query.Offset) >= page.Total {
			break
		}
	}
	writer.Flush()
}

func parseAuditQuery(request *http.Request) (domain.AuditQuery, error) {
	values := request.URL.Query()
	limit, _ := strconv.Atoi(values.Get("limit"))
	offset, _ := strconv.Atoi(values.Get("offset"))
	query := domain.AuditQuery{Limit: limit, Offset: offset, Type: strings.TrimSpace(values.Get("type")), Result: strings.TrimSpace(values.Get("result")), ActorUserID: strings.TrimSpace(values.Get("actor_user_id")), TargetID: strings.TrimSpace(values.Get("target_id")), IP: strings.TrimSpace(values.Get("ip")), Search: strings.TrimSpace(values.Get("search"))}
	for _, value := range []string{query.Type, query.Result, query.ActorUserID, query.TargetID, query.IP} {
		if len(value) > 128 {
			return domain.AuditQuery{}, errors.New("audit filter is too long")
		}
	}
	if len(query.Search) > 256 || offset < 0 || offset > 10_000_000 || limit < 0 || limit > 500 {
		return domain.AuditQuery{}, errors.New("invalid audit query")
	}
	for name, target := range map[string]**time.Time{"from": &query.From, "to": &query.To} {
		if raw := values.Get(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return domain.AuditQuery{}, errors.New("invalid audit date range")
			}
			parsed = parsed.UTC()
			*target = &parsed
		}
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return domain.AuditQuery{}, errors.New("invalid audit date range")
	}
	return query, nil
}
