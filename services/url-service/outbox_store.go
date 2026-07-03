package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxRecord is the domain object mapped from the outbox table.
type OutboxRecord struct {
	ID          string // UUID string
	EventType   string
	Payload     []byte // raw JSONB bytes; sent as AMQP message body
	CreatedAt   time.Time
	LockedUntil *time.Time
	PublishedAt *time.Time // nil if unpublished
}

type OutboxStore interface {
	InsertEvent(ctx context.Context, tx pgx.Tx, outbox *OutboxRecord) error
	FetchUnpublished(ctx context.Context, limit int) ([]*OutboxRecord, error)
	MarkPublished(ctx context.Context, id string) error
}

type pgxOutboxStore struct {
	pool *pgxpool.Pool
}

func NewOutboxStore(pool *pgxpool.Pool) OutboxStore {
	return &pgxOutboxStore{pool: pool}
}

func (s *pgxOutboxStore) InsertEvent(ctx context.Context, tx pgx.Tx, outbox *OutboxRecord) error {
	const query = `INSERT INTO outbox (id, event_type, payload, created_at) VALUES ($1, $2, $3, $4)`
	if tx != nil {
		_, err := tx.Exec(ctx, query, outbox.ID, outbox.EventType, outbox.Payload, outbox.CreatedAt)
		return err
	}
	_, err := s.pool.Exec(ctx, query, outbox.ID, outbox.EventType, outbox.Payload, outbox.CreatedAt)
	return err
}

func (s *pgxOutboxStore) FetchUnpublished(ctx context.Context, limit int) ([]*OutboxRecord, error) {
	const query = `
		WITH claimed AS (
			SELECT id
			FROM outbox
			WHERE published_at IS NULL
			  AND (locked_until IS NULL OR locked_until < now())
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox o
		SET locked_until = now() + interval '30 seconds'
		FROM claimed
		WHERE o.id = claimed.id
		RETURNING o.id, o.event_type, o.payload, o.created_at, o.locked_until, o.published_at
	`
	rows, err := s.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*OutboxRecord
	for rows.Next() {
		var r OutboxRecord
		if err := rows.Scan(&r.ID, &r.EventType, &r.Payload, &r.CreatedAt, &r.LockedUntil, &r.PublishedAt); err != nil {
			return nil, err
		}
		results = append(results, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *pgxOutboxStore) MarkPublished(ctx context.Context, id string) error {
	const query = `UPDATE outbox SET published_at = now(), locked_until = NULL WHERE id = $1 AND published_at IS NULL`
	cmdTag, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
