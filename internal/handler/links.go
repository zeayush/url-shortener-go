package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zeayush/url-shortener-go/internal/model"
	"github.com/zeayush/url-shortener-go/internal/repository"
	"github.com/zeayush/url-shortener-go/internal/shortcode"
)

const maxCodeGenRetries = 5

// CreateLink handles POST /api/links.
//
// Flow:
//  1. Validate request body.
//  2. Resolve or generate the short code (with collision retry).
//  3. Route to the correct shard via consistent hashing.
//  4. INSERT into the shard DB.
//  5. Warm the Redis cache.
//  6. Return the short URL.
func (h *Handler) CreateLink(c *gin.Context) {
	var req model.CreateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// ── Resolve short code ──────────────────────────────────────────────────
	var (
		code     string
		isCustom bool
	)

	if req.CustomAlias != "" {
		if !shortcode.IsValid(req.CustomAlias) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "custom_alias must be 4–32 characters: [0-9 A-Z a-z _ -]",
			})
			return
		}
		code = req.CustomAlias
		isCustom = true

		// Check for collision on the target shard.
		db := h.shardRouter.GetDB(code)
		exists, err := repository.Exists(c.Request.Context(), db, code)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if exists {
			c.JSON(http.StatusConflict, gin.H{"error": "custom alias already taken"})
			return
		}
	} else {
		// Generate a random code, retrying on (rare) collisions.
		for i := range maxCodeGenRetries {
			candidate, err := shortcode.Random()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			db := h.shardRouter.GetDB(candidate)
			exists, err := repository.Exists(c.Request.Context(), db, candidate)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			if !exists {
				code = candidate
				break
			}
			if i == maxCodeGenRetries-1 {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not generate unique code"})
				return
			}
		}
	}

	// ── Compute optional expiry ─────────────────────────────────────────────
	var expiresAt *time.Time
	if req.TTLSeconds != nil && *req.TTLSeconds > 0 {
		t := time.Now().Add(time.Duration(*req.TTLSeconds) * time.Second)
		expiresAt = &t
	}

	link := &model.Link{
		ShortCode:     code,
		LongURL:       req.URL,
		IsCustomAlias: isCustom,
		ExpiresAt:     expiresAt,
	}

	// ── Persist ─────────────────────────────────────────────────────────────
	db := h.shardRouter.GetDB(code)
	if err := repository.Insert(c.Request.Context(), db, link); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create link"})
		return
	}

	// ── Warm cache ──────────────────────────────────────────────────────────
	_ = h.cache.SetLink(c.Request.Context(), link)

	c.JSON(http.StatusCreated, model.CreateLinkResponse{
		ShortCode: code,
		ShortURL:  fmt.Sprintf("%s/%s", h.cfg.BaseURL, code),
		LongURL:   link.LongURL,
		ExpiresAt: link.ExpiresAt,
	})
}

// GetLink handles GET /api/links/:code.
func (h *Handler) GetLink(c *gin.Context) {
	code := c.Param("code")

	link, err := h.resolveLink(c, code)
	if err != nil {
		writeResolveError(c, err)
		return
	}
	c.JSON(http.StatusOK, link)
}

// DeleteLink handles DELETE /api/links/:code.
func (h *Handler) DeleteLink(c *gin.Context) {
	code := c.Param("code")

	db := h.shardRouter.GetDB(code)
	if err := repository.Delete(c.Request.Context(), db, code); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Evict from cache.
	_ = h.cache.DeleteLink(c.Request.Context(), code)

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// Health handles GET /health.
func (h *Handler) Health(c *gin.Context) {
	redisErr := h.cache.Ping(c.Request.Context())
	redisStatus := "ok"
	if redisErr != nil {
		redisStatus = redisErr.Error()
	}
	c.JSON(http.StatusOK, gin.H{"redis": redisStatus})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// resolveLink returns a link for code, hitting Redis first and falling back to
// the shard DB. The live DB result is cached before returning.
func (h *Handler) resolveLink(c *gin.Context, code string) (*model.Link, error) {
	// 1. Redis (sub-1 ms for hot links).
	if link, err := h.cache.GetLink(c.Request.Context(), code); err == nil {
		return link, nil
	}

	// 2. Database — route via consistent hash.
	db := h.shardRouter.GetDB(code)
	link, err := repository.GetByCode(c.Request.Context(), db, code)
	if err != nil {
		return nil, err
	}

	// 3. Re-warm cache.
	_ = h.cache.SetLink(c.Request.Context(), link)
	return link, nil
}

func writeResolveError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
