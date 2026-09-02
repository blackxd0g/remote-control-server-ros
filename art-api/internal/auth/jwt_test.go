package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestJWTContainsRequiredClaims(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	manager := NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	user := domain.User{ID: "user-uuid", Username: "operator", DisplayName: "Remote Operator", Role: domain.RoleUser, TokenVersion: 7}
	session := domain.Session{ID: "session-uuid", ExpiresAt: now.Add(2 * time.Hour), ClientDeviceID: "device-1"}
	raw, _, err := manager.Issue(user, session, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(raw, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sub", "sid", "iat", "exp", "iss", "aud", "username", "display_name", "token_version"} {
		if _, exists := claims[key]; !exists {
			t.Errorf("required claim %q is missing", key)
		}
	}
	parsed, err := manager.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject != user.ID || parsed.SessionID != session.ID || parsed.TokenVersion != 7 {
		t.Fatalf("wrong parsed claims: %+v", parsed)
	}
}

func TestJWTRejectsWrongAudience(t *testing.T) {
	now := time.Now().UTC()
	issuer := NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "other-service", time.Hour)
	verifier := NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	raw, _, err := issuer.Issue(domain.User{ID: "u", Username: "x"}, domain.Session{ID: "s", ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Parse(raw); err == nil {
		t.Fatal("token for another audience was accepted")
	}
}
