package managedclient

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/strategy"
)

type Service struct {
	repository    domain.Repository
	signingSecret []byte
}

func New(repository domain.Repository, signingSecret []byte) *Service {
	return &Service{repository: repository, signingSecret: append([]byte(nil), signingSecret...)}
}

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func ValidateProfile(value domain.ClientProfile) error {
	value.Name, value.Description, value.Platform = strings.TrimSpace(value.Name), strings.TrimSpace(value.Description), strings.TrimSpace(value.Platform)
	if len(value.Name) < 2 || len(value.Name) > 128 || len(value.Description) > 1024 || !map[string]bool{"all": true, "windows": true, "linux": true, "macos": true, "android": true}[value.Platform] {
		return errors.New("invalid client profile")
	}
	if err := strategy.ValidateSettings(value.Settings); err != nil {
		return err
	}
	if len(value.Branding) > 16 {
		return errors.New("too many branding settings")
	}
	for key, raw := range value.Branding {
		switch key {
		case "product_name", "organization":
			text, ok := raw.(string)
			if !ok || len(strings.TrimSpace(text)) > 128 {
				return errors.New("invalid branding text")
			}
		case "primary_color":
			text, ok := raw.(string)
			if !ok || !colorPattern.MatchString(text) {
				return errors.New("invalid branding color")
			}
		case "logo_url", "support_url":
			text, ok := raw.(string)
			if !ok || len(text) > 2048 {
				return errors.New("invalid branding URL")
			}
			parsed, err := url.Parse(text)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return errors.New("branding URL must use HTTPS")
			}
		case "hide_tray", "only_show_local_id", "prevent_outgoing_connections":
			if _, ok := raw.(bool); !ok {
				return errors.New("invalid branding flag")
			}
		default:
			return errors.New("unsupported branding setting")
		}
	}
	return nil
}

type Effective struct {
	Profiles   []string       `json:"profiles"`
	Settings   map[string]any `json:"settings"`
	Branding   map[string]any `json:"branding"`
	Version    int64          `json:"version"`
	ModifiedAt int64          `json:"modified_at"`
}

func (s *Service) EffectiveForDevice(ctx context.Context, device domain.Device) (Effective, error) {
	profiles, err := s.repository.ListClientProfiles(ctx)
	if err != nil {
		return Effective{}, err
	}
	assignments, err := s.repository.ListClientProfileAssignments(ctx)
	if err != nil {
		return Effective{}, err
	}
	memberships, err := s.repository.ListUserGroupMemberships(ctx)
	if err != nil {
		return Effective{}, err
	}
	groups := map[string]bool{}
	for _, membership := range memberships {
		if membership.Active && membership.UserID == device.OwnerUserID {
			groups[membership.GroupID] = true
		}
	}
	byID := map[string]domain.ClientProfile{}
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	matched := make([]domain.ClientProfileAssignment, 0)
	for _, assignment := range assignments {
		if matches(assignment, device, groups) {
			if profile, ok := byID[assignment.ProfileID]; ok && profile.Enabled && (profile.Platform == "all" || strings.EqualFold(profile.Platform, device.Platform)) {
				matched = append(matched, assignment)
			}
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority < matched[j].Priority
		}
		return specificity(matched[i].ScopeType) < specificity(matched[j].ScopeType)
	})
	result := Effective{Profiles: []string{}, Settings: map[string]any{}, Branding: map[string]any{}}
	for _, assignment := range matched {
		profile := byID[assignment.ProfileID]
		result.Profiles = append(result.Profiles, profile.ID)
		for key, value := range profile.Settings {
			result.Settings[key] = value
		}
		for key, value := range profile.Branding {
			result.Branding[key] = value
		}
		if profile.Version > result.Version {
			result.Version = profile.Version
		}
		if stamp := profile.UpdatedAt.UnixMilli(); stamp > result.ModifiedAt {
			result.ModifiedAt = stamp
		}
	}
	return result, nil
}

func matches(value domain.ClientProfileAssignment, device domain.Device, groups map[string]bool) bool {
	switch value.ScopeType {
	case "global":
		return true
	case "user":
		return device.OwnerUserID != "" && value.ScopeID == device.OwnerUserID
	case "user_group":
		return groups[value.ScopeID]
	case "device_group":
		return device.GroupID != "" && value.ScopeID == device.GroupID
	case "device":
		return value.ScopeID == device.RustDeskID
	}
	return false
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
	}
	return -1
}

type Bundle struct {
	Schema    string               `json:"schema"`
	Profile   domain.ClientProfile `json:"profile"`
	IssuedAt  time.Time            `json:"issued_at"`
	Signature string               `json:"signature"`
}

func (s *Service) Bundle(profile domain.ClientProfile, now time.Time) (Bundle, error) {
	unsigned := struct {
		Schema   string               `json:"schema"`
		Profile  domain.ClientProfile `json:"profile"`
		IssuedAt time.Time            `json:"issued_at"`
	}{"art-rustdesk/client-profile/v1", profile, now.UTC()}
	data, err := json.Marshal(unsigned)
	if err != nil {
		return Bundle{}, err
	}
	mac := hmac.New(sha256.New, s.signingSecret)
	_, _ = mac.Write(data)
	return Bundle{Schema: unsigned.Schema, Profile: profile, IssuedAt: unsigned.IssuedAt, Signature: "sha256=" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}, nil
}
