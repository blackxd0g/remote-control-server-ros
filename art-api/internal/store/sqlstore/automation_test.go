package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestAutomationRuleAndRunPersistence(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "automation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	rule := domain.AutomationRule{ID: "rule-1", Name: "Denied connections", EventTypes: []string{"AUDIT_RECORDED"}, Conditions: map[string]string{"type": "connection_denied"}, Actions: []string{"notification"}, Severity: "warning", ThrottleSeconds: 60, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateAutomationRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListAutomationRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Conditions["type"] != "connection_denied" {
		t.Fatalf("unexpected rules: %#v err=%v", rules, err)
	}
	run := domain.AutomationRun{ID: "run-1", RuleID: rule.ID, EventType: "AUDIT_RECORDED", EventID: "api:1", Status: "completed", Message: "actions completed", CreatedAt: now}
	if err = store.CreateAutomationRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListAutomationRuns(ctx, rule.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].EventID != "api:1" {
		t.Fatalf("unexpected runs: %#v err=%v", runs, err)
	}
	last, err := store.LastAutomationRun(ctx, rule.ID)
	if err != nil || !last.Equal(now) {
		t.Fatalf("unexpected last run: %v err=%v", last, err)
	}
}
