package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/runtimeconfig"
)

type runtimeSettingsRequest struct {
	RequireLogin            *bool   `json:"require_login"`
	RequireDeviceDeployment *bool   `json:"require_device_deployment"`
	RegistrationEnabled     *bool   `json:"registration_enabled"`
	RegistrationAutoApprove *bool   `json:"registration_auto_approve"`
	AccessTokenTTL          *string `json:"access_token_ttl"`
	SessionTTL              *string `json:"session_ttl"`
	MFAMode                 *string `json:"mfa_mode"`
	PasswordMinimumLength   *int    `json:"password_minimum_length"`
	PasswordRequireUpper    *bool   `json:"password_require_upper"`
	PasswordRequireLower    *bool   `json:"password_require_lower"`
	PasswordRequireNumber   *bool   `json:"password_require_number"`
	PasswordRequireSpecial  *bool   `json:"password_require_special"`
}

func (s *Server) runtimeConfiguration() runtimeconfig.Values {
	if s.configuration != nil {
		return s.configuration.Values()
	}
	accessTTL, _ := time.ParseDuration(envValue("ART_ACCESS_TOKEN_TTL", "168h"))
	sessionTTL, _ := time.ParseDuration(envValue("ART_SESSION_TTL", "168h"))
	return runtimeconfig.Values{RequireLogin: envBool("ART_REQUIRE_LOGIN", true), RequireDeviceDeployment: envBool("ART_REQUIRE_DEVICE_DEPLOYMENT", false), RegistrationEnabled: s.registrationEnabled, RegistrationAutoApprove: envBool("ART_REGISTRATION_AUTO_APPROVE", false), AccessTokenTTL: accessTTL, SessionTTL: sessionTTL, MFAMode: string(s.mfa.Mode()), PasswordMinimumLength: 12, PasswordRequireUpper: true, PasswordRequireLower: true, PasswordRequireNumber: true, PasswordRequireSpecial: true}
}

func (s *Server) registrationIsEnabled() bool { return s.runtimeConfiguration().RegistrationEnabled }

func (s *Server) updateSettings(response http.ResponseWriter, request *http.Request) {
	if s.configuration == nil {
		writeError(response, http.StatusServiceUnavailable, "runtime settings unavailable")
		return
	}
	var input runtimeSettingsRequest
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(response, 400, "invalid request")
		return
	}
	updates := make(map[string]string)
	if input.RequireLogin != nil {
		updates[runtimeconfig.RequireLogin] = strconv.FormatBool(*input.RequireLogin)
	}
	if input.RequireDeviceDeployment != nil {
		updates[runtimeconfig.RequireDeviceDeployment] = strconv.FormatBool(*input.RequireDeviceDeployment)
	}
	if input.RegistrationEnabled != nil {
		updates[runtimeconfig.RegistrationEnabled] = strconv.FormatBool(*input.RegistrationEnabled)
	}
	if input.RegistrationAutoApprove != nil {
		updates[runtimeconfig.RegistrationAutoApprove] = strconv.FormatBool(*input.RegistrationAutoApprove)
	}
	if input.AccessTokenTTL != nil {
		updates[runtimeconfig.AccessTokenTTL] = *input.AccessTokenTTL
	}
	if input.SessionTTL != nil {
		updates[runtimeconfig.SessionTTL] = *input.SessionTTL
	}
	if input.MFAMode != nil {
		updates[runtimeconfig.MFAMode] = *input.MFAMode
	}
	if input.PasswordMinimumLength != nil {
		updates[runtimeconfig.PasswordMinimumLength] = strconv.Itoa(*input.PasswordMinimumLength)
	}
	if input.PasswordRequireUpper != nil {
		updates[runtimeconfig.PasswordRequireUpper] = strconv.FormatBool(*input.PasswordRequireUpper)
	}
	if input.PasswordRequireLower != nil {
		updates[runtimeconfig.PasswordRequireLower] = strconv.FormatBool(*input.PasswordRequireLower)
	}
	if input.PasswordRequireNumber != nil {
		updates[runtimeconfig.PasswordRequireNumber] = strconv.FormatBool(*input.PasswordRequireNumber)
	}
	if input.PasswordRequireSpecial != nil {
		updates[runtimeconfig.PasswordRequireSpecial] = strconv.FormatBool(*input.PasswordRequireSpecial)
	}
	if len(updates) == 0 {
		writeError(response, 400, "no settings supplied")
		return
	}
	value, err := s.configuration.Update(request.Context(), updates)
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	s.auth.SetTTLs(value.AccessTokenTTL, value.SessionTTL)
	s.auth.SetPasswordPolicy(auth.PasswordPolicy{MinimumLength: value.PasswordMinimumLength, RequireUpper: value.PasswordRequireUpper, RequireLower: value.PasswordRequireLower, RequireNumber: value.PasswordRequireNumber, RequireSpecial: value.PasswordRequireSpecial})
	if err = s.mfa.SetMode(mfa.Mode(value.MFAMode)); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	s.hub.Publish(events.ConfigurationUpdated, map[string]any{"require_login": value.RequireLogin, "require_device_deployment": value.RequireDeviceDeployment})
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "server_configuration_change", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"updated": updates}})
	s.settings(response, request)
}
