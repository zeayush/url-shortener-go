package model

import "time"

// Link represents a shortened URL record stored on a single shard.
type Link struct {
	ID            int64      `db:"id"              json:"id"`
	ShortCode     string     `db:"short_code"      json:"short_code"`
	LongURL       string     `db:"long_url"        json:"long_url"`
	IsCustomAlias bool       `db:"is_custom_alias" json:"is_custom_alias"`
	CreatedAt     time.Time  `db:"created_at"      json:"created_at"`
	ExpiresAt     *time.Time `db:"expires_at"      json:"expires_at,omitempty"`
	ClickCount    int64      `db:"click_count"     json:"click_count"`
}

// Click is a single analytics event recorded on redirect.
type Click struct {
	ID         int64     `db:"id"          json:"id"`
	ShortCode  string    `db:"short_code"  json:"short_code"`
	ClickedAt  time.Time `db:"clicked_at"  json:"clicked_at"`
	IPHash     string    `db:"ip_hash"     json:"-"`
	Country    string    `db:"country"     json:"country"`
	DeviceType string    `db:"device_type" json:"device_type"`
	Referrer   string    `db:"referrer"    json:"referrer"`
}

// ─── Request / Response DTOs ─────────────────────────────────────────────────

// CreateLinkRequest is the JSON body for POST /api/links.
type CreateLinkRequest struct {
	URL         string `json:"url"          binding:"required,url"`
	CustomAlias string `json:"custom_alias"`
	TTLSeconds  *int64 `json:"ttl_seconds"`
}

// CreateLinkResponse is returned after a successful creation.
type CreateLinkResponse struct {
	ShortCode string     `json:"short_code"`
	ShortURL  string     `json:"short_url"`
	LongURL   string     `json:"long_url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// AnalyticsResponse is returned by GET /api/links/:code/analytics.
type AnalyticsResponse struct {
	ShortCode       string           `json:"short_code"`
	TotalClicks     int64            `json:"total_clicks"`
	ClicksByCountry map[string]int64 `json:"clicks_by_country"`
	ClicksByDevice  map[string]int64 `json:"clicks_by_device"`
	RecentClicks    []Click          `json:"recent_clicks"`
}
