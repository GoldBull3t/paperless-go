package auth

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Dummy hash for constant-time comparison to prevent user enumeration
const dummyBcryptHash = "$2a$10$wT8mQzKq9pEUX7fNZaFWoO0Ldmyb0iY7rN2g7x3I4rYJkYkYkYkYk"

// DummyBcryptCompare performs a bcrypt compare against a dummy hash
// to ensure response times are identical whether a user exists or not.
func DummyBcryptCompare(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(password))
}

// VerifyAndConsumeRecoveryCode checks if code matches one in recoveryCodesJSON.
// If matched, it removes the code and returns true with the updated JSON.
func VerifyAndConsumeRecoveryCode(codesJSON, code string) (bool, string) {
	cleanCode := strings.TrimSpace(strings.ToLower(code))
	if cleanCode == "" {
		return false, codesJSON
	}

	var codes []string
	if err := json.Unmarshal([]byte(codesJSON), &codes); err != nil {
		return false, codesJSON
	}

	matchedIndex := -1
	for i, c := range codes {
		if strings.TrimSpace(strings.ToLower(c)) == cleanCode {
			matchedIndex = i
			break
		}
	}

	if matchedIndex >= 0 {
		codes = append(codes[:matchedIndex], codes[matchedIndex+1:]...)
		newJSON, _ := json.Marshal(codes)
		return true, string(newJSON)
	}

	return false, codesJSON
}

// RateLimiter tracks failed login attempts and temporarily throttles brute-force attempts
type RateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
	blockTime   time.Duration
}

// NewRateLimiter creates a new thread-safe in-memory rate limiter
func NewRateLimiter(maxAttempts int, window, blockTime time.Duration) *RateLimiter {
	return &RateLimiter{
		attempts:    make(map[string][]time.Time),
		maxAttempts: maxAttempts,
		window:      window,
		blockTime:   blockTime,
	}
}

// Allow returns true if the key is allowed to attempt authentication, or false if throttled
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	times := l.attempts[key]
	var valid []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	l.attempts[key] = valid

	if len(valid) >= l.maxAttempts {
		oldest := valid[0]
		remaining := l.blockTime - now.Sub(oldest)
		if remaining < time.Second {
			remaining = time.Second
		}
		return false, remaining
	}

	return true, 0
}

// RecordFailure records a failed authentication attempt
func (l *RateLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[key] = append(l.attempts[key], time.Now())
}

// Reset clears failure history for key on successful login
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

