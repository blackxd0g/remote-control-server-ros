package audit_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestIdentityMismatchNotificationsAreDeduplicatedAndReadStatePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.db")
	repository, err := sqlstore.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	service := audit.New(repository)
	now := time.Now().UTC()
	for index := 0; index < 3; index++ {
		if err = service.Record(ctx, domain.AuditEvent{Type: "device_identity_mismatch", TargetRustDeskID: "123456789", OccurredAt: now.Add(time.Duration(index) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	values, err := repository.ListNotifications(ctx, 50, true)
	if err != nil || len(values) != 1 {
		t.Fatalf("expected one deduplicated notification: values=%#v err=%v", values, err)
	}
	if err = repository.MarkAllNotificationsRead(ctx, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = sqlstore.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	values, err = repository.ListNotifications(ctx, 50, true)
	if err != nil || len(values) != 0 {
		t.Fatalf("read state did not survive reopen: values=%#v err=%v", values, err)
	}
}
