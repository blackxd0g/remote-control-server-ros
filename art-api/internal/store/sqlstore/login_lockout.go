package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) AccountLoginAllowed(ctx context.Context, username string, now time.Time) (bool, time.Duration, error) {
	var lockedUntil int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT locked_until FROM account_lockouts WHERE username=?`), strings.ToLower(strings.TrimSpace(username))).Scan(&lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	until := fromMillis(lockedUntil)
	if now.Before(until) {
		return false, until.Sub(now), nil
	}
	return true, 0, nil
}

func (s *Store) RecordAccountLoginFailure(ctx context.Context, username string, now time.Time, burst int, window, lockout time.Duration) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO login_attempts(id,username,occurred_at) VALUES(?,?,?)`), uuid.NewString(), username, millis(now)); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, s.bind(`DELETE FROM login_attempts WHERE occurred_at<?`), millis(now.Add(-window)))
	var count int
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM login_attempts WHERE username=? AND occurred_at>=?`), username, millis(now.Add(-window))).Scan(&count); err != nil {
		return err
	}
	if count < burst {
		return nil
	}
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO account_lockouts(username,locked_until,updated_at) VALUES(?,?,?) ON CONFLICT(username) DO UPDATE SET locked_until=excluded.locked_until,updated_at=excluded.updated_at`), username, millis(now.Add(lockout)), millis(now))
	return err
}

func (s *Store) ClearAccountLoginFailures(ctx context.Context, username string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	username = strings.ToLower(strings.TrimSpace(username))
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM login_attempts WHERE username=?`), username); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM account_lockouts WHERE username=?`), username); err != nil {
		return err
	}
	return tx.Commit()
}
