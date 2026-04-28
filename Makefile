.PHONY: setup build run test docker-up docker-down migrate

setup:
	go get github.com/gin-gonic/gin@v1.10.0
	go get github.com/lib/pq@v1.10.9
	go get github.com/oschwald/maxminddb-golang@v1.13.0
	go get github.com/redis/go-redis/v9@v9.6.1
	go get github.com/zeayush/consistent-hashing-go@latest
	go mod tidy

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

test:
	go test -race ./...

lint:
	golangci-lint run ./...

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v

logs:
	docker compose logs -f app

migrate:
	@for dsn in $${DB_SHARD_0_DSN} $${DB_SHARD_1_DSN}; do \
		echo "Migrating $$dsn"; \
		psql "$$dsn" -f migrations/001_init.sql; \
	done

# Download MaxMind GeoLite2 DB (requires MAXMIND_LICENSE_KEY env var)
geoip:
	curl -L "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country&license_key=$(MAXMIND_LICENSE_KEY)&suffix=tar.gz" \
		| tar -xz --wildcards --strip-components=1 -C data/ '*.mmdb'
