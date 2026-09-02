package sqlstore

import (
	"context"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestClusterLeaseFailover(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "cluster.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range []string{"api-1", "api-2"} {
		if err = store.UpsertClusterNode(ctx, domain.ClusterNode{ID: id, Service: "api", Version: "2.0.0", StartedAt: now, LastSeenAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	acquired, err := store.AcquireClusterLease(ctx, "scheduler", "api-1", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("first lease: %v %v", acquired, err)
	}
	acquired, err = store.AcquireClusterLease(ctx, "scheduler", "api-2", now.Add(10*time.Second), now.Add(time.Minute))
	if err != nil || acquired {
		t.Fatalf("live lease was stolen: %v %v", acquired, err)
	}
	acquired, err = store.AcquireClusterLease(ctx, "scheduler", "api-2", now.Add(2*time.Minute), now.Add(3*time.Minute))
	if err != nil || !acquired {
		t.Fatalf("expired lease was not acquired: %v %v", acquired, err)
	}
	leases, err := store.ListClusterLeases(ctx, now.Add(2*time.Minute))
	if err != nil || len(leases) != 1 || leases[0].OwnerID != "api-2" {
		t.Fatalf("unexpected leases: %#v %v", leases, err)
	}
}
