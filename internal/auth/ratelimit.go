package auth

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate limiting configuration
const (
	DefaultMaxFailedAttempts = 5
	DefaultRateLimitWindow   = 15 * time.Minute
	DefaultCleanupInterval   = 5 * time.Minute
)

// RateLimiterConfig holds rate limiter configuration.
type RateLimiterConfig struct {
	MaxFailedAttempts int
	Window            time.Duration
	CleanupInterval   time.Duration
}

// DefaultRateLimiterConfig returns the default rate limiter configuration.
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		MaxFailedAttempts: DefaultMaxFailedAttempts,
		Window:            DefaultRateLimitWindow,
		CleanupInterval:   DefaultCleanupInterval,
	}
}

// attemptInfo tracks failed authentication attempts for an IP.
type attemptInfo struct {
	count     int
	firstFail time.Time
}

// RateLimiter tracks failed authentication attempts by IP address.
type RateLimiter struct {
	mu       sync.RWMutex
	attempts map[string]*attemptInfo
	config   RateLimiterConfig
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewRateLimiter creates a new RateLimiter with the given configuration.
func NewRateLimiter(config RateLimiterConfig) *RateLimiter {
	rl := &RateLimiter{
		attempts: make(map[string]*attemptInfo),
		config:   config,
		stopCh:   make(chan struct{}),
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}

// cleanup periodically removes expired entries.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.removeExpired()
		}
	}
}

// removeExpired removes entries that are outside the rate limit window.
func (rl *RateLimiter) removeExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, info := range rl.attempts {
		if now.Sub(info.firstFail) > rl.config.Window {
			delete(rl.attempts, ip)
		}
	}
}

// Stop stops the cleanup goroutine.
func (rl *RateLimiter) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stopCh)
	})
}

// IsLimited returns true if the IP has exceeded the maximum failed attempts.
func (rl *RateLimiter) IsLimited(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	info, exists := rl.attempts[ip]
	if !exists {
		return false
	}

	// Reset if outside window
	if time.Since(info.firstFail) > rl.config.Window {
		return false
	}

	return info.count >= rl.config.MaxFailedAttempts
}

// RecordFailure records a failed authentication attempt for the IP.
func (rl *RateLimiter) RecordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	info, exists := rl.attempts[ip]
	if !exists || time.Since(info.firstFail) > rl.config.Window {
		rl.attempts[ip] = &attemptInfo{
			count:     1,
			firstFail: time.Now(),
		}
		return
	}

	info.count++
}

// Reset clears the failed attempts for the IP.
func (rl *RateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// GetClientIP extracts the client IP from the request.
// It checks X-Forwarded-For and X-Real-IP headers first.
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}
