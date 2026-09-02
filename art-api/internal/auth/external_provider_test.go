package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/identity"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

type fakePasswordProvider struct {
	profile identity.Profile
	err     error
}

func (fakePasswordProvider) Name() string { return "ldap:test" }
func (p fakePasswordProvider) Authenticate(context.Context, string, string) (identity.Profile, error) {
	return p.profile, p.err
}

func TestExternalPasswordProviderAutoProvisionAndStableLink(t *testing.T) {
	service, repository := externalAuthService(t)
	profile := identity.Profile{Subject: "uid=alice,dc=example,dc=com", Username: "alice", Email: "alice@example.com", DisplayName: "Alice"}
	service.AddPasswordProvider(fakePasswordProvider{profile: profile}, true)

	first, err := service.VerifyCredentials(context.Background(), "alice", "directory-password")
	if err != nil || first.Username != "alice" || first.ApprovalStatus != domain.ApprovalApproved {
		t.Fatalf("auto provision failed: user=%#v err=%v", first, err)
	}
	linked, err := repository.FindOIDCIdentity(context.Background(), "ldap:test", profile.Subject)
	if err != nil || linked.UserID != first.ID {
		t.Fatalf("external identity was not linked: identity=%#v err=%v", linked, err)
	}
	second, err := service.VerifyCredentials(context.Background(), "alice", "directory-password")
	if err != nil || second.ID != first.ID {
		t.Fatalf("linked identity did not resolve stable user: user=%#v err=%v", second, err)
	}
}

func TestExternalPasswordProviderCannotTakeOverLocalUsername(t *testing.T) {
	service, repository := externalAuthService(t)
	hash, err := auth.HashPassword("local-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	local := domain.User{ID: "local-alice", Username: "alice", PasswordHash: hash, Role: domain.RoleUser, Enabled: true, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateUser(context.Background(), local); err != nil {
		t.Fatal(err)
	}
	service.AddPasswordProvider(fakePasswordProvider{profile: identity.Profile{Subject: "foreign-alice", Username: "alice"}}, true)

	_, err = service.VerifyCredentials(context.Background(), "alice", "directory-password")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("expected collision denial, got %v", err)
	}
	if _, err = repository.FindOIDCIdentity(context.Background(), "ldap:test", "foreign-alice"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("colliding identity was unexpectedly linked: %v", err)
	}
}

func TestExternalPasswordProviderSynchronizesOnlyExplicitlyMappedGroups(t *testing.T) {
	service, repository := externalAuthService(t)
	now := time.Now().UTC()
	if err := repository.CreateGroup(context.Background(), domain.Group{ID: "support", Name: "Support", Kind: domain.GroupKindUser, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	provider := fakePasswordProvider{profile: identity.Profile{Subject: "bob-subject", Username: "bob", Groups: []string{"CN=Support"}}}
	service.AddPasswordProviderWithGroups(provider, true, map[string]string{"CN=Support": "support"})
	user, err := service.VerifyCredentials(context.Background(), "bob", "directory-password")
	if err != nil {
		t.Fatal(err)
	}
	memberships, err := repository.ListUserGroupMemberships(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, membership := range memberships {
		found = found || membership.UserID == user.ID && membership.GroupID == "support"
	}
	if !found {
		t.Fatal("mapped LDAP group membership was not synchronized")
	}
}

func externalAuthService(t *testing.T) (*auth.Service, domain.Repository) {
	t.Helper()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err = repository.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	service, err := auth.NewService(repository, tokens, events.NewHub(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository
}
