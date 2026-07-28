package ratelimit

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestRedisLimiterNilClientFailsOpen(t *testing.T) {
	limiter := NewRedis(nil, Policy{Burst: 1}, "unit")
	if limiter != nil {
		t.Fatalf("expected nil limiter for nil client")
	}
	var gate *RedisLimiter
	if decision := gate.Allow("tenant/app", 1); !decision.Allowed {
		t.Fatalf("nil limiter should fail open: %+v", decision)
	}
	gate.Release("tenant/app")
}

// Set RAGLAB_REDIS_TEST_URL to run this integration check against a real Redis
// instance. It is intentionally opt-in so the normal unit suite stays hermetic.
func TestRedisLimiterAgainstServer(t *testing.T) {
	endpoint := os.Getenv("RAGLAB_REDIS_TEST_URL")
	if endpoint == "" {
		t.Skip("RAGLAB_REDIS_TEST_URL is not set")
	}
	options, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush redis: %v", err)
	}
	limiter := NewRedis(client, Policy{RequestsPerMinute: 2, Burst: 2, TokensPerMinute: 10, MaxConcurrent: 1}, "raglab:test")
	key := "tenant_a/app_a/subject_a"
	first := limiter.Allow(key, 1)
	if !first.Allowed || first.Remaining != 1 {
		t.Fatalf("first decision=%+v", first)
	}
	concurrent := limiter.Allow(key, 1)
	if concurrent.Allowed || concurrent.RetryAfter <= 0 {
		t.Fatalf("expected concurrent denial=%+v", concurrent)
	}
	limiter.Release(key)
	second := limiter.Allow(key, 1)
	if !second.Allowed || second.Remaining != 0 {
		t.Fatalf("second decision=%+v", second)
	}
	limiter.Release(key)
	burst := limiter.Allow(key, 1)
	if burst.Allowed || burst.RetryAfter <= 0 {
		t.Fatalf("expected burst denial=%+v", burst)
	}
}
