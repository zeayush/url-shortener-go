// Package handler wires together all HTTP handlers and the Gin router.
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/zeayush/rate-limiter-go/limiter"
	rlmw "github.com/zeayush/rate-limiter-go/middleware"
	"github.com/zeayush/url-shortener-go/internal/analytics"
	"github.com/zeayush/url-shortener-go/internal/cache"
	"github.com/zeayush/url-shortener-go/internal/config"
	"github.com/zeayush/url-shortener-go/internal/geoip"
	"github.com/zeayush/url-shortener-go/internal/repository"
)

// Handler holds shared dependencies injected into every route handler.
type Handler struct {
	cfg         *config.Config
	shardRouter *repository.ShardRouter
	cache       *cache.Cache
	recorder    *analytics.Recorder
	geo         *geoip.Resolver
}

// New creates a Handler with the supplied dependencies.
func New(
	cfg *config.Config,
	shardRouter *repository.ShardRouter,
	cache *cache.Cache,
	recorder *analytics.Recorder,
	geo *geoip.Resolver,
) *Handler {
	return &Handler{
		cfg:         cfg,
		shardRouter: shardRouter,
		cache:       cache,
		recorder:    recorder,
		geo:         geo,
	}
}

// Register attaches all routes to r.
func (h *Handler) Register(r *gin.Engine, rl limiter.KeyedLimiter) {
	// ── Redirect (hot path — no rate limit, sub-1 ms via Redis cache) ───────
	r.GET("/:code", h.Redirect)

	// ── REST API ─────────────────────────────────────────────────────────────
	api := r.Group("/api")
	api.Use(rlmw.GinMiddleware(rl, rlmw.GinIPExtractor))
	{
		api.POST("/links", h.CreateLink)
		api.GET("/links/:code", h.GetLink)
		api.DELETE("/links/:code", h.DeleteLink)
		api.GET("/links/:code/analytics", h.GetAnalytics)
	}

	// ── Health check ─────────────────────────────────────────────────────────
	r.GET("/health", h.Health)
}
