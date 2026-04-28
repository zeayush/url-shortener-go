# url-shortener-go

Production-grade URL shortener in Go — Base-62 codes, consistent-hash database sharding, Redis caching, click analytics with GeoIP, link expiry, and a Redis-backed token-bucket rate limiter.

Part of a distributed systems portfolio implementing every system from Alex Xu's *System Design Interview* (Vol. 1 & 2). This covers **Chapter 8 — Design a URL Shortener**, integrating patterns from Chapters 4 (Rate Limiting), 5 (Consistent Hashing), and 6 (Key-Value Store / Caching).

---

## Features

| Feature | Detail |
|---|---|
| **Base-62 short codes** | 6 chars · 62⁶ = **56 billion** unique codes · `crypto/rand` prevents enumeration |
| **Custom aliases** | `yourshortener.com/my-custom-link` · 4–32 chars `[0-9 A-Z a-z _ -]` |
| **Consistent hashing** | CRC-32 ring, 150 virtual nodes/shard · only ~1/N keys remapped on topology change |
| **Redis caching** | `url:{code}` → JSON · TTL = min(ExpiresAt, 1 h) · sub-1 ms redirects for hot links |
| **Click analytics** | count · timestamp · country (GeoIP) · device type · referrer |
| **Link expiry** | Per-link TTL in seconds · 410 Gone after deadline · Redis TTL auto-capped |
| **REST API** | create · read · delete · analytics per link |
| **Rate limiting** | Token bucket · Redis Lua (atomic) · IP-keyed · fail-open fallback |

---

## Architecture

```
Browser  ──GET /Xk9mQr──►  Gin Router
                               │
                    ┌──────────▼───────────┐
                    │  Redis cache lookup   │
                    │  "url:Xk9mQr"        │
                    └──────────┬───────────┘
                          HIT  │  MISS
                               ▼
                    Consistent-Hash Ring
                    (CRC-32 · 150 vnodes)
                               │
                    ┌──────────▼───────────┐
                    │  PostgreSQL Shard N   │
                    │  SELECT short_code    │
                    └──────────┬───────────┘
                               │  re-warm Redis
                               ▼
                    Check ExpiresAt → 410 if expired
                               │
                    analytics.Record(click)   ← non-blocking
                               │
                         302 → LongURL
```

**Analytics pipeline** — a buffered channel separates the redirect path from I/O:

```
Redirect handler
  └─ chan model.Click (buffer = 2048)
       └─ background goroutine
            ├─ flush every 2 s  OR  batch of 64 events
            └─ multi-row INSERT INTO clicks (per-shard routing)
                        + UPDATE links SET click_count = click_count + N
```

**Rate limiting** mirrors the design from [zeayush/rate-limiter-go](https://github.com/zeayush/rate-limiter-go):

```
HTTP Request (POST /api/*)
     │
     ▼
GinIPExtractor(c) ──► "192.168.1.1"
     │
     ▼
RedisStore.Allow(ctx, ip)   ← Lua token-bucket script (atomic)
     │                  │
     ▼                  ▼
 Allowed            MemoryStore (fallback if Redis is down)
     │
 X-RateLimit-* headers → next handler
                  OR
 429 Too Many Requests + Retry-After
```

---

## Tech Stack

| Layer | Choice |
|---|---|
| Language | Go 1.22 |
| HTTP framework | [Gin](https://github.com/gin-gonic/gin) |
| Database | PostgreSQL 16 (2 shards) |
| Cache | Redis 7 |
| GeoIP | MaxMind GeoLite2-Country |
| Container | Docker Compose |
| Consistent hashing | [zeayush/consistent-hashing-go](https://github.com/zeayush/consistent-hashing-go) |
| Rate limiting | in-tree port of [zeayush/rate-limiter-go](https://github.com/zeayush/rate-limiter-go) |

---

## Quick Start

### Run with Docker Compose

```bash
git clone https://github.com/zeayush/url-shortener-go
cd url-shortener-go
docker compose up --build
```

This starts:
- **app** on `:8080`
- **postgres-0** on `:5432` (shard 0)
- **postgres-1** on `:5433` (shard 1)
- **redis** on `:6379`

The migration (`migrations/001_init.sql`) runs automatically via `docker-entrypoint-initdb.d`.

### Try the API

```bash
# Create a short link
curl -s -X POST http://localhost:8080/api/links \
  -H "Content-Type: application/json" \
  -d '{"url":"https://github.com/zeayush/url-shortener-go"}' | jq
# {
#   "short_code": "Xk9mQr",
#   "short_url": "http://localhost:8080/Xk9mQr",
#   "long_url": "https://github.com/zeayush/url-shortener-go"
# }

# Create a custom alias with a 7-day TTL
curl -s -X POST http://localhost:8080/api/links \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","custom_alias":"my-link","ttl_seconds":604800}' | jq

# Redirect (follow with -L)
curl -Ls http://localhost:8080/Xk9mQr

# Get link metadata
curl -s http://localhost:8080/api/links/Xk9mQr | jq

# Analytics
curl -s http://localhost:8080/api/links/Xk9mQr/analytics | jq

# Delete
curl -s -X DELETE http://localhost:8080/api/links/Xk9mQr | jq

# Health check
curl -s http://localhost:8080/health
# {"redis":"ok"}
```

### Rate limit behaviour

```bash
# Exhaust the limit (default: 100 req / 60 s per IP)
for i in $(seq 1 105); do
  curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/links/Xk9mQr
done
# 200 × 100 … 429 × 5

# 429 body
# {"error":"rate limit exceeded","retry_after":42}
```

---

## Configuration

All settings come from environment variables (12-factor app). Copy `.env.example` and adjust:

```bash
cp .env.example .env
```

| Variable | Default | Description |
|---|---|---|
| `BASE_URL` | `http://localhost:8080` | Prepended to every short code |
| `SERVER_ADDR` | `:8080` | Bind address |
| `DB_SHARD_0_DSN` | — | **Required.** PostgreSQL DSN for shard 0 |
| `DB_SHARD_1_DSN` | — | Optional second shard |
| `REDIS_ADDR` | `localhost:6379` | Redis host:port |
| `REDIS_PASSWORD` | *(empty)* | Redis AUTH password |
| `GEOIP_DB_PATH` | `./data/GeoLite2-Country.mmdb` | MaxMind database path |
| `RATE_LIMIT` | `100` | Max requests per window |
| `RATE_BURST` | `20` | Token-bucket burst capacity |
| `RATE_WINDOW_SECONDS` | `60` | Window / refill period in seconds |
| `ANALYTICS_BUFFER_SIZE` | `2048` | Async click channel buffer depth |

**Adding a shard** — set `DB_SHARD_2_DSN` and redeploy. The consistent-hash ring remaps only ~1/N of existing keys.

**Docker Compose host port overrides:**

```bash
APP_PORT=9090 PG0_PORT=5440 PG1_PORT=5441 REDIS_PORT=6380 docker compose up --build
```

### GeoIP database (optional)

```bash
# Requires a free MaxMind account and license key
make geoip MAXMIND_LICENSE_KEY=<your-key>
```

Without the `.mmdb` file the server starts normally and stores `""` as the country for every click.

---

## API Reference

### `POST /api/links`

**Request body:**

```json
{
  "url": "https://example.com/very/long/path",
  "custom_alias": "my-link",
  "ttl_seconds": 604800
}
```

`url` is required. `custom_alias` and `ttl_seconds` are optional.

**Response `201 Created`:**

```json
{
  "short_code": "Xk9mQr",
  "short_url": "http://localhost:8080/Xk9mQr",
  "long_url": "https://example.com/very/long/path",
  "expires_at": "2026-05-05T12:00:00Z"
}
```

---

### `GET /api/links/:code`

Returns link metadata (same shape as creation response + `click_count`).

---

### `DELETE /api/links/:code`

Removes the link from the database and evicts it from Redis.
Returns `200 {"message":"deleted"}` or `404`.

---

### `GET /api/links/:code/analytics`

```json
{
  "short_code": "Xk9mQr",
  "total_clicks": 1042,
  "clicks_by_country": { "US": 610, "IN": 220, "GB": 90, "other": 122 },
  "clicks_by_device":  { "desktop": 700, "mobile": 300, "bot": 42 },
  "recent_clicks": [
    {
      "short_code": "Xk9mQr",
      "clicked_at": "2026-04-28T10:05:22Z",
      "country": "US",
      "device_type": "mobile",
      "referrer": "https://twitter.com"
    }
  ]
}
```

---

### `GET /:code`

302 redirect to the long URL. 410 Gone if expired. 404 if not found.

---

### `GET /health`

```json
{"redis":"ok"}
```

---

## Project Structure

```
url-shortener-go/
├── cmd/
│   └── server/
│       └── main.go                  # Entrypoint — wires all dependencies
├── internal/
│   ├── config/config.go             # Env-var config loader
│   ├── model/model.go               # Domain structs + request/response DTOs
│   ├── shortcode/base62.go          # Base-62 generation & validation
│   ├── cache/redis.go               # Redis link cache
│   ├── geoip/geoip.go               # MaxMind country resolver
│   ├── analytics/recorder.go        # Async click event pipeline
│   ├── repository/
│   │   ├── postgres.go              # SQL data-access (single shard)
│   │   └── shard.go                 # Consistent-hash shard router
│   ├── ratelimit/
│   │   ├── limiter.go               # Interfaces + Config / Result types
│   │   ├── tokenbucket.go           # Single-key token-bucket algorithm
│   │   ├── store/
│   │   │   ├── memory.go            # In-memory KeyedLimiter (fallback)
│   │   │   └── redis.go             # Redis KeyedLimiter (Lua-atomic)
│   │   └── middleware/
│   │       └── gin.go               # Gin rate-limit middleware
│   └── handler/
│       ├── router.go                # Route registration
│       ├── links.go                 # Create / get / delete handlers
│       ├── redirect.go              # Redirect hot-path handler
│       └── analytics.go             # Analytics endpoint handler
├── migrations/
│   └── 001_init.sql                 # Idempotent schema (run on every shard)
├── data/                            # GeoLite2-Country.mmdb goes here
├── Dockerfile                       # Multi-stage: builder → alpine runtime
├── docker-compose.yml               # app + 2× postgres + redis
├── Makefile
└── .env.example
```

---

## How Consistent Hashing Works Here

Powered by [zeayush/consistent-hashing-go](https://github.com/zeayush/consistent-hashing-go):

```
          0 ──────────────────────────── 2³²-1
          │                                 │
     ┌────▼────┐                       ┌────▼────┐
     │shard-0  │ ◄── short code hash ──│shard-1  │
     │(vnodes) │    lands on nearest   │(vnodes) │
     └────┬────┘    vnode clockwise    └────┬────┘
          │                                 │
     150 virtual nodes per shard = even distribution
```

- `ring.Add("shard-0", 1)` + `ring.Add("shard-1", 1)` at startup.
- Every `ShardRouter.GetDB(shortCode)` call does a CRC-32 hash + O(log n) binary search.
- Adding `DB_SHARD_2_DSN` remaps only ~33 % of keys instead of ~100 %.

---

## How Rate Limiting Works Here

Ported from [zeayush/rate-limiter-go](https://github.com/zeayush/rate-limiter-go) (Month 2, Week 1):

```
Request
  │
  ▼
GinIPExtractor → "1.2.3.4"
  │
  ▼
RedisStore.Allow(ctx, "1.2.3.4")
  │
  ▼
Lua script (single round-trip, atomic):
  HMGET rl:1.2.3.4 tokens last_ns
  new_tokens = min(burst, tokens + elapsed_s × rate)
  if new_tokens ≥ 1 → HMSET, PEXPIRE, return {1, remaining, 0}
  else              → return {0, 0, retry_after_ns}
  │
  ├─ Allowed  → set X-RateLimit-* headers, call next handler
  └─ Denied   → 429 + Retry-After header + JSON body
```

The in-memory `MemoryStore` is used as an automatic fallback when Redis is unavailable — a Redis outage **never** blocks API traffic (fail-open policy).

---

## Key Design Decisions

**Redirect path is never rate-limited.** Rate limits apply only to `POST /api/links` and metadata/analytics reads. Throttling redirects would defeat the purpose of a URL shortener.

**Analytics are "at most once".** The buffered channel drops events when full rather than blocking the redirect. A burst of traffic serves users fast; a few click events may be lost — a deliberate trade-off.

**Privacy by design.** Raw client IPs are never stored. The SHA-256 hash of the IP is stored in `ip_hash`, which satisfies GDPR data minimisation while still allowing duplicate-click detection.

**Lua script eliminates the INCR/EXPIRE race.** A naive `INCR` followed by `EXPIRE` in two round-trips can leave a key without a TTL if the connection drops between them. The atomic Lua script sets both in a single command.

**Fail-open on all middleware errors.** Both the rate limiter and the GeoIP resolver degrade gracefully: a Redis outage falls back to in-memory limiting; a missing `.mmdb` file stores `""` as the country.

---

## Related Projects

| Project | Chapter |
|---|---|
| [zeayush/consistent-hashing-go](https://github.com/zeayush/consistent-hashing-go) | Chapter 5 — Design Consistent Hashing |
| [zeayush/rate-limiter-go](https://github.com/zeayush/rate-limiter-go) | Chapter 4 — Design a Rate Limiter |

---

## License

MIT
