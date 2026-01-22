// internal/cdn/router.go
package cdn

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

type CDNProvider struct {
	Name      string
	BaseURL   string
	Weight    int // Traffic weight (0-100)
	Healthy   bool
	Latency   time.Duration
	ErrorRate float64
}

type CDNRouter struct {
	providers    []CDNProvider
	mu           sync.RWMutex
	healthTicker *time.Ticker
}

func NewCDNRouter(providers []CDNProvider) *CDNRouter {
	r := &CDNRouter{
		providers:    providers,
		healthTicker: time.NewTicker(30 * time.Second),
	}
	go r.healthCheckLoop()
	return r
}

// SelectCDN chooses a CDN based on weighted selection and health
func (r *CDNRouter) SelectCDN(ctx context.Context, viewerLocation string) *CDNProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Filter healthy providers
	healthy := make([]CDNProvider, 0)
	totalWeight := 0
	for _, p := range r.providers {
		if p.Healthy {
			healthy = append(healthy, p)
			totalWeight += p.Weight
		}
	}

	if len(healthy) == 0 {
		// Fallback to primary even if unhealthy
		return &r.providers[0]
	}

	// Weighted random selection
	pick := rand.Intn(totalWeight)
	cumulative := 0
	for _, p := range healthy {
		cumulative += p.Weight
		if pick < cumulative {
			return &p
		}
	}

	return &healthy[0]
}

// GetManifestURL returns the CDN-specific manifest URL
func (r *CDNRouter) GetManifestURL(videoID string, viewerLocation string) string {
	cdn := r.SelectCDN(context.Background(), viewerLocation)
	return fmt.Sprintf("%s/hls/%s/master.m3u8", cdn.BaseURL, videoID)
}

func (r *CDNRouter) healthCheckLoop() {
	for range r.healthTicker.C {
		r.checkAllProviders()
	}
}

func (r *CDNRouter) checkAllProviders() {
	var wg sync.WaitGroup

	for i := range r.providers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			start := time.Now()
			healthy, err := r.checkProvider(&r.providers[idx])
			latency := time.Since(start)

			r.mu.Lock()
			r.providers[idx].Healthy = healthy
			r.providers[idx].Latency = latency
			if err != nil {
				r.providers[idx].ErrorRate = min(1.0, r.providers[idx].ErrorRate+0.1)
			} else {
				r.providers[idx].ErrorRate = max(0.0, r.providers[idx].ErrorRate-0.05)
			}
			r.mu.Unlock()
		}(i)
	}

	wg.Wait()
}

func (r *CDNRouter) checkProvider(p *CDNProvider) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "HEAD", p.BaseURL+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, nil
}
