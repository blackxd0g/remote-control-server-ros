package oidcauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/identity"
	"github.com/art-rustdesk/platform/art-api/internal/oidcauth"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
	"github.com/golang-jwt/jwt/v5"
)

func TestAuthorizationCodePKCEAndAutoRegistration(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, expectedNonce, expectedChallenge string
	var lock sync.Mutex
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	issuer = server.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		lock.Lock()
		nonce, challenge := expectedNonce, expectedChallenge
		lock.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			http.Error(w, "bad verifier", 400)
			return
		}
		claims := jwt.MapClaims{"iss": issuer, "aud": "client", "sub": "subject-1", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "oidc@example.test", "email_verified": true, "preferred_username": "oidc.user", "name": "OIDC User"}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test"
		signed, _ := token.SignedString(privateKey)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "provider-token", "token_type": "Bearer", "expires_in": 60, "id_token": signed})
	})

	ctx := context.Background()
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "oidc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	authService, _ := auth.NewService(repository, tokens, events.NewHub(), time.Hour)
	service := oidcauth.New(repository, authService, oidcauth.Config{Issuer: issuer, ClientID: "client", ClientSecret: "secret", RedirectURL: issuer + "/callback", Name: "company", Scopes: []string{"openid", "profile", "email"}, AutoRegister: true})
	authorization, err := service.Begin(ctx, identity.LoginContext{RustDeskID: "123", ClientUUID: "uuid", Platform: "Windows", ClientType: "client", DeviceName: "PC"})
	if err != nil {
		t.Fatal(err)
	}
	authURL, _ := url.Parse(authorization.URL)
	lock.Lock()
	expectedNonce = authURL.Query().Get("nonce")
	expectedChallenge = authURL.Query().Get("code_challenge")
	lock.Unlock()
	if expectedNonce == "" || expectedChallenge == "" || authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE/nonce missing: %s", authorization.URL)
	}
	if err = service.Callback(ctx, authURL.Query().Get("state"), "provider-code"); err != nil {
		t.Fatal(err)
	}
	record, user, err := service.Consume(ctx, authorization.PollCode, "123", "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if record.Provider != "company" || user.Username != "oidc.user" || user.Email != "oidc@example.test" {
		t.Fatalf("unexpected result: %#v %#v", record, user)
	}
	if _, _, err = service.Consume(ctx, authorization.PollCode, "123", "uuid"); err == nil {
		t.Fatal("OIDC result was replayed")
	}
	identityRecord, err := repository.FindOIDCIdentity(ctx, "company", "subject-1")
	if err != nil || identityRecord.UserID != user.ID {
		t.Fatalf("identity not linked: %#v %v", identityRecord, err)
	}

	link, err := service.Begin(ctx, identity.LoginContext{LinkUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	linkURL, _ := url.Parse(link.URL)
	lock.Lock()
	expectedNonce = linkURL.Query().Get("nonce")
	expectedChallenge = linkURL.Query().Get("code_challenge")
	lock.Unlock()
	if err = service.Callback(ctx, linkURL.Query().Get("state"), "provider-code"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConsumeLink(ctx, link.PollCode, "another-user"); err != oidcauth.ErrPending {
		t.Fatalf("link result was not bound to the local user: %v", err)
	}
	linked, err := service.ConsumeLink(ctx, link.PollCode, user.ID)
	if err != nil || linked.Subject != "subject-1" {
		t.Fatalf("linked identity not returned: %#v %v", linked, err)
	}
	if _, err = service.ConsumeLink(ctx, link.PollCode, user.ID); err != oidcauth.ErrPending {
		t.Fatalf("link result was replayed: %v", err)
	}
}
