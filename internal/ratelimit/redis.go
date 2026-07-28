package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a shared, atomic fixed-window gate. A short Lua script
// updates requests, token usage and in-flight reservations together, so two
// API replicas cannot both admit the same burst. Redis failures fail open to
// preserve answer availability; deployments that require strict admission
// should put a network policy/health check in front of the API as well.
type RedisLimiter struct {
	client  *redis.Client
	policy  Policy
	prefix  string
	allow   *redis.Script
	release *redis.Script
}

const allowScript = `
local now = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local token_limit = tonumber(ARGV[3])
local requested_tokens = tonumber(ARGV[4])
local max_concurrent = tonumber(ARGV[5])
local window_ms = 60000
local window_start = tonumber(redis.call('HGET', KEYS[1], 'window_start') or '0')
if window_start == 0 or now - window_start >= window_ms then
  redis.call('HSET', KEYS[1], 'window_start', now, 'requests', 0, 'tokens', 0, 'concurrent', 0)
  window_start = now
end
local requests = tonumber(redis.call('HGET', KEYS[1], 'requests') or '0')
local tokens = tonumber(redis.call('HGET', KEYS[1], 'tokens') or '0')
local concurrent = tonumber(redis.call('HGET', KEYS[1], 'concurrent') or '0')
local retry_ms = window_ms - (now - window_start)
if retry_ms < 1 then retry_ms = 1 end
if max_concurrent > 0 and concurrent >= max_concurrent then
  redis.call('PEXPIRE', KEYS[1], window_ms)
  return {0, 1000, 0}
end
if requests >= burst or tokens + requested_tokens > token_limit then
  local remaining = burst - requests
  if remaining < 0 then remaining = 0 end
  redis.call('PEXPIRE', KEYS[1], window_ms)
  return {0, retry_ms, remaining}
end
requests = redis.call('HINCRBY', KEYS[1], 'requests', 1)
redis.call('HINCRBY', KEYS[1], 'tokens', requested_tokens)
redis.call('HINCRBY', KEYS[1], 'concurrent', 1)
redis.call('PEXPIRE', KEYS[1], window_ms)
local remaining = burst - requests
if remaining < 0 then remaining = 0 end
return {1, 0, remaining}
`

const releaseScript = `
local concurrent = tonumber(redis.call('HGET', KEYS[1], 'concurrent') or '0')
if concurrent > 0 then
  return redis.call('HINCRBY', KEYS[1], 'concurrent', -1)
end
return 0
`

func NewRedis(client *redis.Client, policy Policy, prefix string) *RedisLimiter {
	if client == nil {
		return nil
	}
	if strings.TrimSpace(prefix) == "" {
		prefix = "raglab:ratelimit"
	}
	return &RedisLimiter{client: client, policy: normalizePolicy(policy), prefix: strings.TrimSuffix(prefix, ":"), allow: redis.NewScript(allowScript), release: redis.NewScript(releaseScript)}
}

func (limiter *RedisLimiter) Allow(key string, tokens int) Decision {
	if limiter == nil || limiter.client == nil {
		return Decision{Allowed: true}
	}
	if tokens <= 0 {
		tokens = 1
	}
	result, err := limiter.allow.Run(redisContext(), limiter.client, []string{limiter.key(key)}, time.Now().UnixMilli(), limiter.policy.Burst, limiter.policy.TokensPerMinute, tokens, limiter.policy.MaxConcurrent).Result()
	if err != nil {
		return Decision{Allowed: true, Remaining: -1}
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 3 {
		return Decision{Allowed: true, Remaining: -1}
	}
	allowed, errAllowed := redisInt(values[0])
	retryMS, errRetry := redisInt(values[1])
	remaining, errRemaining := redisInt(values[2])
	if errAllowed != nil || errRetry != nil || errRemaining != nil {
		return Decision{Allowed: true, Remaining: -1}
	}
	return Decision{Allowed: allowed == 1, RetryAfter: time.Duration(retryMS) * time.Millisecond, Remaining: int(remaining)}
}

func (limiter *RedisLimiter) Release(key string) {
	if limiter == nil || limiter.client == nil {
		return
	}
	_, _ = limiter.release.Run(redisContext(), limiter.client, []string{limiter.key(key)}).Result()
}

func (limiter *RedisLimiter) Policy() Policy {
	if limiter == nil {
		return Policy{}
	}
	return limiter.policy
}

func (limiter *RedisLimiter) key(value string) string {
	sum := sha256.Sum256([]byte(value))
	return limiter.prefix + ":" + hex.EncodeToString(sum[:])
}

func redisContext() context.Context { return context.Background() }

func redisInt(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected redis integer %T", value)
	}
}

var _ Gate = (*RedisLimiter)(nil)
