package sqlstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectBackupValidatesSchemaAndIntegrity(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, err := Open("sqlite", filepath.Join(directory, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(directory, "backup.db")
	if err = store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	inspection, err := store.InspectBackup(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Valid || inspection.QuickCheck != "ok" || inspection.SchemaTables < 3 {
		t.Fatalf("unexpected backup inspection: %+v", inspection)
	}
}

func TestInspectBackupRejectsCorruptFile(t *testing.T) {
	directory := t.TempDir()
	store, err := Open("sqlite", filepath.Join(directory, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(directory, "corrupt.db")
	if err = os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.InspectBackup(context.Background(), path); err == nil {
		t.Fatal("expected corrupt backup to be rejected")
	}
}
