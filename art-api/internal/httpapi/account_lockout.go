package httpapi

import (
	"context"
	"strconv"
	"time"
)

type accountLockoutRepository interface {
	AccountLoginAllowed(context.Context, string, time.Time) (bool, time.Duration, error)
	RecordAccountLoginFailure(context.Context, string, time.Time, int, time.Duration, time.Duration) error
	ClearAccountLoginFailures(context.Context, string) error
}

func (s *Server) accountLoginAllowed(ctx context.Context, username string, now time.Time) (bool, time.Duration) {
	repository, ok := s.repository.(accountLockoutRepository)
	if !ok || username == "" {
		return true, 0
	}
	allowed, retry, err := repository.AccountLoginAllowed(ctx, username, now)
	if err != nil {
		return false, time.Minute
	}
	return allowed, retry
}

func (s *Server) accountLoginFailure(ctx context.Context, username string, now time.Time) {
	repository, ok := s.repository.(accountLockoutRepository)
	if !ok || username == "" {
		return
	}
	burst, _ := strconv.Atoi(envValue("ART_LOGIN_BURST", "5"))
	window, _ := time.ParseDuration(envValue("ART_LOGIN_WINDOW", "5m"))
	lockout, _ := time.ParseDuration(envValue("ART_LOGIN_LOCKOUT", "15m"))
	if burst < 2 {
		burst = 5
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	if lockout <= 0 {
		lockout = 15 * time.Minute
	}
	_ = repository.RecordAccountLoginFailure(ctx, username, now, burst, window, lockout)
}

func (s *Server) accountLoginSuccess(ctx context.Context, username string) {
	if repository, ok := s.repository.(accountLockoutRepository); ok {
		_ = repository.ClearAccountLoginFailures(ctx, username)
	}
}
