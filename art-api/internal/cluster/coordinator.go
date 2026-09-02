package cluster

import (
	"context"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type Repository interface {
	UpsertClusterNode(context.Context, domain.ClusterNode) error
	AcquireClusterLease(context.Context, string, string, time.Time, time.Time) (bool, error)
	ReleaseClusterLease(context.Context, string, string) error
}

type Coordinator struct {
	repository Repository
	node       domain.ClusterNode
	interval   time.Duration
	now        func() time.Time
}

func New(repository Repository, node domain.ClusterNode) *Coordinator {
	return &Coordinator{repository: repository, node: node, interval: 15 * time.Second, now: func() time.Time { return time.Now().UTC() }}
}

func (c *Coordinator) Run(ctx context.Context) {
	c.heartbeat(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.heartbeat(ctx)
		}
	}
}

func (c *Coordinator) heartbeat(ctx context.Context) {
	now := c.now()
	c.node.LastSeenAt = now
	if c.node.StartedAt.IsZero() {
		c.node.StartedAt = now
	}
	_ = c.repository.UpsertClusterNode(ctx, c.node)
}

func (c *Coordinator) Acquire(ctx context.Context, name string, ttl time.Duration) (bool, error) {
	now := c.now()
	return c.repository.AcquireClusterLease(ctx, name, c.node.ID, now, now.Add(ttl))
}
func (c *Coordinator) Release(ctx context.Context, name string) error {
	return c.repository.ReleaseClusterLease(ctx, name, c.node.ID)
}
func (c *Coordinator) NodeID() string { return c.node.ID }

// RunLeader keeps exactly one worker active while this node owns a renewable
// database lease. Cancellation is delivered immediately after ownership is
// lost, and another node may resume after the lease expires.
func (c *Coordinator) RunLeader(ctx context.Context, name string, ttl time.Duration, worker func(context.Context)) {
	if ttl < 3*time.Second {
		ttl = 3 * time.Second
	}
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	var cancel context.CancelFunc
	defer func() {
		if cancel != nil {
			cancel()
		}
		_ = c.Release(context.Background(), name)
	}()
	reconcile := func() {
		owned, err := c.Acquire(ctx, name, ttl)
		if err != nil || !owned {
			if cancel != nil {
				cancel()
				cancel = nil
			}
			return
		}
		if cancel == nil {
			var workerContext context.Context
			workerContext, cancel = context.WithCancel(ctx)
			go worker(workerContext)
		}
	}
	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
