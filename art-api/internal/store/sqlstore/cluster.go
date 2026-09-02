package sqlstore

import (
	"context"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func (s *Store) UpsertClusterNode(ctx context.Context, value domain.ClusterNode) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO cluster_nodes(id,service,version,address,started_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET service=excluded.service,version=excluded.version,address=excluded.address,last_seen_at=excluded.last_seen_at`), value.ID, value.Service, value.Version, value.Address, millis(value.StartedAt), millis(value.LastSeenAt))
	return err
}

func (s *Store) ListClusterNodes(ctx context.Context, activeAfter time.Time) ([]domain.ClusterNode, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT n.id,n.service,n.version,n.address,n.started_at,n.last_seen_at,(SELECT COUNT(*) FROM cluster_leases l WHERE l.owner_id=n.id AND l.expires_at>?) FROM cluster_nodes n WHERE n.last_seen_at>=? ORDER BY n.service,n.id`), millis(time.Now().UTC()), millis(activeAfter))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.ClusterNode{}
	for rows.Next() {
		var value domain.ClusterNode
		var startedAt, lastSeenAt int64
		if err = rows.Scan(&value.ID, &value.Service, &value.Version, &value.Address, &startedAt, &lastSeenAt, &value.LeaseCount); err != nil {
			return nil, err
		}
		value.StartedAt, value.LastSeenAt = fromMillis(startedAt), fromMillis(lastSeenAt)
		values = append(values, value)
	}
	return values, rows.Err()
}

// AcquireClusterLease is an atomic compare-and-swap. The current owner may
// renew; another node can take over only after expiry.
func (s *Store) AcquireClusterLease(ctx context.Context, name, ownerID string, now, expiresAt time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO cluster_leases(name,owner_id,expires_at,updated_at) VALUES(?,?,?,?) ON CONFLICT(name) DO UPDATE SET owner_id=excluded.owner_id,expires_at=excluded.expires_at,updated_at=excluded.updated_at WHERE cluster_leases.owner_id=excluded.owner_id OR cluster_leases.expires_at<=?`), name, ownerID, millis(expiresAt), millis(now), millis(now))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (s *Store) ReleaseClusterLease(ctx context.Context, name, ownerID string) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM cluster_leases WHERE name=? AND owner_id=?`), name, ownerID)
	return err
}

func (s *Store) ListClusterLeases(ctx context.Context, now time.Time) ([]domain.ClusterLease, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT name,owner_id,expires_at,updated_at FROM cluster_leases WHERE expires_at>? ORDER BY name`), millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.ClusterLease{}
	for rows.Next() {
		var value domain.ClusterLease
		var expiresAt, updatedAt int64
		if err = rows.Scan(&value.Name, &value.OwnerID, &expiresAt, &updatedAt); err != nil {
			return nil, err
		}
		value.ExpiresAt, value.UpdatedAt = fromMillis(expiresAt), fromMillis(updatedAt)
		values = append(values, value)
	}
	return values, rows.Err()
}
