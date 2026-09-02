package managedclient

import (
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestProfileValidationRejectsUnknownAndInsecureBranding(t *testing.T) {
	base := domain.ClientProfile{Name: "Support", Platform: "all", Settings: map[string]any{"id_server": "id.example.com"}, Branding: map[string]any{}}
	if err := ValidateProfile(base); err != nil {
		t.Fatal(err)
	}
	base.Branding = map[string]any{"logo_url": "http://example.com/logo.svg"}
	if err := ValidateProfile(base); err == nil {
		t.Fatal("insecure branding URL accepted")
	}
	base.Branding = map[string]any{"unknown": true}
	if err := ValidateProfile(base); err == nil {
		t.Fatal("unknown branding setting accepted")
	}
}

func TestBundleSignatureIsStableForSamePayload(t *testing.T) {
	service := New(nil, []byte("0123456789abcdef0123456789abcdef"))
	profile := domain.ClientProfile{ID: "profile", Name: "Support", Platform: "all", Settings: map[string]any{"id_server": "id.example.com"}, Branding: map[string]any{}, Version: 1}
	at := time.Unix(1_700_000_000, 0).UTC()
	first, err := service.Bundle(profile, at)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Bundle(profile, at)
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature == "" || first.Signature != second.Signature {
		t.Fatal("bundle signature must be deterministic")
	}
	profile.Version = 2
	third, err := service.Bundle(profile, at)
	if err != nil {
		t.Fatal(err)
	}
	if third.Signature == first.Signature {
		t.Fatal("profile change must invalidate signature")
	}
}
