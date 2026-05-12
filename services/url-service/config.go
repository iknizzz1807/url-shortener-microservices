package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL        string
	RedisURL           string
	RabbitMQURL        string
	JWTSecret          string
	ShortURLBase       string
	IPHashSalt         string
	Port               string
	ServiceName        string
	OutboxPollInterval time.Duration
	OutboxWorkerCount  int
}

func loadConfig() (*Config, error) {
	pollMs := 2000
	if v := os.Getenv("OUTBOX_POLL_INTERVAL_MS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			pollMs = parsed
		}
	}

	cfg := &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		RedisURL:           os.Getenv("REDIS_URL"),
		RabbitMQURL:        os.Getenv("RABBITMQ_URL"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		ShortURLBase:       os.Getenv("SHORT_URL_BASE"),
		IPHashSalt:         os.Getenv("IP_HASH_SALT"),
		Port:               envOrDefault("PORT", "8080"),
		ServiceName:        "url-service",
		OutboxPollInterval: time.Duration(pollMs) * time.Millisecond,
		OutboxWorkerCount:  3,
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	if cfg.RabbitMQURL == "" {
		return nil, fmt.Errorf("RABBITMQ_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.ShortURLBase == "" {
		return nil, fmt.Errorf("SHORT_URL_BASE is required")
	}

	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrDefaultInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
