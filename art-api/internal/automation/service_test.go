package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
)

type memoryRepository struct {
	rules         []domain.AutomationRule
	runs          []domain.AutomationRun
	notifications []domain.Notification
	last          time.Time
}

func (m *memoryRepository) ListAutomationRules(context.Context) ([]domain.AutomationRule, error) {
	return m.rules, nil
}
func (m *memoryRepository) CreateAutomationRun(_ context.Context, v domain.AutomationRun) error {
	m.runs = append(m.runs, v)
	m.last = v.CreatedAt
	return nil
}
func (m *memoryRepository) LastAutomationRun(context.Context, string) (time.Time, error) {
	return m.last, nil
}
func (m *memoryRepository) CreateNotification(_ context.Context, v domain.Notification) error {
	m.notifications = append(m.notifications, v)
	return nil
}

func TestRuleMatchesAuditFieldsAndThrottles(t *testing.T) {
	repository := &memoryRepository{rules: []domain.AutomationRule{{ID: "failed-login", Name: "Repeated failed login", EventTypes: []string{events.AuditRecorded}, Conditions: map[string]string{"type": "login_failed", "result": "denied"}, Actions: []string{"notification"}, Severity: "warning", ThrottleSeconds: 60, Enabled: true}}}
	service := New(repository, events.NewHub())
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	service.now = func() time.Time { return now }
	payload := domain.AuditEvent{ID: "event-1", Type: "login_failed", Result: "denied", ActorUserID: "user-1"}
	data, _ := json.Marshal(payload)
	event := events.Event{SourceID: "api", Revision: 1, Type: events.AuditRecorded, Payload: data}
	service.handle(context.Background(), event)
	service.handle(context.Background(), event)
	if len(repository.notifications) != 1 || len(repository.runs) != 1 {
		t.Fatalf("expected one throttled execution: notifications=%d runs=%d", len(repository.notifications), len(repository.runs))
	}
}

func TestWebhookActionPublishesNormalizedEventWithoutRecursion(t *testing.T) {
	hub := events.NewHub()
	stream, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	repository := &memoryRepository{rules: []domain.AutomationRule{{ID: "relay-alert", Name: "Relay alert", EventTypes: []string{events.RelayUpdated}, Actions: []string{"webhook"}, Severity: "critical", Enabled: true}}}
	service := New(repository, hub)
	service.handle(context.Background(), events.Event{SourceID: "api", Revision: 7, Type: events.RelayUpdated, Payload: json.RawMessage(`{"id":"relay-1","health":"unhealthy"}`)})
	select {
	case event := <-stream:
		if event.Type != events.AutomationTriggered || !strings.Contains(string(event.Payload), `"source_event_id":"api:7"`) {
			t.Fatalf("unexpected automation webhook event: %#v", event)
		}
		service.handle(context.Background(), event)
		if len(repository.runs) != 1 {
			t.Fatalf("automation event recursively executed rules: %d", len(repository.runs))
		}
	case <-time.After(time.Second):
		t.Fatal("automation webhook event was not published")
	}
}
