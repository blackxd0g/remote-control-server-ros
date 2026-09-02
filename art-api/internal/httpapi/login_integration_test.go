package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/art-rustdesk/platform/art-api/internal/runtimeconfig"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestOfficialClientLoginLogoutAndDisableFlow(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := domain.User{ID: "admin-user", Username: "admin", PasswordHash: hash, DisplayName: "Admin",
		Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, err := auth.NewService(repository, tokens, hub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	handler := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"),
		httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()

	token := login(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated request failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"Admin"`) || !strings.Contains(response.Body.String(), `"username":"admin"`) {
		t.Fatalf("client-visible name must prefer display name while preserving login: body=%s", response.Body.String())
	}

	for _, path := range []string{
		"/api/admin/devices",
		"/api/admin/groups",
		"/api/admin/sessions",
		"/api/admin/audit",
		"/api/admin/notifications",
		"/api/admin/settings",
		"/api/admin/infrastructure",
		"/api/admin/address-books",
		"/api/admin/relay-servers",
		"/api/admin/acl",
		"/api/admin/strategies",
		"/api/admin/support-bundle",
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("admin endpoint %s failed: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/api/admin/support-bundle", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	archive, archiveErr := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if response.Code != http.StatusOK || archiveErr != nil {
		t.Fatalf("support bundle failed: status=%d err=%v", response.Code, archiveErr)
	}
	for _, entry := range archive.File {
		reader, openErr := entry.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		content, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(content, []byte("correct horse battery staple")) || bytes.Contains(content, []byte("0123456789abcdef")) {
			t.Fatalf("support bundle leaked secret in %s", entry.Name)
		}
	}

	telemetryBody := []byte(`{"id":"builtin-hbbr","name":"Built-in HBBR","hostname":"relay.example.test","port":21117,"region":"msk","connections":3,"bandwidth":4096}`)
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/relay/telemetry", bytes.NewReader(telemetryBody))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("relay telemetry without internal token must be rejected: status=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/relay/telemetry", bytes.NewReader(telemetryBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"connections":3`) || !strings.Contains(response.Body.String(), `"bandwidth":4096`) {
		t.Fatalf("relay telemetry was not persisted: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/internal/v1/relay/select?region=msk", nil)
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"relay_server":"relay.example.test:21117"`) {
		t.Fatalf("telemetry relay is not selectable: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/internal/v1/devices/heartbeat", bytes.NewReader([]byte(`{"rustdesk_id":"987654321","client_uuid":"peer-uuid","last_seen_ip":"192.0.2.44"}`)))
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("device heartbeat failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/services/heartbeat", bytes.NewReader([]byte(`{"service":"hbbs","instance_id":"test-hbbs","online_peers":42}`)))
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"commands":[]`) {
		t.Fatalf("service heartbeat failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/infrastructure/commands", strings.NewReader(`{"service":"hbbs","target_instance":"test-hbbs","type":"reconcile_auth"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("server command queue failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var queued struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response.Body.Bytes(), &queued) != nil || queued.ID == "" {
		t.Fatalf("invalid queued command: %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/notifications", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"server_control_command"`) || !strings.Contains(response.Body.String(), `"unread":1`) {
		t.Fatalf("server-control notification was not persisted: status=%d body=%s", response.Code, response.Body.String())
	}
	var notificationPage struct {
		Notifications []domain.Notification `json:"notifications"`
	}
	if json.Unmarshal(response.Body.Bytes(), &notificationPage) != nil || len(notificationPage.Notifications) != 1 {
		t.Fatalf("invalid notifications response: %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/notifications/"+notificationPage.Notifications[0].ID+"/read", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("notification could not be marked read: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/services/heartbeat", strings.NewReader(`{"service":"hbbs","instance_id":"test-hbbs","online_peers":42}`))
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), queued.ID) || !strings.Contains(response.Body.String(), `"type":"reconcile_auth"`) {
		t.Fatalf("queued command was not delivered: %d %s", response.Code, response.Body.String())
	}
	ackBody := fmt.Sprintf(`{"service":"hbbs","instance_id":"test-hbbs","online_peers":42,"acknowledged_commands":[%q]}`, queued.ID)
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/services/heartbeat", strings.NewReader(ackBody))
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"commands":[]`) {
		t.Fatalf("command acknowledgment failed: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/admin/devices", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("device listing failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, `"rustdesk_id":"987654321"`) || !strings.Contains(body, `"tags":[]`) || !strings.Contains(body, `"last_seen_ip":"192.0.2.44"`) {
		t.Fatalf("device listing must return array tags: body=%s", body)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/admin/infrastructure", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"online_devices":2`) ||
		!strings.Contains(response.Body.String(), `"offline_devices":0`) || !strings.Contains(response.Body.String(), `"hbbs":"online"`) ||
		!strings.Contains(response.Body.String(), `"rendezvous_peers":42`) || !strings.Contains(response.Body.String(), `"relay_connections":3`) {
		t.Fatalf("infrastructure device counters are invalid: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/admin/users", bytes.NewReader([]byte(`{"username":"operator","password":"a","role":"user"}`)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("user creation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var operator struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &operator); err != nil || operator.ID == "" {
		t.Fatalf("invalid created user: %s", response.Body.String())
	}
	evaluationBody := fmt.Sprintf(`{"user_id":%q,"target_id":"987654321","permission":"remote_control"}`, operator.ID)
	request = httptest.NewRequest(http.MethodPost, "/api/admin/acl/evaluate", strings.NewReader(evaluationBody))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"allowed":true`) || !strings.Contains(response.Body.String(), `"reason":"no_acl_rules_configured"`) {
		t.Fatalf("ACL evaluation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/acl", strings.NewReader(`{"name":"Broken target","subject_type":"user","subject_id":"missing-user","target_type":"device","target_id":"987654321","permissions":["remote_control"],"priority":100}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid ACL reference accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/users/"+operator.ID+"/password", bytes.NewReader([]byte(`{"password":"b"}`)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("password reset failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte(`{"username":"operator","password":"b","type":"web","uuid":"password-test"}`)))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.11:40000"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login with reset password failed: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/admin/groups", bytes.NewReader([]byte(`{"name":"Operators","kind":"user"}`)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("group creation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var createdGroup struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &createdGroup); err != nil || createdGroup.ID == "" {
		t.Fatalf("invalid group response: %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPatch, "/api/admin/groups/"+createdGroup.ID, strings.NewReader(`{"name":"Senior Operators","description":"Production access"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Senior Operators"`) {
		t.Fatalf("group update failed: status=%d body=%s", response.Code, response.Body.String())
	}
	updateBody := fmt.Sprintf(`{"username":"operator.renamed","email":"operator@example.test","phone":"+7 999 000-00-00","display_name":"Remote Operator","password":"c","role":"user","enabled":true,"group_ids":[%q]}`, createdGroup.ID)
	request = httptest.NewRequest(http.MethodPatch, "/api/admin/users/"+operator.ID, strings.NewReader(updateBody))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"phone":"+7 999 000-00-00"`) {
		t.Fatalf("full user update failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"username":"operator.renamed"`) || !strings.Contains(response.Body.String(), `"`+createdGroup.ID+`"`) {
		t.Fatalf("updated user or group missing: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodDelete, "/api/admin/users/admin-user", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("current administrator deletion was not blocked: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+operator.ID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("user deletion failed: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/admin/groups/"+createdGroup.ID+"/members/admin-user", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("add group member failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/groups/"+createdGroup.ID+"/members", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"admin-user"`) {
		t.Fatalf("group member listing failed: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/internal/v1/auth/snapshot", nil)
	request.Header.Set("X-ART-Internal-Token", "internal-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `{"group_id":"`+createdGroup.ID+`","user_id":"admin-user","active":true}`) {
		t.Fatalf("membership missing from auth snapshot: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodDelete, "/api/admin/groups/"+createdGroup.ID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("group deletion failed: status=%d body=%s", response.Code, response.Body.String())
	}
	memberships, membershipErr := repository.ListUserGroupMemberships(context.Background())
	if membershipErr != nil {
		t.Fatalf("membership listing failed: %v", membershipErr)
	}
	for _, membership := range memberships {
		if membership.GroupID == createdGroup.ID {
			t.Fatalf("deleted group membership remains: %#v", membership)
		}
	}

	request = httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("logout failed: status=%d body=%s", response.Code, response.Body.String())
	}
	assertRejected(t, handler, token, "session revoked")

	token = login(t, handler)
	request = httptest.NewRequest(http.MethodPost, "/api/admin/users/admin-user/disable", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("disable failed: status=%d body=%s", response.Code, response.Body.String())
	}
	assertRejected(t, handler, token, "user disabled")
}

func TestRegistrationRequiresAdministratorApproval(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "registration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("admin-password")
	now := time.Now().UTC()
	if err = repository.CreateUser(ctx, domain.User{ID: "admin", Username: "admin", PasswordHash: hash, Role: domain.RoleAdmin, Enabled: true, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, _ := auth.NewService(repository, tokens, hub, time.Hour)
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	server := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(10, time.Minute, time.Minute))
	handler := server.EnableRegistration(true, httpapi.NewLoginLimiter(10, time.Minute, time.Minute)).Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(`{"username":"new.user","email":"new@example.test","password":"any password","display_name":"New User"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"pending"`) {
		t.Fatalf("registration failed: %d %s", response.Code, response.Body.String())
	}
	registered, err := repository.FindUserByUsername(ctx, "new.user")
	if err != nil || registered.ApprovalStatus != domain.ApprovalPending {
		t.Fatalf("user is not pending: %#v %v", registered, err)
	}

	pendingLogin := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"new.user","password":"any password","type":"web","uuid":"pending-browser"}`))
	pendingLogin.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, pendingLogin)
	if response.Code != http.StatusOK {
		t.Fatalf("pending account cannot enter account portal: %d %s", response.Code, response.Body.String())
	}

	adminLogin := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"admin-password","type":"web","uuid":"admin-browser"}`))
	adminLogin.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, adminLogin)
	var loginResult struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &loginResult)
	approve := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+registered.ID+"/approve", nil)
	approve.Header.Set("Authorization", "Bearer "+loginResult.AccessToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, approve)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"approval_status":"approved"`) {
		t.Fatalf("approval failed: %d %s", response.Code, response.Body.String())
	}
	memberships, _ := repository.ListUserGroupMemberships(ctx)
	found := false
	for _, membership := range memberships {
		if membership.UserID == registered.ID && membership.GroupID == domain.ApprovedUsersGroupID {
			found = true
		}
	}
	if !found {
		t.Fatal("approved user was not moved to the authorized group")
	}
}

func TestRegistrationCanAutomaticallyApproveNewUsers(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "automatic-approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, _ := auth.NewService(repository, tokens, hub, time.Hour)
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	configuration, err := runtimeconfig.New(ctx, repository, runtimeconfig.Values{RegistrationEnabled: true, RegistrationAutoApprove: true, AccessTokenTTL: time.Hour, SessionTTL: time.Hour, MFAMode: "optional"})
	if err != nil {
		t.Fatal(err)
	}
	server := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(10, time.Minute, time.Minute))
	handler := server.EnableRegistration(true, httpapi.NewLoginLimiter(10, time.Minute, time.Minute)).EnableRuntimeConfig(configuration).Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(`{"username":"auto.user","email":"auto@example.test","password":"any password","display_name":"Auto User"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"approved"`) {
		t.Fatalf("automatic registration failed: %d %s", response.Code, response.Body.String())
	}
	registered, err := repository.FindUserByUsername(ctx, "auto.user")
	if err != nil || registered.ApprovalStatus != domain.ApprovalApproved {
		t.Fatalf("user was not approved: %#v %v", registered, err)
	}
	memberships, err := repository.ListUserGroupMemberships(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, membership := range memberships {
		if membership.UserID == registered.ID && membership.GroupID == domain.ApprovedUsersGroupID {
			return
		}
	}
	t.Fatal("automatically approved user was not assigned to the approved users group")
}

func TestOfficialClientTFAChallengeAndOneTimeRecoveryCode(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "mfa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("password")
	now := time.Now().UTC()
	user := domain.User{ID: "mfa-user", Username: "mfa", PasswordHash: hash, Role: domain.RoleUser, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, _ := auth.NewService(repository, tokens, hub, time.Hour)
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	enrollment, err := mfaService.Begin(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	user, _ = repository.FindUserByID(ctx, user.ID)
	code, _ := mfa.Code(enrollment.Secret, time.Now().UTC())
	if _, err = mfaService.Confirm(ctx, user, code); err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(20, time.Minute, time.Minute)).Handler()

	challenge := clientTFAChallenge(t, handler)
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte(fmt.Sprintf(`{"type":"tfa_code","tfaCode":%q,"secret":%q}`, code, challenge))))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"access_token"`) {
		t.Fatalf("official TOTP completion failed: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte(fmt.Sprintf(`{"type":"tfa_code","tfaCode":%q,"secret":%q}`, code, challenge))))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed challenge accepted: %d %s", response.Code, response.Body.String())
	}

	recovery := enrollment.RecoveryCodes[0]
	challenge = clientTFAChallenge(t, handler)
	request = httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte(fmt.Sprintf(`{"type":"tfa_code","tfaCode":%q,"secret":%q}`, recovery, challenge))))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("recovery login failed: %d %s", response.Code, response.Body.String())
	}
	remaining, _ := repository.CountMFARecoveryCodes(ctx, user.ID)
	if remaining != 9 {
		t.Fatalf("expected 9 recovery codes, got %d", remaining)
	}

	challenge = clientTFAChallenge(t, handler)
	request = httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte(fmt.Sprintf(`{"type":"tfa_code","tfaCode":%q,"secret":%q}`, recovery, challenge))))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code accepted: %d %s", response.Code, response.Body.String())
	}
}

func clientTFAChallenge(t *testing.T, handler http.Handler) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte(`{"username":"mfa","password":"password","id":"123","uuid":"uuid","type":"account","deviceInfo":{"os":"Windows","type":"client","name":"PC"}}`)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var output struct {
		Type   string `json:"type"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil || response.Code != http.StatusOK || output.Type != "tfa_check" || output.Secret == "" {
		t.Fatalf("invalid TFA challenge: %d %s", response.Code, response.Body.String())
	}
	return output.Secret
}

func login(t *testing.T, handler http.Handler) string {
	t.Helper()
	body := []byte(`{"username":"admin","password":"correct horse battery staple","id":"123456789","uuid":"client-uuid","type":"client","autoLogin":true,"deviceInfo":{"os":"Windows","type":"desktop","name":"PC"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	request.RemoteAddr = "192.0.2.10:40000"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var output struct {
		AccessToken string `json:"access_token"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.AccessToken == "" || output.Type != "access_token" {
		t.Fatalf("incompatible login response: %s", response.Body.String())
	}
	return output.AccessToken
}

func assertRejected(t *testing.T, handler http.Handler, token, reason string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !bytes.Contains(response.Body.Bytes(), []byte(reason)) {
		t.Fatal(fmt.Sprintf("expected rejection %q, got status=%d body=%s", reason, response.Code, response.Body.String()))
	}
}
