package cdn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

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
	Name         string        `json:"name"`
	BaseURL      string        `json:"baseUrl"`
	Weight       int           `json:"weight"`
	Priority     int           `json:"priority"`
	HealthURL    string        `json:"healthUrl"`
	SigningKey   string        `json:"-"`
	Region       string        `json:"region"`
	Healthy      bool          `json:"healthy"`
	Latency      time.Duration `json:"latency"`
	LastCheck    time.Time     `json:"lastCheck"`
	FailureCount int32         `json:"failureCount"`
	RequestCount int64         `json:"requestCount"`
}

// RouterConfig contains configuration for the multi-CDN router.
type RouterConfig struct {
	Providers         []*Provider
	Strategy          SelectionStrategy
	HealthCheckPeriod time.Duration
	HealthTimeout     time.Duration
	MaxFailures       int32
	RecoveryPeriod    time.Duration
}

// DefaultRouterConfig returns the default router configuration.
func DefaultRouterConfig() *RouterConfig {
	return &RouterConfig{
		Strategy:          StrategyWeightedRoundRobin,
		HealthCheckPeriod: 30 * time.Second,
		HealthTimeout:     5 * time.Second,
		MaxFailures:       3,
		RecoveryPeriod:    60 * time.Second,
	}
}

// Router manages multiple CDN providers and routes requests.
type Router struct {
	config      *RouterConfig
	providers   []*Provider
	httpClient  *http.Client
	logger      *slog.Logger
	mu          sync.RWMutex
	totalWeight int
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewRouter creates a new multi-CDN router.
func NewRouter(cfg *RouterConfig, logger *slog.Logger) (*Router, error) {
	if cfg == nil {
		cfg = DefaultRouterConfig()
	}

	if len(cfg.Providers) == 0 {
		return nil, ErrInvalidConfig
	}

	totalWeight := 0
	for _, p := range cfg.Providers {
		if p.Name == "" || p.BaseURL == "" {
			return nil, fmt.Errorf("%w: provider missing name or base URL", ErrInvalidConfig)
		}
		if p.Weight <= 0 {
			p.Weight = 1
		}
		totalWeight += p.Weight
		p.Healthy = true
	}

	sort.Slice(cfg.Providers, func(i, j int) bool {
		return cfg.Providers[i].Priority < cfg.Providers[j].Priority
	})

	router := &Router{
		config:      cfg,
		providers:   cfg.Providers,
		totalWeight: totalWeight,
		logger:      logger,
		httpClient: &http.Client{
			Timeout: cfg.HealthTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		stopCh: make(chan struct{}),
	}

	return router, nil
}

// Start begins health checking for all providers.
func (r *Router) Start(ctx context.Context) {
	r.wg.Add(1)
	go r.healthCheckLoop(ctx)
}

// Stop stops the router and health checks.
func (r *Router) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

// SelectCDN selects the best CDN for the given request.
func (r *Router) SelectCDN(ctx context.Context, clientIP string) (*Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	healthy := r.getHealthyProviders()
	if len(healthy) == 0 {
		return nil, ErrNoCDNAvailable
	}

	var selected *Provider

	switch r.config.Strategy {
	case StrategyWeightedRoundRobin:
		selected = r.selectByWeight(healthy)
	case StrategyLowestLatency:
		selected = r.selectByLatency(healthy)
	case StrategyGeoBased:
		selected = r.selectByGeo(healthy, clientIP)
	case StrategyFailoverOnly:
		selected = r.selectByPriority(healthy)
	case StrategyRandom:
		selected = r.selectRandom(healthy)
	default:
		selected = healthy[0]
	}

	atomic.AddInt64(&selected.RequestCount, 1)

	return selected, nil
}

func (r *Router) getHealthyProviders() []*Provider {
	healthy := make([]*Provider, 0, len(r.providers))
	for _, p := range r.providers {
		if p.Healthy {
			healthy = append(healthy, p)
		}
	}
	return healthy
}

func (r *Router) selectByWeight(providers []*Provider) *Provider {
	if len(providers) == 1 {
		return providers[0]
	}

	totalWeight := 0
	for _, p := range providers {
		totalWeight += p.Weight
	}

	target := rand.Intn(totalWeight)
	current := 0

	for _, p := range providers {
		current += p.Weight
		if target < current {
			return p
		}
	}

	return providers[0]
}

func (r *Router) selectByLatency(providers []*Provider) *Provider {
	if len(providers) == 1 {
		return providers[0]
	}

	var best *Provider
	bestLatency := time.Hour

	for _, p := range providers {
		if p.Latency > 0 && p.Latency < bestLatency {
			bestLatency = p.Latency
			best = p
		}
	}

	if best == nil {
		return providers[0]
	}
	return best
}

func (r *Router) selectByGeo(providers []*Provider, clientIP string) *Provider {
	return r.selectByLatency(providers)
}

func (r *Router) selectByPriority(providers []*Provider) *Provider {
	return providers[0]
}

func (r *Router) selectRandom(providers []*Provider) *Provider {
	if len(providers) == 1 {
		return providers[0]
	}
	return providers[rand.Intn(len(providers))]
}

func (r *Router) healthCheckLoop(ctx context.Context) {
	defer r.wg.Done()

	r.checkAllProviders(ctx)

	ticker := time.NewTicker(r.config.HealthCheckPeriod)
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

func (r *Router) checkAllProviders(ctx context.Context) {
	var wg sync.WaitGroup

	for _, provider := range r.providers {
		wg.Add(1)
		go func(p *Provider) {
			defer wg.Done()
			r.checkProviderHealth(ctx, p)
		}(provider)
	}

	wg.Wait()
}

func (r *Router) checkProviderHealth(ctx context.Context, p *Provider) {
	if p.HealthURL == "" {
		return
	}

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.HealthURL, nil)
	if err != nil {
		r.markUnhealthy(p, err)
		return
	}

	resp, err := r.httpClient.Do(req)
	latency := time.Since(start)

	if err != nil {
		r.markUnhealthy(p, err)
		return
	}
	defer resp.Body.Close()

	r.mu.Lock()
	defer r.mu.Unlock()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.Healthy = true
		p.Latency = latency
		p.LastCheck = time.Now()
		atomic.StoreInt32(&p.FailureCount, 0)

		r.logger.Debug("CDN health check passed",
			"provider", p.Name,
			"latency", latency,
		)
	} else {
		r.markUnhealthyLocked(p, fmt.Errorf("health check returned status %d", resp.StatusCode))
	}
}

func (r *Router) markUnhealthy(p *Provider, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markUnhealthyLocked(p, err)
}

func (r *Router) markUnhealthyLocked(p *Provider, err error) {
	failures := atomic.AddInt32(&p.FailureCount, 1)
	p.LastCheck = time.Now()

	if failures >= r.config.MaxFailures {
		p.Healthy = false
		r.logger.Warn("CDN marked unhealthy",
			"provider", p.Name,
			"failures", failures,
			"error", err,
		)
	}
}

// RecordFailure records a request failure for a provider.
func (r *Router) RecordFailure(p *Provider, err error) {
	r.markUnhealthy(p, err)
}

// RecordSuccess records a successful request for a provider.
func (r *Router) RecordSuccess(p *Provider, latency time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	p.Healthy = true
	if p.Latency == 0 {
		p.Latency = latency
	} else {
		p.Latency = (p.Latency*7 + latency*3) / 10
	}
	atomic.StoreInt32(&p.FailureCount, 0)
}

// GetProviders returns all configured providers.
func (r *Router) GetProviders() []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]*Provider, len(r.providers))
	copy(providers, r.providers)
	return providers
}

// GetHealthyCount returns the number of healthy providers.
func (r *Router) GetHealthyCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, p := range r.providers {
		if p.Healthy {
			count++
		}
	}
	return count
}

// GetStats returns statistics for all providers.
func (r *Router) GetStats() map[string]ProviderStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[string]ProviderStats)
	for _, p := range r.providers {
		stats[p.Name] = ProviderStats{
			Healthy:      p.Healthy,
			Latency:      p.Latency,
			RequestCount: atomic.LoadInt64(&p.RequestCount),
			FailureCount: atomic.LoadInt32(&p.FailureCount),
			LastCheck:    p.LastCheck,
		}
	}
	return stats
}

// ProviderStats contains statistics for a single provider.
type ProviderStats struct {
	Healthy      bool          `json:"healthy"`
	Latency      time.Duration `json:"latency"`
	RequestCount int64         `json:"requestCount"`
	FailureCount int32         `json:"failureCount"`
	LastCheck    time.Time     `json:"lastCheck"`
}

// ParseStrategy parses a strategy string into a SelectionStrategy.
func ParseStrategy(s string) SelectionStrategy {
	switch s {
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
