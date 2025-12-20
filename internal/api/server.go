// Package api provides HTTP server functionality for the HLS pipeline.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/amillerrr/hls-pipeline/internal/auth"
	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/health"
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

// Server configuration constants
const (
	ReadTimeout       = 30 * time.Second
	ReadHeaderTimeout = 10 * time.Second
	WriteTimeout      = 300 * time.Second
	IdleTimeout       = 120 * time.Second
	MaxHeaderBytes    = 1 << 20 // 1 MB
)

// Server represents the HTTP server for the API.
type Server struct {
	httpServer    *http.Server
	cfg           *config.Config
	log           *slog.Logger
	jwtService    *auth.JWTService
	rateLimiter   *auth.RateLimiter
	healthChecker *health.Checker
}

// ServerConfig holds dependencies for the server.
type ServerConfig struct {
	Config        *config.Config
	Logger        *slog.Logger
	S3Client      *storage.S3Client
	SQSClient     health.SQSClient
	VideoRepo     *storage.VideoRepository
	JWTService    *auth.JWTService
	RateLimiter   *auth.RateLimiter
	HealthChecker *health.Checker
}

// NewServer creates a new API server.
func NewServer(cfg *ServerConfig) (*Server, error) {
	handlers := NewHandlers(&HandlersConfig{
		Config:     cfg.Config,
		Logger:     cfg.Logger,
		S3Client:   cfg.S3Client,
		VideoRepo:  cfg.VideoRepo,
		JWTService: cfg.JWTService,
	})

	// Setup routing
	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/health", cfg.HealthChecker.Handler())
	mux.HandleFunc("/health/deep", cfg.HealthChecker.DeepHandler())
	mux.HandleFunc("/login", handlers.LoginHandler)
	mux.HandleFunc("/latest", handlers.GetLatestVideoHandler)

	// Protected endpoints
	authMiddleware := cfg.JWTService.Middleware(cfg.RateLimiter)
	mux.HandleFunc("/upload/init", authMiddleware(handlers.InitUploadHandler))
	mux.HandleFunc("/upload/complete", authMiddleware(handlers.CompleteUploadHandler))

	// Metrics endpoint (internal only)
	mux.Handle("/metrics", internalOnlyMiddleware(promhttp.Handler()))

	// Apply CORS middleware
	handler := CORSMiddleware(cfg.Config.CORS.AllowedOrigins)(mux)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Config.API.Port,
		Handler:           handler,
		ReadTimeout:       ReadTimeout,
		ReadHeaderTimeout: ReadHeaderTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
	}

	return &Server{
		httpServer:    httpServer,
		cfg:           cfg.Config,
		log:           cfg.Logger,
		jwtService:    cfg.JWTService,
		rateLimiter:   cfg.RateLimiter,
		healthChecker: cfg.HealthChecker,
	}, nil
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.log.Info("Starting API server", "port", s.cfg.API.Port)
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.log.Info("Shutting down API server...")

	// Stop rate limiter cleanup goroutine
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}

	return s.httpServer.Shutdown(ctx)
}

// Private networks for internal-only middleware
var privateNetworks = []net.IPNet{
	{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
	{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
	{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
	{IP: net.ParseIP("127.0.0.0"), Mask: net.CIDRMask(8, 32)},
}

// internalOnlyMiddleware restricts access to internal networks.
func internalOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deny if X-Forwarded-For is present (came through load balancer)
		if r.Header.Get("X-Forwarded-For") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Verify connection is from internal network
		if isInternalRequest(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

// isInternalRequest checks if the request is from an internal network.
func isInternalRequest(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	for _, network := range privateNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return ip.IsLoopback()
}
