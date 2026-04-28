// Package repository — shard.go
//
// ShardRouter wraps a consistent-hash ring and a map of shard names to
// *sql.DB instances. It provides a single GetDB(key) entry point used
// everywhere in the application to route link operations to the correct
// PostgreSQL shard.
//
// Shard names follow the convention "shard-0", "shard-1", … matching the
// index of each DSN in config.DBShards. The ring is seeded with 150 virtual
// nodes per shard, giving even key distribution even with few physical nodes.
package repository

import (
	"database/sql"
	"errors"
	"fmt"

	consistenthash "github.com/zeayush/consistent-hashing-go"
)

const virtualNodesPerShard = 150

// ShardRouter routes short codes to PostgreSQL shards using consistent hashing.
//
// Design rationale
//   - Consistent hashing ensures that adding or removing a shard only remaps
//     roughly 1/N of all keys, avoiding a thundering-herd cache invalidation.
//   - Using the short code (not a numeric ID) as the ring key means redirect
//     traffic is spread naturally across shards by the hash function.
type ShardRouter struct {
	ring   *consistenthash.ConsistentHashRing
	shards map[string]*sql.DB // "shard-0" → *sql.DB
	dbs    map[string]*sql.DB // exported copy used by analytics.Recorder
}

// NewShardRouter opens one PostgreSQL connection pool per DSN and adds each to
// the consistent-hash ring.
func NewShardRouter(dsns []string) (*ShardRouter, error) {
	if len(dsns) == 0 {
		return nil, errors.New("shard: at least one DSN required")
	}

	ring := consistenthash.New(virtualNodesPerShard)
	shards := make(map[string]*sql.DB, len(dsns))
	dbs := make(map[string]*sql.DB, len(dsns))

	for i, dsn := range dsns {
		name := fmt.Sprintf("shard-%d", i)
		db, err := Open(dsn)
		if err != nil {
			// Close already-opened connections before surfacing the error.
			for _, opened := range shards {
				_ = opened.Close()
			}
			return nil, fmt.Errorf("shard: open %s: %w", name, err)
		}
		ring.Add(name, 1)
		shards[name] = db
		dbs[name] = db
	}

	return &ShardRouter{ring: ring, shards: shards, dbs: dbs}, nil
}

// GetDB returns the *sql.DB for the shard that owns key.
// Falls back to shard-0 if the ring returns nothing (should never happen with
// at least one node, but defensive).
func (r *ShardRouter) GetDB(key string) *sql.DB {
	name, ok := r.ring.Get(key)
	if !ok {
		return r.shards["shard-0"]
	}
	db, exists := r.shards[name]
	if !exists {
		return r.shards["shard-0"]
	}
	return db
}

// DBs returns the raw shard map for components that need to iterate all shards
// (e.g. the analytics recorder, health checks).
func (r *ShardRouter) DBs() map[string]*sql.DB {
	return r.dbs
}

// Close closes every shard connection pool.
func (r *ShardRouter) Close() {
	for _, db := range r.shards {
		_ = db.Close()
	}
}
