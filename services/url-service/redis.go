package main

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

func NewRedisClient(ctx context.Context, redisURL string, log *slog.Logger) *redis.Client {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Warn("redis parse url failed, using default", "error", err)
		opt = &redis.Options{Addr: "redis:6379"}
	}
	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		log.Warn("redis ping failed", "error", err)
	}
	return client
}
