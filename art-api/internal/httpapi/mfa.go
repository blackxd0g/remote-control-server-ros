package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
)

type totpCodeRequest struct {
	Code string `json:"code"`
}

func (s *Server) enrollTOTP(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if principal.User.TOTPEnabled {
		writeError(response, http.StatusConflict, "two-factor authentication is already enabled")
		return
	}
	enrollment, err := s.mfa.Begin(request.Context(), principal.User)
	if err != nil {
		writeError(response, 500, "two-factor enrollment failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "mfa_enrollment_started", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	writeJSON(response, http.StatusOK, enrollment)
}
func (s *Server) confirmTOTP(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input totpCodeRequest
	if decodeJSON(request, &input, 4<<10) != nil {
		writeError(response, 400, "invalid request")
		return
	}
	user, err := s.mfa.Confirm(request.Context(), principal.User, input.Code)
	if errors.Is(err, mfa.ErrInvalidCode) {
		writeError(response, 401, "invalid two-factor code")
		return
	}
	if err != nil {
		writeError(response, 500, "two-factor confirmation failed")
		return
	}
	now := time.Now().UTC()
	_ = s.repository.RevokeUserSessions(request.Context(), user.ID, now)
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": user.ID})
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "mfa_enabled", ActorUserID: user.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	writeJSON(response, http.StatusOK, clientUser(user))
}
func (s *Server) disableTOTP(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input totpCodeRequest
	if decodeJSON(request, &input, 4<<10) != nil {
		writeError(response, 400, "invalid request")
		return
	}
	user, err := s.mfa.Disable(request.Context(), principal.User, input.Code)
	if errors.Is(err, mfa.ErrInvalidCode) {
		writeError(response, 401, "invalid two-factor code")
		return
	}
	if err != nil {
		writeError(response, 500, "two-factor disable failed")
		return
	}
	now := time.Now().UTC()
	_ = s.repository.RevokeUserSessions(request.Context(), user.ID, now)
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": user.ID})
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "mfa_disabled", ActorUserID: user.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	writeJSON(response, 200, nil)
}
func (s *Server) regenerateRecoveryCodes(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input totpCodeRequest
	if decodeJSON(request, &input, 4<<10) != nil {
		writeError(response, 400, "invalid request")
		return
	}
	codes, err := s.mfa.RegenerateRecoveryCodes(request.Context(), principal.User, input.Code)
	if errors.Is(err, mfa.ErrInvalidCode) {
		writeError(response, 401, "invalid two-factor code")
		return
	}
	if err != nil {
		writeError(response, 500, "recovery code generation failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "mfa_recovery_codes_regenerated", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	writeJSON(response, 200, map[string]any{"recovery_codes": codes})
}
func (s *Server) adminResetTOTP(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	user, err := s.repository.SetUserTOTP(request.Context(), request.PathValue("userID"), "", false, time.Now().UTC())
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "user not found")
		return
	}
	if err != nil {
		writeError(response, 500, "two-factor reset failed")
		return
	}
	if err = s.repository.ReplaceMFARecoveryCodes(request.Context(), user.ID, nil, time.Now().UTC()); err != nil {
		writeError(response, 500, "two-factor reset failed")
		return
	}
	_ = s.repository.RevokeUserSessions(request.Context(), user.ID, time.Now().UTC())
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": user.ID})
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "mfa_admin_reset", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, 200, nil)
}
