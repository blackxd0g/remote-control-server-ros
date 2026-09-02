package oidcauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/identity"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var ErrPending = errors.New("OIDC authorization pending")

type Config struct {
	Issuer, ClientID, ClientSecret, RedirectURL, Name string
	Scopes                                            []string
	AutoRegister                                      bool
}
type Service struct {
	repository domain.Repository
	auth       *auth.Service
	config     Config
	now        func() time.Time
	mu         sync.Mutex
	provider   *oidc.Provider
	oauth      *oauth2.Config
}

func New(repository domain.Repository, authService *auth.Service, config Config) *Service {
	return &Service{repository: repository, auth: authService, config: config, now: func() time.Time { return time.Now().UTC() }}
}
func (s *Service) Name() string { return s.config.Name }
func (s *Service) Begin(ctx context.Context, input identity.LoginContext) (identity.Authorization, error) {
	provider, oauthConfig, err := s.client(ctx)
	if err != nil {
		return identity.Authorization{}, err
	}
	_ = provider
	state, err := randomToken(32)
	if err != nil {
		return identity.Authorization{}, err
	}
	poll, err := randomToken(32)
	if err != nil {
		return identity.Authorization{}, err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return identity.Authorization{}, err
	}
	nonce, err := randomToken(24)
	if err != nil {
		return identity.Authorization{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	now := s.now()
	record := domain.OIDCAuthRequest{State: state, PollCode: poll, Provider: s.config.Name, Verifier: verifier, Nonce: nonce, LinkUserID: input.LinkUserID, RustDeskID: input.RustDeskID, ClientUUID: input.ClientUUID, Platform: input.Platform, ClientType: input.ClientType, DeviceName: input.DeviceName, IP: input.IP, UserAgent: input.UserAgent, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if err = s.repository.CreateOIDCAuthRequest(ctx, record); err != nil {
		return identity.Authorization{}, err
	}
	authURL := oauthConfig.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	return identity.Authorization{State: state, PollCode: poll, URL: authURL}, nil
}
func (s *Service) Callback(ctx context.Context, state, code string) error {
	record, err := s.repository.FindOIDCAuthRequestByState(ctx, state, s.now())
	if err != nil {
		return err
	}
	provider, oauthConfig, err := s.client(ctx)
	if err != nil {
		return s.fail(ctx, state, err)
	}
	token, err := oauthConfig.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", record.Verifier))
	if err != nil {
		return s.fail(ctx, state, err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return s.fail(ctx, state, errors.New("missing id_token"))
	}
	verified, err := provider.Verifier(&oidc.Config{ClientID: s.config.ClientID}).Verify(ctx, raw)
	if err != nil {
		return s.fail(ctx, state, err)
	}
	var claims struct {
		Subject           string `json:"sub"`
		Nonce             string `json:"nonce"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	if err = verified.Claims(&claims); err != nil {
		return s.fail(ctx, state, err)
	}
	if claims.Subject == "" || claims.Nonce != record.Nonce {
		return s.fail(ctx, state, errors.New("invalid OIDC subject or nonce"))
	}
	identityRecord, err := s.repository.FindOIDCIdentity(ctx, s.config.Name, claims.Subject)
	if record.LinkUserID != "" {
		return s.completeLink(ctx, record, identityRecord, err, claims.Subject, claims.Email)
	}
	var user domain.User
	if err == nil {
		user, err = s.repository.FindUserByID(ctx, identityRecord.UserID)
	} else if errors.Is(err, domain.ErrNotFound) && s.config.AutoRegister {
		user, err = s.register(ctx, claims.PreferredUsername, claims.Email, claims.Name)
		if err == nil {
			err = s.repository.CreateOIDCIdentity(ctx, domain.OIDCIdentity{Provider: s.config.Name, Subject: claims.Subject, UserID: user.ID, Email: claims.Email, CreatedAt: s.now()})
		}
	} else if errors.Is(err, domain.ErrNotFound) {
		err = errors.New("OIDC identity is not linked and auto-registration is disabled")
	}
	if err != nil {
		return s.fail(ctx, state, err)
	}
	if !user.Enabled {
		return s.fail(ctx, state, errors.New("user disabled"))
	}
	return s.repository.CompleteOIDCAuthRequest(ctx, state, user.ID, "")
}

func (s *Service) completeLink(ctx context.Context, request domain.OIDCAuthRequest, existing domain.OIDCIdentity, findErr error, subject, email string) error {
	user, err := s.repository.FindUserByID(ctx, request.LinkUserID)
	if err != nil || !user.Enabled {
		if err == nil {
			err = errors.New("user disabled")
		}
		return s.fail(ctx, request.State, err)
	}
	switch {
	case findErr == nil && existing.UserID != request.LinkUserID:
		return s.fail(ctx, request.State, errors.New("OIDC identity is already linked to another user"))
	case findErr == nil:
		// Re-linking the same verified subject is idempotent.
	case errors.Is(findErr, domain.ErrNotFound):
		err = s.repository.CreateOIDCIdentity(ctx, domain.OIDCIdentity{Provider: s.config.Name, Subject: subject, UserID: request.LinkUserID, Email: email, CreatedAt: s.now()})
		if err != nil {
			return s.fail(ctx, request.State, err)
		}
	default:
		return s.fail(ctx, request.State, findErr)
	}
	return s.repository.CompleteOIDCAuthRequest(ctx, request.State, request.LinkUserID, "")
}
func (s *Service) Consume(ctx context.Context, pollCode, id, uuid string) (domain.OIDCAuthRequest, domain.User, error) {
	record, err := s.repository.ConsumeOIDCAuthRequest(ctx, pollCode, id, uuid, s.now())
	if errors.Is(err, domain.ErrNotFound) {
		return record, domain.User{}, ErrPending
	}
	if err != nil {
		return record, domain.User{}, err
	}
	if record.Error != "" {
		return record, domain.User{}, errors.New(record.Error)
	}
	user, err := s.repository.FindUserByID(ctx, record.UserID)
	return record, user, err
}

func (s *Service) ConsumeLink(ctx context.Context, pollCode, userID string) (domain.OIDCIdentity, error) {
	record, err := s.repository.ConsumeOIDCLinkRequest(ctx, pollCode, userID, s.now())
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OIDCIdentity{}, ErrPending
	}
	if err != nil {
		return domain.OIDCIdentity{}, err
	}
	if record.Error != "" {
		return domain.OIDCIdentity{}, errors.New(record.Error)
	}
	values, err := s.repository.ListOIDCIdentities(ctx, userID)
	if err != nil {
		return domain.OIDCIdentity{}, err
	}
	for _, value := range values {
		if value.Provider == record.Provider {
			return value, nil
		}
	}
	return domain.OIDCIdentity{}, domain.ErrNotFound
}
func (s *Service) client(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return s.provider, s.oauth, nil
	}
	provider, err := oidc.NewProvider(ctx, s.config.Issuer)
	if err != nil {
		return nil, nil, err
	}
	scopes := s.config.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	oauthConfig := &oauth2.Config{ClientID: s.config.ClientID, ClientSecret: s.config.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: s.config.RedirectURL, Scopes: scopes}
	s.provider, s.oauth = provider, oauthConfig
	return provider, oauthConfig, nil
}
func (s *Service) fail(ctx context.Context, state string, cause error) error {
	_ = s.repository.CompleteOIDCAuthRequest(ctx, state, "", "OIDC authentication failed")
	return cause
}
func (s *Service) register(ctx context.Context, preferred, email, display string) (domain.User, error) {
	base := sanitizeUsername(preferred)
	if base == "" {
		base = sanitizeUsername(strings.Split(email, "@")[0])
	}
	if base == "" {
		base = "oidc-user"
	}
	password, err := randomToken(32)
	if err != nil {
		return domain.User{}, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		user, createErr := s.auth.CreateLocalUser(ctx, auth.CreateUserInput{Username: name, Email: email, Password: password, DisplayName: display, Role: domain.RoleUser, Enabled: true})
		if createErr == nil {
			return user, nil
		}
		err = createErr
	}
	return domain.User{}, err
}
func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func sanitizeUsername(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			out.WriteRune(r)
		}
	}
	return strings.Trim(out.String(), ".-_")
}
