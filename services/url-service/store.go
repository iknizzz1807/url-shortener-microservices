package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type URLRecord struct {
	ID          string
	ShortCode   string
	OriginalURL string
	UserID      string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	IsActive    bool
}

type OutboxRecord struct {
	ID          string
	EventType   string
	Payload     []byte
	CreatedAt   time.Time
	PublishedAt *time.Time
}

type URLRepository interface {
	Insert(ctx context.Context, rec *URLRecord) (*URLRecord, error)
	FindByCode(ctx context.Context, shortCode string) (*URLRecord, error)
	FindByUserID(ctx context.Context, userID, afterID string, limit int) ([]*URLRecord, string, error)
	Deactivate(ctx context.Context, shortCode, userID string) error
}

type pgxURLStore struct {
	pool *pgxpool.Pool
}

func NewURLStore(pool *pgxpool.Pool) URLRepository {
	return &pgxURLStore{pool: pool}
}

func (s *pgxURLStore) Insert(ctx context.Context, rec *URLRecord) (*URLRecord, error) {
	query := `
		INSERT INTO urls (short_code, original_url, user_id, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, short_code, original_url, user_id, created_at, expires_at, is_active
	`
	var out URLRecord
	err := s.pool.QueryRow(ctx, query, rec.ShortCode, rec.OriginalURL, rec.UserID, rec.ExpiresAt).Scan(
		&out.ID, &out.ShortCode, &out.OriginalURL, &out.UserID, &out.CreatedAt, &out.ExpiresAt, &out.IsActive,
	)
	if err != nil {
		if isPgUniqueViolation(err) {
			return nil, ErrShortCodeConflict
		}
		return nil, fmt.Errorf("insert url: %w", err)
	}
	return &out, nil
}

func (s *pgxURLStore) FindByCode(ctx context.Context, shortCode string) (*URLRecord, error) {
	query := `
		SELECT id, short_code, original_url, user_id, created_at, expires_at, is_active
		FROM urls WHERE short_code = $1
	`
	var rec URLRecord
	err := s.pool.QueryRow(ctx, query, shortCode).Scan(
		&rec.ID, &rec.ShortCode, &rec.OriginalURL, &rec.UserID, &rec.CreatedAt, &rec.ExpiresAt, &rec.IsActive,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrURLNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find url by code: %w", err)
	}
	return &rec, nil
}

func (s *pgxURLStore) FindByUserID(ctx context.Context, userID, afterID string, limit int) ([]*URLRecord, string, error) {
	var rows []URLRecord
	var err error
	var nextCursor string

	if afterID == "" {
		query := `
			SELECT id, short_code, original_url, user_id, created_at, expires_at, is_active
			FROM urls WHERE user_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2
		`
		r, err := s.pool.Query(ctx, query, userID, limit+1)
		if err != nil {
			return nil, "", fmt.Errorf("find urls by user: %w", err)
		}
		rows, err = pgx.CollectRows(r, pgx.RowToStructByName[URLRecord])
	} else {
		query := `
			SELECT id, short_code, original_url, user_id, created_at, expires_at, is_active
			FROM urls WHERE user_id = $1
			  AND (created_at, id) < (SELECT created_at, id FROM urls WHERE id = $2)
			ORDER BY created_at DESC, id DESC LIMIT $3
		`
		r, err := s.pool.Query(ctx, query, userID, afterID, limit+1)
		if err != nil {
			return nil, "", fmt.Errorf("find urls by user: %w", err)
		}
		rows, err = pgx.CollectRows(r, pgx.RowToStructByName[URLRecord])
	}
	if err != nil {
		return nil, "", fmt.Errorf("find urls by user: %w", err)
	}

	if len(rows) > limit {
		nextCursor = rows[limit].ID
		rows = rows[:limit]
	}

	ptrs := make([]*URLRecord, len(rows))
	for i := range rows {
		ptrs[i] = &rows[i]
	}
	return ptrs, nextCursor, nil
}

func (s *pgxURLStore) Deactivate(ctx context.Context, shortCode, userID string) error {
	query := `UPDATE urls SET is_active = false WHERE short_code = $1 RETURNING user_id`
	var ownerID string
	err := s.pool.QueryRow(ctx, query, shortCode).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrURLNotFound
	}
	if err != nil {
		return fmt.Errorf("deactivate url: %w", err)
	}
	if ownerID != userID {
		return ErrNotOwner
	}
	return nil
}

type OutboxRepository interface {
	InsertWithURL(ctx context.Context, tx pgx.Tx, urlRec *URLRecord, outboxRec *OutboxRecord) error
	InsertEvent(ctx context.Context, tx pgx.Tx, outboxRec *OutboxRecord) error
	FetchUnpublished(ctx context.Context, limit int) ([]*OutboxRecord, error)
	MarkPublished(ctx context.Context, id string) error
}

type pgxOutboxStore struct {
	pool *pgxpool.Pool
}

func NewOutboxStore(pool *pgxpool.Pool) OutboxRepository {
	return &pgxOutboxStore{pool: pool}
}

func (s *pgxOutboxStore) InsertWithURL(ctx context.Context, tx pgx.Tx, urlRec *URLRecord, outboxRec *OutboxRecord) error {
	row := tx.QueryRow(ctx,
		`INSERT INTO urls (short_code, original_url, user_id, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, short_code, original_url, user_id, created_at, expires_at, is_active`,
		urlRec.ShortCode, urlRec.OriginalURL, urlRec.UserID, urlRec.ExpiresAt,
	)
	if err := row.Scan(&urlRec.ID, &urlRec.ShortCode, &urlRec.OriginalURL,
		&urlRec.UserID, &urlRec.CreatedAt, &urlRec.ExpiresAt, &urlRec.IsActive); err != nil {
		if isPgUniqueViolation(err) {
			return ErrShortCodeConflict
		}
		return fmt.Errorf("insert url in tx: %w", err)
	}
	return s.InsertEvent(ctx, tx, outboxRec)
}

func (s *pgxOutboxStore) InsertEvent(ctx context.Context, tx pgx.Tx, outboxRec *OutboxRecord) error {
	row := tx.QueryRow(ctx,
		`INSERT INTO outbox (event_type, payload) VALUES ($1, $2) RETURNING id, created_at`,
		outboxRec.EventType, outboxRec.Payload,
	)
	if err := row.Scan(&outboxRec.ID, &outboxRec.CreatedAt); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func (s *pgxOutboxStore) FetchUnpublished(ctx context.Context, limit int) ([]*OutboxRecord, error) {
	query := `
		SELECT id, event_type, payload, created_at
		FROM outbox WHERE published_at IS NULL
		ORDER BY created_at ASC LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := s.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch unpublished outbox: %w", err)
	}
	recs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*OutboxRecord, error) {
		var r OutboxRecord
		if err := row.Scan(&r.ID, &r.EventType, &r.Payload, &r.CreatedAt); err != nil {
			return nil, err
		}
		return &r, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetch unpublished outbox: %w", err)
	}
	return recs, nil
}

func (s *pgxOutboxStore) MarkPublished(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	return nil
}

func isPgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return strings.Contains(err.Error(), "duplicate key")
}
