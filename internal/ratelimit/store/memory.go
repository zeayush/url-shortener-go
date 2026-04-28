// Package store provides KeyedLimiter implementations backed by in-memory maps
// and Redis, mirroring the store package from github.com/zeayush/rate-limiter-go.
package store

import (
	"context"
	"sync"

	"github.com/zeayush/url-shortener-go/internal/ratelimit"
)

// LimiterFactory creates a new single-key Limiter for a given key.
type LimiterFactory func(key string) (ratelimit.Limiter, error)

// MemoryStore is an in-memory KeyedLimiter.
//
// Design: double-checked locking via sync.RWMutex — a read lock is acquired
// first on the hot path, upgraded to a write lock only when the key is new.
// This minimises contention under concurrent load, matching the design
// decision in rate-limiter-go/store/memory.go.
type MemoryStore struct {
	mu       sync.RWMutex
	limiters map[string]ratelimit.Limiter
	factory  LimiterFactory
}

// NewMemoryStore returns a MemoryStore that creates per-key limiters using
// factory whenever a new key is first seen.
func NewMemoryStore(factory LimiterFactory) (*MemoryStore, error) {
	return &MemoryStore{
		limiters: make(map[string]ratelimit.Limiter),
		factory:  factory,
	}, nil
}

// Allow implements KeyedLimiter.
func (m *MemoryStore) Allow(ctx context.Context, key string) (ratelimit.Result, error) {
	limiter, err := m.getOrCreate(key)
	if err != nil {
		return ratelimit.Result{Allowed: true}, err // fail-open on factory errors
	}
	return limiter.Allow(ctx)
}

// Len returns the number of tracked keys (useful for metrics).
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.limiters)
}

func (m *MemoryStore) getOrCreate(key string) (ratelimit.Limiter, error) {
	// Hot path: read lock.
	m.mu.RLock()
	l, ok := m.limiters[key]
	m.mu.RUnlock()
	if ok {
		return l, nil
	}

	// Cold path: upgrade to write lock.
	m.mu.Lock()
	defer m.mu.Unlock()
	// Re-check after acquiring the write lock (double-checked locking).
	if l, ok = m.limiters[key]; ok {
		return l, nil
	}
	l, err := m.factory(key)
	if err != nil {
		return nil, err
	}
	m.limiters[key] = l
	return l, nil
}
