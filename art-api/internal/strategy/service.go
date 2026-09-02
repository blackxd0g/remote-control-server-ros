package strategy

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type Service struct{ repository domain.Repository }

type Result struct {
	ModifiedAt         int64             `json:"modified_at"`
	ConfigOptions      map[string]string `json:"config_options"`
	EffectiveSettings  map[string]any    `json:"effective_settings"`
	MatchedStrategyIDs []string          `json:"matched_strategy_ids"`
}

type SettingDefinition struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	LabelEN    string `json:"label_en"`
	Category   string `json:"category"`
	Kind       string `json:"kind"`
	ServerOnly bool   `json:"server_only"`
}

var definitions = []SettingDefinition{
	{Key: "require_login", Label: "Требовать вход", LabelEN: "Require login", Category: "Security", Kind: "boolean", ServerOnly: true},
	{Key: "require_managed_device", Label: "Только управляемые устройства", LabelEN: "Managed devices only", Category: "Security", Kind: "boolean", ServerOnly: true},
	{Key: "allow_remote_control", Label: "Удалённое управление", LabelEN: "Remote control", Category: "Permissions", Kind: "boolean"},
	{Key: "allow_file_transfer", Label: "Передача файлов", LabelEN: "File transfer", Category: "Permissions", Kind: "boolean"},
	{Key: "allow_clipboard", Label: "Буфер обмена", LabelEN: "Clipboard", Category: "Permissions", Kind: "boolean"},
	{Key: "allow_tcp_tunnel", Label: "TCP-туннель", LabelEN: "TCP tunnel", Category: "Permissions", Kind: "boolean"},
	{Key: "force_relay", Label: "Принудительный relay", LabelEN: "Force relay", Category: "Network", Kind: "boolean"},
	{Key: "disable_direct_ip", Label: "Отключить Direct IP", LabelEN: "Disable Direct IP", Category: "Network", Kind: "boolean"},
	{Key: "disable_lan_discovery", Label: "Отключить LAN discovery", LabelEN: "Disable LAN discovery", Category: "Network", Kind: "boolean"},
	{Key: "disable_settings", Label: "Заблокировать настройки клиента", LabelEN: "Lock client settings", Category: "UI", Kind: "boolean"},
	{Key: "id_server", Label: "ID Server", LabelEN: "ID Server", Category: "Network", Kind: "string"},
	{Key: "relay_server", Label: "Relay Server", LabelEN: "Relay Server", Category: "Network", Kind: "string"},
	{Key: "api_server", Label: "API Server", LabelEN: "API Server", Category: "Network", Kind: "string"},
}

func Definitions() []SettingDefinition { return append([]SettingDefinition(nil), definitions...) }

func ClientOptions(settings map[string]any) map[string]string {
	result := make(map[string]string)
	for key, raw := range settings {
		if option, value, ok := clientOption(key, raw); ok {
			result[option] = value
		}
	}
	return result
}

func New(repository domain.Repository) *Service { return &Service{repository: repository} }

func (s *Service) EffectiveForDevice(ctx context.Context, device domain.Device) (Result, error) {
	values, err := s.repository.ListStrategies(ctx)
	if err != nil {
		return Result{}, err
	}
	memberships, err := s.repository.ListUserGroupMemberships(ctx)
	if err != nil {
		return Result{}, err
	}
	groups := map[string]bool{}
	for _, membership := range memberships {
		if membership.Active && membership.UserID == device.OwnerUserID {
			groups[membership.GroupID] = true
		}
	}
	return Resolve(values, device, groups), nil
}

// Resolve merges lower precedence values first. A larger priority wins, and equal-priority
// values are resolved from global to the most device-specific scope.
func Resolve(values []domain.Strategy, device domain.Device, userGroups map[string]bool) Result {
	matched := make([]domain.Strategy, 0, len(values))
	result := Result{ConfigOptions: map[string]string{}, EffectiveSettings: map[string]any{}, MatchedStrategyIDs: []string{}}
	for _, value := range values {
		if !matches(value, device, userGroups) {
			continue
		}
		if stamp := value.UpdatedAt.UnixMilli(); stamp > result.ModifiedAt {
			result.ModifiedAt = stamp
		}
		for key, raw := range value.Settings {
			if option, _, ok := clientOption(key, raw); ok {
				result.ConfigOptions[option] = ""
			}
		}
		if value.Enabled && !value.Deleted {
			matched = append(matched, value)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority < matched[j].Priority
		}
		if specificity(matched[i].ScopeType) != specificity(matched[j].ScopeType) {
			return specificity(matched[i].ScopeType) < specificity(matched[j].ScopeType)
		}
		return matched[i].UpdatedAt.Before(matched[j].UpdatedAt)
	})
	for _, value := range matched {
		result.MatchedStrategyIDs = append(result.MatchedStrategyIDs, value.ID)
		for key, raw := range value.Settings {
			result.EffectiveSettings[key] = raw
			if option, setting, ok := clientOption(key, raw); ok {
				result.ConfigOptions[option] = setting
			}
		}
	}
	return result
}

func ValidateSettings(settings map[string]any) error {
	if len(settings) == 0 || len(settings) > 64 {
		return fmt.Errorf("strategy must contain 1 to 64 settings")
	}
	kinds := map[string]string{}
	for _, definition := range definitions {
		kinds[definition.Key] = definition.Kind
	}
	for key, raw := range settings {
		kind, known := kinds[key]
		if !known {
			if !strings.HasPrefix(key, "rustdesk.") || len(key) > 136 || strings.ContainsAny(key, " \t\r\n") {
				return fmt.Errorf("unsupported setting %q", key)
			}
			switch value := raw.(type) {
			case bool, float64:
			case string:
				if len(value) > 2048 {
					return fmt.Errorf("setting %q is too long", key)
				}
			default:
				return fmt.Errorf("invalid value for %q", key)
			}
			continue
		}
		if kind == "boolean" {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("setting %q must be boolean", key)
			}
		} else {
			value, ok := raw.(string)
			if !ok || len(value) > 2048 {
				return fmt.Errorf("setting %q must be a string", key)
			}
		}
	}
	return nil
}

func matches(value domain.Strategy, device domain.Device, userGroups map[string]bool) bool {
	switch value.ScopeType {
	case "global":
		return true
	case "user":
		return device.OwnerUserID != "" && value.ScopeID == device.OwnerUserID
	case "user_group":
		return userGroups[value.ScopeID]
	case "device_group":
		return device.GroupID != "" && value.ScopeID == device.GroupID
	case "device":
		return value.ScopeID == device.RustDeskID
	default:
		return false
	}
}

func specificity(scope string) int {
	switch scope {
	case "global":
		return 0
	case "user_group":
		return 1
	case "user":
		return 2
	case "device_group":
		return 3
	case "device":
		return 4
	default:
		return -1
	}
}

func clientOption(key string, raw any) (string, string, bool) {
	boolean, isBoolean := raw.(bool)
	yn := func(value bool) string {
		if value {
			return "Y"
		}
		return "N"
	}
	switch key {
	case "allow_remote_control":
		if isBoolean {
			return "enable-keyboard", yn(boolean), true
		}
	case "allow_file_transfer":
		if isBoolean {
			return "enable-file-transfer", yn(boolean), true
		}
	case "allow_clipboard":
		if isBoolean {
			return "enable-clipboard", yn(boolean), true
		}
	case "allow_tcp_tunnel":
		if isBoolean {
			return "enable-tunnel", yn(boolean), true
		}
	case "force_relay":
		if isBoolean {
			return "force-relay", yn(boolean), true
		}
	case "disable_direct_ip":
		if isBoolean {
			return "direct-server", yn(!boolean), true
		}
	case "disable_lan_discovery":
		if isBoolean {
			return "enable-lan-discovery", yn(!boolean), true
		}
	case "disable_settings":
		if isBoolean {
			return "disable-settings", yn(boolean), true
		}
	case "id_server":
		return stringOption("custom-rendezvous-server", raw)
	case "relay_server":
		return stringOption("relay-server", raw)
	case "api_server":
		return stringOption("api-server", raw)
	case "require_login", "require_managed_device":
		return "", "", false
	}
	if strings.HasPrefix(key, "rustdesk.") {
		option := strings.TrimPrefix(key, "rustdesk.")
		if option == "" || len(option) > 128 || strings.ContainsAny(option, " \t\r\n") {
			return "", "", false
		}
		switch value := raw.(type) {
		case bool:
			return option, yn(value), true
		case string:
			if len(value) <= 2048 {
				return option, value, true
			}
		case float64:
			return option, fmt.Sprintf("%g", value), true
		}
	}
	return "", "", false
}

func stringOption(option string, raw any) (string, string, bool) {
	value, ok := raw.(string)
	value = strings.TrimSpace(value)
	return option, value, ok && len(value) <= 2048
}
