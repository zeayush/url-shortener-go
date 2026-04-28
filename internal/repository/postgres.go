// Package repository implements PostgreSQL data-access for a single shard.
// Every function receives a *sql.DB so callers (the ShardRouter) can route
// to the correct instance before calling in.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/zeayush/url-shortener-go/internal/model"
)

// ErrNotFound is returned when a short code is not present on the queried shard.
var ErrNotFound = errors.New("repository: link not found")

// Open opens a PostgreSQL connection pool for dsn and verifies connectivity.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("repository: open %q: %w", dsn, err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("repository: ping %q: %w", dsn, err)
	}
	return db, nil
}

// Insert persists a new link. Returns an error wrapping a unique-constraint
// violation so callers can detect code collisions.
func Insert(ctx context.Context, db *sql.DB, link *model.Link) error {
	const q = `
		INSERT INTO links (short_code, long_url, is_custom_alias, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, click_count`

	row := db.QueryRowContext(ctx, q,
		link.ShortCode, link.LongURL, link.IsCustomAlias, link.ExpiresAt)

	if err := row.Scan(&link.ID, &link.CreatedAt, &link.ClickCount); err != nil {
		return fmt.Errorf("repository: insert %q: %w", link.ShortCode, err)
	}
	return nil
}

// GetByCode fetches the link for shortCode. Returns ErrNotFound when absent.
func GetByCode(ctx context.Context, db *sql.DB, shortCode string) (*model.Link, error) {
	const q = `
		SELECT id, short_code, long_url, is_custom_alias,
		       created_at, expires_at, click_count
		FROM   links
		WHERE  short_code = $1`

	var link model.Link
	err := db.QueryRowContext(ctx, q, shortCode).Scan(
		&link.ID, &link.ShortCode, &link.LongURL, &link.IsCustomAlias,
		&link.CreatedAt, &link.ExpiresAt, &link.ClickCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("repository: get %q: %w", shortCode, err)
	}
	return &link, nil
}

// Delete removes the link (and all cascaded clicks) for shortCode.
// Returns ErrNotFound if the code does not exist.
func Delete(ctx context.Context, db *sql.DB, shortCode string) error {
	const q = `DELETE FROM links WHERE short_code = $1`
	res, err := db.ExecContext(ctx, q, shortCode)
	if err != nil {
		return fmt.Errorf("repository: delete %q: %w", shortCode, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAnalytics returns aggregated click stats for shortCode.
func GetAnalytics(ctx context.Context, db *sql.DB, shortCode string) (*model.AnalyticsResponse, error) {
	// Verify the link exists and grab total click_count.
	link, err := GetByCode(ctx, db, shortCode)
	if err != nil {
		return nil, err
	}

	resp := &model.AnalyticsResponse{
		ShortCode:       shortCode,
		TotalClicks:     link.ClickCount,
		ClicksByCountry: make(map[string]int64),
		ClicksByDevice:  make(map[string]int64),
	}

	// Country breakdown.
	rowsC, err := db.QueryContext(ctx,
		`SELECT COALESCE(country,'unknown'), COUNT(*) FROM clicks WHERE short_code=$1 GROUP BY country`,
		shortCode)
	if err != nil {
		return nil, fmt.Errorf("repository: analytics country %q: %w", shortCode, err)
	}
	defer rowsC.Close()
	for rowsC.Next() {
		var country string
		var cnt int64
		if err := rowsC.Scan(&country, &cnt); err != nil {
			return nil, err
		}
		resp.ClicksByCountry[country] = cnt
	}

	// Device breakdown.
	rowsD, err := db.QueryContext(ctx,
		`SELECT COALESCE(device_type,'unknown'), COUNT(*) FROM clicks WHERE short_code=$1 GROUP BY device_type`,
		shortCode)
	if err != nil {
		return nil, fmt.Errorf("repository: analytics device %q: %w", shortCode, err)
	}
	defer rowsD.Close()
	for rowsD.Next() {
		var device string
		var cnt int64
		if err := rowsD.Scan(&device, &cnt); err != nil {
			return nil, err
		}
		resp.ClicksByDevice[device] = cnt
	}

	// 20 most-recent clicks.
	rowsR, err := db.QueryContext(ctx,
		`SELECT id, short_code, clicked_at, COALESCE(ip_hash,''), COALESCE(country,''),
		        COALESCE(device_type,''), COALESCE(referrer,'')
		 FROM   clicks
		 WHERE  short_code = $1
		 ORDER  BY clicked_at DESC
		 LIMIT  20`,
		shortCode)
	if err != nil {
		return nil, fmt.Errorf("repository: analytics recent %q: %w", shortCode, err)
	}
	defer rowsR.Close()
	for rowsR.Next() {
		var c model.Click
		if err := rowsR.Scan(
			&c.ID, &c.ShortCode, &c.ClickedAt, &c.IPHash,
			&c.Country, &c.DeviceType, &c.Referrer,
		); err != nil {
			return nil, err
		}
		resp.RecentClicks = append(resp.RecentClicks, c)
	}

	return resp, nil
}

// Exists reports whether shortCode is present in db.
// Used by the create handler to detect alias collisions.
func Exists(ctx context.Context, db *sql.DB, shortCode string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM links WHERE short_code=$1)`, shortCode,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository: exists %q: %w", shortCode, err)
	}
	return exists, nil
}
