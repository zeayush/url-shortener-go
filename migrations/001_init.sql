-- Run this migration on every shard database.
-- Each shard is an independent PostgreSQL instance storing a horizontal partition
-- of links and their click events, routed via consistent hashing in the app tier.

CREATE TABLE IF NOT EXISTS links (
    id             BIGSERIAL    PRIMARY KEY,
    short_code     VARCHAR(32)  NOT NULL,
    long_url       TEXT         NOT NULL,
    is_custom_alias BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ,
    click_count    BIGINT       NOT NULL DEFAULT 0,
    CONSTRAINT uq_short_code UNIQUE (short_code)
);

CREATE INDEX IF NOT EXISTS idx_links_short_code  ON links (short_code);
CREATE INDEX IF NOT EXISTS idx_links_expires_at  ON links (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS clicks (
    id          BIGSERIAL   PRIMARY KEY,
    short_code  VARCHAR(32) NOT NULL REFERENCES links (short_code) ON DELETE CASCADE,
    clicked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_hash     VARCHAR(64),          -- SHA-256 of client IP (privacy-preserving)
    country     VARCHAR(3),           -- ISO 3166-1 alpha-2 / alpha-3
    device_type VARCHAR(16),          -- desktop | mobile | tablet | bot | unknown
    referrer    TEXT
);

CREATE INDEX IF NOT EXISTS idx_clicks_short_code  ON clicks (short_code);
CREATE INDEX IF NOT EXISTS idx_clicks_clicked_at  ON clicks (short_code, clicked_at DESC);
