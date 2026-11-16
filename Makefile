.PHONY: run build test race vet fmt fmt-check check tidy

run:
	@set -e; \
	set -a; \
	if [ -f .env ]; then . ./.env; fi; \
	set +a; \
	exec go run ./cmd/shortener

build:
	go build -o bin/shortener ./cmd/shortener

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	@files="$$(gofmt -l cmd internal)"; \
	if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi

tidy:
	go mod tidy

check: fmt-check test race vet build
