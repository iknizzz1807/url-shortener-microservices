package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type CachedURL struct {
	OriginalURL string     `json:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    bool       `json:"is_active"`
}

type RedisCache struct {
	client *redis.Client
	log    *slog.Logger
}

func NewRedisCache(client *redis.Client, log *slog.Logger) *RedisCache {
	return &RedisCache{client: client, log: log}
}

func (c *RedisCache) Get(ctx context.Context, shortCode string) (*CachedURL, bool) {
	key := "url:" + shortCode
	val, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, false
		}
		c.log.Warn("redis get failed", "key", key, "error", err)
		return nil, false
	}
	var cached CachedURL
	if err := json.Unmarshal(val, &cached); err != nil {
		c.log.Warn("redis unmarshal failed", "key", key, "error", err)
		return nil, false
	}
	return &cached, true
}

func (c *RedisCache) Set(ctx context.Context, shortCode string, url *CachedURL, expiresAt *time.Time) {
	key := "url:" + shortCode
	data, err := json.Marshal(url)
	if err != nil {
		c.log.Warn("redis marshal failed", "key", key, "error", err)
		return
	}
	ttl := time.Hour
	if expiresAt != nil {
		ttl = time.Until(*expiresAt)
		if ttl <= 0 {
			return
		}
	}
	if err := c.client.Set(ctx, key, data, ttl).Err(); err != nil {
		c.log.Warn("redis set failed", "key", key, "error", err)
	}
}

func (c *RedisCache) Del(ctx context.Context, shortCode string) {
	key := "url:" + shortCode
	if err := c.client.Del(ctx, key).Err(); err != nil {
		c.log.Warn("redis del failed", "key", key, "error", err)
	}
}
