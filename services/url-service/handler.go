package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/ikniz/url-shortener/shared/auth"
	"github.com/ikniz/url-shortener/shared/events"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool        *pgxpool.Pool
	urlStore    URLRepository
	outboxStore OutboxRepository
	cache       *RedisCache
	codeGen     *ShortCodeGenerator
	cfg         *Config
	log         *slog.Logger
}

func NewHandler(pool *pgxpool.Pool, urlStore URLRepository, outboxStore OutboxRepository, cache *RedisCache, codeGen *ShortCodeGenerator, cfg *Config, log *slog.Logger) *Handler {
	return &Handler{
		pool:        pool,
		urlStore:    urlStore,
		outboxStore: outboxStore,
		cache:       cache,
		codeGen:     codeGen,
		cfg:         cfg,
		log:         log,
	}
}

type shortenRequest struct {
	URL       string     `json:"url"`
	CustomCode *string    `json:"custom_code,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type shortenResponse struct {
	ShortCode   string  `json:"short_code"`
	ShortURL    string  `json:"short_url"`
	OriginalURL string  `json:"original_url"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

func (h *Handler) Shorten(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if r.Header.Get("Content-Type") != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Warn("malformed JSON", "path", r.URL.Path)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateURL(req.URL); err != nil {
		writeFieldError(w, http.StatusUnprocessableEntity, err.Error(), "url")
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		if req.ExpiresAt.Before(time.Now()) {
			writeFieldError(w, http.StatusUnprocessableEntity, "expires_at must be in the future", "expires_at")
			return
		}
		expiresAt = req.ExpiresAt
	}

	shortCode := ""
	if req.CustomCode != nil {
		if err := validateCustomCode(*req.CustomCode); err != nil {
			writeFieldError(w, http.StatusUnprocessableEntity, err.Error(), "custom_code")
			return
		}
		shortCode = *req.CustomCode
	}

	var urlRec *URLRecord
	var insertErr error
	const maxRetries = 5

	for attempt := 0; attempt < maxRetries; attempt++ {
		if shortCode == "" {
			shortCode = h.codeGen.Generate()
		}

		urlRec = &URLRecord{
			ShortCode:   shortCode,
			OriginalURL: req.URL,
			UserID:      claims.Sub,
			ExpiresAt:   expiresAt,
		}

		event := &events.URLCreatedEvent{
			BaseEvent: events.BaseEvent{
				EventType:     string(events.EventTypeURLCreated),
				OccurredAt:    time.Now().UTC(),
				EventID:       newUUID(),
				CorrelationID: correlationIDFromRequest(r),
			},
			ShortCode:   shortCode,
			OriginalURL: req.URL,
			UserID:      claims.Sub,
			UserEmail:   claims.Email,
			ExpiresAt:   expiresAt,
		}
		payload, err := json.Marshal(event)
		if err != nil {
			h.log.Error("marshal event", "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		outboxRec := &OutboxRecord{
			EventType: string(events.EventTypeURLCreated),
			Payload:   payload,
		}

		tx, err := h.pool.Begin(r.Context())
		if err != nil {
			h.log.Error("begin tx", "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		insertErr = h.outboxStore.InsertWithURL(r.Context(), tx, urlRec, outboxRec)
		if insertErr == nil {
			if err := tx.Commit(r.Context()); err != nil {
				tx.Rollback(r.Context())
				h.log.Error("commit tx", "error", err)
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			break
		}

		tx.Rollback(r.Context())

		if errors.Is(insertErr, ErrShortCodeConflict) {
			if req.CustomCode != nil {
				writeFieldError(w, http.StatusConflict, "short code already taken", "custom_code")
				return
			}
			shortCode = ""
			continue
		}

		h.log.Error("insert url+outbox", "error", insertErr, "attempt", attempt)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if insertErr != nil {
		h.log.Error("short code collision exhausted", "attempts", maxRetries)
		writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable, try again")
		return
	}

	var expiresAtStr *string
	if urlRec.ExpiresAt != nil {
		s := urlRec.ExpiresAt.Format(time.RFC3339)
		expiresAtStr = &s
	}
	writeJSON(w, http.StatusCreated, shortenResponse{
		ShortCode:   urlRec.ShortCode,
		ShortURL:    h.cfg.ShortURLBase + "/" + urlRec.ShortCode,
		OriginalURL: urlRec.OriginalURL,
		ExpiresAt:   expiresAtStr,
	})
}

func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	cacheCtx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
	defer cancel()
	cached, hit := h.cache.Get(cacheCtx, code)
	if hit {
		if !cached.IsActive {
			writeError(w, http.StatusGone, "url has been deactivated")
			return
		}
		if cached.ExpiresAt != nil && cached.ExpiresAt.Before(time.Now()) {
			writeError(w, http.StatusGone, "url has expired")
			return
		}
		http.Redirect(w, r, cached.OriginalURL, http.StatusMovedPermanently)
		return
	}

	urlRec, err := h.urlStore.FindByCode(r.Context(), code)
	if errors.Is(err, ErrURLNotFound) {
		writeError(w, http.StatusNotFound, "short url not found")
		return
	}
	if err != nil {
		h.log.Error("find url by code", "code", code, "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if !urlRec.IsActive {
		writeError(w, http.StatusGone, "url has been deactivated")
		return
	}
	if urlRec.ExpiresAt != nil && urlRec.ExpiresAt.Before(time.Now()) {
		writeError(w, http.StatusGone, "url has expired")
		return
	}

	setCtx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
	defer cancel()
	h.cache.Set(setCtx, code, &CachedURL{
		OriginalURL: urlRec.OriginalURL,
		ExpiresAt:   urlRec.ExpiresAt,
		IsActive:    urlRec.IsActive,
	}, urlRec.ExpiresAt)

	clickEvent := &events.URLClickedEvent{
		BaseEvent: events.BaseEvent{
			EventType:     string(events.EventTypeURLClicked),
			OccurredAt:    time.Now().UTC(),
			EventID:       newUUID(),
			CorrelationID: correlationIDFromRequest(r),
		},
		ShortCode: code,
		IPHash:    hashIP(r.RemoteAddr),
		UserAgent: r.Header.Get("User-Agent"),
		Referer:   r.Header.Get("Referer"),
		ClickedAt: time.Now().UTC(),
	}
	payload, _ := json.Marshal(clickEvent)
	tx, err := h.pool.Begin(r.Context())
	if err == nil {
		outboxRec := &OutboxRecord{EventType: string(events.EventTypeURLClicked), Payload: payload}
		if err := h.outboxStore.InsertEvent(r.Context(), tx, outboxRec); err != nil {
			tx.Rollback(r.Context())
			h.log.Warn("insert click event outbox", "code", code, "error", err)
		} else {
			if err := tx.Commit(r.Context()); err != nil {
				h.log.Warn("commit click tx", "error", err)
			}
		}
	} else {
		h.log.Warn("begin click tx", "error", err)
	}

	http.Redirect(w, r, urlRec.OriginalURL, http.StatusMovedPermanently)
}

type listResponse struct {
	URLs       []urlItem `json:"urls"`
	NextCursor *string  `json:"next_cursor"`
}

type urlItem struct {
	ShortCode   string  `json:"short_code"`
	ShortURL    string  `json:"short_url"`
	OriginalURL string  `json:"original_url"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

func (h *Handler) ListURLs(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	after := r.URL.Query().Get("after")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n := parseIntSafe(l); n > 0 && n <= 50 {
			limit = n
		}
	}

	recs, nextCursor, err := h.urlStore.FindByUserID(r.Context(), claims.Sub, after, limit)
	if err != nil {
		h.log.Error("find urls by user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	items := make([]urlItem, len(recs))
	for i, rec := range recs {
		item := urlItem{
			ShortCode:   rec.ShortCode,
			ShortURL:    h.cfg.ShortURLBase + "/" + rec.ShortCode,
			OriginalURL: rec.OriginalURL,
			CreatedAt:   rec.CreatedAt.Format(time.RFC3339),
		}
		if rec.ExpiresAt != nil {
			s := rec.ExpiresAt.Format(time.RFC3339)
			item.ExpiresAt = &s
		}
		items[i] = item
	}

	var cursor *string
	if nextCursor != "" {
		cursor = &nextCursor
	}

	writeJSON(w, http.StatusOK, listResponse{URLs: items, NextCursor: cursor})
}

func (h *Handler) DeleteURL(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	code := r.PathValue("code")
	if code == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	urlRec, err := h.urlStore.FindByCode(r.Context(), code)
	if errors.Is(err, ErrURLNotFound) {
		tx.Rollback(r.Context())
		writeError(w, http.StatusNotFound, "short url not found")
		return
	}
	if err != nil {
		tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if urlRec.UserID != claims.Sub {
		tx.Rollback(r.Context())
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if err := h.urlStore.Deactivate(r.Context(), code, claims.Sub); err != nil {
		tx.Rollback(r.Context())
		if errors.Is(err, ErrURLNotFound) {
			writeError(w, http.StatusNotFound, "short url not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	event := &events.URLDeletedEvent{
		BaseEvent: events.BaseEvent{
			EventType:     string(events.EventTypeURLDeleted),
			OccurredAt:    time.Now().UTC(),
			EventID:       newUUID(),
			CorrelationID: correlationIDFromRequest(r),
		},
		ShortCode: code,
		UserID:    claims.Sub,
		UserEmail: claims.Email,
	}
	payload, _ := json.Marshal(event)
	outboxRec := &OutboxRecord{EventType: string(events.EventTypeURLDeleted), Payload: payload}
	if err := h.outboxStore.InsertEvent(r.Context(), tx, outboxRec); err != nil {
		tx.Rollback(r.Context())
		h.log.Error("insert delete event outbox", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		h.log.Error("commit delete tx", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	delCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	h.cache.Del(delCtx, code)

	w.WriteHeader(http.StatusNoContent)
}

func parseIntSafe(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("url must not be empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("url must be a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}

func validateCustomCode(code string) error {
	if len(code) < 3 || len(code) > 10 {
		return fmt.Errorf("custom code must be 3-10 characters")
	}
	for _, c := range code {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return fmt.Errorf("custom code must be alphanumeric")
		}
	}
	return nil
}

var ipHashSalt = os.Getenv("IP_HASH_SALT")

func hashIP(remoteAddr string) string {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}
	h := sha256.Sum256([]byte(ip + ipHashSalt))
	return fmt.Sprintf("%x", h)
}

func correlationIDFromRequest(r *http.Request) string {
	id := r.Header.Get("X-Correlation-ID")
	if id == "" {
		return newUUID()
	}
	return id
}
