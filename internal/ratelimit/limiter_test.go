package ratelimit

import "testing"

func TestLimiterBurstAndRelease(t *testing.T) {
	limiter := New(Policy{RequestsPerMinute: 2, Burst: 2, TokensPerMinute: 10, MaxConcurrent: 1})
	first := limiter.Allow("tenant_a:app", 1)
	if !first.Allowed || first.Remaining != 1 {
		t.Fatalf("first=%+v", first)
	}
	blocked := limiter.Allow("tenant_a:app", 1)
	if blocked.Allowed {
		t.Fatalf("expected concurrent/request denial=%+v", blocked)
	}
	limiter.Release("tenant_a:app")
	second := limiter.Allow("tenant_a:app", 1)
	if !second.Allowed {
		t.Fatalf("second=%+v", second)
	}
	limiter.Release("tenant_a:app")
	blocked = limiter.Allow("tenant_a:app", 1)
	if blocked.Allowed {
		t.Fatalf("expected burst denial=%+v", blocked)
	}
}
