package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/httpapi"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestCustomRolePermissionsAreEnforcedByAPI(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "rbac.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	role := domain.RoleDefinition{ID: "device-auditor", Name: "Device auditor", Permissions: []string{domain.PermissionAdminPortal, domain.PermissionDevicesRead}, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateRole(ctx, role); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("rbac-password")
	if err = repository.CreateUser(ctx, domain.User{ID: "rbac-user", Username: "auditor", PasswordHash: hash, Role: role.ID, Enabled: true, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, _ := auth.NewService(repository, tokens, hub, time.Hour)
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	handler := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()
	token := loginAs(t, handler, "auditor", "rbac-password")

	assertRBACStatus(t, handler, token, http.MethodGet, "/api/admin/devices", http.StatusOK)
	assertRBACStatus(t, handler, token, http.MethodGet, "/api/admin/users", http.StatusForbidden)
	assertRBACStatus(t, handler, token, http.MethodGet, "/api/admin/roles", http.StatusForbidden)
	assertRBACStatus(t, handler, token, http.MethodGet, "/api/admin/support-bundle", http.StatusForbidden)

	request := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"devices.read"`) || strings.Contains(response.Body.String(), `"users.write"`) {
		t.Fatalf("wrong effective permissions: %d %s", response.Code, response.Body.String())
	}
}

func loginAs(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": username, "password": password, "type": "web", "uuid": "rbac-test", "deviceInfo": map[string]string{"os": "test", "type": "web", "name": "test"}})
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.AccessToken == "" {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	return result.AccessToken
}

func assertRBACStatus(t *testing.T, handler http.Handler, token, method, path string, expected int) {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != expected {
		t.Fatalf("%s: expected %d, got %d: %s", path, expected, response.Code, response.Body.String())
	}
}
