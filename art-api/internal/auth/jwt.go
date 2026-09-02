package auth

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrTokenInvalid = errors.New("token invalid")
	ErrTokenExpired = errors.New("token expired")
)

type Claims struct {
	SessionID    string `json:"sid"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name,omitempty"`
	Role         string `json:"role"`
	TokenVersion int64  `json:"token_version"`
	DeviceID     string `json:"device_id,omitempty"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret   []byte
	issuer   string
	audience string
	ttl      time.Duration
	mutex    sync.RWMutex
}

func NewTokenManager(secret []byte, issuer, audience string, ttl time.Duration) *TokenManager {
	return &TokenManager{secret: secret, issuer: issuer, audience: audience, ttl: ttl}
}

func (m *TokenManager) Issue(user domain.User, session domain.Session, now time.Time) (string, Claims, error) {
	m.mutex.RLock()
	ttl := m.ttl
	m.mutex.RUnlock()
	expiresAt := now.Add(ttl)
	if session.ExpiresAt.Before(expiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := Claims{
		SessionID: session.ID,
		Username:  user.Username, DisplayName: user.DisplayName, Role: string(user.Role), TokenVersion: user.TokenVersion,
		DeviceID: session.ClientDeviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: user.ID, Issuer: m.issuer,
			Audience: jwt.ClaimStrings{m.audience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return token, claims, err
}

func (m *TokenManager) SetTTL(ttl time.Duration) { m.mutex.Lock(); m.ttl = ttl; m.mutex.Unlock() }

func (m *TokenManager) Parse(raw string) (Claims, error) {
	claims := Claims{}
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Claims{}, ErrTokenExpired
		}
		return Claims{}, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	if claims.Subject == "" || claims.SessionID == "" || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return Claims{}, ErrTokenInvalid
	}
	return claims, nil
}
