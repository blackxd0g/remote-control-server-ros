package httpapi_test

import (
	"context"
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

func TestContainLiveConnectionRevokesSessionAndClosesProjection(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "contain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := domain.User{ID: "admin-user", Username: "admin", PasswordHash: hash, DisplayName: "Admin", Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, err := auth.NewService(repository, tokens, hub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	handler := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()
	token := login(t, handler)
	sessions, err := repository.ListSessions(ctx, now)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("unexpected sessions: %#v err=%v", sessions, err)
	}
	record := domain.ConnectionRecord{Key: "target:controller:11:12", ActorUserID: user.ID, ActorSessionID: sessions[0].ID, ControllerDevice: "controller", TargetRustDeskID: "target", StartedAt: now.Add(-time.Minute), LastSeenAt: now}
	if err = repository.UpsertConnection(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/connections/contain", strings.NewReader(`{"key":"target:controller:11:12"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"new_connections_blocked":true`) || !strings.Contains(response.Body.String(), `"transport_interrupted":false`) {
		t.Fatalf("containment failed: status=%d body=%s", response.Code, response.Body.String())
	}
	closed, err := repository.ConnectionRecord(ctx, record.Key)
	if err != nil || closed.ClosedAt == nil {
		t.Fatalf("connection was not closed: %#v err=%v", closed, err)
	}
	revoked, err := repository.FindSession(ctx, sessions[0].ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("session was not revoked: session=%#v err=%v", revoked, err)
	}
}

func TestRelayLifecycleClosesPersistedProjection(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "relay-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, err := auth.NewService(repository, tokens, hub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	handler := httpapi.New(authService, mfaService, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()

	request := httptest.NewRequest(http.MethodPost, "/internal/v1/relay/connections/state", strings.NewReader(`{"uuid":"relay-lifecycle-uuid","state":"active"}`))
	request.Header.Set("X-RDS-Internal-Token", "internal-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("active lifecycle rejected: status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/relay/connections/state", strings.NewReader(`{"uuid":"relay-lifecycle-uuid","state":"closed"}`))
	request.Header.Set("X-RDS-Internal-Token", "internal-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("closed lifecycle rejected: status=%d body=%s", response.Code, response.Body.String())
	}
	record, err := repository.ConnectionRecord(ctx, "relay:relay-lifecycle-uuid")
	if err != nil || record.ClosedAt == nil || record.Transport != "relay" {
		t.Fatalf("relay projection was not closed: %#v err=%v", record, err)
	}
}
