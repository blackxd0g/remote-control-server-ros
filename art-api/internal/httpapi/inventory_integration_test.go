package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/httpapi"
	"github.com/art-rustdesk/platform/art-api/internal/managedclient"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestOfficialClientInventoryFlowAndUUIDProtection(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("inventory-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := domain.User{ID: "inventory-admin", Username: "inventory", PasswordHash: hash, DisplayName: "Inventory Admin",
		Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
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
	server := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"),
		httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).EnableManagedClients(managedclient.New(repository, []byte("0123456789abcdef0123456789abcdef")))
	handler := server.Handler()

	loginBody := `{"username":"inventory","password":"inventory-password","id":"100200300","uuid":"stable-device-uuid","type":"desktop","deviceInfo":{"os":"Windows","type":"desktop","name":"WORKSTATION"}}`
	loginResponse := inventoryRequest(handler, http.MethodPost, "/api/login", "", loginBody)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginResponse.Code, loginResponse.Body.String())
	}
	var loginPayload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginPayload); err != nil {
		t.Fatal(err)
	}

	sysinfo := `{"id":"100200300","uuid":"stable-device-uuid","hostname":"WORKSTATION-01","os":"Windows 11","username":"operator","version":"1.4.2","cpu":"AMD64 / 8","memory":"16 GB","preset-address-book-name":"future-compatible"}`
	response := inventoryRequest(handler, http.MethodPost, "/api/sysinfo", "", sysinfo)
	if response.Code != http.StatusOK || response.Body.String() != "SYSINFO_UPDATED" {
		t.Fatalf("sysinfo failed: %d %q", response.Code, response.Body.String())
	}

	strategyUpdated := now.Add(time.Minute).Truncate(time.Millisecond)
	if err := repository.CreateStrategy(ctx, domain.Strategy{ID: "global-strategy", Name: "Global", ScopeType: "global", Priority: 50,
		Settings: map[string]any{"allow_file_transfer": false, "require_login": true}, Enabled: true, CreatedAt: now, UpdatedAt: strategyUpdated}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateStrategy(ctx, domain.Strategy{ID: "device-strategy", Name: "Device", ScopeType: "device", ScopeID: "100200300", Priority: 50,
		Settings: map[string]any{"allow_file_transfer": true, "rustdesk.theme": "dark"}, Enabled: true, CreatedAt: now, UpdatedAt: strategyUpdated}); err != nil {
		t.Fatal(err)
	}
	profileUpdated := now.Add(2 * time.Minute).Truncate(time.Millisecond)
	if err := repository.CreateClientProfile(ctx, domain.ClientProfile{ID: "managed-profile", Name: "Managed Windows", Platform: "all", Settings: map[string]any{"force_relay": true}, Branding: map[string]any{}, Version: 1, Enabled: true, CreatedAt: now, UpdatedAt: profileUpdated}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateClientProfileAssignment(ctx, domain.ClientProfileAssignment{ID: "managed-global", ProfileID: "managed-profile", ScopeType: "global", Priority: 100, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	heartbeat := `{"id":"100200300","uuid":"stable-device-uuid","ver":1040200,"modified_at":17}`
	response = inventoryRequest(handler, http.MethodPost, "/api/heartbeat", "", heartbeat)
	if response.Code != http.StatusOK {
		t.Fatalf("heartbeat failed: %d %s", response.Code, response.Body.String())
	}
	var heartbeatPayload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &heartbeatPayload); err != nil {
		t.Fatal(err)
	}
	if heartbeatPayload["modified_at"] != float64(profileUpdated.UnixMilli()) {
		t.Fatalf("modified_at not preserved: %#v", heartbeatPayload)
	}
	strategyPayload, ok := heartbeatPayload["strategy"].(map[string]any)
	if !ok {
		t.Fatalf("strategy missing: %#v", heartbeatPayload)
	}
	options, ok := strategyPayload["config_options"].(map[string]any)
	if !ok || options["enable-file-transfer"] != "Y" || options["theme"] != "dark" || options["force-relay"] != "Y" {
		t.Fatalf("strategy options incompatible: %#v", strategyPayload)
	}
	if _, leaked := options["require_login"]; leaked {
		t.Fatalf("server-only setting leaked: %#v", options)
	}
	response = inventoryRequest(handler, http.MethodGet, "/api/admin/strategies/schema", loginPayload.AccessToken, "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"key":"require_login"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"server_only":true`)) {
		t.Fatalf("strategy schema failed: %d %s", response.Code, response.Body.String())
	}
	response = inventoryRequest(handler, http.MethodPost, "/api/admin/strategies/evaluate", loginPayload.AccessToken, `{"device_id":"100200300"}`)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"allow_file_transfer":true`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"matched_strategy_ids":["global-strategy","device-strategy"]`)) {
		t.Fatalf("strategy evaluation failed: %d %s", response.Code, response.Body.String())
	}

	response = inventoryRequest(handler, http.MethodGet, "/api/peers", loginPayload.AccessToken, "")
	if response.Code != http.StatusOK {
		t.Fatalf("peers failed: %d %s", response.Code, response.Body.String())
	}
	var peers struct {
		Total int `json:"total"`
		Data  []struct {
			ID     string            `json:"id"`
			Info   map[string]string `json:"info"`
			Status int               `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &peers); err != nil {
		t.Fatal(err)
	}
	if peers.Total != 1 || peers.Data[0].ID != "100200300" || peers.Data[0].Info["os"] != "Windows 11" || peers.Data[0].Status != 1 {
		t.Fatalf("incompatible peer payload: %#v", peers)
	}

	response = inventoryRequest(handler, http.MethodPost, "/api/sysinfo_ver", "", "")
	if response.Code != http.StatusOK || response.Body.String() == "" {
		t.Fatalf("sysinfo version failed: %d %q", response.Code, response.Body.String())
	}
	response = inventoryRequest(handler, http.MethodGet, "/api/users?current=1&pageSize=100", loginPayload.AccessToken, "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"total":1`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"username":"inventory"`)) {
		t.Fatalf("client users failed: %d %s", response.Code, response.Body.String())
	}
	response = inventoryRequest(handler, http.MethodGet, "/api/device-group/accessible?current=1&pageSize=100", loginPayload.AccessToken, "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"total":0`)) {
		t.Fatalf("accessible device groups failed: %d %s", response.Code, response.Body.String())
	}
	response = inventoryRequest(handler, http.MethodPost, "/api/audit/conn", "", `{"action":"new","conn_id":7,"id":"100200300","peer":["400500600","Operator"],"session_id":9.5,"type":1,"uuid":"stable-device-uuid"}`)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"code":0`)) {
		t.Fatalf("client connection audit failed: %d %s", response.Code, response.Body.String())
	}
	response = inventoryRequest(handler, http.MethodPost, "/api/audit/file", "", `{"id":"100200300","info":"{\"name\":\"Operator\",\"num\":1}","is_file":true,"path":"C:\\Documents\\report.pdf","peer_id":"100200300","type":1,"uuid":"stable-device-uuid"}`)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"code":0`)) {
		t.Fatalf("client file audit failed: %d %s", response.Code, response.Body.String())
	}
	auditEvents, err := repository.ListAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var connectionSeen, fileSeen bool
	for _, event := range auditEvents {
		connectionSeen = connectionSeen || event.Type == "connection_started" && event.TargetRustDeskID == "100200300"
		fileSeen = fileSeen || event.Type == "file_transfer" && event.ControllerDevice == "100200300" && event.ActorUserID == user.ID && event.Metadata["file_name"] == "report.pdf"
	}
	if !connectionSeen || !fileSeen {
		t.Fatalf("client audit events not persisted: %#v", auditEvents)
	}
	response = inventoryRequest(handler, http.MethodPost, "/api/audit/conn", "", `{"action":"new","conn_id":8,"id":"100200300","peer":["attacker"],"session_id":10,"type":1,"uuid":"attacker-uuid"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("forged client audit accepted: %d %s", response.Code, response.Body.String())
	}

	rotated := `{"id":"100200300","uuid":"rotated-device-uuid","hostname":"WORKSTATION-01","os":"Windows 11","username":"operator","version":"1.4.9"}`
	response = inventoryRequest(handler, http.MethodPost, "/api/sysinfo", "", rotated)
	if response.Code != http.StatusOK || response.Body.String() != "SYSINFO_UPDATED" {
		t.Fatalf("ordinary client UUID rotation rejected: %d %q", response.Code, response.Body.String())
	}
	devices, err := repository.ListDevices(ctx)
	if err != nil || len(devices) != 1 || devices[0].ClientUUID != "rotated-device-uuid" {
		t.Fatalf("ordinary client UUID was not updated: devices=%#v err=%v", devices, err)
	}
	devices[0].Deployed = true
	if err = repository.UpsertDevice(ctx, devices[0]); err != nil {
		t.Fatal(err)
	}
	forged := `{"id":"100200300","uuid":"attacker-uuid","hostname":"ATTACKER","os":"Other","username":"bad","version":"9"}`
	response = inventoryRequest(handler, http.MethodPost, "/api/sysinfo", "", forged)
	if response.Code != http.StatusOK || response.Body.String() != "ID_NOT_FOUND" {
		t.Fatalf("UUID mismatch not rejected: %d %q", response.Code, response.Body.String())
	}
	devices, err = repository.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Hostname != "WORKSTATION-01" || devices[0].ClientUUID != "rotated-device-uuid" || devices[0].OwnerUserID != user.ID {
		t.Fatalf("device identity was modified: %#v", devices)
	}
}

func inventoryRequest(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
