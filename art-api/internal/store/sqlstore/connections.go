package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func (s *Store) ConnectionRecord(ctx context.Context, key string) (domain.ConnectionRecord, error) {
	var value domain.ConnectionRecord
	var started, lastSeen int64
	var closed sql.NullInt64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT connection_key,actor_user_id,actor_session_id,controller_device_id,controller_name,controller_login,target_rustdesk_id,connection_type,ip,transport,relay_uuid,relay_server,started_at,last_seen_at,closed_at FROM connection_records WHERE connection_key=?`), key).
		Scan(&value.Key, &value.ActorUserID, &value.ActorSessionID, &value.ControllerDevice, &value.ControllerName, &value.ControllerLogin, &value.TargetRustDeskID, &value.ConnectionType, &value.IP, &value.Transport, &value.RelayUUID, &value.RelayServer, &started, &lastSeen, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConnectionRecord{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ConnectionRecord{}, err
	}
	value.StartedAt, value.LastSeenAt = fromMillis(started), fromMillis(lastSeen)
	if closed.Valid {
		at := fromMillis(closed.Int64)
		value.ClosedAt = &at
	}
	return value, nil
}

func (s *Store) UpsertConnection(ctx context.Context, value domain.ConnectionRecord) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO connection_records
        (connection_key,actor_user_id,actor_session_id,controller_device_id,controller_name,controller_login,target_rustdesk_id,connection_type,ip,transport,relay_uuid,relay_server,started_at,last_seen_at,closed_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(connection_key) DO UPDATE SET
        actor_user_id=CASE WHEN excluded.actor_user_id<>'' THEN excluded.actor_user_id ELSE connection_records.actor_user_id END,
        actor_session_id=CASE WHEN excluded.actor_session_id<>'' THEN excluded.actor_session_id ELSE connection_records.actor_session_id END,
        controller_device_id=CASE WHEN excluded.controller_device_id<>'' THEN excluded.controller_device_id ELSE connection_records.controller_device_id END,
        controller_name=CASE WHEN excluded.controller_name<>'' THEN excluded.controller_name ELSE connection_records.controller_name END,
        controller_login=CASE WHEN excluded.controller_login<>'' THEN excluded.controller_login ELSE connection_records.controller_login END,
        target_rustdesk_id=excluded.target_rustdesk_id,connection_type=excluded.connection_type,
        ip=CASE WHEN excluded.ip<>'' THEN excluded.ip ELSE connection_records.ip END,
        transport=CASE WHEN excluded.transport<>'' THEN excluded.transport ELSE connection_records.transport END,
        relay_uuid=CASE WHEN excluded.relay_uuid<>'' THEN excluded.relay_uuid ELSE connection_records.relay_uuid END,
        relay_server=CASE WHEN excluded.relay_server<>'' THEN excluded.relay_server ELSE connection_records.relay_server END,
        started_at=CASE WHEN excluded.started_at<connection_records.started_at THEN excluded.started_at ELSE connection_records.started_at END,
        last_seen_at=CASE WHEN excluded.last_seen_at>connection_records.last_seen_at THEN excluded.last_seen_at ELSE connection_records.last_seen_at END,
        closed_at=CASE WHEN excluded.closed_at IS NOT NULL THEN excluded.closed_at ELSE connection_records.closed_at END`),
		value.Key, value.ActorUserID, value.ActorSessionID, value.ControllerDevice, value.ControllerName, value.ControllerLogin,
		value.TargetRustDeskID, value.ConnectionType, value.IP, value.Transport, value.RelayUUID, value.RelayServer, millis(value.StartedAt), millis(value.LastSeenAt), nullableTimePtrMillis(value.ClosedAt))
	return err
}

func (s *Store) ListConnectionRecords(ctx context.Context, since time.Time, limit int) ([]domain.ConnectionRecord, error) {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT connection_key,actor_user_id,actor_session_id,controller_device_id,controller_name,controller_login,target_rustdesk_id,connection_type,ip,transport,relay_uuid,relay_server,started_at,last_seen_at,closed_at FROM connection_records WHERE last_seen_at>=? ORDER BY last_seen_at DESC LIMIT ?`), millis(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.ConnectionRecord, 0)
	for rows.Next() {
		var value domain.ConnectionRecord
		var started, lastSeen int64
		var closed sql.NullInt64
		if err = rows.Scan(&value.Key, &value.ActorUserID, &value.ActorSessionID, &value.ControllerDevice, &value.ControllerName, &value.ControllerLogin, &value.TargetRustDeskID, &value.ConnectionType, &value.IP, &value.Transport, &value.RelayUUID, &value.RelayServer, &started, &lastSeen, &closed); err != nil {
			return nil, err
		}
		value.StartedAt, value.LastSeenAt = fromMillis(started), fromMillis(lastSeen)
		if closed.Valid {
			timestamp := fromMillis(closed.Int64)
			value.ClosedAt = &timestamp
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) PruneConnectionRecords(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM connection_records WHERE closed_at IS NOT NULL AND closed_at<?`), millis(before))
	return err
}
