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

func TestAddressBookOwnershipGrantsAndClientCompatibility(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "address-books.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("address-book-test-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	users := []domain.User{
		{ID: "admin", Username: "admin", Role: domain.RoleAdmin},
		{ID: "reader", Username: "reader", Role: domain.RoleUser},
		{ID: "writer", Username: "writer", Role: domain.RoleUser},
		{ID: "outsider", Username: "outsider", Role: domain.RoleUser},
	}
	for _, user := range users {
		user.PasswordHash, user.Enabled, user.TokenVersion = hash, true, 1
		user.CreatedAt, user.UpdatedAt = now, now
		if err := repository.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	group := domain.Group{ID: "operators", Name: "Operators", Kind: domain.GroupKindUser, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetUserGroupMember(context.Background(), group.ID, "writer", true); err != nil {
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
		httpapi.NewLoginLimiter(20, time.Minute, time.Minute)).Handler()

	adminToken := loginUser(t, handler, "admin")
	readerToken := loginUser(t, handler, "reader")
	writerToken := loginUser(t, handler, "writer")
	outsiderToken := loginUser(t, handler, "outsider")

	created := apiRequest(t, handler, http.MethodPost, "/api/address-books", adminToken, `{"name":"Support fleet","kind":"shared"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create shared book failed: status=%d body=%s", created.Code, created.Body.String())
	}
	var book domain.AddressBook
	if err := json.Unmarshal(created.Body.Bytes(), &book); err != nil || book.ID == "" {
		t.Fatalf("invalid address book response: %s", created.Body.String())
	}

	for _, grant := range []string{
		`{"subject_type":"user","subject_id":"reader","permission":"read"}`,
		`{"subject_type":"user_group","subject_id":"operators","permission":"write"}`,
	} {
		response := apiRequest(t, handler, http.MethodPut, "/api/address-books/"+book.ID+"/grants", adminToken, grant)
		if response.Code != http.StatusOK {
			t.Fatalf("grant failed: status=%d body=%s", response.Code, response.Body.String())
		}
	}

	response := apiRequest(t, handler, http.MethodPost, "/api/address-books/"+book.ID+"/entries", adminToken, `{"rustdesk_id":"100000001","alias":"Gateway"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("admin entry creation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodGet, "/api/address-books", readerToken, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"permission":"read"`) || strings.Contains(response.Body.String(), `"can_manage":true`) {
		t.Fatalf("reader effective permission is invalid: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/address-books/"+book.ID+"/entries", readerToken, `{"rustdesk_id":"100000002"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only user modified shared book: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/address-books/"+book.ID+"/entries", writerToken, `{"rustdesk_id":"100000003","alias":"Operator PC"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("group writer could not modify shared book: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/peers?ab="+book.ID, readerToken, `{}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"100000001"`) {
		t.Fatalf("compatible shared peers response failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodGet, "/api/ab/peers?current=1&pageSize=100&ab="+book.ID, readerToken, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"100000001"`) {
		t.Fatalf("current client shared peers response failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/tags/"+book.ID, readerToken, "")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `[]` {
		t.Fatalf("RustDesk 1.4.9 tag pull failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/tag/add/"+book.ID, readerToken, `{"name":"Critical","color":4294901760}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only user created a tag: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/tag/add/"+book.ID, writerToken, `{"name":"Critical","color":4294901760}`)
	if response.Code != http.StatusOK {
		t.Fatalf("tag creation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/peer/add/"+book.ID, writerToken,
		`{"id":"226424246","username":"operator","hostname":"desktop-om66nsm","platform":"Windows","alias":"Support PC","tags":[],"forceAlwaysRelay":"false","rdpPort":"","rdpUsername":"","loginName":"writer","sameServer":true,"online":true,"row_id":0,"user_id":0,"collection_id":0,"created_at":"","updated_at":""}`)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("RustDesk 1.4.9 peer creation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/peers?ab="+book.ID, readerToken, `{}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"226424246"`) ||
		!strings.Contains(response.Body.String(), `"username":"operator"`) || !strings.Contains(response.Body.String(), `"hostname":"desktop-om66nsm"`) ||
		!strings.Contains(response.Body.String(), `"platform":"Windows"`) || !strings.Contains(response.Body.String(), `"sameServer":true`) {
		t.Fatalf("created RustDesk 1.4.9 peer was not returned: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPut, "/api/ab/peer/update/"+book.ID, writerToken, `{"id":"100000003","tags":["Critical"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("peer tag assignment failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPut, "/api/ab/tag/rename/"+book.ID, writerToken, `{"old":"Critical","new":"Priority"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("tag rename failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/tags/"+book.ID, readerToken, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Priority"`) || !strings.Contains(response.Body.String(), `"color":4294901760`) {
		t.Fatalf("tag pull after rename failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodPost, "/api/ab/peers?ab="+book.ID, readerToken, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tags":["Priority"]`) {
		t.Fatalf("peer tags were not renamed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodDelete, "/api/ab/tag/"+book.ID, writerToken, `["Priority"]`)
	if response.Code != http.StatusOK {
		t.Fatalf("tag deletion failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodGet, "/api/address-books/"+book.ID+"/entries", outsiderToken, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("unrelated user accessed shared book: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodGet, "/api/address-books", outsiderToken, "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), book.ID) {
		t.Fatalf("unrelated user listing leaked shared book: status=%d body=%s", response.Code, response.Body.String())
	}

	response = apiRequest(t, handler, http.MethodPost, "/api/ab/personal", readerToken, `{}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"rule":3`) {
		t.Fatalf("personal address book compatibility failed: status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiRequest(t, handler, http.MethodGet, "/api/ab", readerToken, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":`) {
		t.Fatalf("legacy address book compatibility failed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func loginUser(t *testing.T, handler http.Handler, username string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"username": username, "password": "address-book-test-password", "type": "client", "uuid": "client-" + username})
	if err != nil {
		t.Fatal(err)
	}
	response := apiRequest(t, handler, http.MethodPost, "/api/login", "", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("login %s failed: status=%d body=%s", username, response.Code, response.Body.String())
	}
	var output struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil || output.AccessToken == "" {
		t.Fatalf("invalid login response: %s", response.Body.String())
	}
	return output.AccessToken
}

func apiRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.RemoteAddr = "192.0.2.20:40000"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
