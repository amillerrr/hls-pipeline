package cdn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("hls-pipeline/cdn")

// Common errors
var (
	ErrNoCDNAvailable    = errors.New("no CDN available")
	ErrInvalidConfig     = errors.New("invalid CDN configuration")
	ErrHealthCheckFailed = errors.New("health check failed")
)

// SelectionStrategy defines how CDN selection is performed.
type SelectionStrategy int

const (
	StrategyWeightedRoundRobin SelectionStrategy = iota
	StrategyLowestLatency
	StrategyGeoBased
	StrategyFailoverOnly
	StrategyRandom
)

// Provider represents a CDN provider.
type Provider struct {
	// Name is the unique identifier for this CDN.
	Name string `json:"name"`

	// BaseURL is the base URL for this CDN.
	BaseURL string `json:"baseUrl"`

	// Weight is the traffic weight (0-100) for weighted routing.
	Weight int `json:"weight"`

	// Priority is used for failover (lower = higher priority).
	Priority int `json:"priority"`

	// HealthURL is the URL used for health checks.
	HealthURL string `json:"healthUrl"`

	// SigningKey for URL signing (if required).
	SigningKey string `json:"-"`

	// Healthy indicates if this provider is currently healthy.
	Healthy bool `json:"healthy"`

	// Latency is the measured latency to this CDN.
	Latency time.Duration `json:"latency"`

	// ErrorRate is the current error rate (0.0-1.0).
	ErrorRate float64 `json:"errorRate"`

	// Regions specifies which regions this CDN serves best.
	Regions []string `json:"regions,omitempty"`

	// Features lists supported features (e.g., "ll-hls", "drm", "ssai").
	Features []string `json:"features,omitempty"`

	// Headers contains custom headers to add for this CDN.
	Headers map[string]string `json:"headers,omitempty"`

	// LastCheck is when the last health check was performed.
	LastCheck time.Time `json:"lastCheck"`

	// RequestCount tracks total requests to this provider.
	RequestCount int64 `json:"requestCount"`

	// FailureCount tracks consecutive failures.
	FailureCount int32 `json:"failureCount"`

	// mu protects mutable fields.
	mu sync.RWMutex

	// Internal tracking
	consecutiveFailures int
	successCount        int64
	errorCount          int64
}

// RouterConfig contains configuration for the CDN router.
type RouterConfig struct {
	// Providers is the list of CDN providers.
	Providers []*Provider `json:"providers"`

	// Strategy is the selection strategy.
	Strategy SelectionStrategy `json:"strategy"`

	// HealthCheckInterval is how often to check CDN health.
	HealthCheckInterval time.Duration `json:"healthCheckInterval"`

	// HealthCheckTimeout is the timeout for health checks.
	HealthCheckTimeout time.Duration `json:"healthCheckTimeout"`

	// FailoverThreshold is the number of consecutive failures before failover.
	FailoverThreshold int `json:"failoverThreshold"`

	// MaxFailures is an alias for FailoverThreshold (backwards compatibility).
	MaxFailures int32 `json:"maxFailures"`

	// ErrorRateThreshold is the error rate above which a CDN is marked unhealthy.
	ErrorRateThreshold float64 `json:"errorRateThreshold"`

	// EnableWeightedRouting enables weighted traffic distribution.
	EnableWeightedRouting bool `json:"enableWeightedRouting"`

	// EnableLatencyRouting enables latency-based routing.
	EnableLatencyRouting bool `json:"enableLatencyRouting"`

	// EnableRegionRouting enables region-based routing.
	EnableRegionRouting bool `json:"enableRegionRouting"`

	// DefaultRegion is the default region when client region is unknown.
	DefaultRegion string `json:"defaultRegion"`

	// RecoveryPeriod is how long to wait before trying an unhealthy provider again.
	RecoveryPeriod time.Duration `json:"recoveryPeriod"`
}

// DefaultRouterConfig returns the default router configuration.
func DefaultRouterConfig() *RouterConfig {
	return &RouterConfig{
		Providers:             []*Provider{},
		Strategy:              StrategyWeightedRoundRobin,
		HealthCheckInterval:   30 * time.Second,
		HealthCheckTimeout:    5 * time.Second,
		FailoverThreshold:     3,
		MaxFailures:           3,
		ErrorRateThreshold:    0.1, // 10% error rate
		EnableWeightedRouting: true,
		EnableLatencyRouting:  false,
		EnableRegionRouting:   false,
		DefaultRegion:         "us-east-1",
		RecoveryPeriod:        60 * time.Second,
	}
}

// Router handles multi-CDN routing.
type Router struct {
	config      *RouterConfig
	httpClient  *http.Client
	logger      *slog.Logger
	mu          sync.RWMutex
	totalWeight int
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewRouter creates a new CDN router.
func NewRouter(config *RouterConfig, logger *slog.Logger) (*Router, error) {
	if config == nil {
		config = DefaultRouterConfig()
	}

	if len(config.Providers) == 0 {
		// Return router without providers (can be added later)
		return &Router{
			config: config,
			httpClient: &http.Client{
				Timeout: config.HealthCheckTimeout,
			},
			logger: logger,
			stopCh: make(chan struct{}),
		}, nil
	}

	totalWeight := 0
	for _, p := range config.Providers {
		if p.Name == "" || p.BaseURL == "" {
			return nil, fmt.Errorf("%w: provider missing name or base URL", ErrInvalidConfig)
		}
		if p.Weight <= 0 {
			p.Weight = 1
		}
		totalWeight += p.Weight
		p.Healthy = true
	}

	// Sort by priority
	sort.Slice(config.Providers, func(i, j int) bool {
		return config.Providers[i].Priority < config.Providers[j].Priority
	})

	return &Router{
		config:      config,
		totalWeight: totalWeight,
		logger:      logger,
		httpClient: &http.Client{
			Timeout: config.HealthCheckTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		stopCh: make(chan struct{}),
	}, nil
}

// Start starts the health check goroutine.
func (r *Router) Start(ctx context.Context) {
	r.wg.Add(1)
	go r.healthCheckLoop(ctx)
}

// Stop stops the router.
func (r *Router) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

// healthCheckLoop periodically checks CDN health.
func (r *Router) healthCheckLoop(ctx context.Context) {
	defer r.wg.Done()

	// Initial health check
	r.checkAllProviders(ctx)

	ticker := time.NewTicker(r.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.checkAllProviders(ctx)
		}
	}
}

// checkAllProviders checks health of all providers.
func (r *Router) checkAllProviders(ctx context.Context) {
	r.mu.RLock()
	providers := make([]*Provider, len(r.config.Providers))
	copy(providers, r.config.Providers)
	r.mu.RUnlock()

	var wg sync.WaitGroup
	for _, provider := range providers {
		wg.Add(1)
		go func(p *Provider) {
			defer wg.Done()
			r.checkProvider(ctx, p)
		}(provider)
	}
	wg.Wait()
}

// checkProvider checks health of a single provider.
func (r *Router) checkProvider(ctx context.Context, provider *Provider) {
	ctx, span := tracer.Start(ctx, "cdn-health-check",
		trace.WithAttributes(
			attribute.String("cdn.name", provider.Name),
			attribute.String("cdn.health_url", provider.HealthURL),
		))
	defer span.End()

	if provider.HealthURL == "" {
		provider.mu.Lock()
		provider.Healthy = true
		provider.mu.Unlock()
		return
	}

	startTime := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.HealthURL, nil)
	if err != nil {
		r.markProviderUnhealthy(provider, err)
		span.RecordError(err)
		return
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		r.markProviderUnhealthy(provider, err)
		span.RecordError(err)
		return
	}
	defer resp.Body.Close()

	latency := time.Since(startTime)

	provider.mu.Lock()
	provider.Latency = latency
	provider.LastCheck = time.Now()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		provider.consecutiveFailures = 0
		atomic.StoreInt32(&provider.FailureCount, 0)
		provider.Healthy = true
		r.logger.DebugContext(ctx, "CDN health check passed",
			"cdn", provider.Name,
			"latency", latency,
		)
	} else {
		provider.consecutiveFailures++
		atomic.AddInt32(&provider.FailureCount, 1)
		if provider.consecutiveFailures >= r.config.FailoverThreshold {
			provider.Healthy = false
			r.logger.WarnContext(ctx, "CDN marked unhealthy",
				"cdn", provider.Name,
				"consecutive_failures", provider.consecutiveFailures,
			)
		}
	}
	provider.mu.Unlock()

	span.SetAttributes(
		attribute.Bool("cdn.healthy", provider.Healthy),
		attribute.Int64("cdn.latency_ms", latency.Milliseconds()),
	)
}

// markProviderUnhealthy marks a provider as unhealthy.
func (r *Router) markProviderUnhealthy(provider *Provider, err error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	provider.consecutiveFailures++
	provider.LastCheck = time.Now()
	atomic.AddInt32(&provider.FailureCount, 1)

	if provider.consecutiveFailures >= r.config.FailoverThreshold {
		provider.Healthy = false
		r.logger.Warn("CDN marked unhealthy due to health check failure",
			"cdn", provider.Name,
			"error", err,
			"consecutive_failures", provider.consecutiveFailures,
		)
	}
}

// SelectCDN selects the best CDN for a request.
func (r *Router) SelectCDN(ctx context.Context, clientRegion string) (*Provider, error) {
	ctx, span := tracer.Start(ctx, "cdn-select",
		trace.WithAttributes(
			attribute.String("client.region", clientRegion),
		))
	defer span.End()

	r.mu.RLock()
	providers := make([]*Provider, len(r.config.Providers))
	copy(providers, r.config.Providers)
	r.mu.RUnlock()

	// Filter healthy providers
	healthy := r.filterHealthy(providers)
	if len(healthy) == 0 {
		return nil, ErrNoCDNAvailable
	}

	var selected *Provider

	// Apply routing strategy based on config
	switch r.config.Strategy {
	case StrategyGeoBased:
		if clientRegion != "" {
			selected = r.selectByRegion(healthy, clientRegion)
		}
	case StrategyLowestLatency:
		selected = r.selectByLatency(healthy)
	case StrategyWeightedRoundRobin:
		selected = r.selectByWeight(healthy)
	case StrategyFailoverOnly:
		selected = r.selectByPriority(healthy)
	case StrategyRandom:
		selected = r.selectRandom(healthy)
	}

	// Fallback selection if strategy didn't select
	if selected == nil {
		if r.config.EnableRegionRouting && clientRegion != "" {
			selected = r.selectByRegion(healthy, clientRegion)
		}
		if selected == nil && r.config.EnableLatencyRouting {
			selected = r.selectByLatency(healthy)
		}
		if selected == nil && r.config.EnableWeightedRouting {
			selected = r.selectByWeight(healthy)
		}
		if selected == nil {
			selected = r.selectByPriority(healthy)
		}
	}

	if selected != nil {
		atomic.AddInt64(&selected.RequestCount, 1)
	}

	span.SetAttributes(
		attribute.String("cdn.selected", selected.Name),
		attribute.Int("cdn.healthy_count", len(healthy)),
	)

	r.logger.DebugContext(ctx, "CDN selected",
		"cdn", selected.Name,
		"region", clientRegion,
	)

	return selected, nil
}

// filterHealthy returns only healthy providers.
func (r *Router) filterHealthy(providers []*Provider) []*Provider {
	var healthy []*Provider
	for _, p := range providers {
		p.mu.RLock()
		isHealthy := p.Healthy && p.ErrorRate < r.config.ErrorRateThreshold
		p.mu.RUnlock()

		if isHealthy {
			healthy = append(healthy, p)
		}
	}
	return healthy
}

// selectByRegion selects the best CDN for a region.
func (r *Router) selectByRegion(providers []*Provider, region string) *Provider {
	for _, p := range providers {
		for _, r := range p.Regions {
			if r == region {
				return p
			}
		}
	}
	return nil
}

// selectByLatency selects the CDN with lowest latency.
func (r *Router) selectByLatency(providers []*Provider) *Provider {
	if len(providers) == 0 {
		return nil
	}

	best := providers[0]
	for _, p := range providers[1:] {
		p.mu.RLock()
		pLatency := p.Latency
		p.mu.RUnlock()

		best.mu.RLock()
		bestLatency := best.Latency
		best.mu.RUnlock()

		if pLatency > 0 && (bestLatency == 0 || pLatency < bestLatency) {
			best = p
		}
	}
	return best
}

// selectByWeight selects a CDN using weighted random selection.
func (r *Router) selectByWeight(providers []*Provider) *Provider {
	if len(providers) == 0 {
		return nil
	}
	if len(providers) == 1 {
		return providers[0]
	}

	totalWeight := 0
	for _, p := range providers {
		totalWeight += p.Weight
	}

	if totalWeight == 0 {
		return providers[rand.Intn(len(providers))]
	}

	target := rand.Intn(totalWeight)
	current := 0

	for _, p := range providers {
		current += p.Weight
		if target < current {
			return p
		}
	}

	return providers[len(providers)-1]
}

// selectByPriority selects the highest priority (lowest number) healthy provider.
func (r *Router) selectByPriority(providers []*Provider) *Provider {
	if len(providers) == 0 {
		return nil
	}

	best := providers[0]
	for _, p := range providers[1:] {
		if p.Priority < best.Priority {
			best = p
		}
	}
	return best
}

// selectRandom selects a random provider.
func (r *Router) selectRandom(providers []*Provider) *Provider {
	if len(providers) == 0 {
		return nil
	}
	if len(providers) == 1 {
		return providers[0]
	}
	return providers[rand.Intn(len(providers))]
}

// RecordSuccess records a successful request to a CDN.
func (r *Router) RecordSuccess(provider *Provider, latency ...time.Duration) {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	provider.successCount++
	provider.consecutiveFailures = 0
	atomic.StoreInt32(&provider.FailureCount, 0)

	// Update latency if provided
	if len(latency) > 0 && latency[0] > 0 {
		if provider.Latency == 0 {
			provider.Latency = latency[0]
		} else {
			// Exponential moving average
			provider.Latency = (provider.Latency*7 + latency[0]*3) / 10
		}
	}

	// Update error rate with exponential decay
	total := float64(provider.successCount + provider.errorCount)
	if total > 0 {
		provider.ErrorRate = float64(provider.errorCount) / total
	}

	// Mark as healthy if below threshold
	if provider.ErrorRate < r.config.ErrorRateThreshold {
		provider.Healthy = true
	}
}

// RecordError records a failed request to a CDN.
func (r *Router) RecordError(provider *Provider, err error) {
	r.RecordFailure(provider, err)
}

// RecordFailure records a failed request to a CDN.
func (r *Router) RecordFailure(provider *Provider, err error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	provider.errorCount++
	provider.consecutiveFailures++
	atomic.AddInt32(&provider.FailureCount, 1)

	// Update error rate
	total := float64(provider.successCount + provider.errorCount)
	if total > 0 {
		provider.ErrorRate = float64(provider.errorCount) / total
	}

	// Mark as unhealthy if above threshold
	if provider.ErrorRate >= r.config.ErrorRateThreshold ||
		provider.consecutiveFailures >= r.config.FailoverThreshold {
		provider.Healthy = false
	}
}

// GetProviders returns all configured providers.
func (r *Router) GetProviders() []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]*Provider, len(r.config.Providers))
	copy(providers, r.config.Providers)
	return providers
}

// GetHealthyCount returns the number of healthy providers.
func (r *Router) GetHealthyCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, p := range r.config.Providers {
		if p.Healthy {
			count++
		}
	}
	return count
}

// ProviderStats contains statistics for a single provider.
type ProviderStats struct {
	Healthy      bool          `json:"healthy"`
	Latency      time.Duration `json:"latency"`
	RequestCount int64         `json:"requestCount"`
	FailureCount int32         `json:"failureCount"`
	ErrorRate    float64       `json:"errorRate"`
	LastCheck    time.Time     `json:"lastCheck"`
}

// GetStats returns statistics for all providers.
func (r *Router) GetStats() map[string]ProviderStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[string]ProviderStats)
	for _, p := range r.config.Providers {
		p.mu.RLock()
		stats[p.Name] = ProviderStats{
			Healthy:      p.Healthy,
			Latency:      p.Latency,
			RequestCount: atomic.LoadInt64(&p.RequestCount),
			FailureCount: atomic.LoadInt32(&p.FailureCount),
			ErrorRate:    p.ErrorRate,
			LastCheck:    p.LastCheck,
		}
		p.mu.RUnlock()
	}
	return stats
}

// RewriteManifest rewrites URLs in an HLS manifest to use the selected CDN.
func (r *Router) RewriteManifest(ctx context.Context, manifest string, provider *Provider) (string, error) {
	ctx, span := tracer.Start(ctx, "cdn-rewrite-manifest",
		trace.WithAttributes(
			attribute.String("cdn.name", provider.Name),
		))
	defer span.End()

	if provider.BaseURL == "" {
		return manifest, nil
	}

	baseURL, err := url.Parse(provider.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid CDN base URL: %w", err)
	}

	// Pattern to match segment URLs in HLS
	urlPattern := regexp.MustCompile(`(?m)^([^#\s].+\.(m3u8|ts|m4s|mp4|aac))`)

	rewritten := urlPattern.ReplaceAllStringFunc(manifest, func(match string) string {
		// Skip absolute URLs
		if strings.HasPrefix(match, "http://") || strings.HasPrefix(match, "https://") {
			return match
		}

		// Construct new URL
		newURL := baseURL.JoinPath(match)
		return newURL.String()
	})

	return rewritten, nil
}

// ParseStrategy parses a strategy string into a SelectionStrategy.
func ParseStrategy(s string) SelectionStrategy {
	switch strings.ToLower(s) {
	case "weighted", "weighted_round_robin":
		return StrategyWeightedRoundRobin
	case "latency", "lowest_latency":
		return StrategyLowestLatency
	case "geo", "geo_based":
		return StrategyGeoBased
	case "failover", "failover_only":
		return StrategyFailoverOnly
	case "random":
		return StrategyRandom
	default:
		return StrategyWeightedRoundRobin
	}
}
