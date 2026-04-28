# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Download dependencies first (layer-cache friendly)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/server ./cmd/server

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

# Non-root user
RUN addgroup -S app && adduser -S app -G app
USER app

WORKDIR /app

COPY --from=builder /bin/server /app/server

# Optional: GeoLite2 database (mount at runtime or bake in)
RUN mkdir -p /app/data

EXPOSE 8080

ENTRYPOINT ["/app/server"]
