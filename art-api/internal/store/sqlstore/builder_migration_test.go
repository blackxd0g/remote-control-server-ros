package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBuilderQueueColumnsMigrateFromLegacyClientBuilds(t *testing.T) {
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err = store.db.ExecContext(ctx, `CREATE TABLE client_builds (id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, target_os TEXT NOT NULL, architecture TEXT NOT NULL, format TEXT NOT NULL, status TEXT NOT NULL, artifact_name TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', artifact TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL, created_at BIGINT NOT NULL, completed_at BIGINT)`); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"media_type", "worker_id", "attempts", "started_at", "lease_until"} {
		var count int
		if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('client_builds') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("column %s was not migrated: count=%d err=%v", column, count, err)
		}
	}
}
