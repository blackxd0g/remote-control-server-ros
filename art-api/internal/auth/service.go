package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/identity"
	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserDisabled       = errors.New("user disabled")
	ErrApprovalRejected   = errors.New("registration rejected")
	ErrSessionExpired     = errors.New("session expired")
	ErrSessionRevoked     = errors.New("session revoked")
	ErrStaleToken         = errors.New("force re-login required")
)

type LoginInput struct {
	Username       string
	Password       string
	IP             string
	UserAgent      string
	ClientDeviceID string
}

type LoginOutput struct {
	AccessToken string
	User        domain.User
	Session     domain.Session
	Claims      Claims
}

type CreateUserInput struct {
	Username    string
	Email       string
	Password    string
	DisplayName string
	Role        domain.Role
	Enabled     bool
}

type Service struct {
	repository        domain.Repository
	tokens            *TokenManager
	hub               *events.Hub
	sessionTTL        time.Duration
	now               func() time.Time
	dummyHash         string
	passwordProviders []passwordProvider
	passwordPolicy    PasswordPolicy
	mutex             sync.RWMutex
}

func (s *Service) SetPasswordPolicy(value PasswordPolicy) {
	s.mutex.Lock()
	s.passwordPolicy = value
	s.mutex.Unlock()
}
func (s *Service) validatePassword(value string) error {
	s.mutex.RLock()
	policy := s.passwordPolicy
	s.mutex.RUnlock()
	return policy.Validate(value)
}

func (s *Service) SetTTLs(accessTokenTTL, sessionTTL time.Duration) {
	s.mutex.Lock()
	s.sessionTTL = sessionTTL
	s.mutex.Unlock()
	s.tokens.SetTTL(accessTokenTTL)
}

func (s *Service) currentSessionTTL() time.Duration {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.sessionTTL
}

type passwordProvider struct {
	provider      identity.PasswordProvider
	autoProvision bool
	groupMapping  map[string]string
}

func (s *Service) AddPasswordProvider(provider identity.PasswordProvider, autoProvision bool) {
	s.passwordProviders = append(s.passwordProviders, passwordProvider{provider: provider, autoProvision: autoProvision})
}

func (s *Service) AddPasswordProviderWithGroups(provider identity.PasswordProvider, autoProvision bool, groupMapping map[string]string) {
	s.passwordProviders = append(s.passwordProviders, passwordProvider{provider: provider, autoProvision: autoProvision, groupMapping: groupMapping})
}

func NewService(repository domain.Repository, tokens *TokenManager, hub *events.Hub, sessionTTL time.Duration) (*Service, error) {
	dummyHash, err := HashPassword("not-a-real-password-57F2eC4d")
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository, tokens: tokens, hub: hub, sessionTTL: sessionTTL,
		now: func() time.Time { return time.Now().UTC() }, dummyHash: dummyHash, passwordPolicy: PasswordPolicy{MinimumLength: 1}}, nil
}

func (s *Service) Login(ctx context.Context, input LoginInput) (LoginOutput, error) {
	user, err := s.VerifyCredentials(ctx, input.Username, input.Password)
	if err != nil {
		return LoginOutput{}, err
	}
	return s.CompleteLogin(ctx, user, input)
}

func (s *Service) VerifyCredentials(ctx context.Context, username, password string) (domain.User, error) {
	user, err := s.repository.FindUserByUsername(ctx, username)
	if err == nil {
		valid, verifyErr := VerifyPassword(user.PasswordHash, password)
		if verifyErr == nil && valid {
			return s.validateLoginUser(ctx, user)
		}
	} else {
		_, _ = VerifyPassword(s.dummyHash, password)
	}
	for _, configured := range s.passwordProviders {
		profile, providerErr := configured.provider.Authenticate(ctx, username, password)
		if providerErr != nil {
			continue
		}
		identityRecord, identityErr := s.repository.FindOIDCIdentity(ctx, configured.provider.Name(), profile.Subject)
		if identityErr == nil {
			user, err = s.repository.FindUserByID(ctx, identityRecord.UserID)
			if err != nil {
				return domain.User{}, ErrInvalidCredentials
			}
			if err = s.syncExternalGroups(ctx, user.ID, profile.Groups, configured.groupMapping); err != nil {
				return domain.User{}, ErrInvalidCredentials
			}
			return s.validateLoginUser(ctx, user)
		}
		if !errors.Is(identityErr, domain.ErrNotFound) || !configured.autoProvision {
			return domain.User{}, ErrInvalidCredentials
		}
		if _, collisionErr := s.repository.FindUserByUsername(ctx, profile.Username); collisionErr == nil {
			return domain.User{}, ErrInvalidCredentials
		}
		provisioned, provisionErr := s.provisionExternalUser(ctx, configured.provider.Name(), profile)
		if provisionErr != nil {
			return domain.User{}, ErrInvalidCredentials
		}
		if err = s.syncExternalGroups(ctx, provisioned.ID, profile.Groups, configured.groupMapping); err != nil {
			return domain.User{}, ErrInvalidCredentials
		}
		return s.validateLoginUser(ctx, provisioned)
	}
	return domain.User{}, ErrInvalidCredentials
}

func (s *Service) syncExternalGroups(ctx context.Context, userID string, sourceGroups []string, mapping map[string]string) error {
	if len(mapping) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(mapping))
	for _, source := range sourceGroups {
		if target := mapping[source]; target != "" {
			wanted[target] = true
		}
	}
	seenTargets := make(map[string]bool, len(mapping))
	for _, target := range mapping {
		if seenTargets[target] {
			continue
		}
		seenTargets[target] = true
		if _, err := s.repository.FindGroupByID(ctx, target); err != nil {
			return fmt.Errorf("mapped external group %s: %w", target, err)
		}
		if err := s.repository.SetUserGroupMember(ctx, target, userID, wanted[target]); err != nil {
			return err
		}
		s.hub.Publish(events.UserGroupMembershipUpdated, domain.UserGroupMembership{GroupID: target, UserID: userID, Active: wanted[target]})
	}
	return nil
}

func (s *Service) validateLoginUser(ctx context.Context, user domain.User) (domain.User, error) {
	if !user.Enabled {
		return domain.User{}, ErrUserDisabled
	}
	if user.ApprovalStatus == domain.ApprovalRejected {
		return domain.User{}, ErrApprovalRejected
	}
	user = s.withPermissions(ctx, user)
	return user, nil
}

func (s *Service) provisionExternalUser(ctx context.Context, provider string, profile identity.Profile) (domain.User, error) {
	passwordHash, err := HashPassword(uuid.NewString() + uuid.NewString())
	if err != nil {
		return domain.User{}, err
	}
	now := s.now()
	user := domain.User{ID: uuid.NewString(), Username: profile.Username, Email: profile.Email, DisplayName: profile.DisplayName, PasswordHash: passwordHash, Role: domain.RoleUser, Enabled: true, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err = s.repository.CreateUser(ctx, user); err != nil {
		return domain.User{}, err
	}
	if err = s.repository.CreateOIDCIdentity(ctx, domain.OIDCIdentity{Provider: provider, Subject: profile.Subject, UserID: user.ID, Email: profile.Email, CreatedAt: now}); err != nil {
		_ = s.repository.DeleteUser(ctx, user.ID)
		return domain.User{}, err
	}
	_ = s.repository.SetUserGroupMember(ctx, domain.ApprovedUsersGroupID, user.ID, true)
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.UserGroupMembershipUpdated, domain.UserGroupMembership{GroupID: domain.ApprovedUsersGroupID, UserID: user.ID, Active: true})
	return user, nil
}

func (s *Service) withPermissions(ctx context.Context, user domain.User) domain.User {
	if user.Role == domain.RoleAdmin {
		user.Permissions = []string{domain.PermissionAll}
		return user
	}
	if role, err := s.repository.FindRoleByID(ctx, user.Role); err == nil {
		user.Permissions = append([]string(nil), role.Permissions...)
	}
	return user
}

func (s *Service) CompleteLogin(ctx context.Context, user domain.User, input LoginInput) (LoginOutput, error) {
	user = s.withPermissions(ctx, user)
	now := s.now()
	session := domain.Session{ID: uuid.NewString(), UserID: user.ID, CreatedAt: now,
		ExpiresAt: now.Add(s.currentSessionTTL()), LastSeenAt: now, IP: input.IP,
		UserAgent: input.UserAgent, ClientDeviceID: input.ClientDeviceID}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return LoginOutput{}, fmt.Errorf("create session: %w", err)
	}
	if err := s.repository.UpdateLastLogin(ctx, user.ID, now); err != nil {
		_, _ = s.repository.RevokeSession(ctx, session.ID, now)
		return LoginOutput{}, fmt.Errorf("update last login: %w", err)
	}
	token, claims, err := s.tokens.Issue(user, session, now)
	if err != nil {
		_, _ = s.repository.RevokeSession(ctx, session.ID, now)
		return LoginOutput{}, fmt.Errorf("issue access token: %w", err)
	}
	s.hub.Publish(events.SessionCreated, session)
	return LoginOutput{AccessToken: token, User: user, Session: session, Claims: claims}, nil
}

func (s *Service) CreateLocalUser(ctx context.Context, input CreateUserInput) (domain.User, error) {
	if err := s.validatePassword(input.Password); err != nil {
		return domain.User{}, err
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now()
	user := domain.User{ID: uuid.NewString(), Username: input.Username, Email: input.Email,
		PasswordHash: passwordHash, DisplayName: input.DisplayName, Role: input.Role,
		Enabled: input.Enabled, ApprovalStatus: domain.ApprovalApproved, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return domain.User{}, err
	}
	s.hub.Publish(events.UserUpdated, user)
	return user, nil
}

func (s *Service) Register(ctx context.Context, input CreateUserInput, autoApprove bool) (domain.User, error) {
	if err := s.validatePassword(input.Password); err != nil {
		return domain.User{}, err
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now()
	status := domain.ApprovalPending
	if autoApprove {
		status = domain.ApprovalApproved
	}
	user := domain.User{ID: uuid.NewString(), Username: input.Username, Email: input.Email, PasswordHash: passwordHash,
		DisplayName: input.DisplayName, Role: domain.RoleUser, Enabled: true, ApprovalStatus: status,
		TokenVersion: 1, CreatedAt: now, UpdatedAt: now}
	if autoApprove {
		err = s.repository.CreateUser(ctx, user)
	} else {
		err = s.repository.CreateRegisteredUser(ctx, user)
	}
	if err != nil {
		return domain.User{}, err
	}
	s.hub.Publish(events.UserUpdated, user)
	groupID := domain.PendingUsersGroupID
	if autoApprove {
		groupID = domain.ApprovedUsersGroupID
	}
	s.hub.Publish(events.UserGroupMembershipUpdated, domain.UserGroupMembership{GroupID: groupID, UserID: user.ID, Active: true})
	return user, nil
}

func (s *Service) SetUserApproval(ctx context.Context, userID string, status domain.ApprovalStatus) (domain.User, error) {
	now := s.now()
	user, err := s.repository.SetUserApproval(ctx, userID, status, now)
	if err != nil {
		return domain.User{}, err
	}
	if status == domain.ApprovalRejected {
		if err = s.repository.RevokeUserSessions(ctx, userID, now); err != nil {
			return domain.User{}, err
		}
		s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	}
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.UserGroupMembershipUpdated, domain.UserGroupMembership{GroupID: domain.PendingUsersGroupID, UserID: userID, Active: false})
	s.hub.Publish(events.UserGroupMembershipUpdated, domain.UserGroupMembership{GroupID: domain.ApprovedUsersGroupID, UserID: userID, Active: status == domain.ApprovalApproved})
	return user, nil
}

func (s *Service) SetPassword(ctx context.Context, userID, password string) (domain.User, error) {
	if err := s.validatePassword(password); err != nil {
		return domain.User{}, err
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now()
	user, err := s.repository.UpdateUserPassword(ctx, userID, passwordHash, now)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.repository.RevokeUserSessions(ctx, userID, now); err != nil {
		return domain.User{}, err
	}
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	return user, nil
}

func (s *Service) UpdateUser(ctx context.Context, userID string, input domain.UserUpdate, password string) (domain.User, error) {
	if password != "" {
		if err := s.validatePassword(password); err != nil {
			return domain.User{}, err
		}
		passwordHash, err := HashPassword(password)
		if err != nil {
			return domain.User{}, err
		}
		input.PasswordHash = passwordHash
	}
	now := s.now()
	user, err := s.repository.UpdateUser(ctx, userID, input, now)
	if err != nil {
		return domain.User{}, err
	}
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	memberships, err := s.repository.ListUserGroupMemberships(ctx)
	if err == nil {
		for _, membership := range memberships {
			if membership.UserID == userID {
				s.hub.Publish(events.UserGroupMembershipUpdated, membership)
			}
		}
	}
	return user, nil
}

func (s *Service) DeleteUser(ctx context.Context, userID string) error {
	if err := s.repository.DeleteUser(ctx, userID); err != nil {
		return err
	}
	s.hub.Publish(events.UserUpdated, domain.User{ID: userID, Enabled: false})
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	return nil
}

func (s *Service) Authenticate(ctx context.Context, raw string) (domain.User, domain.Session, Claims, error) {
	claims, err := s.tokens.Parse(raw)
	if err != nil {
		return domain.User{}, domain.Session{}, Claims{}, err
	}
	user, err := s.repository.FindUserByID(ctx, claims.Subject)
	if err != nil {
		return domain.User{}, domain.Session{}, Claims{}, ErrSessionRevoked
	}
	if !user.Enabled {
		return domain.User{}, domain.Session{}, Claims{}, ErrUserDisabled
	}
	if user.ApprovalStatus == domain.ApprovalRejected {
		return domain.User{}, domain.Session{}, Claims{}, ErrApprovalRejected
	}
	session, err := s.repository.FindSession(ctx, claims.SessionID)
	if err != nil {
		return domain.User{}, domain.Session{}, Claims{}, ErrSessionRevoked
	}
	now := s.now()
	if session.RevokedAt != nil {
		return domain.User{}, domain.Session{}, Claims{}, ErrSessionRevoked
	}
	if !now.Before(session.ExpiresAt) {
		return domain.User{}, domain.Session{}, Claims{}, ErrSessionExpired
	}
	if user.TokenVersion != claims.TokenVersion || session.UserID != user.ID {
		return domain.User{}, domain.Session{}, Claims{}, ErrStaleToken
	}
	user = s.withPermissions(ctx, user)
	_ = s.repository.TouchSession(ctx, session.ID, now)
	return user, session, claims, nil
}

func (s *Service) RevokeSession(ctx context.Context, sessionID string) error {
	session, err := s.repository.RevokeSession(ctx, sessionID, s.now())
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	s.hub.Publish(events.SessionRevoked, session)
	return nil
}

func (s *Service) RevokeAll(ctx context.Context, userID string) error {
	if err := s.repository.RevokeUserSessions(ctx, userID, s.now()); err != nil {
		return err
	}
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	return nil
}

func (s *Service) SetUserEnabled(ctx context.Context, userID string, enabled bool) (domain.User, error) {
	now := s.now()
	user, err := s.repository.SetUserEnabled(ctx, userID, enabled, now)
	if err != nil {
		return domain.User{}, err
	}
	if !enabled {
		if err := s.repository.RevokeUserSessions(ctx, userID, now); err != nil {
			return domain.User{}, err
		}
		s.hub.Publish(events.UserDisabled, user)
		s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	} else {
		s.hub.Publish(events.UserUpdated, user)
	}
	return user, nil
}

func (s *Service) ForceRelogin(ctx context.Context, userID string) (domain.User, error) {
	now := s.now()
	user, err := s.repository.ForceRelogin(ctx, userID, now)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.repository.RevokeUserSessions(ctx, userID, now); err != nil {
		return domain.User{}, err
	}
	s.hub.Publish(events.UserUpdated, user)
	s.hub.Publish(events.SessionRevokedAll, map[string]string{"user_id": userID})
	return user, nil
}
