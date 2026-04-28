// Package geoip wraps the MaxMind GeoLite2-Country database.
//
// If the .mmdb file is absent (e.g. first run without the DB downloaded),
// every lookup returns the empty string so the rest of the system degrades
// gracefully rather than refusing to start.
package geoip

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/oschwald/maxminddb-golang"
)

// record mirrors the fields we need from the GeoLite2-Country mmdb.
type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// Resolver looks up the ISO 3166-1 alpha-2 country code for an IP address.
type Resolver struct {
	db *maxminddb.Reader
}

// New opens the MaxMind database at path.
// If the file does not exist or cannot be read, a no-op resolver is returned
// and a warning is logged; the application still starts successfully.
func New(path string) (*Resolver, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		slog.Warn("geoip: database unavailable – country lookups disabled",
			"path", path, "err", err)
		return &Resolver{}, nil
	}
	return &Resolver{db: db}, nil
}

// Country returns the ISO 3166-1 alpha-2 country code (e.g. "US") for ip,
// or an empty string when the IP is private, unresolvable, or the DB is absent.
func (r *Resolver) Country(ipStr string) string {
	if r.db == nil {
		return ""
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}

	var rec record
	if err := r.db.Lookup(ip, &rec); err != nil {
		return ""
	}
	return rec.Country.ISOCode
}

// Close releases the mmdb file handle.
func (r *Resolver) Close() error {
	if r.db == nil {
		return nil
	}
	if err := r.db.Close(); err != nil {
		return fmt.Errorf("geoip: close: %w", err)
	}
	return nil
}
