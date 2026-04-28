// Package middleware provides Gin and net/http adapters for KeyedLimiter,
// mirroring the middleware package from github.com/zeayush/rate-limiter-go.
//
// On every request the middleware:
//  1. Extracts a rate-limit key (IP address by default).
//  2. Calls KeyedLimiter.Allow(ctx, key).
//  3. Writes standard X-RateLimit-* response headers.
//  4. Returns 429 Too Many Requests (with Retry-After) when denied.
//  5. Fails open on limiter errors — a Redis outage won't stop traffic.
package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zeayush/url-shortener-go/internal/ratelimit"
)

// KeyExtractor derives a rate-limit key from the incoming Gin context.
type KeyExtractor func(c *gin.Context) string

// GinIPExtractor returns the client IP address as the rate-limit key.
func GinIPExtractor(c *gin.Context) string {
	return c.ClientIP()
}

// GinHeaderExtractor returns a KeyExtractor that uses the value of header as
// the rate-limit key (e.g. "X-API-Key").
func GinHeaderExtractor(header string) KeyExtractor {
	return func(c *gin.Context) string {
		return c.GetHeader(header)
	}
}

// GinMiddleware returns a Gin HandlerFunc that enforces rate limiting.
//
// Response headers set on every request:
//
//	X-RateLimit-Limit     — max requests in window
//	X-RateLimit-Remaining — requests remaining in current window
//	X-RateLimit-Reset     — Unix timestamp of next window reset
//
// Additional header on 429:
//
//	Retry-After — seconds until the client may retry
func GinMiddleware(rl ratelimit.KeyedLimiter, extract KeyExtractor) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extract(c)
		if key == "" {
			// No key (e.g. missing header) — pass through without limiting.
			c.Next()
			return
		}

		result, err := rl.Allow(c.Request.Context(), key)
		if err != nil {
			// Fail open: limiter error should not block requests.
			c.Next()
			return
		}

		// Always set informational headers.
		c.Header("X-RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
		c.Header("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(result.Reset.Unix(), 10))

		if !result.Allowed {
			retrySecs := int64(result.RetryAfter / time.Second)
			if retrySecs < 1 {
				retrySecs = 1
			}
			c.Header("Retry-After", strconv.FormatInt(retrySecs, 10))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": retrySecs,
			})
			return
		}

		c.Next()
	}
}
