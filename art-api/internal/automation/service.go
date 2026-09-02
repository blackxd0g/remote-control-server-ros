package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/google/uuid"
)

type Repository interface {
	ListAutomationRules(context.Context) ([]domain.AutomationRule, error)
	CreateAutomationRun(context.Context, domain.AutomationRun) error
	LastAutomationRun(context.Context, string) (time.Time, error)
	CreateNotification(context.Context, domain.Notification) error
}

type Service struct {
	repository Repository
	hub        *events.Hub
	now        func() time.Time
}

func New(repository Repository, hub *events.Hub) *Service {
	return &Service{repository: repository, hub: hub, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Run(ctx context.Context) {
	channel, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-channel:
			if !ok {
				return
			}
			s.handle(ctx, event)
		}
	}
}

func (s *Service) handle(ctx context.Context, event events.Event) {
	// Automation-generated events are delivered to webhooks but never fed back
	// into the rule engine, including rules subscribed to "*".
	if event.Type == events.AutomationTriggered {
		return
	}
	rules, err := s.repository.ListAutomationRules(ctx)
	if err != nil {
		slog.Error("list automation rules", "error", err)
		return
	}
	fields := eventFields(event)
	for _, rule := range rules {
		if !rule.Enabled || (!slices.Contains(rule.EventTypes, "*") && !slices.Contains(rule.EventTypes, event.Type)) || !conditionsMatch(rule.Conditions, fields) {
			continue
		}
		now := s.now()
		if rule.ThrottleSeconds > 0 {
			last, lookupErr := s.repository.LastAutomationRun(ctx, rule.ID)
			if lookupErr != nil {
				slog.Error("automation throttle lookup", "rule_id", rule.ID, "error", lookupErr)
				continue
			}
			if !last.IsZero() && now.Sub(last) < time.Duration(rule.ThrottleSeconds)*time.Second {
				continue
			}
		}
		status, message := "completed", "actions completed"
		for _, action := range rule.Actions {
			switch action {
			case "notification":
				resource := first(fields["target_rustdesk_id"], fields["actor_user_id"], fields["id"])
				err = s.repository.CreateNotification(ctx, domain.Notification{ID: uuid.NewString(), Type: "automation", Severity: rule.Severity, Title: rule.Name, Message: fmt.Sprintf("Rule matched %s", event.Type), Resource: resource, CreatedAt: now})
			case "webhook":
				s.hub.Publish(events.AutomationTriggered, map[string]any{"rule_id": rule.ID, "rule_name": rule.Name, "severity": rule.Severity, "source_event_type": event.Type, "source_event_id": fmt.Sprintf("%s:%d", event.SourceID, event.Revision), "fields": fields})
				err = nil
			default:
				err = fmt.Errorf("unsupported action %q", action)
			}
			if err != nil {
				status, message = "failed", err.Error()
				break
			}
		}
		eventID := fmt.Sprintf("%s:%d", event.SourceID, event.Revision)
		if err = s.repository.CreateAutomationRun(ctx, domain.AutomationRun{ID: uuid.NewString(), RuleID: rule.ID, EventType: event.Type, EventID: eventID, Status: status, Message: message, CreatedAt: now}); err != nil {
			slog.Error("create automation run", "rule_id", rule.ID, "error", err)
		}
	}
}

func eventFields(event events.Event) map[string]string {
	fields := map[string]string{"event_type": event.Type}
	var payload map[string]any
	if json.Unmarshal(event.Payload, &payload) != nil {
		return fields
	}
	for key, value := range payload {
		switch typed := value.(type) {
		case string:
			fields[key] = typed
		case float64, bool:
			fields[key] = fmt.Sprint(typed)
		}
	}
	return fields
}

func conditionsMatch(conditions, fields map[string]string) bool {
	for key, expected := range conditions {
		actual := fields[key]
		if !strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(expected)) {
			return false
		}
	}
	return true
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
