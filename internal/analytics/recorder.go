// Package analytics records click events asynchronously.
//
// Clicks are pushed onto a buffered channel; a background worker drains the
// channel and flushes them to the database in small batches. The main redirect
// path never blocks on I/O.
//
// If the buffer is full (burst of traffic), new clicks are silently dropped
// rather than slowing down redirects — analytics are "at most once".
package analytics

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zeayush/url-shortener-go/internal/model"
)

// DBGetter returns the *sql.DB responsible for a given short code.
// The Recorder calls this to route each batch to the correct shard.
type DBGetter func(shortCode string) *sql.DB

const (
	flushInterval = 2 * time.Second
	batchSize     = 64
)

// Recorder buffers click events and persists them in the background.
type Recorder struct {
	ch    chan model.Click
	getDB DBGetter
}

// New creates a Recorder with a channel buffer of bufferSize.
// getDB must be safe for concurrent use — ShardRouter.GetDB satisfies this.
func New(bufferSize int, getDB DBGetter) *Recorder {
	return &Recorder{
		ch:    make(chan model.Click, bufferSize),
		getDB: getDB,
	}
}

// Record enqueues a click event. Non-blocking: events are dropped if the
// buffer is full.
func (r *Recorder) Record(click model.Click) {
	select {
	case r.ch <- click:
	default:
		slog.Debug("analytics: buffer full, dropping click", "code", click.ShortCode)
	}
}

// Run drains the event channel in batches and writes to the appropriate shard.
// It returns when ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	// Local batch; flushed on tick or size threshold.
	batch := make([]model.Click, 0, batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Group by DB instance to minimise round-trips.
		byDB := make(map[*sql.DB][]model.Click)
		for _, c := range batch {
			db := r.getDB(c.ShortCode)
			byDB[db] = append(byDB[db], c)
		}
		for db, clicks := range byDB {
			if err := insertClicks(ctx, db, clicks); err != nil {
				slog.Error("analytics: insert failed", "err", err)
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Drain remaining buffered events then return.
			draining := true
			for draining {
				select {
				case c := <-r.ch:
					batch = append(batch, c)
				default:
					draining = false
				}
			}
			flush()
			return

		case <-ticker.C:
			flush()

		case c := <-r.ch:
			batch = append(batch, c)
			if len(batch) >= batchSize {
				flush()
			}
		}
	}
}

// HashIP returns the hex-encoded SHA-256 of the IP address, used to
// deduplicate clicks without storing raw IPs (privacy by design).
func HashIP(ip string) string {
	h := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(h[:])
}

// DetectDevice classifies the User-Agent header into one of:
// desktop | mobile | tablet | bot | unknown
func DetectDevice(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "bot") ||
		strings.Contains(lower, "crawler") ||
		strings.Contains(lower, "spider"):
		return "bot"
	case strings.Contains(lower, "ipad") ||
		strings.Contains(lower, "tablet"):
		return "tablet"
	case strings.Contains(lower, "mobile") ||
		strings.Contains(lower, "android") ||
		strings.Contains(lower, "iphone"):
		return "mobile"
	case ua == "":
		return "unknown"
	default:
		return "desktop"
	}
}

// ─── DB helpers ──────────────────────────────────────────────────────────────

func insertClicks(ctx context.Context, db *sql.DB, clicks []model.Click) error {
	if len(clicks) == 0 {
		return nil
	}

	// Build a multi-row INSERT for all columns including referrer.
	const cols = 6
	vals := make([]interface{}, 0, len(clicks)*cols)
	placeholders := make([]string, 0, len(clicks))

	for i, c := range clicks {
		base := i * cols
		placeholders = append(placeholders,
			fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6))
		vals = append(vals,
			c.ShortCode, c.ClickedAt, c.IPHash, c.Country, c.DeviceType, c.Referrer)
	}

	query := "INSERT INTO clicks (short_code,clicked_at,ip_hash,country,device_type,referrer) VALUES " +
		strings.Join(placeholders, ",")

	if _, err := db.ExecContext(ctx, query, vals...); err != nil {
		return err
	}

	// Increment click_count on the link rows (best-effort; not transactional).
	codes := make(map[string]int)
	for _, c := range clicks {
		codes[c.ShortCode]++
	}
	for code, n := range codes {
		_, _ = db.ExecContext(ctx,
			"UPDATE links SET click_count = click_count + $1 WHERE short_code = $2", n, code)
	}
	return nil
}
