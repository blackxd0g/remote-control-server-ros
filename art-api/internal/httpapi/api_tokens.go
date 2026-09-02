package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/google/uuid"
)

const deployScope = "devices:deploy"

func (s *Server) listAPITokens(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	values, err := s.repository.ListAPITokens(request.Context(), principal.User.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "API tokens unavailable")
		return
	}
	writeJSON(response, http.StatusOK, values)
}

func (s *Server) createAPIToken(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input struct {
		Name    string `json:"name"`
		TTLDays int    `json:"ttl_days"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid API token")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 128 || input.TTLDays < 1 || input.TTLDays > 365 {
		writeError(response, http.StatusBadRequest, "invalid API token")
		return
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		writeError(response, http.StatusInternalServerError, "API token generation failed")
		return
	}
	secret := "art_pat_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	now := time.Now().UTC()
	value := domain.APIToken{ID: uuid.NewString(), UserID: principal.User.ID, Name: input.Name, TokenHash: hashAPIToken(secret),
		Prefix: secret[:16], Scopes: []string{deployScope}, CreatedAt: now, ExpiresAt: now.Add(time.Duration(input.TTLDays) * 24 * time.Hour)}
	if err := s.repository.CreateAPIToken(request.Context(), value); err != nil {
		writeError(response, http.StatusInternalServerError, "API token creation failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "api_token_create", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, IP: clientIP(request), Result: "success", Metadata: map[string]any{"token_id": value.ID, "scopes": value.Scopes}})
	writeJSON(response, http.StatusCreated, map[string]any{"token": secret, "details": value})
}

func (s *Server) revokeAPIToken(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if err := s.repository.RevokeAPIToken(request.Context(), principal.User.ID, request.PathValue("tokenID"), time.Now().UTC()); errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusNotFound, "API token not found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "API token revoke failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "api_token_revoke", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, IP: clientIP(request), Result: "success", Metadata: map[string]any{"token_id": request.PathValue("tokenID")}})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) deployDevice(response http.ResponseWriter, request *http.Request) {
	secret := bearerToken(request.Header.Get("Authorization"))
	token, err := s.repository.FindAPITokenByHash(request.Context(), hashAPIToken(secret))
	if err != nil || secret == "" || token.RevokedAt != nil || !token.ExpiresAt.After(time.Now().UTC()) || !hasScope(token.Scopes, deployScope) {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"result": "INVALID_TOKEN"})
		return
	}
	user, err := s.repository.FindUserByID(request.Context(), token.UserID)
	if err != nil || !user.Enabled || user.ApprovalStatus != domain.ApprovalApproved {
		writeJSON(response, http.StatusForbidden, map[string]string{"result": "INVALID_TOKEN"})
		return
	}
	var input struct {
		ID   string `json:"id"`
		UUID string `json:"uuid"`
		PK   string `json:"pk"`
	}
	if decodeInventoryJSON(request, &input, 16<<10) != nil {
		writeJSON(response, http.StatusOK, map[string]string{"result": "INVALID_INPUT"})
		return
	}
	input.ID, input.UUID, input.PK = strings.TrimSpace(input.ID), strings.TrimSpace(input.UUID), strings.TrimSpace(input.PK)
	publicKey, keyErr := base64.StdEncoding.DecodeString(input.PK)
	if len(input.ID) < 3 || len(input.ID) > 64 || input.UUID == "" || len(input.UUID) > 512 || keyErr != nil || len(publicKey) != 32 {
		writeJSON(response, http.StatusOK, map[string]string{"result": "INVALID_INPUT"})
		return
	}
	device, exists, mismatch, err := s.inventoryDevice(request, input.ID, input.UUID)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]string{"result": "SERVER_ERROR"})
		return
	}
	if exists && mismatch {
		writeJSON(response, http.StatusOK, map[string]string{"result": "ID_TAKEN"})
		return
	}
	now := time.Now().UTC()
	device.RustDeskID, device.ClientUUID, device.PublicKey = input.ID, input.UUID, input.PK
	device.Deployed, device.DeployedBy, device.DeployedAt = true, user.ID, now
	device.OwnerUserID = firstNonEmpty(device.OwnerUserID, user.ID)
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if err := s.repository.UpsertDevice(request.Context(), device); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]string{"result": "SERVER_ERROR"})
		return
	}
	_ = s.repository.TouchAPIToken(request.Context(), token.ID, now)
	s.hub.Publish(events.DeviceUpdated, device)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_deploy", ActorUserID: user.ID, TargetRustDeskID: input.ID,
		IP: clientIP(request), Result: "success", Metadata: map[string]any{"token_id": token.ID}})
	writeJSON(response, http.StatusOK, map[string]string{"result": "OK"})
}

func hashAPIToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func hasScope(scopes []string, expected string) bool {
	for _, scope := range scopes {
		if scope == expected {
			return true
		}
	}
	return false
}
