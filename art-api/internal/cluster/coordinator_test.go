package cluster

import (
	"context"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"testing"
	"time"
)

type repositoryStub struct {
	nodes   []domain.ClusterNode
	owner   string
	expires time.Time
}

func (r *repositoryStub) UpsertClusterNode(_ context.Context, node domain.ClusterNode) error {
	r.nodes = append(r.nodes, node)
	return nil
}
func (r *repositoryStub) AcquireClusterLease(_ context.Context, _ string, owner string, now, expires time.Time) (bool, error) {
	if r.owner != "" && r.owner != owner && r.expires.After(now) {
		return false, nil
	}
	r.owner, r.expires = owner, expires
	return true, nil
}
func (r *repositoryStub) ReleaseClusterLease(_ context.Context, _ string, owner string) error {
	if r.owner == owner {
		r.owner = ""
	}
	return nil
}
func TestCoordinatorHeartbeatAndLease(t *testing.T) {
	repository := &repositoryStub{}
	node := domain.ClusterNode{ID: "api-1", Service: "api"}
	coordinator := New(repository, node)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { return now }
	coordinator.heartbeat(context.Background())
	if len(repository.nodes) != 1 || repository.nodes[0].LastSeenAt != now {
		t.Fatalf("heartbeat missing: %#v", repository.nodes)
	}
	acquired, err := coordinator.Acquire(context.Background(), "scheduler", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("lease not acquired: %v %v", acquired, err)
	}
}
