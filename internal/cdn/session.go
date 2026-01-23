package cdn

import (
	"context"
	"sync"
	"time"
)

// Session represents a CDN session for a viewer.
type Session struct {
	SessionID   string    `json:"sessionId"`
	CDN         *Provider `json:"cdn"`
	CreatedAt   time.Time `json:"createdAt"`
	LastAccess  time.Time `json:"lastAccess"`
	ClientIP    string    `json:"clientIP"`
	FailCount   int       `json:"failCount"`
	FailoverCDN *Provider `json:"failoverCdn,omitempty"`
}

// SessionManagerConfig contains configuration for the session manager.
type SessionManagerConfig struct {
	SessionTTL      time.Duration
	CleanupInterval time.Duration
	MaxFailures     int
	StickyEnabled   bool
}

// DefaultSessionManagerConfig returns the default session manager configuration.
func DefaultSessionManagerConfig() *SessionManagerConfig {
	return &SessionManagerConfig{
		SessionTTL:      2 * time.Hour,
		CleanupInterval: 10 * time.Minute,
		MaxFailures:     3,
		StickyEnabled:   true,
	}
}

// SessionManager manages CDN sessions for viewers.
type SessionManager struct {
	router   *Router
	config   *SessionManagerConfig
	sessions sync.Map
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewSessionManager creates a new session manager.
func NewSessionManager(router *Router, cfg *SessionManagerConfig) *SessionManager {
	if cfg == nil {
		cfg = DefaultSessionManagerConfig()
	}

	return &SessionManager{
		router: router,
		config: cfg,
		stopCh: make(chan struct{}),
	}
}

// Start begins the session cleanup goroutine.
func (sm *SessionManager) Start(ctx context.Context) {
	sm.wg.Add(1)
	go sm.cleanupLoop(ctx)
}

// Stop stops the session manager.
func (sm *SessionManager) Stop() {
	close(sm.stopCh)
	sm.wg.Wait()
}

// GetOrCreate gets an existing session or creates a new one.
func (sm *SessionManager) GetOrCreate(ctx context.Context, sessionID, clientIP string) (*Provider, error) {
	if existing, ok := sm.sessions.Load(sessionID); ok {
		session := existing.(*Session)

		if time.Since(session.LastAccess) > sm.config.SessionTTL {
			sm.sessions.Delete(sessionID)
		} else if sm.config.StickyEnabled && session.CDN.Healthy {
			session.LastAccess = time.Now()
			return session.CDN, nil
		} else if !session.CDN.Healthy {
			return sm.handleFailover(ctx, session)
		}
	}

	cdn, err := sm.router.SelectCDN(ctx, clientIP)
	if err != nil {
		return nil, err
	}

	session := &Session{
		SessionID:  sessionID,
		CDN:        cdn,
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
		ClientIP:   clientIP,
	}

	sm.sessions.Store(sessionID, session)
	return cdn, nil
}

func (sm *SessionManager) handleFailover(ctx context.Context, session *Session) (*Provider, error) {
	session.FailCount++

	if session.FailoverCDN != nil && session.FailoverCDN.Healthy {
		session.LastAccess = time.Now()
		return session.FailoverCDN, nil
	}

	newCDN, err := sm.selectFailoverCDN(ctx, session.ClientIP, session.CDN.Name)
	if err != nil {
		return nil, err
	}

	session.FailoverCDN = newCDN
	session.LastAccess = time.Now()
	return newCDN, nil
}

func (sm *SessionManager) selectFailoverCDN(ctx context.Context, clientIP, excludeName string) (*Provider, error) {
	providers := sm.router.GetProviders()

	var candidates []*Provider
	for _, p := range providers {
		if p.Healthy && p.Name != excludeName {
			candidates = append(candidates, p)
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoCDNAvailable
	}

	best := candidates[0]
	for _, p := range candidates[1:] {
		if p.Priority < best.Priority {
			best = p
		}
	}

	return best, nil
}

// RecordFailure records a failure for a session.
func (sm *SessionManager) RecordFailure(sessionID string) {
	if existing, ok := sm.sessions.Load(sessionID); ok {
		session := existing.(*Session)
		session.FailCount++

		if session.FailCount >= sm.config.MaxFailures {
			sm.sessions.Delete(sessionID)
		}

		sm.router.RecordFailure(session.CDN, nil)
	}
}

// RecordSuccess records a successful request for a session.
func (sm *SessionManager) RecordSuccess(sessionID string, latency time.Duration) {
	if existing, ok := sm.sessions.Load(sessionID); ok {
		session := existing.(*Session)
		session.LastAccess = time.Now()
		session.FailCount = 0

		if session.FailoverCDN != nil && session.CDN.Healthy {
			session.FailoverCDN = nil
		}

		sm.router.RecordSuccess(session.CDN, latency)
	}
}

// GetSession returns a session by ID.
func (sm *SessionManager) GetSession(sessionID string) *Session {
	if existing, ok := sm.sessions.Load(sessionID); ok {
		return existing.(*Session)
	}
	return nil
}

// InvalidateSession removes a session.
func (sm *SessionManager) InvalidateSession(sessionID string) {
	sm.sessions.Delete(sessionID)
}

func (sm *SessionManager) cleanupLoop(ctx context.Context) {
	defer sm.wg.Done()

	ticker := time.NewTicker(sm.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.stopCh:
			return
		case <-ticker.C:
			sm.cleanup()
		}
	}
}

func (sm *SessionManager) cleanup() {
	now := time.Now()

	sm.sessions.Range(func(key, value interface{}) bool {
		session := value.(*Session)
		if now.Sub(session.LastAccess) > sm.config.SessionTTL {
			sm.sessions.Delete(key)
		}
		return true
	})
}

// GetStats returns statistics about sessions.
func (sm *SessionManager) GetStats() SessionStats {
	stats := SessionStats{
		CDNDistribution: make(map[string]int),
	}

	sm.sessions.Range(func(key, value interface{}) bool {
		session := value.(*Session)
		stats.TotalSessions++

		if session.CDN != nil {
			stats.CDNDistribution[session.CDN.Name]++
		}

		if session.FailoverCDN != nil {
			stats.ActiveFailovers++
		}

		return true
	})

	return stats
}

// SessionStats contains session statistics.
type SessionStats struct {
	TotalSessions   int            `json:"totalSessions"`
	ActiveFailovers int            `json:"activeFailovers"`
	CDNDistribution map[string]int `json:"cdnDistribution"`
}
