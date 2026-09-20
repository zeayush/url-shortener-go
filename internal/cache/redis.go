// Package cache wraps Redis with a simple link-caching layer.
//
// Cache key scheme : "url:{shortCode}"
// TTL              : min(link.ExpiresAt – now, 1 hour), or 1 hour when no expiry.
// Serialisation    : JSON via encoding/json.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zeayush/url-shortener-go/internal/model"
)

const (
	keyPrefix  = "url:"
	defaultTTL = time.Hour
)

// ErrNotFound is returned when a key is absent in Redis.
var ErrNotFound = errors.New("cache: key not found")

// ErrDisabled is returned by Ping when no Redis client is configured.
var ErrDisabled = errors.New("cache: disabled (no Redis configured)")

// Cache is a Redis-backed read-through / write-through cache for Link records.
type Cache struct {
	rdb *redis.Client
}

// New creates a Cache backed by the supplied Redis client. A nil client is
// valid and means "no cache": every read misses, every write is dropped, and
// nothing is dialed. Callers therefore need no separate cache-enabled check.
func New(rdb *redis.Client) *Cache {
	return &Cache{rdb: rdb}
}

// Enabled reports whether a Redis client is attached.
func (c *Cache) Enabled() bool { return c != nil && c.rdb != nil }

// GetLink fetches the link for shortCode from Redis.
// Returns ErrNotFound on a cache miss.
func (c *Cache) GetLink(ctx context.Context, shortCode string) (*model.Link, error) {
	if !c.Enabled() {
		return nil, ErrNotFound
	}
	val, err := c.rdb.Get(ctx, keyPrefix+shortCode).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cache: get %q: %w", shortCode, err)
	}

	var link model.Link
	if err := json.Unmarshal(val, &link); err != nil {
		return nil, fmt.Errorf("cache: unmarshal %q: %w", shortCode, err)
	}
	return &link, nil
}

// SetLink stores link in Redis and sets an appropriate TTL.
func (c *Cache) SetLink(ctx context.Context, link *model.Link) error {
	if !c.Enabled() {
		return nil
	}
	ttl := defaultTTL
	if link.ExpiresAt != nil {
		remaining := time.Until(*link.ExpiresAt)
		if remaining <= 0 {
			// Already expired; don't cache it.
			return nil
		}
		if remaining < ttl {
			ttl = remaining
		}
	}

	data, err := json.Marshal(link)
	if err != nil {
		return fmt.Errorf("cache: marshal %q: %w", link.ShortCode, err)
	}

	if err := c.rdb.Set(ctx, keyPrefix+link.ShortCode, data, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", link.ShortCode, err)
	}
	return nil
}

// DeleteLink evicts shortCode from Redis.
func (c *Cache) DeleteLink(ctx context.Context, shortCode string) error {
	if !c.Enabled() {
		return nil
	}
	if err := c.rdb.Del(ctx, keyPrefix+shortCode).Err(); err != nil {
		return fmt.Errorf("cache: delete %q: %w", shortCode, err)
	}
	return nil
}

// IncrClickCount bumps the cached click_count by 1 to keep the read-path
// approximately up to date without requiring a DB round-trip on every redirect.
func (c *Cache) IncrClickCount(ctx context.Context, shortCode string) {
	link, err := c.GetLink(ctx, shortCode)
	if err != nil {
		return // cache miss – ignore
	}
	link.ClickCount++
	_ = c.SetLink(ctx, link)
}

// Ping checks that Redis is reachable. ErrDisabled distinguishes "switched
// off deliberately" from "configured but broken" for the health endpoint.
func (c *Cache) Ping(ctx context.Context) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	return c.rdb.Ping(ctx).Err()
}
