package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/zeayush/url-shortener-go/internal/analytics"
	"github.com/zeayush/url-shortener-go/internal/cache"
	"github.com/zeayush/url-shortener-go/internal/config"
	"github.com/zeayush/url-shortener-go/internal/geoip"
	"github.com/zeayush/url-shortener-go/internal/handler"
	"github.com/zeayush/url-shortener-go/internal/ratelimit"
	rlstore "github.com/zeayush/url-shortener-go/internal/ratelimit/store"
	"github.com/zeayush/url-shortener-go/internal/ratelimit/middleware"
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

	// ── Redis ────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("redis unreachable at startup — cache and distributed rate limiting disabled",
			"addr", cfg.RedisAddr, "err", err)
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
	//
	// Mirrors the zeayush/rate-limiter-go Month 2 Week 1 setup:
	//   store.NewMemoryStore → fallback
	//   store.NewRedisStore  → primary (Lua-atomic token bucket)
	//   middleware.GinMiddleware + GinIPExtractor
	rlCfg := ratelimit.Config{
		Rate:   cfg.RateLimit,
		Window: cfg.RateWindow,
		Burst:  cfg.RateBurst,
	}
	memStore, err := rlstore.NewMemoryStore(func(_ string) (ratelimit.Limiter, error) {
		return ratelimit.NewTokenBucket(rlCfg)
	})
	if err != nil {
		slog.Error("memory store init failed", "err", err)
		os.Exit(1)
	}
	redisStore, err := rlstore.NewRedisStore(rdb, rlCfg, memStore)
	if err != nil {
		slog.Error("redis store init failed", "err", err)
		os.Exit(1)
	}

	_ = middleware.GinIPExtractor // ensure the package is used (router.go references it)

	// ── Gin router ───────────────────────────────────────────────────────────
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())

	h := handler.New(cfg, shard, cacheLayer, recorder, geo)
	h.Register(r, redisStore)

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
