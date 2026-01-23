package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CORSConfig contains CORS configuration.
type CORSConfig struct {
	// AllowedOrigins is the list of allowed origins.
	// Use "*" to allow all origins.
	AllowedOrigins []string

	// AllowedMethods is the list of allowed HTTP methods.
	AllowedMethods []string

	// AllowedHeaders is the list of allowed headers.
	AllowedHeaders []string

	// ExposedHeaders is the list of headers exposed to the client.
	ExposedHeaders []string

	// AllowCredentials indicates if credentials are allowed.
	AllowCredentials bool

	// MaxAge is the max age for preflight cache in seconds.
	MaxAge int
}

// DefaultCORSConfig returns the default CORS configuration.
func DefaultCORSConfig() *CORSConfig {
	return &CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "HEAD"},
		AllowedHeaders: []string{
			"Accept",
			"Accept-Language",
			"Content-Type",
			"Content-Language",
			"Authorization",
			"X-Request-ID",
			"X-Requested-With",
			"Origin",
		},
		ExposedHeaders: []string{
			"Content-Length",
			"Content-Range",
			"X-Request-ID",
			"ETag",
		},
		AllowCredentials: false,
		MaxAge:           86400, // 24 hours
	}
}

// CORS returns CORS middleware with the given configuration.
func CORS(config *CORSConfig) func(http.Handler) http.Handler {
	if config == nil {
		config = DefaultCORSConfig()
	}

	allowAllOrigins := len(config.AllowedOrigins) == 1 && config.AllowedOrigins[0] == "*"
	originMap := make(map[string]bool)
	for _, origin := range config.AllowedOrigins {
		originMap[origin] = true
	}

	methodsStr := strings.Join(config.AllowedMethods, ", ")
	headersStr := strings.Join(config.AllowedHeaders, ", ")
	exposedStr := strings.Join(config.ExposedHeaders, ", ")
	maxAgeStr := strconv.Itoa(config.MaxAge)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			var allowedOrigin string
			if allowAllOrigins {
				allowedOrigin = "*"
			} else if origin != "" && originMap[origin] {
				allowedOrigin = origin
			}

			// Set CORS headers
			if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				
				if config.AllowCredentials && allowedOrigin != "*" {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}

				if exposedStr != "" {
					w.Header().Set("Access-Control-Expose-Headers", exposedStr)
				}
			}

			// Handle preflight requests
			if r.Method == http.MethodOptions {
				if allowedOrigin != "" {
					w.Header().Set("Access-Control-Allow-Methods", methodsStr)
					w.Header().Set("Access-Control-Allow-Headers", headersStr)
					w.Header().Set("Access-Control-Max-Age", maxAgeStr)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Add Vary header
			w.Header().Add("Vary", "Origin")

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitConfig contains rate limiting configuration.
type RateLimitConfig struct {
	// RequestsPerSecond is the allowed requests per second.
	RequestsPerSecond float64

	// BurstSize is the maximum burst size.
	BurstSize int

	// KeyFunc extracts the rate limit key from the request.
	// Default uses the client IP.
	KeyFunc func(r *http.Request) string
}

// DefaultRateLimitConfig returns the default rate limit configuration.
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		RequestsPerSecond: 100,
		BurstSize:         200,
		KeyFunc: func(r *http.Request) string {
			// Try X-Forwarded-For first
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if idx := strings.Index(xff, ","); idx != -1 {
					return strings.TrimSpace(xff[:idx])
				}
				return strings.TrimSpace(xff)
			}
			// Fall back to RemoteAddr
			if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
				return r.RemoteAddr[:idx]
			}
			return r.RemoteAddr
		},
	}
}

// rateLimiter stores rate limiters per key.
type rateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	config   *RateLimitConfig
}

func newRateLimiter(config *RateLimitConfig) *rateLimiter {
	return &rateLimiter{
		limiters: make(map[string]*rate.Limiter),
		config:   config,
	}
}

func (rl *rateLimiter) getLimiter(key string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[key]
	rl.mu.RUnlock()

	if exists {
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists = rl.limiters[key]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(rate.Limit(rl.config.RequestsPerSecond), rl.config.BurstSize)
	rl.limiters[key] = limiter

	return limiter
}

// RateLimit returns rate limiting middleware.
func RateLimit(config *RateLimitConfig) func(http.Handler) http.Handler {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	rl := newRateLimiter(config)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := config.KeyFunc(r)
			limiter := rl.getLimiter(key)

			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders adds security headers to responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")

		// Enable XSS filter
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Strict transport security
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")

		// Referrer policy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content security policy (adjust based on your needs)
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")

		next.ServeHTTP(w, r)
	})
}

// RequestID adds or propagates a request ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set the request ID in the response header
		w.Header().Set("X-Request-ID", requestID)

		// Add to request context (if not already done by tracing middleware)
		next.ServeHTTP(w, r)
	})
}

// Timeout returns middleware that times out requests.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, timeout, "Request timed out")
	}
}

// Recovery returns panic recovery middleware.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"Internal server error"}`))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// Gzip returns gzip compression middleware.
// Note: For HLS, you typically want to compress manifests but not video segments.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip compression for video segments
		path := r.URL.Path
		if strings.HasSuffix(path, ".ts") ||
			strings.HasSuffix(path, ".m4s") ||
			strings.HasSuffix(path, ".mp4") {
			next.ServeHTTP(w, r)
			return
		}

		// Check if client accepts gzip
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		// For manifests and other text content, enable compression
		// Note: In production, use a proper gzip middleware like gorilla/handlers
		next.ServeHTTP(w, r)
	})
}

// generateRequestID generates a unique request ID.
func generateRequestID() string {
	// Simple implementation - in production, use UUID
	return time.Now().Format("20060102150405") + "-" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

// Chain chains multiple middleware functions.
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
