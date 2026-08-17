package server

import (
	"container/list"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultLimiterMaxKeys = 10_000
	defaultLimiterIdleTTL = 10 * time.Minute
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	element  *list.Element
}

// multiLimiter holds one rate.Limiter per string key. Construct with
// newMultiLimiter; a zero ratePerSec means unlimited (rate.Inf).
type multiLimiter struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	order    *list.List
	r        rate.Limit
	b        int
	maxKeys  int
	idleTTL  time.Duration
	now      func() time.Time
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
		limiters: make(map[string]*limiterEntry),
		order:    list.New(),
		r:        r,
		b:        burst,
		maxKeys:  defaultLimiterMaxKeys,
		idleTTL:  defaultLimiterIdleTTL,
		now:      time.Now,
	}
}

// allow returns true if key is within its rate limit. It is safe for
// concurrent use.
func (m *multiLimiter) allow(key string) bool {
	if m.r == rate.Inf {
		return true
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.limiters[key]
	if !ok {
		m.evictIdleLocked(now)
		if len(m.limiters) >= m.maxKeys {
			m.evictOldestLocked()
		}
		entry = &limiterEntry{limiter: rate.NewLimiter(m.r, m.b)}
		entry.element = m.order.PushBack(key)
		m.limiters[key] = entry
	} else {
		m.order.MoveToBack(entry.element)
	}
	entry.lastSeen = now
	return entry.limiter.AllowN(now, 1)
}

func (m *multiLimiter) evictIdleLocked(now time.Time) {
	for {
		oldest := m.order.Front()
		if oldest == nil {
			return
		}
		key := oldest.Value.(string)
		entry := m.limiters[key]
		if now.Sub(entry.lastSeen) < m.idleTTL {
			return
		}
		m.removeLocked(key, entry)
	}
}

func (m *multiLimiter) evictOldestLocked() {
	oldest := m.order.Front()
	if oldest == nil {
		return
	}
	key := oldest.Value.(string)
	m.removeLocked(key, m.limiters[key])
}

func (m *multiLimiter) removeLocked(key string, entry *limiterEntry) {
	delete(m.limiters, key)
	m.order.Remove(entry.element)
}
