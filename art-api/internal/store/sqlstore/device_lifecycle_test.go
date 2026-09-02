package sqlstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestDeviceArchiveRestoreAndProtectedDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	device := domain.Device{RustDeskID: "123456789", ClientUUID: "uuid", Hostname: "host", LastSeen: now, CreatedAt: now}
	if err = store.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteArchivedDevice(ctx, device.RustDeskID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("active device deletion should be rejected, got %v", err)
	}
	archived, err := store.SetDeviceArchived(ctx, device.RustDeskID, true, now)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive failed: %+v %v", archived, err)
	}
	restored, err := store.SetDeviceArchived(ctx, device.RustDeskID, false, now)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore failed: %+v %v", restored, err)
	}
	if _, err = store.SetDeviceArchived(ctx, device.RustDeskID, true, now); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteArchivedDevice(ctx, device.RustDeskID); err != nil {
		t.Fatal(err)
	}
	if devices, listErr := store.ListDevices(ctx); listErr != nil || len(devices) != 0 {
		t.Fatalf("device was not deleted: %+v %v", devices, listErr)
	}
}

func TestBulkDeviceTagsCanBeAddedAndRemovedAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = store.UpsertDevice(ctx, domain.Device{RustDeskID: "987654321", ClientUUID: "uuid", Tags: []string{"old", "keep"}, LastSeen: now, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.BulkUpdateDevices(ctx, []string{"987654321"}, nil, []string{"new"}, []string{"old"}); err != nil {
		t.Fatal(err)
	}
	devices, err := store.ListDevices(ctx)
	if err != nil || len(devices) != 1 || strings.Join(devices[0].Tags, ",") != "keep,new" {
		t.Fatalf("unexpected tags: %+v %v", devices, err)
	}
}
