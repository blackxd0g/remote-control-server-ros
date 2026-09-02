package httpapi

import (
	"errors"
	"html/template"
	"net/http"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/identity"
	"github.com/art-rustdesk/platform/art-api/internal/oidcauth"
)

type oidcAuthRequest struct {
	Op, ID, UUID, APIDomain string
	DeviceInfo              deviceInfo `json:"deviceInfo"`
}

func (s *Server) oidcAuth(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeError(response, 404, "OIDC is not configured")
		return
	}
	var input oidcAuthRequest
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, 400, "invalid request")
		return
	}
	if input.Op != "" && input.Op != s.oidc.Name() {
		writeError(response, 400, "unknown OIDC provider")
		return
	}
	authorization, err := s.oidc.Begin(request.Context(), identity.LoginContext{RustDeskID: input.ID, ClientUUID: input.UUID, Platform: input.DeviceInfo.OS, ClientType: input.DeviceInfo.Type, DeviceName: input.DeviceInfo.Name, IP: clientIP(request), UserAgent: request.UserAgent()})
	if err != nil {
		writeError(response, 502, "OIDC provider unavailable")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "oidc_authorization_started", ControllerDevice: firstNonEmpty(input.UUID, input.ID), IP: clientIP(request), Result: "success"})
	writeJSON(response, 200, map[string]string{"code": authorization.PollCode, "url": authorization.URL})
}

func (s *Server) oidcCallback(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeError(response, 404, "OIDC is not configured")
		return
	}
	state, code := request.URL.Query().Get("state"), request.URL.Query().Get("code")
	message, success := "Authentication failed. You may close this window.", false
	if state != "" {
		if err := s.oidc.Callback(request.Context(), state, code); err == nil {
			message, success = "Authentication completed. You may return to the client.", true
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(200)
	_ = template.Must(template.New("callback").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Remote Control Server RouterOS</title></head><body style="font:16px system-ui;background:#0b1220;color:#eef;padding:10vh 8vw"><h1>{{if .Success}}Signed in{{else}}Sign-in failed{{end}}</h1><p>{{.Message}}</p></body></html>`)).Execute(response, map[string]any{"Success": success, "Message": message})
}

func (s *Server) oidcAuthQuery(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeError(response, 404, "OIDC is not configured")
		return
	}
	query := request.URL.Query()
	record, user, err := s.oidc.Consume(request.Context(), query.Get("code"), query.Get("id"), query.Get("uuid"))
	if errors.Is(err, oidcauth.ErrPending) {
		writeJSON(response, 200, map[string]string{"message": "Authorization in progress, please login", "error": "No authed oidc is found"})
		return
	}
	if err != nil {
		writeError(response, 401, err.Error())
		return
	}
	if !user.Enabled {
		writeError(response, 403, "user disabled")
		return
	}
	result, err := s.auth.CompleteLogin(request.Context(), user, auth.LoginInput{Username: user.Username, IP: record.IP, UserAgent: record.UserAgent, ClientDeviceID: firstNonEmpty(record.ClientUUID, record.RustDeskID)})
	if err != nil {
		writeError(response, 500, "OIDC login failed")
		return
	}
	now := time.Now().UTC()
	if record.RustDeskID != "" {
		device, _, mismatch, inventoryErr := s.inventoryDevice(request, record.RustDeskID, record.ClientUUID)
		if inventoryErr == nil && !mismatch {
			device.RustDeskID, device.ClientUUID = record.RustDeskID, record.ClientUUID
			if device.Hostname == "" {
				device.Hostname = record.DeviceName
			}
			if device.Platform == "" {
				device.Platform = record.Platform
			}
			device.Online, device.LastSeen, device.LastSeenIP, device.OwnerUserID = true, now, record.IP, user.ID
			if device.CreatedAt.IsZero() {
				device.CreatedAt = now
			}
			_ = s.repository.UpsertDevice(request.Context(), device)
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "login_success", ActorUserID: user.ID, ActorSessionID: result.Session.ID, ControllerDevice: firstNonEmpty(record.ClientUUID, record.RustDeskID), IP: record.IP, Result: "allowed", Metadata: map[string]any{"provider": record.Provider}})
	writeJSON(response, 200, map[string]any{"access_token": result.AccessToken, "type": "access_token", "expires_at": result.Claims.ExpiresAt.Time.UTC().Format(time.RFC3339), "user": clientUser(result.User)})
}

func (s *Server) listMyOIDCIdentities(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	s.listOIDCIdentities(response, request, principal.User.ID)
}

func (s *Server) listAdminOIDCIdentities(response http.ResponseWriter, request *http.Request) {
	s.listOIDCIdentities(response, request, request.URL.Query().Get("user_id"))
}

func (s *Server) listOIDCIdentities(response http.ResponseWriter, request *http.Request, userID string) {
	values, err := s.repository.ListOIDCIdentities(request.Context(), userID)
	if err != nil {
		writeError(response, 500, "OIDC identities unavailable")
		return
	}
	writeJSON(response, 200, values)
}

func (s *Server) beginOIDCLink(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeError(response, 404, "OIDC is not configured")
		return
	}
	principal, _ := principalFrom(request.Context())
	authorization, err := s.oidc.Begin(request.Context(), identity.LoginContext{LinkUserID: principal.User.ID, IP: clientIP(request), UserAgent: request.UserAgent()})
	if err != nil {
		writeError(response, 502, "OIDC provider unavailable")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "oidc_link_started", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, IP: clientIP(request), Result: "success"})
	writeJSON(response, 200, map[string]string{"code": authorization.PollCode, "url": authorization.URL})
}

func (s *Server) queryOIDCLink(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeError(response, 404, "OIDC is not configured")
		return
	}
	principal, _ := principalFrom(request.Context())
	value, err := s.oidc.ConsumeLink(request.Context(), request.URL.Query().Get("code"), principal.User.ID)
	if errors.Is(err, oidcauth.ErrPending) {
		writeJSON(response, 200, map[string]any{"pending": true})
		return
	}
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "oidc_identity_linked", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, IP: clientIP(request), Result: "success", Metadata: map[string]any{"provider": value.Provider, "subject": value.Subject}})
	writeJSON(response, 200, value)
}

func (s *Server) deleteMyOIDCIdentity(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	s.deleteOIDCIdentity(response, request, principal.User.ID, principal)
}

func (s *Server) deleteAdminOIDCIdentity(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	userID := request.URL.Query().Get("user_id")
	if userID == "" {
		writeError(response, 400, "user_id is required")
		return
	}
	s.deleteOIDCIdentity(response, request, userID, principal)
}

func (s *Server) deleteOIDCIdentity(response http.ResponseWriter, request *http.Request, userID string, actor Principal) {
	provider, subject := request.URL.Query().Get("provider"), request.URL.Query().Get("subject")
	if provider == "" || subject == "" {
		writeError(response, 400, "provider and subject are required")
		return
	}
	if err := s.repository.DeleteOIDCIdentity(request.Context(), provider, subject, userID); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "OIDC identity not found")
		return
	} else if err != nil {
		writeError(response, 500, "OIDC identity removal failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "oidc_identity_unlinked", ActorUserID: actor.User.ID, ActorSessionID: actor.Session.ID, IP: clientIP(request), Result: "success", Metadata: map[string]any{"provider": provider, "subject": subject, "user_id": userID}})
	response.WriteHeader(http.StatusNoContent)
}
