package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/google/uuid"
)

type automationRepository interface {
	CreateAutomationRule(context.Context, domain.AutomationRule) error
	ListAutomationRules(context.Context) ([]domain.AutomationRule, error)
	UpdateAutomationRule(context.Context, domain.AutomationRule) error
	DeleteAutomationRule(context.Context, string) error
	ListAutomationRuns(context.Context, string, int) ([]domain.AutomationRun, error)
}

func (s *Server) automationRepository(response http.ResponseWriter) (automationRepository, bool) {
	repository, ok := s.repository.(automationRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "automation unavailable")
	}
	return repository, ok
}

func (s *Server) listAutomationRules(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.automationRepository(response)
	if !ok {
		return
	}
	values, err := repository.ListAutomationRules(request.Context())
	if err != nil {
		writeError(response, 500, "automation rules unavailable")
		return
	}
	writeJSON(response, 200, values)
}

func (s *Server) createAutomationRule(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.automationRepository(response)
	if !ok {
		return
	}
	var value domain.AutomationRule
	if decodeJSON(request, &value, 64<<10) != nil || !validAutomationRule(value) {
		writeError(response, 400, "invalid automation rule")
		return
	}
	now := time.Now().UTC()
	value.ID = uuid.NewString()
	value.Name = strings.TrimSpace(value.Name)
	value.Enabled = true
	value.CreatedAt = now
	value.UpdatedAt = now
	if err := repository.CreateAutomationRule(request.Context(), value); err != nil {
		writeError(response, 500, "failed to create automation rule")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "automation_rule_create", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"rule_id": value.ID}})
	writeJSON(response, 201, value)
}

func (s *Server) updateAutomationRule(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.automationRepository(response)
	if !ok {
		return
	}
	var value domain.AutomationRule
	if decodeJSON(request, &value, 64<<10) != nil || !validAutomationRule(value) {
		writeError(response, 400, "invalid automation rule")
		return
	}
	value.ID = request.PathValue("ruleID")
	value.Name = strings.TrimSpace(value.Name)
	value.UpdatedAt = time.Now().UTC()
	if err := repository.UpdateAutomationRule(request.Context(), value); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "automation rule not found")
		return
	} else if err != nil {
		writeError(response, 500, "failed to update automation rule")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "automation_rule_update", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"rule_id": value.ID}})
	writeJSON(response, 200, value)
}

func (s *Server) deleteAutomationRule(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.automationRepository(response)
	if !ok {
		return
	}
	id := request.PathValue("ruleID")
	if err := repository.DeleteAutomationRule(request.Context(), id); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "automation rule not found")
		return
	} else if err != nil {
		writeError(response, 500, "failed to delete automation rule")
		return
	}
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "automation_rule_delete", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"rule_id": id}})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAutomationRuns(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.automationRepository(response)
	if !ok {
		return
	}
	values, err := repository.ListAutomationRuns(request.Context(), request.URL.Query().Get("rule_id"), 200)
	if err != nil {
		writeError(response, 500, "automation runs unavailable")
		return
	}
	writeJSON(response, 200, values)
}

func validAutomationRule(value domain.AutomationRule) bool {
	if len(strings.TrimSpace(value.Name)) < 2 || len(value.Name) > 128 || len(value.EventTypes) == 0 || len(value.EventTypes) > 32 || len(value.Actions) == 0 || len(value.Actions) > 8 || value.ThrottleSeconds < 0 || value.ThrottleSeconds > 2592000 {
		return false
	}
	if value.Severity != "info" && value.Severity != "warning" && value.Severity != "critical" {
		return false
	}
	for _, action := range value.Actions {
		if !slices.Contains([]string{"notification", "webhook"}, action) {
			return false
		}
	}
	for key, value := range value.Conditions {
		if len(key) < 1 || len(key) > 64 || len(value) > 256 {
			return false
		}
	}
	return true
}
