package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewJWTService(t *testing.T) {
	tests := []struct {
		name    string
		secret  []byte
		wantErr error
	}{
		{"valid secret", []byte("a-very-long-secret-that-is-at-least-32-chars"), nil},
		{"short secret", []byte("short"), nil}, // Still valid, just not recommended
		{"empty secret", []byte{}, ErrMissingSecret},
		{"nil secret", nil, ErrMissingSecret},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewJWTService(tt.secret)
			if err != tt.wantErr {
				t.Errorf("NewJWTService() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestJWTService_GenerateAndValidate(t *testing.T) {
	secret := []byte("test-secret-that-is-long-enough-for-testing")
	svc, err := NewJWTService(secret)
	if err != nil {
		t.Fatalf("NewJWTService() error = %v", err)
	}

	// Generate token
	token, err := svc.GenerateToken("testuser")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	if token == "" {
		t.Fatal("GenerateToken() returned empty token")
	}

	// Validate token
	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	if claims.Username != "testuser" {
		t.Errorf("claims.Username = %s, want testuser", claims.Username)
	}
}

func TestJWTService_GenerateToken_EmptyUsername(t *testing.T) {
	secret := []byte("test-secret-that-is-long-enough-for-testing")
	svc, _ := NewJWTService(secret)

	_, err := svc.GenerateToken("")
	if err != ErrEmptyUsername {
		t.Errorf("GenerateToken(\"\") error = %v, want %v", err, ErrEmptyUsername)
	}
}

func TestJWTService_ValidateToken_Invalid(t *testing.T) {
	secret := []byte("test-secret-that-is-long-enough-for-testing")
	svc, _ := NewJWTService(secret)

	tests := []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"invalid format", "not-a-jwt"},
		{"wrong signature", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6InRlc3QifQ.wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.ValidateToken(tt.token)
			if err == nil {
				t.Error("ValidateToken() expected error for invalid token")
			}
		})
	}
}

func TestJWTService_ValidateToken_WrongSecret(t *testing.T) {
	svc1, _ := NewJWTService([]byte("secret-one-that-is-long-enough"))
	svc2, _ := NewJWTService([]byte("secret-two-that-is-different"))

	token, _ := svc1.GenerateToken("testuser")

	_, err := svc2.ValidateToken(token)
	if err == nil {
		t.Error("ValidateToken() should fail with wrong secret")
	}
}

func TestExtractTokenFromRequest(t *testing.T) {
	tests := []struct {
		name      string
		authValue string
		wantToken string
		wantErr   error
	}{
		{"valid bearer", "Bearer eyJtoken", "eyJtoken", nil},
		{"valid bearer lowercase", "bearer eyJtoken", "eyJtoken", nil},
		{"missing header", "", "", ErrMissingAuthHeader},
		{"invalid format no space", "BearereyJtoken", "", ErrInvalidAuthFormat},
		{"invalid format wrong prefix", "Basic eyJtoken", "", ErrInvalidAuthFormat},
		{"empty token", "Bearer ", "", ErrInvalidAuthFormat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authValue != "" {
				req.Header.Set("Authorization", tt.authValue)
			}

			token, err := ExtractTokenFromRequest(req)
			if err != tt.wantErr {
				t.Errorf("ExtractTokenFromRequest() error = %v, want %v", err, tt.wantErr)
			}
			if token != tt.wantToken {
				t.Errorf("ExtractTokenFromRequest() = %s, want %s", token, tt.wantToken)
			}
		})
	}
}

func TestClaimsContext(t *testing.T) {
	claims := &Claims{Username: "testuser"}
	ctx := context.Background()

	// Set claims
	ctx = SetClaimsInContext(ctx, claims)

	// Get claims
	got, ok := GetClaimsFromContext(ctx)
	if !ok {
		t.Fatal("GetClaimsFromContext() ok = false, want true")
	}
	if got.Username != "testuser" {
		t.Errorf("GetClaimsFromContext().Username = %s, want testuser", got.Username)
	}

	// Test missing claims
	_, ok = GetClaimsFromContext(context.Background())
	if ok {
		t.Error("GetClaimsFromContext() ok = true for empty context, want false")
	}
}

func TestRateLimiter_IsLimited(t *testing.T) {
	config := RateLimiterConfig{
		MaxFailedAttempts: 3,
		Window:            time.Minute,
		CleanupInterval:   time.Hour, // Don't cleanup during test
	}
	rl := NewRateLimiter(config)
	defer rl.Stop()

	ip := "192.168.1.1"

	// Should not be limited initially
	if rl.IsLimited(ip) {
		t.Error("IsLimited() = true before any failures")
	}

	// Record failures
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)
	if rl.IsLimited(ip) {
		t.Error("IsLimited() = true after 2 failures (max is 3)")
	}

	// Third failure should trigger limit
	rl.RecordFailure(ip)
	if !rl.IsLimited(ip) {
		t.Error("IsLimited() = false after 3 failures")
	}

	// Reset should clear the limit
	rl.Reset(ip)
	if rl.IsLimited(ip) {
		t.Error("IsLimited() = true after Reset()")
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	config := RateLimiterConfig{
		MaxFailedAttempts: 1,
		Window:            50 * time.Millisecond,
		CleanupInterval:   time.Hour,
	}
	rl := NewRateLimiter(config)
	defer rl.Stop()

	ip := "192.168.1.1"

	rl.RecordFailure(ip)
	if !rl.IsLimited(ip) {
		t.Error("IsLimited() = false immediately after failure")
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	if rl.IsLimited(ip) {
		t.Error("IsLimited() = true after window expired")
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		want       string
	}{
		{"X-Forwarded-For single", "192.168.1.1", "", "127.0.0.1:8080", "192.168.1.1"},
		{"X-Forwarded-For multiple", "192.168.1.1, 10.0.0.1, 172.16.0.1", "", "127.0.0.1:8080", "192.168.1.1"},
		{"X-Real-IP", "", "192.168.1.1", "127.0.0.1:8080", "192.168.1.1"},
		{"RemoteAddr with port", "", "", "192.168.1.1:12345", "192.168.1.1"},
		{"RemoteAddr without port", "", "", "192.168.1.1", "192.168.1.1"},
		{"X-Forwarded-For takes precedence", "10.0.0.1", "192.168.1.1", "127.0.0.1:8080", "10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}

			got := GetClientIP(req)
			if got != tt.want {
				t.Errorf("GetClientIP() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestJWTService_Middleware(t *testing.T) {
	secret := []byte("test-secret-that-is-long-enough-for-testing")
	svc, _ := NewJWTService(secret)
	rl := NewRateLimiter(DefaultRateLimiterConfig())
	defer rl.Stop()

	// Create a test handler that checks for claims
	handler := svc.Middleware(rl)(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := GetClaimsFromContext(r.Context())
		if !ok {
			t.Error("Claims not found in context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(claims.Username))
	})

	// Generate valid token
	token, _ := svc.GenerateToken("testuser")

	t.Run("valid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("handler returned %d, want %d", rr.Code, http.StatusOK)
		}
		if rr.Body.String() != "testuser" {
			t.Errorf("handler returned %s, want testuser", rr.Body.String())
		}
	})

	t.Run("missing token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("handler returned %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("handler returned %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}
