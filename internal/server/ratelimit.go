package server

import (
	"sync"

	"golang.org/x/time/rate"
)

// multiLimiter holds one rate.Limiter per string key. Construct with
// newMultiLimiter; a zero ratePerSec means unlimited (rate.Inf).
type multiLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newMultiLimiter(ratePerSec float64, burst int) *multiLimiter {
	r := rate.Limit(ratePerSec)
	if ratePerSec == 0 {
		r = rate.Inf
	}
	if burst <= 0 {
		burst = 1
	}
	return &multiLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		b:        burst,
	}
}

// allow returns true if key is within its rate limit. It is safe for
// concurrent use.
func (m *multiLimiter) allow(key string) bool {
	if m.r == rate.Inf {
		return true
	}
	m.mu.Lock()
	lim, ok := m.limiters[key]
	if !ok {
		lim = rate.NewLimiter(m.r, m.b)
		m.limiters[key] = lim
	}
	m.mu.Unlock()
	return lim.Allow()
}
