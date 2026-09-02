package mfa

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

var ErrInvalidCode = errors.New("invalid two-factor code")

type Mode string

const (
	ModeOptional Mode = "optional"
	ModeAdmins   Mode = "required_for_admins"
	ModeAll      Mode = "required_for_all_users"
)

type Service struct {
	repository  domain.Repository
	aead        cipher.AEAD
	recoveryKey [32]byte
	mode        Mode
	issuer      string
	now         func() time.Time
	mutex       sync.RWMutex
}
type Enrollment struct {
	Secret        string   `json:"secret"`
	URI           string   `json:"otpauth_uri"`
	RecoveryCodes []string `json:"recovery_codes"`
}

func New(repository domain.Repository, encryptionKey []byte, mode Mode, issuer string) (*Service, error) {
	if mode != ModeOptional && mode != ModeAdmins && mode != ModeAll {
		return nil, fmt.Errorf("invalid 2FA mode %q", mode)
	}
	key := sha256.Sum256(encryptionKey)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	recoveryKey := sha256.Sum256(append([]byte("art-rustdesk:mfa-recovery:"), encryptionKey...))
	return &Service{repository: repository, aead: aead, recoveryKey: recoveryKey, mode: mode, issuer: issuer, now: func() time.Time { return time.Now().UTC() }}, nil
}
func (s *Service) Required(user domain.User) bool {
	s.mutex.RLock()
	mode := s.mode
	s.mutex.RUnlock()
	return mode == ModeAll || mode == ModeAdmins && user.Role == domain.RoleAdmin
}
func (s *Service) Mode() Mode { s.mutex.RLock(); defer s.mutex.RUnlock(); return s.mode }
func (s *Service) SetMode(mode Mode) error {
	if mode != ModeOptional && mode != ModeAdmins && mode != ModeAll {
		return fmt.Errorf("invalid 2FA mode %q", mode)
	}
	s.mutex.Lock()
	s.mode = mode
	s.mutex.Unlock()
	return nil
}
func (s *Service) Begin(ctx context.Context, user domain.User) (Enrollment, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return Enrollment{}, err
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	encrypted, err := s.encrypt(secret)
	if err != nil {
		return Enrollment{}, err
	}
	if _, err = s.repository.SetUserTOTP(ctx, user.ID, encrypted, false, s.now()); err != nil {
		return Enrollment{}, err
	}
	codes, hashes, err := s.generateRecoveryCodes()
	if err != nil {
		return Enrollment{}, err
	}
	if err = s.repository.ReplaceMFARecoveryCodes(ctx, user.ID, hashes, s.now()); err != nil {
		return Enrollment{}, err
	}
	label := url.PathEscape(s.issuer + ":" + user.Username)
	query := url.Values{"secret": {secret}, "issuer": {s.issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return Enrollment{Secret: secret, URI: "otpauth://totp/" + label + "?" + query.Encode(), RecoveryCodes: codes}, nil
}
func (s *Service) Confirm(ctx context.Context, user domain.User, code string) (domain.User, error) {
	secret, err := s.decrypt(user.TOTPSecret)
	if err != nil || !Validate(secret, code, s.now()) {
		return domain.User{}, ErrInvalidCode
	}
	return s.repository.SetUserTOTP(ctx, user.ID, user.TOTPSecret, true, s.now())
}
func (s *Service) Disable(ctx context.Context, user domain.User, code string) (domain.User, error) {
	if user.TOTPEnabled {
		secret, err := s.decrypt(user.TOTPSecret)
		if err != nil || !Validate(secret, code, s.now()) {
			return domain.User{}, ErrInvalidCode
		}
	}
	updated, err := s.repository.SetUserTOTP(ctx, user.ID, "", false, s.now())
	if err != nil {
		return domain.User{}, err
	}
	if err = s.repository.ReplaceMFARecoveryCodes(ctx, user.ID, nil, s.now()); err != nil {
		return domain.User{}, err
	}
	return updated, nil
}
func (s *Service) Verify(ctx context.Context, user domain.User, code string) (bool, bool, error) {
	if !user.TOTPEnabled {
		return !s.Required(user), false, nil
	}
	secret, err := s.decrypt(user.TOTPSecret)
	if err == nil && Validate(secret, code, s.now()) {
		return true, false, nil
	}
	consumed, err := s.repository.ConsumeMFARecoveryCode(ctx, user.ID, s.recoveryHash(code))
	return consumed, consumed, err
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, user domain.User, code string) ([]string, error) {
	secret, err := s.decrypt(user.TOTPSecret)
	if err != nil || !Validate(secret, code, s.now()) {
		return nil, ErrInvalidCode
	}
	codes, hashes, err := s.generateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err = s.repository.ReplaceMFARecoveryCodes(ctx, user.ID, hashes, s.now()); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) RecoveryCodesRemaining(ctx context.Context, userID string) (int, error) {
	return s.repository.CountMFARecoveryCodes(ctx, userID)
}

func (s *Service) generateRecoveryCodes() ([]string, []string, error) {
	codes, hashes := make([]string, 10), make([]string, 10)
	for index := range codes {
		raw := make([]byte, 7)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)[:10]
		codes[index] = encoded[:5] + "-" + encoded[5:]
		hashes[index] = s.recoveryHash(codes[index])
	}
	return codes, hashes, nil
}

func (s *Service) recoveryHash(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	mac := hmac.New(sha256.New, s.recoveryKey[:])
	_, _ = mac.Write([]byte(normalized))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Service) encrypt(value string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(value), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
func (s *Service) decrypt(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errors.New("invalid encrypted TOTP secret")
	}
	plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], nil)
	return string(plain), err
}
func Validate(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	candidate, err := strconv.Atoi(code)
	if err != nil {
		return false
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}
	counter := now.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		expected := codeForCounter(decoded, counter+offset)
		if hmac.Equal([]byte(expected), []byte(fmt.Sprintf("%06d", candidate))) {
			return true
		}
	}
	return false
}

func Code(secret string, now time.Time) (string, error) {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return codeForCounter(decoded, now.Unix()/30), nil
}

func codeForCounter(secret []byte, counter int64) string {
	var input [8]byte
	binary.BigEndian.PutUint64(input[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(input[:])
	sum := mac.Sum(nil)
	index := sum[len(sum)-1] & 15
	value := (int(sum[index])&127)<<24 | (int(sum[index+1])&255)<<16 | (int(sum[index+2])&255)<<8 | (int(sum[index+3]) & 255)
	return fmt.Sprintf("%06d", value%1000000)
}
