package cdn

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("cdn-router")

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

	// Healthy indicates if this provider is currently healthy.
	Healthy bool `json:"healthy"`

	// Latency is the measured latency to this CDN.
	Latency time.Duration `json:"latency"`

	// ErrorRate is the current error rate (0.0-1.0).
	ErrorRate float64 `json:"errorRate"`

	// Region specifies which regions this CDN serves best.
	Regions []string `json:"regions,omitempty"`

	// Features lists supported features (e.g., "ll-hls", "drm", "ssai").
	Features []string `json:"features,omitempty"`

	// Headers contains custom headers to add for this CDN.
	Headers map[string]string `json:"headers,omitempty"`

	// mu protects mutable fields.
	mu sync.RWMutex

	// Internal tracking
	consecutiveFailures int
	lastCheck           time.Time
	successCount        int64
	errorCount          int64
}

// RouterConfig contains configuration for the CDN router.
type RouterConfig struct {
	// Providers is the list of CDN providers.
	Providers []*Provider `json:"providers"`

	// HealthCheckInterval is how often to check CDN health.
	HealthCheckInterval time.Duration `json:"healthCheckInterval"`

	// HealthCheckTimeout is the timeout for health checks.
	HealthCheckTimeout time.Duration `json:"healthCheckTimeout"`

	// FailoverThreshold is the number of consecutive failures before failover.
	FailoverThreshold int `json:"failoverThreshold"`

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
}

// DefaultRouterConfig returns the default router configuration.
func DefaultRouterConfig() *RouterConfig {
	return &RouterConfig{
		Providers:             []*Provider{},
		HealthCheckInterval:   30 * time.Second,
		HealthCheckTimeout:    5 * time.Second,
		FailoverThreshold:     3,
		ErrorRateThreshold:    0.1, // 10% error rate
		EnableWeightedRouting: true,
		EnableLatencyRouting:  false,
		EnableRegionRouting:   false,
		DefaultRegion:         "us-east-1",
	}
}

// Router handles multi-CDN routing.
type Router struct {
	config     *RouterConfig
	httpClient *http.Client
	logger     *slog.Logger
	mu         sync.RWMutex
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewRouter creates a new CDN router.
func NewRouter(config *RouterConfig, logger *slog.Logger) *Router {
	if config == nil {
		config = DefaultRouterConfig()
	}

	return &Router{
		config: config,
		httpClient: &http.Client{
			Timeout: config.HealthCheckTimeout,
		},
		logger: logger,
		stopCh: make(chan struct{}),
	}
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

	ticker := time.NewTicker(r.config.HealthCheckInterval)
	defer ticker.Stop()

	// Initial health check
	r.checkAllProviders(ctx)

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
	provider.lastCheck = time.Now()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		provider.consecutiveFailures = 0
		provider.Healthy = true
		r.logger.DebugContext(ctx, "CDN health check passed",
			"cdn", provider.Name,
			"latency", latency,
		)
	} else {
		provider.consecutiveFailures++
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
	provider.lastCheck = time.Now()

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
		return nil, fmt.Errorf("no healthy CDN providers available")
	}

	var selected *Provider

	// Apply routing strategy
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
		// Fallback to first healthy provider by priority
		selected = healthy[0]
		for _, p := range healthy {
			if p.Priority < selected.Priority {
				selected = p
			}
		}
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

		if pLatency > 0 && pLatency < bestLatency {
			best = p
		}
	}
	return best
}

// selectByWeight selects a CDN using weighted random selection.
func (r *Router) selectByWeight(providers []*Provider) *Provider {
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

// RecordSuccess records a successful request to a CDN.
func (r *Router) RecordSuccess(provider *Provider) {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	provider.successCount++
	provider.consecutiveFailures = 0

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
	provider.mu.Lock()
	defer provider.mu.Unlock()

	provider.errorCount++
	provider.consecutiveFailures++

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
	urlPattern := regexp.MustCompile(`(?m)^([^#\s].+\.(m3u8|m4s|ts|mp4|key))$`)

	rewritten := urlPattern.ReplaceAllStringFunc(manifest, func(match string) string {
		// If already absolute URL, replace host
		if strings.HasPrefix(match, "http://") || strings.HasPrefix(match, "https://") {
			parsed, err := url.Parse(match)
			if err != nil {
				return match
			}
			parsed.Scheme = baseURL.Scheme
			parsed.Host = baseURL.Host
			return parsed.String()
		}

		// Relative URL - prepend CDN base
		return baseURL.String() + "/" + strings.TrimPrefix(match, "/")
	})

	// Add CDN-specific headers if configured
	if len(provider.Headers) > 0 {
		var headerLines []string
		for k, v := range provider.Headers {
			headerLines = append(headerLines, fmt.Sprintf("#EXT-X-CDN-HEADER:%s=%s", k, v))
		}
		rewritten = strings.Join(headerLines, "\n") + "\n" + rewritten
	}

	return rewritten, nil
}

// GetProviderByName returns a provider by name.
func (r *Router) GetProviderByName(name string) *Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.config.Providers {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// GetAllProviders returns all providers.
func (r *Router) GetAllProviders() []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]*Provider, len(r.config.Providers))
	copy(providers, r.config.Providers)
	return providers
}

// AddProvider adds a new CDN provider.
func (r *Router) AddProvider(provider *Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	provider.Healthy = true
	r.config.Providers = append(r.config.Providers, provider)
}

// RemoveProvider removes a CDN provider by name.
func (r *Router) RemoveProvider(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, p := range r.config.Providers {
		if p.Name == name {
			r.config.Providers = append(r.config.Providers[:i], r.config.Providers[i+1:]...)
			return true
		}
	}
	return false
}

// UpdateWeight updates the weight for a provider.
func (r *Router) UpdateWeight(name string, weight int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, p := range r.config.Providers {
		if p.Name == name {
			p.Weight = weight
			return true
		}
	}
	return false
}

// GetStats returns current CDN statistics.
func (r *Router) GetStats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[string]interface{})
	providerStats := make([]map[string]interface{}, 0)

	for _, p := range r.config.Providers {
		p.mu.RLock()
		pStats := map[string]interface{}{
			"name":          p.Name,
			"healthy":       p.Healthy,
			"weight":        p.Weight,
			"priority":      p.Priority,
			"latency_ms":    p.Latency.Milliseconds(),
			"error_rate":    p.ErrorRate,
			"success_count": p.successCount,
			"error_count":   p.errorCount,
			"last_check":    p.lastCheck,
		}
		p.mu.RUnlock()
		providerStats = append(providerStats, pStats)
	}

	stats["providers"] = providerStats
	stats["total_providers"] = len(r.config.Providers)
	stats["healthy_providers"] = len(r.filterHealthy(r.config.Providers))

	return stats
}
