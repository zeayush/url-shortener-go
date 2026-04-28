package ratelimit

import (
	"context"
	"sync"
	"time"
)

// tokenBucket implements Limiter using the token-bucket algorithm.
//
// Tokens are calculated lazily on every Allow call (no background goroutine):
//
//	refill = min(burst, tokens + elapsed_seconds * rate)
//
// This matches the "lazy refill" design decision in rate-limiter-go and means
// zero overhead between requests.
type tokenBucket struct {
	mu       sync.Mutex
	cfg      Config
	tokens   float64
	lastTime time.Time
}

// NewTokenBucket returns a single-key token-bucket Limiter.
func NewTokenBucket(cfg Config) (Limiter, error) {
	return &tokenBucket{
		cfg:      cfg,
		tokens:   float64(cfg.Burst),
		lastTime: time.Now(),
	}, nil
}

// Allow consumes one token. If the bucket is empty the call is denied and
// RetryAfter indicates how long the caller must wait.
func (tb *tokenBucket) Allow(_ context.Context) (Result, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.lastTime = now

	rate := float64(tb.cfg.Rate)
	burst := float64(tb.cfg.Burst)

	tb.tokens = min64(burst, tb.tokens+elapsed*rate)

	if tb.tokens >= 1 {
		tb.tokens--
		reset := now.Add(time.Duration((burst-tb.tokens)/rate) * time.Second)
		return Result{
			Allowed:   true,
			Limit:     tb.cfg.Burst,
			Remaining: int64(tb.tokens),
			Reset:     reset,
		}, nil
	}

	retryAfter := time.Duration((1-tb.tokens)/rate*float64(time.Second))
	return Result{
		Allowed:    false,
		Limit:      tb.cfg.Burst,
		Remaining:  0,
		Reset:      now.Add(retryAfter),
		RetryAfter: retryAfter,
	}, nil
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
