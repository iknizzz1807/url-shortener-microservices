package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func NewRabbitMQConn(ctx context.Context, url string, log *slog.Logger, maxAttempts int) (*amqp.Connection, error) {
	var conn *amqp.Connection
	var err error
	for i := 0; i < maxAttempts; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			break
		}
		log.Warn("rabbitmq connection attempt failed", "attempt", i+1, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connection failed after %d attempts: %w", maxAttempts, err)
	}
	return conn, nil
}

func DeclareExchange(ch *amqp.Channel, log *slog.Logger) error {
	err := ch.ExchangeDeclare(
		"url-shortener",
		"topic",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("declare exchange: %w", err)
	}
	log.Info("exchange declared", "name", "url-shortener", "type", "topic")
	return nil
}
