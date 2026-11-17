.PHONY: docker-up docker-down run build test test-integration check

LOAD_ENV = set -e; set -a; if [ -f .env ]; then . ./.env; fi; set +a;

# Complete Compose stack; named volumes survive shutdown.
docker-up:
	docker compose --env-file .env up --build -d --wait

docker-down:
	docker compose --env-file .env down

# Local execution and build.
run:
	@$(LOAD_ENV) exec go run ./cmd/shortener

build:
	go build -o bin/shortener ./cmd/shortener

# Unit tests and isolated integration environment.
test:
	go test ./...

test-integration:
	@set -e; \
	trap 'docker compose --env-file /dev/null -f docker-compose.test.yml -p shortener-test down' EXIT; \
	docker compose --env-file /dev/null -f docker-compose.test.yml -p shortener-test up -d --wait postgres redis; \
	TEST_POSTGRES_DSN="$${TEST_POSTGRES_DSN:-postgres://shortener:shortener@localhost:55432/shortener?sslmode=disable}" \
	TEST_REDIS_ADDR="$${TEST_REDIS_ADDR:-localhost:56379}" \
	go test -count=1 -timeout=2m -tags=integration ./tests/integration/...

# Formatting failures stop checks, including failures to execute gofmt.
check:
	@files="$$(gofmt -l cmd internal migrations tests web)" || exit $$?; \
	if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi
	go test ./...
	go test -race ./...
	go vet ./...
	go build ./...
