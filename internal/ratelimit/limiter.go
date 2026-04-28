// Package ratelimit provides the core types for the rate-limiting subsystem.
//
// This implementation is a faithful in-tree reproduction of the design and
// interfaces from github.com/zeayush/rate-limiter-go (Month 2, Week 1 of the
// distributed-systems portfolio).  The module path issue of that library
// (module name "rate-limiter-go" rather than a resolvable URL) is the only
// reason the code lives here rather than being imported directly.
//
// Public API surface intentionally mirrors rate-limiter-go so the code can be
// swapped for the upstream library with minimal changes.
package ratelimit

import (
	"context"
	"time"
)

// Config parameterises a rate-limiter instance.
type Config struct {
	Rate   int64         // max requests per Window (also the token-bucket refill rate)
	Window time.Duration // window / refill period
	Burst  int64         // token-bucket only: max burst capacity above steady-state rate
}

// Result is returned by every Allow call.
type Result struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	Reset      time.Time     // when the current window / bucket refills
	RetryAfter time.Duration // non-zero only when Allowed == false
}

// Limiter is a single-key rate limiter.
type Limiter interface {
	Allow(ctx context.Context) (Result, error)
}

// KeyedLimiter is a per-key rate limiter (per IP, per API key, etc.).
type KeyedLimiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}
