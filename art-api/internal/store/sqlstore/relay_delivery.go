package sqlstore

import (
	"context"
	"errors"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"time"
)

// Authorization is durable before the command can reach HBBR. UUIDs cannot be reassigned.
func (s *Store) AuthorizeRelay(ctx context.Context, id, token, uuid string) error {
	c, err := s.RelayCredential(ctx, id)
	if err != nil {
		return err
	}
	if !c.Matches(token) {
		return relaygateway.ErrUnauthorized
	}
	result, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO relay_authorizations(uuid,relay_id,generation,state,created_at)
 SELECT ?,?,?,'permitted',? WHERE (SELECT COUNT(*) FROM relay_authorizations WHERE relay_id=?)<100000
 AND EXISTS(SELECT 1 FROM relay_credentials WHERE relay_id=? AND token_hash=? AND generation=?)
 AND NOT EXISTS(SELECT 1 FROM relay_delivery_archive WHERE uuid=?) ON CONFLICT(uuid) DO NOTHING`), uuid, id, c.Generation, millis(time.Now()), id, id, c.Hash, c.Generation, uuid)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("relay authorization reused or capacity exhausted")
	}
	return nil
}

// Receipt, monotonic state and connection projection commit together. An ack is safe only after Commit.
func (s *Store) ApplyRelayEvent(ctx context.Context, id, token, address string, m relaygateway.Message) error {
	if m.RequestID == "" || len(m.RequestID) > 64 || len(m.UUID) < 8 || len(m.UUID) > 128 || (m.Status != "active" && m.Status != "closed") {
		return relaygateway.ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A write locks the authorization row across concurrent API consumers (also serializes SQLite writers).
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE relay_authorizations SET state=state WHERE uuid=? AND relay_id=?
 AND EXISTS(SELECT 1 FROM relay_credentials c JOIN relay_servers r ON r.id=c.relay_id WHERE c.relay_id=? AND c.generation=relay_authorizations.generation AND c.token_hash=? AND r.enabled=TRUE)`), m.UUID, id, id, relaygateway.Hash(token))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		// Release the transaction before consulting the durable archive (SQLite may use one connection).
		_ = tx.Rollback()
		return s.checkArchivedRelayEvent(ctx, id, token, m)
	}
	var generation, state string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT generation,state FROM relay_authorizations WHERE uuid=?`), m.UUID).Scan(&generation, &state); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, s.bind(`INSERT INTO relay_event_receipts(relay_id,event_id,uuid,generation,state,received_at) VALUES(?,?,?,?,?,?) ON CONFLICT(relay_id,event_id) DO NOTHING`), id, m.RequestID, m.UUID, generation, m.Status, millis(time.Now()))
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var u, g, st string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT uuid,generation,state FROM relay_event_receipts WHERE relay_id=? AND event_id=?`), id, m.RequestID).Scan(&u, &g, &st); err != nil {
			return err
		}
		if u != m.UUID || g != generation || st != m.Status {
			return relaygateway.ErrUnauthorized
		}
		return tx.Commit()
	}
	if state == "closed" && m.Status != "closed" {
		return relaygateway.ErrUnauthorized
	}
	now := millis(time.Now())
	var closed any
	if m.Status == "closed" {
		closed = now
	}
	result, err = tx.ExecContext(ctx, s.bind(`INSERT INTO connection_records(connection_key,actor_user_id,actor_session_id,controller_device_id,controller_name,controller_login,target_rustdesk_id,connection_type,ip,transport,relay_uuid,relay_server,started_at,last_seen_at,closed_at)
 VALUES(?,'','','','','','',0,'','relay',?,?,?,?,?) ON CONFLICT(connection_key) DO UPDATE SET
 relay_server=excluded.relay_server,relay_uuid=excluded.relay_uuid,transport='relay',last_seen_at=excluded.last_seen_at,
 closed_at=COALESCE(connection_records.closed_at,excluded.closed_at)
 WHERE connection_records.relay_server='' OR connection_records.relay_server=excluded.relay_server`), "relay:"+m.UUID, m.UUID, address, now, now, closed)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return relaygateway.ErrUnauthorized
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE relay_authorizations SET state=? WHERE uuid=?`), m.Status, m.UUID); err != nil {
		return err
	}
	return tx.Commit()
}
