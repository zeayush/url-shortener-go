package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration sourced from environment variables.
type Config struct {
	BaseURL    string
	ServerAddr string

	// DBShards is the ordered list of PostgreSQL DSNs, one per shard.
	// The consistent-hash ring is seeded with "shard-0", "shard-1", … matching
	// the slice index, so DSN order must remain stable across deploys.
	DBShards []string

	RedisAddr     string
	RedisPassword string

	GeoIPDBPath string

	// Rate limiting (token-bucket per IP)
	RateLimit  int64
	RateBurst  int64
	RateWindow time.Duration

	AnalyticsBufferSize int
}

// Load reads configuration from environment variables.
// Any missing required variable causes a descriptive error.
func Load() (*Config, error) {
	c := &Config{
		BaseURL:             getEnv("BASE_URL", "http://localhost:8080"),
		ServerAddr:          getEnv("SERVER_ADDR", ":8080"),
		RedisAddr:           getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       getEnv("REDIS_PASSWORD", ""),
		GeoIPDBPath:         getEnv("GEOIP_DB_PATH", "./data/GeoLite2-Country.mmdb"),
		AnalyticsBufferSize: 2048,
	}

	// Collect shard DSNs: DB_SHARD_0_DSN, DB_SHARD_1_DSN, …
	for i := 0; ; i++ {
		dsn := os.Getenv(fmt.Sprintf("DB_SHARD_%d_DSN", i))
		if dsn == "" {
			break
		}
		c.DBShards = append(c.DBShards, dsn)
	}
	if len(c.DBShards) == 0 {
		return nil, fmt.Errorf("config: at least DB_SHARD_0_DSN must be set")
	}

	var err error
	if c.RateLimit, err = parseInt64Env("RATE_LIMIT", 100); err != nil {
		return nil, err
	}
	if c.RateBurst, err = parseInt64Env("RATE_BURST", 20); err != nil {
		return nil, err
	}
	windowSecs, err := parseInt64Env("RATE_WINDOW_SECONDS", 60)
	if err != nil {
		return nil, err
	}
	c.RateWindow = time.Duration(windowSecs) * time.Second

	if v := os.Getenv("ANALYTICS_BUFFER_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ANALYTICS_BUFFER_SIZE: %w", err)
		}
		c.AnalyticsBufferSize = n
	}

	// Trim trailing slash from BaseURL so we can safely append "/code"
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")

	return c, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseInt64Env(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: invalid %s: %w", key, err)
	}
	return n, nil
}
