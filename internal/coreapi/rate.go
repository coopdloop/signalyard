package coreapi

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// rateLimiter is a per-agent token bucket (single-replica, in-memory).
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
	rps     float64
	burst   float64
	now     func() time.Time // injectable for tests
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rps, burst float64) *rateLimiter {
	if rps <= 0 {
		rps = 50
	}
	if burst <= 0 {
		burst = 100
	}
	return &rateLimiter{
		buckets: make(map[uuid.UUID]*bucket),
		rps:     rps,
		burst:   burst,
		now:     time.Now,
	}
}

func (l *rateLimiter) allow(id uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[id]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[id] = b
	}
	// refill
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimit applies the per-agent token bucket. Must run after RequireAPIKey.
func (s *Server) RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter != nil && !s.limiter.allow(agentFrom(r.Context()).ID) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
