package httpapi

import (
	"sync"
	"time"
)

type loginAttempt struct {
	failed      []time.Time
	lockedUntil time.Time
}

type LoginLimiter struct {
	mutex   sync.Mutex
	entries map[string]loginAttempt
	burst   int
	window  time.Duration
	lockout time.Duration
}

func NewLoginLimiter(burst int, window, lockout time.Duration) *LoginLimiter {
	return &LoginLimiter{entries: make(map[string]loginAttempt), burst: burst, window: window, lockout: lockout}
}

func (l *LoginLimiter) Allowed(key string, now time.Time) (bool, time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	entry := l.entries[key]
	if now.Before(entry.lockedUntil) {
		return false, entry.lockedUntil.Sub(now)
	}
	entry.failed = recent(entry.failed, now.Add(-l.window))
	l.entries[key] = entry
	return true, 0
}

func (l *LoginLimiter) Failure(key string, now time.Time) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if len(l.entries) >= 10_000 {
		l.prune(now)
	}
	entry := l.entries[key]
	entry.failed = append(recent(entry.failed, now.Add(-l.window)), now)
	if len(entry.failed) >= l.burst {
		entry.lockedUntil = now.Add(l.lockout)
		entry.failed = nil
	}
	l.entries[key] = entry
}

func (l *LoginLimiter) prune(now time.Time) {
	threshold := now.Add(-l.window)
	for key, entry := range l.entries {
		entry.failed = recent(entry.failed, threshold)
		if len(entry.failed) == 0 && !now.Before(entry.lockedUntil) {
			delete(l.entries, key)
			continue
		}
		l.entries[key] = entry
	}
}

func (l *LoginLimiter) Success(key string) {
	l.mutex.Lock()
	delete(l.entries, key)
	l.mutex.Unlock()
}

func recent(values []time.Time, threshold time.Time) []time.Time {
	for index, value := range values {
		if !value.Before(threshold) {
			return values[index:]
		}
	}
	return nil
}
