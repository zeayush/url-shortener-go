package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zeayush/url-shortener-go/internal/ratelimit"
)

// redisTokenBucketScript is a Lua script that atomically refills and drains
// a token-bucket stored in Redis.
//
// Matches the atomic Lua approach in rate-limiter-go/store/redis.go —
// a single script execution eliminates the INCR/EXPIRE race condition.
//
// KEYS[1]  — hash key for this bucket
// ARGV[1]  — rate (tokens per second, float)
// ARGV[2]  — burst capacity (float)
// ARGV[3]  — current Unix nanoseconds (string)
// ARGV[4]  — TTL in milliseconds (burst/rate + 1 s headroom)
//
// Returns {allowed(0|1), remaining(int), retry_after_ns(int)}
var redisTokenBucketScript = redis.NewScript(`
local key     = KEYS[1]
local rate    = tonumber(ARGV[1])
local burst   = tonumber(ARGV[2])
local now_ns  = tonumber(ARGV[3])
local ttl_ms  = tonumber(ARGV[4])

local data      = redis.call("HMGET", key, "tokens", "last_ns")
local tokens    = tonumber(data[1]) or burst
local last_ns   = tonumber(data[2]) or now_ns

-- Refill: elapsed seconds × rate, capped at burst.
local elapsed_s = (now_ns - last_ns) / 1e9
local new_tokens = math.min(burst, tokens + elapsed_s * rate)

if new_tokens >= 1 then
    new_tokens = new_tokens - 1
    redis.call("HMSET", key, "tokens", new_tokens, "last_ns", now_ns)
    redis.call("PEXPIRE", key, ttl_ms)
    return {1, math.floor(new_tokens), 0}
else
    -- retry_after in nanoseconds
    local retry_ns = math.ceil((1 - new_tokens) / rate * 1e9)
    return {0, 0, retry_ns}
end
`)

// RedisStore is a distributed KeyedLimiter backed by Redis.
// On Redis errors it fails open by delegating to the in-memory fallback,
// matching the fail-open policy in rate-limiter-go.
type RedisStore struct {
	rdb      *redis.Client
	cfg      ratelimit.Config
	ttlMS    int64  // pre-computed TTL in milliseconds
	fallback *MemoryStore
}

// NewRedisStore returns a RedisStore that uses rdb as the primary backend and
// falls back to mem when Redis is unavailable.
func NewRedisStore(rdb *redis.Client, cfg ratelimit.Config, fallback *MemoryStore) (*RedisStore, error) {
	ttlMS := int64(cfg.Burst/cfg.Rate)*1000 + 1000
	return &RedisStore{
		rdb:      rdb,
		cfg:      cfg,
		ttlMS:    ttlMS,
		fallback: fallback,
	}, nil
}

// Allow implements KeyedLimiter. Keys are namespaced as "rl:{key}".
func (s *RedisStore) Allow(ctx context.Context, key string) (ratelimit.Result, error) {
	redisKey := "rl:" + key
	nowNS := time.Now().UnixNano()

	vals, err := redisTokenBucketScript.Run(ctx, s.rdb,
		[]string{redisKey},
		float64(s.cfg.Rate)/s.cfg.Window.Seconds(), // tokens per second
		float64(s.cfg.Burst),
		fmt.Sprintf("%d", nowNS),
		s.ttlMS,
	).Int64Slice()

	if err != nil {
		// Redis unavailable — fail open via in-memory fallback.
		return s.fallback.Allow(ctx, key)
	}

	allowed := vals[0] == 1
	remaining := vals[1]
	retryNS := time.Duration(vals[2])

	now := time.Now()
	reset := now.Add(s.cfg.Window)

	if !allowed {
		return ratelimit.Result{
			Allowed:    false,
			Limit:      s.cfg.Burst,
			Remaining:  0,
			Reset:      now.Add(retryNS),
			RetryAfter: retryNS,
		}, nil
	}

	return ratelimit.Result{
		Allowed:   true,
		Limit:     s.cfg.Burst,
		Remaining: remaining,
		Reset:     reset,
	}, nil
}
