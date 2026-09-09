package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"time"
)

func (s *Store) RelayCredential(ctx context.Context, id string) (relaygateway.Credential, error) {
	var c relaygateway.Credential
	var expires, updated int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT relay_id,token_hash,enrollment_hash,enrollment_expires,generation,updated_at FROM relay_credentials WHERE relay_id=?`), id).Scan(&c.RelayID, &c.Hash, &c.EnrollmentHash, &expires, &c.Generation, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	c.EnrollmentExpires, c.UpdatedAt = fromMillis(expires), fromMillis(updated)
	return c, err
}

func (s *Store) PutRelayEnrollment(ctx context.Context, c relaygateway.Credential) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO relay_credentials(relay_id,token_hash,enrollment_hash,enrollment_expires,generation,updated_at) VALUES(?,'',?,?,?,?) ON CONFLICT(relay_id) DO UPDATE SET token_hash='',enrollment_hash=excluded.enrollment_hash,enrollment_expires=excluded.enrollment_expires,generation=excluded.generation,updated_at=excluded.updated_at`), c.RelayID, c.EnrollmentHash, millis(c.EnrollmentExpires), c.Generation, millis(c.UpdatedAt))
	return err
}

// Conditional UPDATE makes enrollment single-use even across API processes.
func (s *Store) ConsumeRelayEnrollment(ctx context.Context, id, enrollmentHash, tokenHash string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE relay_credentials SET token_hash=?,enrollment_hash='',enrollment_expires=0,updated_at=? WHERE relay_id=? AND enrollment_hash=? AND enrollment_hash<>'' AND enrollment_expires>? AND EXISTS(SELECT 1 FROM relay_servers WHERE id=? AND enabled=TRUE)`), tokenHash, millis(now), id, enrollmentHash, millis(now), id)
	return affected(result, err)
}

func (s *Store) RevokeRelayCredential(ctx context.Context, id string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE relay_credentials SET token_hash='',enrollment_hash='',enrollment_expires=0,updated_at=? WHERE relay_id=?`), millis(now), id)
	return affected(result, err)
}
