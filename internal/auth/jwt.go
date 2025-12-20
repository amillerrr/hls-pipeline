package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Configuration constants
const (
	MinSecretLength = 32
	TokenExpiration = 24 * time.Hour
)

// Errors
var (
	ErrMissingSecret     = errors.New("JWT secret is not configured")
	ErrSecretTooShort    = errors.New("JWT secret must be at least 32 characters")
	ErrMissingAuthHeader = errors.New("authorization header missing")
	ErrInvalidAuthFormat = errors.New("invalid authorization format")
	ErrInvalidToken      = errors.New("invalid or expired token")
	ErrEmptyUsername     = errors.New("username cannot be empty")
)

// Claims represents the JWT claims structure.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// JWTService handles JWT token generation and validation.
type JWTService struct {
	secret []byte
	issuer string
}

// NewJWTService creates a new JWTService with the given secret.
func NewJWTService(secret []byte) (*JWTService, error) {
	if len(secret) == 0 {
		return nil, ErrMissingSecret
	}
	return &JWTService{
		secret: secret,
		issuer: "hls-pipeline",
	}, nil
}

// GenerateToken creates a new JWT token for the given username.
func (s *JWTService) GenerateToken(username string) (string, error) {
	if username == "" {
		return "", ErrEmptyUsername
	}

	now := time.Now()
	expirationTime := now.Add(TokenExpiration)

	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    s.issuer,
			Subject:   username,
			ID:        fmt.Sprintf("%d", now.UnixNano()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// ValidateToken validates a JWT token and returns the claims.
func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	if !token.Valid {
		return nil, ErrInvalidToken
	}

	if claims.Username == "" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// ExtractTokenFromRequest extracts the JWT token from the Authorization header.
func ExtractTokenFromRequest(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrMissingAuthHeader
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrInvalidAuthFormat
	}

	token := parts[1]
	if token == "" {
		return "", ErrInvalidAuthFormat
	}

	return token, nil
}

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const claimsContextKey contextKey = "claims"

// SetClaimsInContext adds claims to the request context.
func SetClaimsInContext(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

// GetClaimsFromContext retrieves claims from the request context.
func GetClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*Claims)
	return claims, ok
}

// Middleware creates an HTTP middleware that validates JWT tokens.
func (s *JWTService) Middleware(rateLimiter *RateLimiter) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			clientIP := GetClientIP(r)

			// Check rate limiting
			if rateLimiter != nil && rateLimiter.IsLimited(clientIP) {
				http.Error(w, "Too many failed attempts, try again later", http.StatusTooManyRequests)
				return
			}

			// Extract token
			tokenString, err := ExtractTokenFromRequest(r)
			if err != nil {
				if rateLimiter != nil {
					rateLimiter.RecordFailure(clientIP)
				}
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			// Validate token
			claims, err := s.ValidateToken(tokenString)
			if err != nil {
				if rateLimiter != nil {
					rateLimiter.RecordFailure(clientIP)
				}
				http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
				return
			}

			// Reset rate limiter on success
			if rateLimiter != nil {
				rateLimiter.Reset(clientIP)
			}

			// Add claims to context
			ctx := SetClaimsInContext(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		}
	}
}
