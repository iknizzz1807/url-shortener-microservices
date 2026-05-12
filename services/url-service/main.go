package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ikniz/url-shortener/shared/auth"
	"github.com/ikniz/url-shortener/shared/logger"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg.ServiceName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := NewDBPool(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Error("db pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := runMigrations(ctx, pool, log); err != nil {
		log.Error("migrations", "error", err)
		os.Exit(1)
	}

	redisClient := NewRedisClient(ctx, cfg.RedisURL, log)
	defer redisClient.Close()
	cache := NewRedisCache(redisClient, log)

	mqConn, err := NewRabbitMQConn(ctx, cfg.RabbitMQURL, log, 10)
	if err != nil {
		log.Error("rabbitmq connection", "error", err)
		os.Exit(1)
	}
	defer mqConn.Close()

	ch, err := mqConn.Channel()
	if err != nil {
		log.Error("rabbitmq channel", "error", err)
		os.Exit(1)
	}
	defer ch.Close()

	if err := DeclareExchange(ch, log); err != nil {
		log.Error("declare exchange", "error", err)
		os.Exit(1)
	}

	urlStore := NewURLStore(pool)
	outboxStore := NewOutboxStore(pool)
	publisher := NewRabbitMQPublisher(ch, log)
	codeGen := NewShortCodeGenerator()
	handler := NewHandler(pool, urlStore, outboxStore, cache, codeGen, cfg, log)

	coordinator := NewOutboxCoordinator(outboxStore, publisher, log, cfg.OutboxPollInterval, cfg.OutboxWorkerCount)
	go coordinator.Run(ctx)

	authMw := auth.JWTMiddleware(cfg.JWTSecret)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", NewHealthHandler(cfg.ServiceName))
	mux.Handle("POST /shorten", authMw(http.HandlerFunc(handler.Shorten)))
	mux.Handle("GET /urls", authMw(http.HandlerFunc(handler.ListURLs)))
	mux.Handle("DELETE /urls/{code}", authMw(http.HandlerFunc(handler.DeleteURL)))
	mux.HandleFunc("GET /{code}", handler.Redirect)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		cancel()
		srv.Shutdown(ctx)
	}()

	log.Info("server listening", "port", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}
