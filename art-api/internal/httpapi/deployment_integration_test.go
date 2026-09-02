package httpapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
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

func TestRustDesk149APITokenDeviceDeployment(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "deployment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("deployment-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := domain.User{ID: "deployment-user", Username: "deployer", PasswordHash: hash, Role: domain.RoleUser, Enabled: true,
		ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateUser(ctx, user); err != nil {
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
		httpapi.NewLoginLimiter(10, time.Minute, time.Minute)).Handler()

	accessToken := loginUserWithPassword(t, handler, "deployer", "deployment-password")
	created := apiRequest(t, handler, http.MethodPost, "/api/api-tokens", accessToken, `{"name":"Installer","ttl_days":30}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create API token failed: %d %s", created.Code, created.Body.String())
	}
	var tokenResult struct {
		Token   string          `json:"token"`
		Details domain.APIToken `json:"details"`
	}
	if json.Unmarshal(created.Body.Bytes(), &tokenResult) != nil || !strings.HasPrefix(tokenResult.Token, "art_pat_") || tokenResult.Details.TokenHash != "" {
		t.Fatalf("invalid API token response: %s", created.Body.String())
	}
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	deploy := apiRequest(t, handler, http.MethodPost, "/api/devices/deploy", tokenResult.Token,
		`{"id":"226424246","uuid":"stable-deploy-uuid","pk":"`+publicKey+`"}`)
	if deploy.Code != http.StatusOK || strings.TrimSpace(deploy.Body.String()) != `{"result":"OK"}` {
		t.Fatalf("RustDesk 1.4.9 deployment failed: %d %s", deploy.Code, deploy.Body.String())
	}
	devices, err := repository.ListDevices(ctx)
	if err != nil || len(devices) != 1 || !devices[0].Deployed || devices[0].DeployedBy != user.ID || devices[0].PublicKey != publicKey {
		t.Fatalf("deployed device not persisted: err=%v devices=%#v", err, devices)
	}
	taken := apiRequest(t, handler, http.MethodPost, "/api/devices/deploy", tokenResult.Token,
		`{"id":"226424246","uuid":"different-uuid","pk":"`+publicKey+`"}`)
	if taken.Code != http.StatusOK || !strings.Contains(taken.Body.String(), `"ID_TAKEN"`) {
		t.Fatalf("duplicate ID was not rejected: %d %s", taken.Code, taken.Body.String())
	}
	revoked := apiRequest(t, handler, http.MethodDelete, "/api/api-tokens/"+tokenResult.Details.ID, accessToken, "")
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke API token failed: %d %s", revoked.Code, revoked.Body.String())
	}
	denied := apiRequest(t, handler, http.MethodPost, "/api/devices/deploy", tokenResult.Token,
		`{"id":"226424247","uuid":"other-uuid","pk":"`+publicKey+`"}`)
	if denied.Code != http.StatusUnauthorized || !strings.Contains(denied.Body.String(), `"INVALID_TOKEN"`) {
		t.Fatalf("revoked API token was accepted: %d %s", denied.Code, denied.Body.String())
	}
}

func loginUserWithPassword(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password, "type": "client", "uuid": "deploy-client"})
	response := apiRequest(t, handler, http.MethodPost, "/api/login", "", string(body))
	var output struct {
		AccessToken string `json:"access_token"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &output) != nil || output.AccessToken == "" {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	return output.AccessToken
}
