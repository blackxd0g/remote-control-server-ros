package sqlstore

import (
	"context"
	"fmt"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestRelayDeliveryConcurrentLoad(t *testing.T) {
	if os.Getenv("RDS_TEST_RELAY_LOAD") != "1" {
		t.Skip("RDS_TEST_RELAY_LOAD=1 required")
	}
	drivers := []string{"sqlite"}
	if os.Getenv("RDS_TEST_POSTGRES_DSN") != "" {
		drivers = append(drivers, "postgres")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			dsn := filepath.Join(t.TempDir(), "load.db")
			if driver == "postgres" {
				dsn = os.Getenv("RDS_TEST_POSTGRES_DSN")
			}
			s, err := Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			id := "load-" + fmt.Sprint(now.UnixNano())
			token, _ := relaygateway.NewToken()
			enroll, _ := relaygateway.NewToken()
			if err = s.CreateRelayServer(ctx, domain.RelayServer{ID: id, Name: "Load", Hostname: id + ".example", Port: 21117, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if err = s.PutRelayEnrollment(ctx, relaygateway.Credential{RelayID: id, Generation: "load-gen", EnrollmentHash: relaygateway.Hash(enroll), EnrollmentExpires: now.Add(time.Minute), UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if err = s.ConsumeRelayEnrollment(ctx, id, relaygateway.Hash(enroll), relaygateway.Hash(token), now); err != nil {
				t.Fatal(err)
			}
			durations := make(chan time.Duration, 400)
			failures := make(chan error, 8)
			var wg sync.WaitGroup
			start := time.Now()
			for worker := 0; worker < 8; worker++ {
				wg.Add(1)
				go func(worker int) {
					defer wg.Done()
					for n := 0; n < 50; n++ {
						uuid := fmt.Sprintf("%s-%d-%d", id, worker, n)
						at := time.Now()
						if e := s.AuthorizeRelay(ctx, id, token, uuid); e != nil {
							failures <- e
							return
						}
						for _, state := range []string{"active", "closed", "closed"} {
							if e := s.ApplyRelayEvent(ctx, id, token, id+".example:21117", relaygateway.Message{UUID: uuid, RequestID: uuid + state, Status: state}); e != nil {
								failures <- e
								return
							}
						}
						durations <- time.Since(at)
					}
				}(worker)
			}
			wg.Wait()
			close(failures)
			for e := range failures {
				t.Error(e)
			}
			if t.Failed() {
				return
			}
			close(durations)
			var samples []time.Duration
			for d := range durations {
				samples = append(samples, d)
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			counts, e := s.RelayDeliveryCounts(ctx, id)
			if e != nil || counts.Authorizations != 400 || counts.Receipts != 800 {
				t.Fatalf("load receipt loss: %+v %v", counts, e)
			}
			t.Logf("8 workers, 400 connections, 1200 lifecycle deliveries incl 400 repeats; elapsed=%s, connection cycle p95=%s", time.Since(start), samples[len(samples)*95/100])
		})
	}
}
