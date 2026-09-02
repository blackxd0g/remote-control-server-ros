package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestBuilderWorkerCredentialRotationAndLookup(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "workers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	worker := domain.BuilderWorker{ID: "builder-1", Name: "Builder", Formats: []string{"portable"}, Platforms: []string{"windows"}, Architectures: []string{"amd64"}, Concurrency: 1, Status: "online", TokenHash: "hash-one", LastSeenAt: now, CreatedAt: now, UpdatedAt: now}
	if err = store.UpsertBuilderWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if value, lookupErr := store.FindBuilderWorkerByTokenHash(ctx, "hash-one"); lookupErr != nil || value.ID != worker.ID {
		t.Fatalf("credential lookup failed: %#v %v", value, lookupErr)
	}
	worker.TokenHash = ""
	worker.LastSeenAt = now.Add(time.Second)
	if err = store.UpsertBuilderWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, lookupErr := store.FindBuilderWorkerByTokenHash(ctx, "hash-one"); lookupErr != nil {
		t.Fatalf("heartbeat erased credential: %v", lookupErr)
	}
	worker.TokenHash = "hash-two"
	if err = store.UpsertBuilderWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, lookupErr := store.FindBuilderWorkerByTokenHash(ctx, "hash-one"); lookupErr == nil {
		t.Fatal("old credential remained valid after rotation")
	}
	if value, lookupErr := store.FindBuilderWorkerByTokenHash(ctx, "hash-two"); lookupErr != nil || value.ID != worker.ID {
		t.Fatalf("rotated credential unavailable: %#v %v", value, lookupErr)
	}
}

func TestMigrateLegacyBuilderWorkersAddsCredentialColumnBeforeIndex(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "legacy-workers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.db.ExecContext(ctx, `CREATE TABLE builder_workers (id TEXT PRIMARY KEY, name TEXT NOT NULL, hostname TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', formats TEXT NOT NULL DEFAULT '[]', platforms TEXT NOT NULL DEFAULT '[]', architectures TEXT NOT NULL DEFAULT '[]', concurrency INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'online', last_seen_at BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("legacy migration failed: %v", err)
	}
	worker := domain.BuilderWorker{ID: "legacy-worker", Name: "Legacy", Formats: []string{"installer"}, Platforms: []string{"windows"}, Architectures: []string{"amd64"}, Concurrency: 1, Status: "online", TokenHash: "new-hash", LastSeenAt: time.Now().UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err = store.UpsertBuilderWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err = store.FindBuilderWorkerByTokenHash(ctx, "new-hash"); err != nil {
		t.Fatalf("credential index unavailable after migration: %v", err)
	}
}
