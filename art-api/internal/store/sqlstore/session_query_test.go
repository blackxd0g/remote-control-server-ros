package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestSessionQueryIncludesUserAndEveryLifecycleState(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	user := domain.User{ID: "user-1", Username: "operator", DisplayName: "Operator One", Email: "operator@example.test", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	values := []domain.Session{{ID: "active", UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now, IP: "10.0.0.1", ClientDeviceID: "desktop"}, {ID: "expired", UserID: user.ID, CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Hour), IP: "10.0.0.2"}, {ID: "revoked", UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now, IP: "10.0.0.3"}}
	for _, value := range values {
		if err = store.CreateSession(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.RevokeSession(ctx, "revoked", now); err != nil {
		t.Fatal(err)
	}
	page, err := store.QuerySessions(ctx, domain.SessionQuery{Status: "active", Search: "operator", Limit: 10, Now: now})
	if err != nil || page.Total != 1 || page.Sessions[0].ID != "active" || page.Sessions[0].Username != "operator" || page.Sessions[0].Status != "active" {
		t.Fatalf("unexpected active page: %+v err=%v", page, err)
	}
	page, err = store.QuerySessions(ctx, domain.SessionQuery{Status: "expired", Limit: 10, Now: now})
	if err != nil || page.Total != 1 || page.Sessions[0].Status != "expired" {
		t.Fatalf("unexpected expired page: %+v err=%v", page, err)
	}
	summary, err := store.SessionSummary(ctx, now)
	if err != nil || summary.Total != 3 || summary.Active != 1 || summary.Expired != 1 || summary.Revoked != 1 {
		t.Fatalf("unexpected summary: %+v err=%v", summary, err)
	}
}
