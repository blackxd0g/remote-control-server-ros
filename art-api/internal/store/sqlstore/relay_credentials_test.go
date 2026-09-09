package sqlstore

import (
	"context"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRelayEnrollmentSingleUseExpiryAndRevocation(t *testing.T) {
	ctx := context.Background()
	s, err := Open("sqlite", filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = s.CreateRelayServer(ctx, domain.RelayServer{ID: "relay-a", Name: "Relay", Hostname: "a.example", Port: 21117, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	token, _ := relaygateway.NewToken()
	enrollment, _ := relaygateway.NewToken()
	c := relaygateway.Credential{RelayID: "relay-a", EnrollmentHash: relaygateway.Hash(enrollment), EnrollmentExpires: now.Add(time.Minute), Generation: "1", UpdatedAt: now}
	if err = s.PutRelayEnrollment(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err = s.ConsumeRelayEnrollment(ctx, "relay-b", c.EnrollmentHash, relaygateway.Hash(token), now); err == nil {
		t.Fatal("cross-relay enrollment accepted")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if s.ConsumeRelayEnrollment(ctx, c.RelayID, c.EnrollmentHash, relaygateway.Hash(token), now) == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("single-use enrollment: %d successes", wins.Load())
	}
	current, err := s.RelayCredential(ctx, c.RelayID)
	if err != nil || !current.Matches(token) || current.EnrollmentHash != "" {
		t.Fatal("credential not activated")
	}
	if err = s.RevokeRelayCredential(ctx, c.RelayID, now); err != nil {
		t.Fatal(err)
	}
	current, err = s.RelayCredential(ctx, c.RelayID)
	if err != nil || current.Matches(token) {
		t.Fatal("revocation must retain tombstone and reject token")
	}
	c.EnrollmentExpires = now.Add(-time.Second)
	if err = s.PutRelayEnrollment(ctx, c); err != nil {
		t.Fatal(err)
	}
	if s.ConsumeRelayEnrollment(ctx, c.RelayID, c.EnrollmentHash, relaygateway.Hash(token), now) == nil {
		t.Fatal("expired enrollment accepted")
	}
}
