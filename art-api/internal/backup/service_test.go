package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/backup"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestCreateRetentionAndStageRestore(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, err := sqlstore.Open("sqlite", filepath.Join(dataDir, "art.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	user := domain.User{ID: "admin", Username: "admin", Email: "admin@example.test", PasswordHash: "hash", DisplayName: "Admin", Role: domain.RoleAdmin, Enabled: true, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	service, err := backup.New(store, dataDir, time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if _, err = service.Create(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	values, err := service.List(ctx)
	if err != nil || len(values) != 2 {
		t.Fatalf("retention failed: %+v err=%v", values, err)
	}
	path, err := service.Path(values[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := service.StageRestore(ctx, bytes.NewReader(payload))
	if err != nil || !inspection.Valid || !service.RestorePending() {
		t.Fatalf("stage failed: %+v err=%v pending=%v", inspection, err, service.RestorePending())
	}
	if err = service.CancelRestore(); err != nil || service.RestorePending() {
		t.Fatalf("cancel failed: %v", err)
	}
}

func TestPathRejectsTraversal(t *testing.T) {
	service, err := backup.New(stubRepository{}, t.TempDir(), time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Path("../art.db"); err == nil {
		t.Fatal("path traversal was accepted")
	}
}

type stubRepository struct{}

func (stubRepository) Backup(context.Context, string) error { return nil }
func (stubRepository) InspectBackup(context.Context, string) (domain.BackupInspection, error) {
	return domain.BackupInspection{}, nil
}
