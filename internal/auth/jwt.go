package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Configuration constants
const (
	MinSecretLength   = 32
	TokenExpiration   = 24 * time.Hour
	MaxFailedAttempts = 5
	RateLimitWindow   = 15 * time.Minute
	CleanupInterval   = 5 * time.Minute
)

// Errors
var (
	ErrMissingSecret     = errors.New("JWT_SECRET environment variable is not set")
	ErrSecretTooShort    = errors.New("JWT_SECRET must be at least 32 characters")
	ErrMissingAuthHeader = errors.New("authorization header missing")
	ErrInvalidAuthFormat = errors.New("invalid authorization format")
	ErrInvalidToken      = errors.New("invalid or expired token")
	ErrRateLimited       = errors.New("too many failed attempts, try again later")
)

var jwtKey []byte

type contextKey string

var claimsKey contextKey = "claims"

// Simple rate limiter for failed auth attempts
type rateLimiter struct {
	mu       sync.RWMutex
	attempts map[string]*attemptInfo
}

type attemptInfo struct {
	count     int
	firstFail time.Time
}

var authRateLimiter = &rateLimiter{
	attempts: make(map[string]*attemptInfo),
}

func init() {
	secret := os.Getenv("JWT_SECRET")
	env := os.Getenv("ENV")
	isProduction := env == "prod" || env == "production"

	if secret == "" {
		if isProduction {
			panic("FATAL: JWT_SECRET environment variable is not set")
		}
		fmt.Fprintln(os.Stderr, "WARNING: Using default JWT secret - DO NOT USE IN PRODUCTION")
		secret = "default_secret_do_not_use_in_prod"
	}

	// Validate key length
	if len(secret) < MinSecretLength {
		if isProduction {
			panic("FATAL: JWT_SECRET must be at least 32 characters")
		}
		fmt.Fprintln(os.Stderr, "WARNING: JWT_SECRET should be at least 32 characters")
	}

	jwtKey = []byte(secret)

	// Start cleanup goroutine for rate limiter
	go authRateLimiter.cleanup()
}

func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, info := range rl.attempts {
			if now.Sub(info.firstFail) > RateLimitWindow {
				delete(rl.attempts, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) isRateLimited(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	info, exists := rl.attempts[ip]
	if !exists {
		return false
	}

	// Reset if outside window
	if time.Since(info.firstFail) > RateLimitWindow {
		return false
	}

	return info.count >= MaxFailedAttempts
}

func (rl *rateLimiter) recordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	info, exists := rl.attempts[ip]
	if !exists || time.Since(info.firstFail) > RateLimitWindow {
		rl.attempts[ip] = &attemptInfo{
			count:     1,
			firstFail: time.Now(),
		}
		return
	}

	info.count++
}

func (rl *rateLimiter) resetAttempts(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// Extract the client IP from the request
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
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
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

// Checks if an IP is rate limited
func IsRateLimited(ip string) bool {
	return authRateLimiter.isRateLimited(ip)
}

// Records a failed authentication attempt
func RecordAuthFailure(ip string) {
	authRateLimiter.recordFailure(ip)
}

// Resets the failed attempts counter for an IP
func ResetAuthAttempts(ip string) {
	authRateLimiter.resetAttempts(ip)
}

type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func GenerateToken(username string) (string, error) {
	if username == "" {
		return "", errors.New("username cannot be empty")
	}

	expirationTime := time.Now().Add(24 * time.Hour)

	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "hls-pipeline",
			Subject:   username,
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

// Validate JWT token and return
func ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	if claims.Username == "" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// Wrap HTTP handler for JWT authentication
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetClientIP(r)

		if IsRateLimited(clientIP) {
			http.Error(w, "Too many failed attempts, try again later", http.StatusTooManyRequests)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			RecordAuthFailure(clientIP)
			http.Error(w, "Authorization header missing", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			RecordAuthFailure(clientIP)
			http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		if tokenString == "" {
			RecordAuthFailure(clientIP)
			http.Error(w, "Token is empty", http.StatusUnauthorized)
			return
		}

		claims, err := ValidateToken(tokenString)
		if err != nil {
			RecordAuthFailure(clientIP)
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Reset counter on successful auth
		ResetAuthAttempts(clientIP)

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	}
}

// Extract claims from the request context
func GetClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}

// Extract JWT token from header
func ExtractTokenFromRequest(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrMissingAuthHeader
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrInvalidAuthFormat
	}

	return parts[1], nil
}
