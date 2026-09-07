package httpapi_test

import (
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

func TestUserCreationPasswordPolicyErrors(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repository.CreateUser(ctx, domain.User{ID: "admin", Username: "admin", PasswordHash: hash, Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	service, err := auth.NewService(repository, tokens, hub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.SetPasswordPolicy(auth.PasswordPolicy{MinimumLength: 12, RequireUpper: true, RequireLower: true, RequireNumber: true, RequireSpecial: true})
	multifactor, err := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(service, multifactor, audit.New(repository), repository, hub, []byte("internal-secret"), httpapi.NewLoginLimiter(10, time.Minute, time.Minute)).EnableRegistration(true, httpapi.NewLoginLimiter(10, time.Minute, time.Minute)).Handler()
	for _, minimum := range []int{16, 12} {
		service.SetPasswordPolicy(auth.PasswordPolicy{MinimumLength: minimum, RequireUpper: true, RequireLower: true, RequireNumber: true, RequireSpecial: true})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/registration-options", nil))
		var options struct {
			Enabled bool `json:"enabled"`
			Policy  struct {
				Minimum int  `json:"minimum_length"`
				Upper   bool `json:"require_upper"`
				Lower   bool `json:"require_lower"`
				Number  bool `json:"require_number"`
				Special bool `json:"require_special"`
			} `json:"password_policy"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &options); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || !options.Enabled || options.Policy.Minimum != minimum || !options.Policy.Upper || !options.Policy.Lower || !options.Policy.Number || !options.Policy.Special {
			t.Fatalf("public registration policy differs from enforced policy: %d %s", response.Code, response.Body.String())
		}
	}
	token := login(t, handler)
	for _, scenario := range []struct {
		name, path string
		status     int
	}{
		{"admin-created", "/api/admin/users", http.StatusCreated},
		{"registered", "/api/register", http.StatusAccepted},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, password := range []string{"short", "Strong-Password7"} {
				body, err := json.Marshal(map[string]any{"username": scenario.name, "password": password, "role": "user", "enabled": true})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, scenario.path, strings.NewReader(string(body)))
				request.Header.Set("Content-Type", "application/json")
				if scenario.path == "/api/admin/users" {
					request.Header.Set("Authorization", "Bearer "+token)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if password == "short" {
					if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "at least 12 characters") || !strings.Contains(response.Body.String(), "special character") {
						t.Fatalf("missing policy explanation: %d %s", response.Code, response.Body.String())
					}
					if _, err := repository.FindUserByUsername(ctx, scenario.name); err == nil {
						t.Fatal("rejected account was persisted")
					}
				} else if response.Code != scenario.status {
					t.Fatalf("valid account rejected: %d %s", response.Code, response.Body.String())
				}
			}
		})
	}
}
