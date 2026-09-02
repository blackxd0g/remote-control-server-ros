package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestAuditQueryFiltersPagesAndSummarizes(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	events := []domain.AuditEvent{
		{ID: "a", OccurredAt: now.Add(-2 * time.Minute), Type: "login_failed", IP: "10.0.0.1", Result: "denied", Reason: "invalid_credentials"},
		{ID: "b", OccurredAt: now.Add(-time.Minute), Type: "connection_denied", ActorUserID: "user-1", TargetRustDeskID: "123", Result: "denied", Metadata: map[string]any{"permission": "remote_control"}},
		{ID: "c", OccurredAt: now, Type: "connection_allowed", ActorUserID: "user-1", TargetRustDeskID: "456", Result: "success"},
	}
	for _, event := range events {
		if err = store.AppendAudit(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.QueryAudit(ctx, domain.AuditQuery{ActorUserID: "user-1", Limit: 1})
	if err != nil || page.Total != 2 || len(page.Events) != 1 || page.Events[0].ID != "c" {
		t.Fatalf("unexpected page: %+v err=%v", page, err)
	}
	page, err = store.QueryAudit(ctx, domain.AuditQuery{Search: "remote_control", Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("metadata must not be included in broad search: %+v err=%v", page, err)
	}
	page, err = store.QueryAudit(ctx, domain.AuditQuery{TargetID: "123", Result: "denied", Limit: 10})
	if err != nil || page.Total != 1 || page.Events[0].Metadata["permission"] != "remote_control" {
		t.Fatalf("unexpected filtered page: %+v err=%v", page, err)
	}
	summary, err := store.AuditSummary(ctx, domain.AuditQuery{})
	if err != nil || summary.Total != 3 || summary.AllowedConnections != 1 || summary.DeniedConnections != 1 || summary.FailedLogins != 1 {
		t.Fatalf("unexpected summary: %+v err=%v", summary, err)
	}
}

func TestAppendAuditIsIdempotentForReporterRetries(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "audit-idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	event := domain.AuditEvent{ID: "stable-event-id", OccurredAt: time.Now().UTC(), Type: "connection_allowed", ActorUserID: "user-1", TargetRustDeskID: "123456789", Result: "allowed"}
	if err = store.AppendAudit(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err = store.AppendAudit(ctx, event); err != nil {
		t.Fatalf("reporter retry must be accepted: %v", err)
	}
	page, err := store.QueryAudit(ctx, domain.AuditQuery{Type: "connection_allowed", Limit: 10})
	if err != nil || page.Total != 1 || len(page.Events) != 1 {
		t.Fatalf("retry created a duplicate audit record: %+v err=%v", page, err)
	}
}
