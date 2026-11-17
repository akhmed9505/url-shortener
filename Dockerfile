FROM golang:1.27.3-alpine AS build
WORKDIR /src
RUN apk add --no-cache make
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-trimpath make build build-goose

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 shortener \
    && adduser -S -D -H -u 10001 -G shortener shortener
USER 10001:10001
WORKDIR /app

FROM runtime AS migrate
COPY --from=build /src/bin/goose /usr/local/bin/goose
COPY migrations/*.sql /app/migrations/
ENTRYPOINT ["goose", "-env=none", "-dir=/app/migrations", "-timeout=30s"]
CMD ["up"]

FROM runtime AS app
COPY --from=build /src/bin/shortener /app/shortener
EXPOSE 8080
ENTRYPOINT ["/app/shortener"]
