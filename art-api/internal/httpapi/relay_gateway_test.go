package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/httpapi"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelaySecureControlEnrollmentPermitAndRevoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("correct horse battery staple")
	now := time.Now().UTC()
	if err = repository.CreateUser(ctx, domain.User{ID: "admin", Username: "admin", PasswordHash: hash, Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	service, err := auth.NewService(repository, tokens, hub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	handler := httpapi.New(service, mfaService, audit.New(repository), repository, hub, []byte("test-internal-secret"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()
	admin := login(t, handler)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	call := func(method, path, token string, input any) (int, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(input)
		request, _ := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var value map[string]any
		_ = json.NewDecoder(response.Body).Decode(&value)
		return response.StatusCode, value
	}
	code, value := call("POST", "/api/admin/relay-servers", admin, map[string]any{"name": "Gateway relay", "hostname": "relay.example", "port": 21117})
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	id := value["id"].(string)
	code, value = call("POST", "/api/admin/relay-servers/"+id+"/enrollment", admin, nil)
	if code != 201 {
		t.Fatalf("enrollment: %d", code)
	}
	enrollment := value["enrollment_token"].(string)
	// A forged proxy header from an untrusted peer cannot allow plaintext enrollment.
	insecure := httptest.NewRequest("POST", "http://localhost/api/relay/enroll", strings.NewReader(`{}`))
	insecure.Header.Set("X-Forwarded-Proto", "https")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, insecure)
	if denied.Code != 400 {
		t.Fatal("forged proxy TLS accepted")
	}
	code, value = call("POST", "/api/relay/enroll", "", map[string]string{"relay_id": id, "enrollment_token": enrollment})
	if code != 201 {
		t.Fatalf("enroll: %d", code)
	}
	token := value["credential"].(string)
	if err = repository.CreateUser(ctx, domain.User{ID: "ordinary", Username: "ordinary", PasswordHash: hash, Role: domain.RoleUser, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	loginCode, loginValue := call("POST", "/api/login", "", map[string]string{"username": "ordinary", "password": "correct horse battery staple", "type": "client", "uuid": "ordinary-test-client"})
	if loginCode != 200 {
		t.Fatal("ordinary fixture login failed")
	}
	ordinary := loginValue["access_token"].(string)
	for _, route := range []struct{ method, path string }{{"GET", "/quarantine"}, {"GET", "/quarantine/export"}, {"POST", "/quarantine/archive"}} {
		for _, credential := range []string{"", token, ordinary} {
			if code, _ := call(route.method, "/api/admin/relay-servers/"+id+route.path, credential, nil); code < 400 {
				t.Fatal("unprivileged quarantine access")
			}
		}
	}
	unavailableAudit := audit.New(failingQuarantineAudit{repository})
	failHandler := httpapi.New(service, mfaService, unavailableAudit, repository, hub, []byte("test-internal-secret"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()
	requestFail := httptest.NewRequest("POST", "/api/admin/relay-servers/"+id+"/quarantine/archive", strings.NewReader(`{"revision":"00000000-0000-4000-8000-000000000001","confirm":true}`))
	requestFail.Header.Set("Authorization", "Bearer "+admin)
	resultFail := httptest.NewRecorder()
	failHandler.ServeHTTP(resultFail, requestFail)
	if resultFail.Code != 503 || !strings.Contains(resultFail.Body.String(), "command not sent") {
		t.Fatal("archive did not fail closed on audit failure")
	}
	for _, route := range []struct{ method, path string }{{"GET", "/delivery"}, {"POST", "/delivery/compact"}} {
		if code, _ := call(route.method, "/api/admin/relay-servers/"+id+route.path, token, nil); code < 400 {
			t.Fatal("relay credential accessed operations")
		}
		if code, _ := call(route.method, "/api/admin/relay-servers/"+id+route.path, "", nil); code < 400 {
			t.Fatal("anonymous accessed operations")
		}
		if code, _ := call(route.method, "/api/admin/relay-servers/"+id+route.path, admin, nil); code != 200 {
			t.Fatalf("admin operations: %d", code)
		}
	}
	code, _ = call("POST", "/api/relay/enroll", "", map[string]string{"relay_id": id, "enrollment_token": enrollment})
	if code != 401 {
		t.Fatal("enrollment reused")
	}
	code, _ = call("GET", "/api/admin/users", token, nil)
	if code == 200 {
		t.Fatal("relay credential gained admin rights")
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/internal/v1/auth/snapshot", nil)
	request.Header.Set("X-RDS-Internal-Token", token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode == 200 {
		t.Fatal("relay read auth snapshot")
	}
	wsURL := "wss" + strings.TrimPrefix(server.URL, "https") + "/api/relay/" + id + "/control"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var hello relaygateway.Message
	if err = wsjson.Read(ctx, conn, &hello); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Write(ctx, conn, relaygateway.Message{Type: "ready", Session: hello.Session}); err != nil {
		t.Fatal(err)
	}
	// Telemetry sent after ready also proves that the reader is advancing.
	time.Sleep(30 * time.Millisecond)
	result := make(chan int, 1)
	go func() {
		data := strings.NewReader(`{"address":"relay.example:21117","uuid":"test-uuid-0001"}`)
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/internal/v1/relay/permit", data)
		req.Header.Set("X-RDS-Internal-Token", "test-internal-secret")
		resp, err := server.Client().Do(req)
		if err != nil {
			result <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		result <- resp.StatusCode
	}()
	var command relaygateway.Message
	if err = wsjson.Read(ctx, conn, &command); err != nil {
		t.Fatal(err)
	}
	if command.Action != "permit" || command.Session != hello.Session {
		t.Fatal("unexpected command")
	}
	select {
	case <-result:
		t.Fatal("permit returned before acknowledgement")
	default:
	}
	if err = wsjson.Write(ctx, conn, relaygateway.Message{Type: "ack", Session: hello.Session, RequestID: command.RequestID, UUID: "foreign-uuid", Status: "permitted"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-result:
		t.Fatal("uncorrelated acknowledgement accepted")
	case <-time.After(30 * time.Millisecond):
	}
	if err = wsjson.Write(ctx, conn, relaygateway.Message{Type: "ack", Session: hello.Session, RequestID: command.RequestID, UUID: command.UUID, Status: "permitted"}); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 200 {
		t.Fatalf("permit response: %d", code)
	}
	code, _ = call("DELETE", "/api/admin/relay-servers/"+id+"/credential", admin, nil)
	if code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("revoked channel remains open")
	}
	_, response, err = websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	if err == nil {
		t.Fatal("revoked credential reconnected")
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
}

type failingQuarantineAudit struct{ domain.Repository }

func (failingQuarantineAudit) AppendAudit(context.Context, domain.AuditEvent) error {
	return errors.New("synthetic audit outage")
}
