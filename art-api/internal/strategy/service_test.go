package strategy

import (
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func TestResolvePrioritySpecificityAndMappings(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	device := domain.Device{RustDeskID: "peer-1", OwnerUserID: "user-1", GroupID: "devices-1"}
	values := []domain.Strategy{
		{ID: "global", ScopeType: "global", Priority: 10, Enabled: true, UpdatedAt: now, Settings: map[string]any{"allow_clipboard": false, "disable_direct_ip": true}},
		{ID: "group", ScopeType: "user_group", ScopeID: "users-1", Priority: 20, Enabled: true, UpdatedAt: now.Add(time.Second), Settings: map[string]any{"allow_clipboard": false}},
		{ID: "device", ScopeType: "device", ScopeID: "peer-1", Priority: 20, Enabled: true, UpdatedAt: now.Add(2 * time.Second), Settings: map[string]any{"allow_clipboard": true, "rustdesk.lang": "ru", "require_login": true}},
		{ID: "other", ScopeType: "device", ScopeID: "peer-2", Priority: 100, Enabled: true, UpdatedAt: now.Add(3 * time.Second), Settings: map[string]any{"allow_clipboard": false}},
	}
	result := Resolve(values, device, map[string]bool{"users-1": true})
	if result.ConfigOptions["enable-clipboard"] != "Y" || result.ConfigOptions["direct-server"] != "N" || result.ConfigOptions["lang"] != "ru" {
		t.Fatalf("unexpected options: %#v", result.ConfigOptions)
	}
	if _, exists := result.ConfigOptions["require_login"]; exists {
		t.Fatal("server-only setting must not be sent")
	}
	if result.EffectiveSettings["require_login"] != true || len(result.MatchedStrategyIDs) != 3 || result.MatchedStrategyIDs[2] != "device" {
		t.Fatalf("effective policy trace is invalid: %#v", result)
	}
	if result.ModifiedAt != now.Add(2*time.Second).UnixMilli() {
		t.Fatalf("unexpected revision: %d", result.ModifiedAt)
	}
}

func TestValidateSettingsRejectsWrongTypesAndUnknownKeys(t *testing.T) {
	for _, settings := range []map[string]any{{"allow_clipboard": "yes"}, {"unknown": true}, {"rustdesk.bad key": true}, {}} {
		if ValidateSettings(settings) == nil {
			t.Fatalf("invalid settings accepted: %#v", settings)
		}
	}
	if err := ValidateSettings(map[string]any{"allow_clipboard": false, "api_server": "https://api.example.test", "rustdesk.lang": "ru"}); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
}

func TestDeletedStrategyClearsPreviouslyAppliedOptions(t *testing.T) {
	updated := time.Unix(200, 0).UTC()
	result := Resolve([]domain.Strategy{{ID: "deleted", ScopeType: "device", ScopeID: "peer-1", Priority: 10,
		Enabled: false, Deleted: true, UpdatedAt: updated, Settings: map[string]any{"allow_file_transfer": false}}},
		domain.Device{RustDeskID: "peer-1"}, nil)
	if value, exists := result.ConfigOptions["enable-file-transfer"]; !exists || value != "" {
		t.Fatalf("deleted strategy must clear the client option: %#v", result.ConfigOptions)
	}
	if result.ModifiedAt != updated.UnixMilli() {
		t.Fatalf("unexpected tombstone revision: %d", result.ModifiedAt)
	}
}
