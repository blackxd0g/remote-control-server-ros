package relaygateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

var ErrUnauthorized = errors.New("invalid relay credential")
var ErrStaleGeneration = errors.New("relay event belongs to an earlier credential generation")

// A row persists even after revocation: its existence prevents legacy fallback.
type Credential struct {
	RelayID           string    `json:"relay_id"`
	Hash              string    `json:"-"`
	EnrollmentHash    string    `json:"-"`
	EnrollmentExpires time.Time `json:"enrollment_expires_at"`
	Generation        string    `json:"generation"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Repository interface {
	RelayCredential(context.Context, string) (Credential, error)
	PutRelayEnrollment(context.Context, Credential) error
	ConsumeRelayEnrollment(context.Context, string, string, string, time.Time) error
	RevokeRelayCredential(context.Context, string, time.Time) error
}

func Hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func (c Credential) Matches(token string) bool {
	return len(token) == 43 && c.Hash != "" && subtle.ConstantTimeCompare([]byte(c.Hash), []byte(Hash(token))) == 1
}
