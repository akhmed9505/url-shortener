.PHONY: run build build-goose test race vet fmt fmt-check check tidy postgres-up postgres-down postgres-test-up postgres-test-down redis-up redis-down redis-test-up redis-test-down deps-up deps-down test-deps-up test-deps-down migrate-up migrate-down migrate-status test-integration

GOOSE_VERSION := v3.30.1
GOOSE_TAGS := no_clickhouse,no_libsql,no_mssql,no_mysql,no_sqlite3,no_vertica,no_ydb
LOAD_ENV = set -e; set -a; if [ -f .env ]; then . ./.env; fi; set +a;
GOOSE = GOOSE_DRIVER=postgres GOOSE_DBSTRING="$${POSTGRES_DSN:-postgres://shortener:shortener@localhost:5432/shortener?sslmode=disable}" go run -tags='$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -env=none -dir migrations -timeout 30s

# Local execution and builds.
run:
	@$(LOAD_ENV) exec go run ./cmd/shortener

build:
	go build -o bin/shortener ./cmd/shortener

build-goose:
	GOBIN="$(CURDIR)/bin" go install -tags='$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

# Unit tests and local checks.
test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal migrations tests web

fmt-check:
	@files="$$(gofmt -l cmd internal migrations tests web)" || exit $$?; \
	if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi

tidy:
	go mod tidy

check: fmt-check test race vet build

# Local dependency containers.
postgres-up:
	docker compose -f docker-compose.yml up -d --wait postgres

postgres-down:
	docker compose -f docker-compose.yml stop postgres

# Isolated PostgreSQL test instance.
postgres-test-up:
	POSTGRES_PORT=55432 docker compose -f docker-compose.yml -p shortener-test up -d --wait postgres

postgres-test-down:
	POSTGRES_PORT=55432 docker compose -f docker-compose.yml -p shortener-test stop postgres

# Local Redis containers.
redis-up:
	docker compose -f docker-compose.yml up -d --wait redis

redis-down:
	docker compose -f docker-compose.yml stop redis

redis-test-up:
	REDIS_PORT=56379 docker compose -f docker-compose.yml -p shortener-test up -d --wait redis

redis-test-down:
	REDIS_PORT=56379 docker compose -f docker-compose.yml -p shortener-test stop redis

# Combined dependency lifecycle.
deps-up:
	docker compose -f docker-compose.yml up -d --wait postgres redis

deps-down:
	docker compose -f docker-compose.yml down

test-deps-up:
	POSTGRES_PORT=55432 REDIS_PORT=56379 docker compose -f docker-compose.yml -p shortener-test up -d --wait postgres redis

test-deps-down:
	POSTGRES_PORT=55432 REDIS_PORT=56379 docker compose -f docker-compose.yml -p shortener-test down

# Goose migration commands.
migrate-up:
	@$(LOAD_ENV) $(GOOSE) up

migrate-down:
	@$(LOAD_ENV) $(GOOSE) down

migrate-status:
	@$(LOAD_ENV) $(GOOSE) status

# TEST_POSTGRES_DSN must be a URL on a dedicated test PostgreSQL instance.
# The user must be allowed to create databases. Each test creates and drops its own database.
test-integration:
	@$(LOAD_ENV) TEST_POSTGRES_DSN="$${TEST_POSTGRES_DSN:-postgres://shortener:shortener@localhost:55432/shortener?sslmode=disable}" TEST_REDIS_ADDR="$${TEST_REDIS_ADDR:-localhost:56379}" go test -count=1 -timeout=2m -tags=integration ./tests/integration/...
