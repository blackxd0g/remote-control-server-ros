package sqlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestACLRuleEffectPersists(t *testing.T) {
	ctx := context.Background()
	store, err := Open("sqlite", filepath.Join(t.TempDir(), "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	rule := domain.ACLRule{ID: "deny-file-transfer", Name: "Deny file transfer", SubjectType: "user_group", TargetType: "device_group", Permissions: []string{"file_transfer"}, Effect: "deny", Enabled: true, Priority: 10, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateACLRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	values, err := store.ListACLRules(ctx)
	if err != nil || len(values) != 1 || values[0].Effect != "deny" {
		t.Fatalf("unexpected ACL rules: %#v err=%v", values, err)
	}
	rule.Effect, rule.UpdatedAt = "allow", now.Add(time.Second)
	if err = store.UpdateACLRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	values, err = store.ListACLRules(ctx)
	if err != nil || len(values) != 1 || values[0].Effect != "allow" {
		t.Fatalf("updated effect was not persisted: %#v err=%v", values, err)
	}
}
