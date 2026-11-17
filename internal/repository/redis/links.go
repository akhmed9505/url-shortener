// Package redis implements the link cache.
package redis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/redis/go-redis/v9"
)

const keyPrefix = "shortener:link:v1:"

// LinkCache caches stored links by short code.
type LinkCache struct {
	client *redis.Client
	ttl    time.Duration
}

type cachedLink struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}

// NewLinkCache creates a Redis link cache with the specified TTL.
func NewLinkCache(client *redis.Client, ttl time.Duration) *LinkCache {
	return &LinkCache{client: client, ttl: ttl}
}

// Get returns a cached link and reports whether it was found.
func (c *LinkCache) Get(ctx context.Context, code string) (domain.Link, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, config.RedisOperationTimeout)
	defer cancel()
	data, err := c.client.Get(ctx, keyPrefix+code).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.Link{}, false, nil
	}
	if err != nil {
		return domain.Link{}, false, fmt.Errorf("get cached link: %w", err)
	}
	payload, err := decodeLink(data)
	if err != nil {
		return domain.Link{}, false, err
	}
	return domain.Link{ID: payload.ID, Code: code, OriginalURL: payload.URL}, true, nil
}

func decodeLink(data []byte) (cachedLink, error) {
	invalid := errors.New("invalid cached link")
	if len(data) > 16<<10 || !utf8.Valid(data) {
		return cachedLink{}, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload cachedLink
	if err := decoder.Decode(&payload); err != nil || payload.ID <= 0 || payload.URL == "" || len(payload.URL) > 2048 {
		return cachedLink{}, invalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return cachedLink{}, invalid
	}
	return payload, nil
}

// Set caches a link until its TTL expires.
func (c *LinkCache) Set(ctx context.Context, link domain.Link) error {
	ctx, cancel := context.WithTimeout(ctx, config.RedisOperationTimeout)
	defer cancel()
	data, err := json.Marshal(cachedLink{ID: link.ID, URL: link.OriginalURL})
	if err != nil {
		return fmt.Errorf("encode cached link: %w", err)
	}
	if err := c.client.Set(ctx, keyPrefix+link.Code, data, c.ttl).Err(); err != nil {
		return fmt.Errorf("set cached link: %w", err)
	}
	return nil
}
