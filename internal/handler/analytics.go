package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zeayush/url-shortener-go/internal/repository"
)

// GetAnalytics handles GET /api/links/:code/analytics.
func (h *Handler) GetAnalytics(c *gin.Context) {
	code := c.Param("code")

	db := h.shardRouter.GetDB(code)
	resp, err := repository.GetAnalytics(c.Request.Context(), db, code)
	if err != nil {
		writeResolveError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
