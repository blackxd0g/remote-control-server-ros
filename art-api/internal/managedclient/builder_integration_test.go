package managedclient_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/managedclient"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestConfigurationBuildPersistsImmutableArtifact(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "build.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profile := domain.ClientProfile{ID: "support", Name: "Support Fleet", Platform: "all", Settings: map[string]any{"id_server": "id.example.com"}, Branding: map[string]any{}, Version: 1, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateClientProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	service := managedclient.New(repository, []byte("0123456789abcdef0123456789abcdef"))
	build, err := service.CreateBuild(ctx, managedclient.BuildRequest{ProfileID: profile.ID, TargetOS: "windows", Architecture: "amd64", Format: "configuration", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if build.Status != "ready" || build.Artifact == "" || build.CompletedAt == nil {
		t.Fatalf("unexpected build: %#v", build)
	}
	sum := sha256.Sum256([]byte(build.Artifact))
	if build.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("artifact digest mismatch")
	}
	stored, err := repository.FindClientBuildByID(ctx, build.ID)
	if err != nil || stored.Artifact != build.Artifact {
		t.Fatalf("artifact was not persisted: err=%v", err)
	}
}

func TestRemoteBuildCanOnlyBeClaimedOnce(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "atomic-claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profile := domain.ClientProfile{ID: "atomic", Name: "Atomic Client", Platform: "all", Settings: map[string]any{}, Branding: map[string]any{}, Version: 1, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateClientProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	service := managedclient.New(repository, []byte("0123456789abcdef0123456789abcdef"))
	if _, err = service.CreateBuild(ctx, managedclient.BuildRequest{ProfileID: profile.ID, TargetOS: "linux", Architecture: "amd64", Format: "portable", CreatedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ClaimBuild(ctx, "incompatible-worker", []string{"portable"}, []string{"windows"}, []string{"amd64"}, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("incompatible worker claimed Linux build: %v", err)
	}
	type result struct {
		value domain.ClientBuild
		err   error
	}
	results := make(chan result, 2)
	for _, worker := range []string{"worker-a", "worker-b"} {
		go func(id string) {
			value, claimErr := service.ClaimBuild(ctx, id, []string{"portable"}, []string{"linux"}, []string{"amd64"}, time.Minute)
			results <- result{value, claimErr}
		}(worker)
	}
	first, second := <-results, <-results
	successes := 0
	for _, candidate := range []result{first, second} {
		if candidate.err == nil && candidate.value.Status == "leased" {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one claim, got %#v and %#v", first, second)
	}
}

func TestRemoteBuilderLeaseOwnershipAndCompletion(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profile := domain.ClientProfile{ID: "managed", Name: "Managed Client", Platform: "all", Settings: map[string]any{}, Branding: map[string]any{}, Version: 1, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateClientProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	service := managedclient.New(repository, []byte("0123456789abcdef0123456789abcdef"))
	queued, err := service.CreateBuild(ctx, managedclient.BuildRequest{ProfileID: profile.ID, TargetOS: "windows", Architecture: "amd64", Format: "portable", CreatedBy: "admin"})
	if err != nil || queued.Status != "queued" {
		t.Fatalf("expected queued build: value=%#v err=%v", queued, err)
	}
	claimed, err := service.ClaimBuild(ctx, "worker-01", []string{"portable"}, []string{"windows"}, []string{"amd64"}, 2*time.Minute)
	if err != nil || claimed.ID != queued.ID || claimed.Status != "leased" || claimed.Attempts != 1 {
		t.Fatalf("unexpected claim: value=%#v err=%v", claimed, err)
	}
	if _, err = service.CompleteBuild(ctx, claimed.ID, "worker-02", "client.exe", "application/octet-stream", "", []byte("artifact")); err == nil {
		t.Fatal("a different worker completed the lease")
	}
	completed, err := service.CompleteBuild(ctx, claimed.ID, "worker-01", "client.exe", "application/octet-stream", "", []byte("artifact"))
	if err != nil || completed.Status != "ready" || completed.MediaType != "application/octet-stream" || completed.LeaseUntil != nil || completed.CompletedAt == nil {
		t.Fatalf("unexpected completion: value=%#v err=%v", completed, err)
	}
	persisted, err := repository.FindClientBuildByID(ctx, completed.ID)
	if err != nil || persisted.MediaType != "application/octet-stream" || persisted.Artifact == "artifact" {
		t.Fatalf("artifact metadata or encoding was not persisted: value=%#v err=%v", persisted, err)
	}
}
