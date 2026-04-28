package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zeayush/url-shortener-go/internal/analytics"
	"github.com/zeayush/url-shortener-go/internal/model"
)

// Redirect handles GET /:code — the hot-path redirect.
//
// Flow:
//  1. Look up the link in Redis (cache hit ≈ sub-1 ms).
//  2. On cache miss, query the correct shard and re-warm Redis.
//  3. Check link expiry (410 Gone for expired links).
//  4. Enqueue a click event on the analytics channel (non-blocking).
//  5. 302 redirect to the long URL.
func (h *Handler) Redirect(c *gin.Context) {
	code := c.Param("code")

	link, err := h.resolveLink(c, code)
	if err != nil {
		writeResolveError(c, err)
		return
	}

	// Check expiry.
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "link has expired"})
		return
	}

	// Record click asynchronously (never blocks the redirect).
	h.recorder.Record(model.Click{
		ShortCode:  code,
		ClickedAt:  time.Now().UTC(),
		IPHash:     analytics.HashIP(c.ClientIP()),
		Country:    h.geo.Country(c.ClientIP()),
		DeviceType: analytics.DetectDevice(c.GetHeader("User-Agent")),
		Referrer:   c.GetHeader("Referer"),
	})

	// Optimistically bump the cached click count so readers see an approximate
	// count without waiting for the background flush.
	h.cache.IncrClickCount(c.Request.Context(), code)

	c.Redirect(http.StatusFound, link.LongURL)
}
