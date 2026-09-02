package presence_test

import (
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/presence"
)

func TestCalculateSeparatesPresenceFromAuthenticationLifetime(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	users := []domain.User{
		{ID: "online", Username: "online", Enabled: true, ApprovalStatus: domain.ApprovalApproved},
		{ID: "idle", Username: "idle", Enabled: true, ApprovalStatus: domain.ApprovalApproved},
		{ID: "offline", Username: "offline", Enabled: true, ApprovalStatus: domain.ApprovalApproved},
		{ID: "disabled", Username: "disabled", Enabled: false, ApprovalStatus: domain.ApprovalApproved},
	}
	sessions := []domain.Session{
		{ID: "one", UserID: "online", ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-time.Minute), ClientDeviceID: "pc-1"},
		{ID: "two", UserID: "online", ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-2 * time.Minute), ClientDeviceID: "pc-2"},
		{ID: "idle", UserID: "idle", ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-10 * time.Minute)},
		{ID: "stale", UserID: "offline", ExpiresAt: now.Add(24 * time.Hour), LastSeenAt: now.Add(-20 * time.Minute)},
	}
	result := presence.Calculate(users, sessions, now)
	if result.Online != 1 || result.Idle != 1 || result.Offline != 1 {
		t.Fatalf("unexpected counters: %#v", result)
	}
	if result.Users[0].UserID != "online" || result.Users[0].ActiveDevices != 2 {
		t.Fatalf("unexpected online presence: %#v", result.Users[0])
	}
	if result.Users[2].State != "offline" {
		t.Fatal("a long-lived JWT was incorrectly treated as online")
	}
}
