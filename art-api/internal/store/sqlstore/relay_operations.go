package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"time"
)

type RelayDeliveryCounts = relaygateway.DeliveryCounts

func (s *Store) RelayDeliveryCounts(ctx context.Context, id string) (RelayDeliveryCounts, error) {
	var v RelayDeliveryCounts
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT
 (SELECT COUNT(*) FROM relay_authorizations WHERE relay_id=?),
 (SELECT COUNT(*) FROM relay_event_receipts WHERE relay_id=?),
 (SELECT COUNT(*) FROM relay_delivery_archive WHERE relay_id=?),
 (SELECT COUNT(*) FROM relay_authorizations a JOIN relay_event_receipts e ON e.uuid=a.uuid AND e.state='closed' WHERE a.relay_id=? AND a.state='closed' AND e.received_at<?)`), id, id, id, id, millis(time.Now().Add(-30*24*time.Hour))).Scan(&v.Authorizations, &v.Receipts, &v.Archived, &v.Eligible)
	return v, err
}

// Keep a compact ownership + event identity tombstone forever: late repeats cannot reopen a connection.
func (s *Store) CompactRelayDelivery(ctx context.Context, id string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cutoff := millis(time.Now().Add(-30 * 24 * time.Hour))
	rows, err := tx.QueryContext(ctx, s.bind(`SELECT a.uuid FROM relay_authorizations a JOIN relay_event_receipts e ON e.uuid=a.uuid AND e.state='closed' WHERE a.relay_id=? AND a.state='closed' AND e.received_at<? ORDER BY e.received_at,a.uuid LIMIT 1000`), id, cutoff)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var uuid string
		if err = rows.Scan(&uuid); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, uuid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, uuid := range ids {
		// Serialize with event application and other compaction requests on the same authorization.
		result, e := tx.ExecContext(ctx, s.bind(`UPDATE relay_authorizations SET state=state WHERE uuid=? AND relay_id=? AND state='closed'`), uuid, id)
		if e != nil {
			return 0, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return 0, e
		}
		if n == 0 {
			continue
		}
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO relay_delivery_archive(uuid,relay_id,generation,active_event_id,closed_event_id,archived_at)
 SELECT a.uuid,a.relay_id,a.generation,COALESCE((SELECT event_id FROM relay_event_receipts WHERE uuid=a.uuid AND state='active'),''),
 (SELECT event_id FROM relay_event_receipts WHERE uuid=a.uuid AND state='closed'),? FROM relay_authorizations a WHERE a.uuid=?`), millis(time.Now()), uuid)
		if err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM relay_event_receipts WHERE uuid=?`), uuid); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM relay_authorizations WHERE uuid=?`), uuid); err != nil {
			return 0, err
		}
		count++
	}
	return count, tx.Commit()
}

func (s *Store) checkArchivedRelayEvent(ctx context.Context, id, token string, m relaygateway.Message) error {
	// Only a currently valid, enabled owner can learn that its event belongs to an older generation.
	var generation string
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT c.generation FROM relay_credentials c JOIN relay_servers r ON r.id=c.relay_id WHERE c.relay_id=? AND c.token_hash=? AND r.enabled=TRUE`), id, relaygateway.Hash(token)).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return relaygateway.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	var owner, previous string
	err = s.db.QueryRowContext(ctx, s.bind(`SELECT relay_id,generation FROM relay_authorizations WHERE uuid=?`), m.UUID).Scan(&owner, &previous)
	if err == nil {
		if owner == id && previous != generation {
			return relaygateway.ErrStaleGeneration
		}
		return relaygateway.ErrUnauthorized
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var active, closed string
	err = s.db.QueryRowContext(ctx, s.bind(`SELECT relay_id,generation,active_event_id,closed_event_id FROM relay_delivery_archive WHERE uuid=?`), m.UUID).Scan(&owner, &previous, &active, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return relaygateway.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if owner != id {
		return relaygateway.ErrUnauthorized
	}
	if previous != generation {
		return relaygateway.ErrStaleGeneration
	}
	if (m.Status == "active" && active != "" && m.RequestID == active) || (m.Status == "closed" && m.RequestID == closed) {
		return nil
	}
	return relaygateway.ErrUnauthorized
}
