package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestConnectionProjectionPersistsLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "connections.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	start := domain.ConnectionRecord{Key: "200:100:7:4", ActorUserID: "user-1", ControllerDevice: "100", TargetRustDeskID: "200", StartedAt: now.Add(-5 * time.Minute), LastSeenAt: now.Add(-5 * time.Minute)}
	if err = store.UpsertConnection(ctx, start); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ConnectionRecord(ctx, start.Key)
	if err != nil || loaded.Key != start.Key || loaded.TargetRustDeskID != "200" {
		t.Fatalf("unexpected connection lookup: %#v err=%v", loaded, err)
	}
	closed := now
	update := start
	update.ControllerName, update.ControllerLogin, update.LastSeenAt, update.ClosedAt = "Operator", "operator", now, &closed
	if err = store.UpsertConnection(ctx, update); err != nil {
		t.Fatal(err)
	}
	values, err := store.ListConnectionRecords(ctx, now.Add(-time.Hour), 10)
	if err != nil || len(values) != 1 {
		t.Fatalf("unexpected records: %#v err=%v", values, err)
	}
	if values[0].StartedAt != start.StartedAt || values[0].ClosedAt == nil || values[0].ControllerLogin != "operator" {
		t.Fatalf("unexpected lifecycle: %#v", values[0])
	}
	if err = store.PruneConnectionRecords(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	values, err = store.ListConnectionRecords(ctx, now.Add(-time.Hour), 10)
	if err != nil || len(values) != 0 {
		t.Fatalf("closed record was not pruned: %#v err=%v", values, err)
	}
}

func TestConnectionRecordNotFound(t *testing.T) {
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "connections.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ConnectionRecord(context.Background(), "missing"); err != domain.ErrNotFound {
		t.Fatalf("expected domain.ErrNotFound, got %v", err)
	}
}
