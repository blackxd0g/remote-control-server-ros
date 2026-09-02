package runtimeconfig

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const (
	RequireLogin            = "require_login"
	RequireDeviceDeployment = "require_device_deployment"
	RegistrationEnabled     = "registration_enabled"
	RegistrationAutoApprove = "registration_auto_approve"
	AccessTokenTTL          = "access_token_ttl"
	SessionTTL              = "session_ttl"
	MFAMode                 = "mfa_mode"
	PasswordMinimumLength   = "password_minimum_length"
	PasswordRequireUpper    = "password_require_upper"
	PasswordRequireLower    = "password_require_lower"
	PasswordRequireNumber   = "password_require_number"
	PasswordRequireSpecial  = "password_require_special"
)

type Values struct {
	RequireLogin            bool          `json:"require_login"`
	RequireDeviceDeployment bool          `json:"require_device_deployment"`
	RegistrationEnabled     bool          `json:"registration_enabled"`
	RegistrationAutoApprove bool          `json:"registration_auto_approve"`
	AccessTokenTTL          time.Duration `json:"-"`
	SessionTTL              time.Duration `json:"-"`
	MFAMode                 string        `json:"mfa_mode"`
	PasswordMinimumLength   int           `json:"password_minimum_length"`
	PasswordRequireUpper    bool          `json:"password_require_upper"`
	PasswordRequireLower    bool          `json:"password_require_lower"`
	PasswordRequireNumber   bool          `json:"password_require_number"`
	PasswordRequireSpecial  bool          `json:"password_require_special"`
}

type Service struct {
	repository domain.Repository
	mutex      sync.RWMutex
	values     Values
}

func New(ctx context.Context, repository domain.Repository, defaults Values) (*Service, error) {
	s := &Service{repository: repository, values: defaults}
	stored, err := repository.ListRuntimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.apply(stored); err != nil {
		return nil, fmt.Errorf("load runtime settings: %w", err)
	}
	return s, nil
}

func (s *Service) Values() Values { s.mutex.RLock(); defer s.mutex.RUnlock(); return s.values }

func (s *Service) Update(ctx context.Context, input map[string]string) (Values, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	next := s.values
	if err := applyTo(&next, input); err != nil {
		return Values{}, err
	}
	if err := s.repository.UpsertRuntimeSettings(ctx, input, time.Now().UTC()); err != nil {
		return Values{}, err
	}
	s.values = next
	return next, nil
}

func (s *Service) apply(input map[string]string) error { return applyTo(&s.values, input) }

func applyTo(value *Values, input map[string]string) error {
	for key, raw := range input {
		switch key {
		case RequireLogin, RequireDeviceDeployment, RegistrationEnabled, RegistrationAutoApprove, PasswordRequireUpper, PasswordRequireLower, PasswordRequireNumber, PasswordRequireSpecial:
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("%s must be boolean", key)
			}
			switch key {
			case RequireLogin:
				value.RequireLogin = parsed
			case RequireDeviceDeployment:
				value.RequireDeviceDeployment = parsed
			case RegistrationEnabled:
				value.RegistrationEnabled = parsed
			case RegistrationAutoApprove:
				value.RegistrationAutoApprove = parsed
			case PasswordRequireUpper:
				value.PasswordRequireUpper = parsed
			case PasswordRequireLower:
				value.PasswordRequireLower = parsed
			case PasswordRequireNumber:
				value.PasswordRequireNumber = parsed
			case PasswordRequireSpecial:
				value.PasswordRequireSpecial = parsed
			}
		case PasswordMinimumLength:
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 8 || parsed > 128 {
				return fmt.Errorf("password_minimum_length must be between 8 and 128")
			}
			value.PasswordMinimumLength = parsed
		case AccessTokenTTL, SessionTTL:
			parsed, err := time.ParseDuration(raw)
			if err != nil || parsed < 5*time.Minute || parsed > 90*24*time.Hour {
				return fmt.Errorf("%s must be between 5m and 2160h", key)
			}
			if key == AccessTokenTTL {
				value.AccessTokenTTL = parsed
			} else {
				value.SessionTTL = parsed
			}
		case MFAMode:
			if raw != "optional" && raw != "required_for_admins" && raw != "required_for_all_users" {
				return fmt.Errorf("invalid mfa_mode")
			}
			value.MFAMode = raw
		default:
			return fmt.Errorf("unknown runtime setting %q", key)
		}
	}
	if value.AccessTokenTTL > value.SessionTTL {
		return fmt.Errorf("access_token_ttl cannot exceed session_ttl")
	}
	return nil
}
