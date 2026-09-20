package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/zeayush/rate-limiter-go/limiter"
	rlstore "github.com/zeayush/rate-limiter-go/store"
	"github.com/zeayush/url-shortener-go/internal/analytics"
	"github.com/zeayush/url-shortener-go/internal/cache"
	"github.com/zeayush/url-shortener-go/internal/config"
	"github.com/zeayush/url-shortener-go/internal/geoip"
	"github.com/zeayush/url-shortener-go/internal/handler"
	"github.com/zeayush/url-shortener-go/internal/repository"
)

func main() {
	// ── Configuration ────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// ── Database shards (consistent-hash ring) ───────────────────────────────
	shard, err := repository.NewShardRouter(cfg.DBShards)
	if err != nil {
		slog.Error("shard router init failed", "err", err)
		os.Exit(1)
	}
	defer shard.Close()

	// ── Redis (optional) ─────────────────────────────────────────────────────
	// When REDIS_ADDR is unset-to-empty the client is never built. That is the
	// difference between running without a cache and running against a dead
	// one: a dead endpoint is dialed and retried on every request, which costs
	// seconds per call before the in-memory fallback takes over.
	ctx := context.Background()
	var rdb *redis.Client
	if cfg.RedisEnabled() {
		opts, err := redisOptions(cfg)
		if err != nil {
			slog.Error("redis config invalid", "err", err)
			os.Exit(1)
		}
		rdb = redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			slog.Warn("redis unreachable at startup — cache and distributed rate limiting disabled",
				"addr", cfg.RedisAddr, "err", err)
		}
	} else {
		slog.Info("redis not configured — cache disabled, rate limiting in-process")
	}

	// ── Cache ────────────────────────────────────────────────────────────────
	cacheLayer := cache.New(rdb)

	// ── GeoIP ────────────────────────────────────────────────────────────────
	geo, err := geoip.New(cfg.GeoIPDBPath)
	if err != nil {
		slog.Error("geoip init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = geo.Close() }()

	// ── Analytics recorder (background goroutine) ────────────────────────────
	recorder := analytics.New(cfg.AnalyticsBufferSize, shard.GetDB)
	recCtx, recCancel := context.WithCancel(ctx)
	defer recCancel()
	go recorder.Run(recCtx)

	// ── Rate limiter (Token Bucket, Redis-backed, IP-keyed) ──────────────────
	rlCfg := limiter.Config{
		Rate:   cfg.RateLimit,
		Window: cfg.RateWindow,
		Burst:  cfg.RateBurst,
	}
	memStore, err := rlstore.NewMemoryStore(func(_ string) (limiter.Limiter, error) {
		return limiter.NewTokenBucket(rlCfg)
	})
	if err != nil {
		slog.Error("memory store init failed", "err", err)
		os.Exit(1)
	}
	// Without Redis the memory store is the limiter outright, rather than the
	// fallback behind a Redis store that fails on every call first.
	var rateLimiter limiter.KeyedLimiter = memStore
	if cfg.RedisEnabled() {
		redisStore, err := rlstore.NewRedisStore(rdb, rlCfg, memStore)
		if err != nil {
			slog.Error("redis store init failed", "err", err)
			os.Exit(1)
		}
		rateLimiter = redisStore
	}

	// ── Gin router ───────────────────────────────────────────────────────────
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())

	h := handler.New(cfg, shard, cacheLayer, recorder, geo)
	h.Register(r, rateLimiter)

	// ── HTTP server with graceful shutdown ───────────────────────────────────
	srv := &http.Server{
		Addr:         cfg.ServerAddr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("server starting", "addr", cfg.ServerAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down …")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()

	recCancel() // stop analytics recorder first (flushes pending clicks)
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
	slog.Info("shutdown complete")
}

// redisOptions builds the client config from either REDIS_URL (preferred — it
// carries the scheme, so "rediss://" turns on TLS) or the host/password pair.
//
// Timeouts are deliberately tight and retries are off: the cache and the rate
// limiter both have a correct in-process fallback, so a slow Redis is worse
// than an absent one. The library defaults (3 retries with backoff) turn a
// Redis outage into seconds of added latency on every single request.
func redisOptions(cfg *config.Config) (*redis.Options, error) {
	var opts *redis.Options

	if cfg.RedisURL != "" {
		parsed, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		opts = parsed
	} else {
		opts = &redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
		}
	}

	opts.DialTimeout = 500 * time.Millisecond
	opts.ReadTimeout = 300 * time.Millisecond
	opts.WriteTimeout = 300 * time.Millisecond
	opts.MaxRetries = -1 // -1 disables retries; 0 would mean "use the default"
	return opts, nil
}

// requestLogger returns a minimal structured-logging middleware.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}
