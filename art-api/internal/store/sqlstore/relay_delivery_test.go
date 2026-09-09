package sqlstore

import (
	"context"
	"fmt"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDurableRelayDeliveryRestartIsolationAndRotation(t *testing.T) {
	testDurableRelayDelivery(t, "sqlite", filepath.Join(t.TempDir(), "delivery.db"))
}

func TestPostgresDurableRelayDelivery(t *testing.T) {
	dsn := os.Getenv("RDS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("RDS_TEST_POSTGRES_DSN required: isolated empty test database")
	}
	testDurableRelayDelivery(t, "postgres", dsn)
}

func testDurableRelayDelivery(t *testing.T, driver, path string) {
	ctx := context.Background()
	s, err := Open(driver, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	token, _ := relaygateway.NewToken()
	now := time.Now()
	for _, id := range []string{"relay-a", "relay-b"} {
		if err = s.CreateRelayServer(ctx, domain.RelayServer{ID: id, Name: id, Hostname: id + ".example", Port: 21117, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		enrollment, _ := relaygateway.NewToken()
		if err = s.PutRelayEnrollment(ctx, relaygateway.Credential{RelayID: id, Generation: "g1", EnrollmentHash: relaygateway.Hash(enrollment), EnrollmentExpires: now.Add(time.Minute), UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err = s.ConsumeRelayEnrollment(ctx, id, relaygateway.Hash(enrollment), relaygateway.Hash(token), now); err != nil {
			t.Fatal(err)
		}
	}
	uuid := "persistent-relay-uuid"
	if err = s.AuthorizeRelay(ctx, "relay-a", token, uuid); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeRelay(ctx, "relay-b", token, uuid); err == nil {
		t.Fatal("UUID reassigned")
	}
	event := relaygateway.Message{RequestID: "event-active", UUID: uuid, Status: "active"}
	if err = s.ApplyRelayEvent(ctx, "relay-b", token, "relay-b.example:21117", event); err == nil {
		t.Fatal("foreign relay accepted")
	}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", event); err != nil {
		t.Fatal(err)
	}
	record, err := s.ConnectionRecord(ctx, "relay:"+uuid)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(driver, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", event); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	replayed, err := s.ConnectionRecord(ctx, "relay:"+uuid)
	if err != nil {
		t.Fatal(err)
	}
	if !record.LastSeenAt.Equal(replayed.LastSeenAt) {
		t.Fatal("replay mutated projection")
	}
	conflict := event
	conflict.Status = "closed"
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", conflict); err == nil {
		t.Fatal("event identity reused")
	}
	closed := relaygateway.Message{RequestID: "event-closed", UUID: uuid, Status: "closed"}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", closed); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", closed); err != nil {
		t.Fatal(err)
	}
	record, err = s.ConnectionRecord(ctx, "relay:"+uuid)
	if err != nil || record.ClosedAt == nil {
		t.Fatalf("missing closed projection: %v", err)
	}
	reopened := event
	reopened.RequestID = "new-active"
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", reopened); err == nil {
		t.Fatal("closed connection reopened")
	}
	// Only aged closed records may compact. Active permits survive and late receipts stay idempotent.
	if n, e := s.CompactRelayDelivery(ctx, "relay-a"); e != nil || n != 0 {
		t.Fatalf("fresh record compacted: %d %v", n, e)
	}
	if err = s.AuthorizeRelay(ctx, "relay-a", token, "still-pending-uuid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, s.bind(`UPDATE relay_event_receipts SET received_at=? WHERE uuid=? AND state='closed'`), millis(now.Add(-31*24*time.Hour)), uuid); err != nil {
		t.Fatal(err)
	}
	if n, e := s.CompactRelayDelivery(ctx, "relay-b"); e != nil || n != 0 {
		t.Fatal("foreign history compacted")
	}
	if n, e := s.CompactRelayDelivery(ctx, "relay-a"); e != nil || n != 1 {
		t.Fatalf("compaction: %d %v", n, e)
	}
	counts, e := s.RelayDeliveryCounts(ctx, "relay-a")
	if e != nil || counts.Authorizations != 1 || counts.Archived != 1 || counts.Receipts != 0 {
		t.Fatalf("bad compact counts: %+v %v", counts, e)
	}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", event); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", closed); err != nil {
		t.Fatal(err)
	}
	after, e := s.ConnectionRecord(ctx, "relay:"+uuid)
	if e != nil || !after.LastSeenAt.Equal(record.LastSeenAt) {
		t.Fatal("archived repeat mutated projection")
	}
	if err = s.AuthorizeRelay(ctx, "relay-a", token, uuid); err == nil {
		t.Fatal("archived UUID reauthorized")
	}
	if err = s.ApplyRelayEvent(ctx, "relay-b", token, "relay-b.example:21117", closed); err == nil {
		t.Fatal("foreign archive owner accepted")
	}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", reopened); err == nil {
		t.Fatal("new event accepted for archived UUID")
	}
	// Rotate to another generation, even with the same synthetic token: old authorizations cease to work.
	enrollment, _ := relaygateway.NewToken()
	if err = s.PutRelayEnrollment(ctx, relaygateway.Credential{RelayID: "relay-a", Generation: "g2", EnrollmentHash: relaygateway.Hash(enrollment), EnrollmentExpires: now.Add(time.Minute), UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.ConsumeRelayEnrollment(ctx, "relay-a", relaygateway.Hash(enrollment), relaygateway.Hash(token), now); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyRelayEvent(ctx, "relay-a", token, "relay-a.example:21117", closed); err == nil {
		t.Fatal("old generation accepted")
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM relay_event_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unexpected receipts after retries/rejections: %d", count)
	}
}

func TestRelayCompactionBatchBound(t *testing.T) {
	ctx := context.Background()
	s, err := Open("sqlite", filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = s.CreateRelayServer(ctx, domain.RelayServer{ID: "batch-relay", Name: "Batch", Hostname: "batch.example", Port: 21117, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1001; i++ {
		uuid := fmt.Sprintf("batch-relay-uuid-%04d", i)
		if _, err = tx.ExecContext(ctx, `INSERT INTO relay_authorizations(uuid,relay_id,generation,state,created_at) VALUES(?,'batch-relay','g1','closed',?)`, uuid, millis(now.Add(-40*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO relay_event_receipts(relay_id,event_id,uuid,generation,state,received_at) VALUES('batch-relay',?,?,'g1','closed',?)`, fmt.Sprintf("event-%04d", i), uuid, millis(now.Add(-31*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, e := s.CompactRelayDelivery(ctx, "batch-relay"); e != nil || n != 1000 {
		t.Fatalf("first batch %d: %v", n, e)
	}
	if n, e := s.CompactRelayDelivery(ctx, "batch-relay"); e != nil || n != 1 {
		t.Fatalf("second batch %d: %v", n, e)
	}
	counts, e := s.RelayDeliveryCounts(ctx, "batch-relay")
	if e != nil || counts.Archived != 1001 || counts.Authorizations != 0 || counts.Receipts != 0 {
		t.Fatalf("unexpected counts %+v %v", counts, e)
	}
}
