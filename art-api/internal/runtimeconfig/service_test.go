package runtimeconfig_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/runtimeconfig"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestSettingsPersistAndValidateSecurityBounds(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	defaults := runtimeconfig.Values{RequireLogin: true, AccessTokenTTL: time.Hour, SessionTTL: 2 * time.Hour, MFAMode: "optional"}
	service, err := runtimeconfig.New(ctx, repository, defaults)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(ctx, map[string]string{runtimeconfig.RequireLogin: "false", runtimeconfig.RegistrationEnabled: "true", runtimeconfig.RegistrationAutoApprove: "true", runtimeconfig.AccessTokenTTL: "30m", runtimeconfig.SessionTTL: "4h", runtimeconfig.MFAMode: "required_for_admins"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RequireLogin || !updated.RegistrationEnabled || !updated.RegistrationAutoApprove || updated.MFAMode != "required_for_admins" {
		t.Fatalf("unexpected settings: %#v", updated)
	}
	reloaded, err := runtimeconfig.New(ctx, repository, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if value := reloaded.Values(); value.RequireLogin || !value.RegistrationAutoApprove || value.SessionTTL != 4*time.Hour {
		t.Fatalf("settings were not persisted: %#v", value)
	}
	if _, err = service.Update(ctx, map[string]string{runtimeconfig.AccessTokenTTL: "5h"}); err == nil {
		t.Fatal("access token lifetime longer than session accepted")
	}
	if service.Values().AccessTokenTTL != 30*time.Minute {
		t.Fatal("failed update mutated in-memory settings")
	}
}
