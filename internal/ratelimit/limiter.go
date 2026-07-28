package ratelimit

import (
	"sync"
	"time"
)

// Gate is the runtime boundary used by the Knowledge Gateway. The default
// Limiter is process-local; RedisLimiter implements the same contract for
// horizontally scaled API replicas.
type Gate interface {
	Allow(key string, tokens int) Decision
	Release(key string)
}

type Policy struct {
	RequestsPerMinute int
	Burst             int
	TokensPerMinute   int
	MaxConcurrent     int
}

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Remaining  int
}

type bucket struct {
	windowStart                  time.Time
	requests, tokens, concurrent int
}

type Limiter struct {
	mu      sync.Mutex
	policy  Policy
	buckets map[string]*bucket
	now     func() time.Time
}

func New(policy Policy) *Limiter {
	policy = normalizePolicy(policy)
	return &Limiter{policy: policy, buckets: make(map[string]*bucket), now: time.Now}
}

func normalizePolicy(policy Policy) Policy {
	if policy.RequestsPerMinute <= 0 {
		policy.RequestsPerMinute = 60
	}
	if policy.Burst <= 0 {
		policy.Burst = policy.RequestsPerMinute
	}
	if policy.TokensPerMinute <= 0 {
		policy.TokensPerMinute = 100_000
	}
	return policy
}

func (limiter *Limiter) Allow(key string, tokens int) Decision {
	if limiter == nil {
		return Decision{Allowed: true}
	}
	if tokens <= 0 {
		tokens = 1
	}
	now := limiter.now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	state := limiter.buckets[key]
	if state == nil || now.Sub(state.windowStart) >= time.Minute {
		state = &bucket{windowStart: now}
		limiter.buckets[key] = state
	}
	if state.concurrent >= limiter.policy.MaxConcurrent && limiter.policy.MaxConcurrent > 0 {
		return Decision{RetryAfter: time.Second, Remaining: 0}
	}
	if state.requests >= limiter.policy.Burst || state.tokens+tokens > limiter.policy.TokensPerMinute {
		return Decision{RetryAfter: time.Until(state.windowStart.Add(time.Minute)), Remaining: maxInt(0, limiter.policy.Burst-state.requests)}
	}
	state.requests++
	state.tokens += tokens
	state.concurrent++
	return Decision{Allowed: true, Remaining: maxInt(0, limiter.policy.Burst-state.requests)}
}

func (limiter *Limiter) Release(key string) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if state := limiter.buckets[key]; state != nil && state.concurrent > 0 {
		state.concurrent--
	}
}
func (limiter *Limiter) Policy() Policy {
	if limiter == nil {
		return Policy{}
	}
	return limiter.policy
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ Gate = (*Limiter)(nil)
