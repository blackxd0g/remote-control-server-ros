package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountLockoutPersistsAndClears(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "lockout.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index := 0; index < 3; index++ {
		if err = store.RecordAccountLoginFailure(ctx, "Operator", now.Add(time.Duration(index)*time.Second), 3, time.Minute, 10*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	allowed, retry, err := store.AccountLoginAllowed(ctx, "operator", now.Add(5*time.Second))
	if err != nil || allowed || retry < 9*time.Minute {
		t.Fatalf("lockout not active: allowed=%v retry=%v err=%v", allowed, retry, err)
	}
	if err = store.ClearAccountLoginFailures(ctx, "OPERATOR"); err != nil {
		t.Fatal(err)
	}
	allowed, _, err = store.AccountLoginAllowed(ctx, "operator", now.Add(5*time.Second))
	if err != nil || !allowed {
		t.Fatalf("lockout was not cleared: allowed=%v err=%v", allowed, err)
	}
}
