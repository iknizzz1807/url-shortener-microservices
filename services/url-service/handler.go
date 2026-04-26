package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ikniz/url-shortener/shared/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HTTPHandler struct {
	pool         *pgxpool.Pool // Required to start database transactions
	store        URLStore
	outboxStore  OutboxStore // Required for the outbox
	cache        Cache
	codegen      ShortCodeGenerator
	shortURLBase string
}

func NewHTTPHandler(pool *pgxpool.Pool, store URLStore, outboxStore OutboxStore, cache Cache, codegen ShortCodeGenerator, shortURLBase string) *HTTPHandler {
	return &HTTPHandler{
		pool:         pool,
		store:        store,
		outboxStore:  outboxStore,
		cache:        cache,
		codegen:      codegen,
		shortURLBase: shortURLBase,
	}
}

// --- Helper Structs ---

type ShortenRequest struct {
	URL            string `json:"url"`
	ExpiresInHours int    `json:"expires_in_hours"`
}

type ShortenResponse struct {
	ShortCode   string    `json:"short_code"`
	ShortURL    string    `json:"short_url"`
	OriginalURL string    `json:"original_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ListURLsResponse struct {
	URLs       []URLRecord `json:"urls"`
	NextCursor string      `json:"next_cursor"`
	HasMore    bool        `json:"has_more"`
}

// --- Helper HTTP Writers ---

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// --- The Handler ---

func (h *HTTPHandler) HandleShorten(w http.ResponseWriter, r *http.Request) {
	// 3. Extract user claims
	// (Assuming your JWT middleware stores a map of claims in context)
	claims, ok := r.Context().Value("claims").(map[string]any)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}
	userID, _ := claims["sub"].(string)
	userEmail, _ := claims["email"].(string)

	if userID == "" {
		writeError(w, http.StatusUnauthorized, "invalid user token")
		return
	}

	// 1. Parse JSON body
	var req ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 2. Validate URL (from validate.go)
	if err := ValidateURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Normalize expires_in
	if req.ExpiresInHours <= 0 || req.ExpiresInHours > 24*365 { // reject negative or absurdly long
		req.ExpiresInHours = 24 // 1 day default
	}
	expiresAt := time.Now().Add(time.Duration(req.ExpiresInHours) * time.Hour)

	var shortCode string
	var success bool

	// 6. Collision retry (max 3 retries)
	for attempt := 0; attempt < 3; attempt++ {
		// 4. Generate short code
		shortCode = h.codegen.Generate()

		// 5. BEGIN tx -> Insert URL + Insert outbox -> COMMIT
		// pgx.BeginFunc automatically handles Rollback on error and Commit on success
		err := pgx.BeginFunc(r.Context(), h.pool, func(tx pgx.Tx) error {

			// 5a) INSERT URL
			ur := &URLRecord{
				ID:          uuid.NewString(), // postgres uses standard UUID format
				ShortCode:   shortCode,
				OriginalURL: req.URL,
				UserID:      userID,
				CreatedAt:   time.Now(),
				ExpiresAt:   &expiresAt,
				IsActive:    true,
			}
			if err := h.store.Insert(r.Context(), tx, ur); err != nil {
				return err
			}

			// 5b) INSERT OUTBOX (Same transaction) using shared events
			event := events.URLCreatedEvent{
				BaseEvent:   events.NewBaseEvent(events.EventTypeURLCreated, ""), // no correlation ID passed for now
				ShortCode:   shortCode,
				OriginalURL: req.URL,
				UserID:      userID,
				UserEmail:   userEmail,
				ExpiresAt:   &expiresAt,
			}
			payload, _ := json.Marshal(event)

			outbox := &OutboxRecord{
				ID:        uuid.NewString(),
				EventType: string(events.EventTypeURLCreated),
				Payload:   payload,
				CreatedAt: time.Now(),
			}
			if err := h.outboxStore.InsertEvent(r.Context(), tx, outbox); err != nil {
				return err
			}

			return nil // Commits!
		})

		if err == nil {
			success = true
			break // Successfully inserted both, exit retry loop
		}

		// If error is a unique constraint violation (collision)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // 23505 = UNIQUE_VIOLATION
			time.Sleep(time.Duration(attempt*50) * time.Millisecond) // exponential backoff
			continue
		}

		// For any other database error, exit immediately
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	if !success {
		writeError(w, http.StatusConflict, "unable to generate unique short code, try again later")
		return
	}

	// (Optional: Cache the new URL in the background to speed up the very first read)
	go func() {
		cached := &CachedURL{
			OriginalURL: req.URL,
			ExpiresAt:   &expiresAt,
			IsActive:    true,
		}
		ttl := time.Duration(req.ExpiresInHours) * time.Hour
		_ = h.cache.Set(context.Background(), shortCode, cached, ttl)
	}()

	// 7. Return 201 Created
	writeJSON(w, http.StatusCreated, ShortenResponse{
		ShortCode:   shortCode,
		ShortURL:    h.shortURLBase + "/" + shortCode,
		OriginalURL: req.URL,
		ExpiresAt:   expiresAt,
	})
}

func (h *HTTPHandler) HandleRedirect(w http.ResponseWriter, r *http.Request) {
	shortcode := r.PathValue("code")
	if shortcode == "" {
		writeError(w, http.StatusBadRequest, "missing short code")
		return
	}

	// Check cache first (with a small timeout to avoid blocking)
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
	defer cancel()

	cached, err := h.cache.Get(ctx, shortcode)
	if err == nil && cached != nil {
		// Cache HIT

		// Validate cached entry
		if !cached.IsActive {
			writeError(w, http.StatusGone, "URL has been deactivated")
			return
		}
		if cached.ExpiresAt != nil && time.Now().After(*cached.ExpiresAt) {
			writeError(w, http.StatusGone, "URL has expired")
			return
		}

		// Hash IP from RemoteAddr
		ipHash := hashIP(r.RemoteAddr)

		// Write analytics event (fire and forget)
		go h.writeAnalyticsEvent(r, shortcode, ipHash)

		// Redirect
		http.Redirect(w, r, cached.OriginalURL, http.StatusPermanentRedirect)
		return
	}

	// Cache MISS or error → fetch from DB
	urlRecord, err := h.store.FindByCode(r.Context(), shortcode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "URL not found")
		} else {
			writeError(w, http.StatusInternalServerError, "database error")
		}
		return
	}

	// Validate DB record
	if !urlRecord.IsActive {
		writeError(w, http.StatusGone, "URL has been deactivated")
		return
	}
	if urlRecord.ExpiresAt != nil && time.Now().After(*urlRecord.ExpiresAt) {
		writeError(w, http.StatusGone, "URL has expired")
		return
	}

	// Write analytics event (fire and forget)
	go h.writeAnalyticsEvent(r, shortcode, hashIP(r.RemoteAddr))

	// Cache the result for future requests (fire and forget)
	go func() {
		ttl := time.Hour // Default TTL if no expiry
		if urlRecord.ExpiresAt != nil {
			ttl = time.Until(*urlRecord.ExpiresAt)
			if ttl < 0 {
				ttl = 0
			}
		}

		cached := &CachedURL{
			OriginalURL: urlRecord.OriginalURL,
			ExpiresAt:   urlRecord.ExpiresAt,
			IsActive:    urlRecord.IsActive,
		}
		_ = h.cache.Set(context.Background(), shortcode, cached, ttl)
	}()

	// Redirect
	http.Redirect(w, r, urlRecord.OriginalURL, http.StatusPermanentRedirect)
}

func (h *HTTPHandler) HandleGetUrls(w http.ResponseWriter, r *http.Request) {
	// JWT required, extract user_id
	claims, ok := r.Context().Value("claims").(map[string]any)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}
	userID, _ := claims["sub"].(string)

	// Parse query params
	var afterID string
	if val := r.URL.Query().Get("after"); val != "" {
		afterID = val
	}
	limit := 20
	if val := r.URL.Query().Get("limit"); val != "" {
		parsed, err := strconv.Atoi(val)
		if err == nil {
			limit = int(math.Max(math.Min(float64(parsed), 100), 1))
		}
	}

	// Find by user_id cursor pagination
	urls, err := h.store.FindByUserID(r.Context(), userID, afterID, limit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, ListURLsResponse{
				URLs:       []URLRecord{},
				NextCursor: "",
				HasMore:    false,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	// Determine next cursor and hasMore
	var nextCursor string
	hasMore := len(urls) > limit

	if hasMore {
		// Slice off the extra record we fetched
		urls = urls[:limit]
		nextCursor = urls[len(urls)-1].ID
	}

	writeJSON(w, http.StatusOK, ListURLsResponse{
		URLs:       urls,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

func (h *HTTPHandler) HandleDeactivateUrl(w http.ResponseWriter, r *http.Request) {
	shortcode := r.PathValue("code")
	if shortcode == "" {
		writeError(w, http.StatusBadRequest, "missing short code")
		return
	}

	// 1. JWT required, extract user_id & email
	claims, ok := r.Context().Value("claims").(map[string]any)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}
	userID, _ := claims["sub"].(string)
	userEmail, _ := claims["email"].(string)

	if userID == "" {
		writeError(w, http.StatusUnauthorized, "invalid user token")
		return
	}

	// 2. BEGIN tx -> Deactivate + Insert outbox -> COMMIT
	err := pgx.BeginFunc(r.Context(), h.pool, func(tx pgx.Tx) error {
		// Deactivate URL (checks ownership implicitly via the WHERE clause)
		if err := h.store.Deactivate(r.Context(), tx, shortcode, userID); err != nil {
			return err
		}

		// Create the URLDeletedEvent
		event := events.URLDeletedEvent{
			BaseEvent: events.NewBaseEvent(events.EventTypeURLDeleted, ""),
			ShortCode: shortcode,
			UserID:    userID,
			UserEmail: userEmail,
		}
		payload, _ := json.Marshal(event)

		outbox := &OutboxRecord{
			ID:        uuid.NewString(),
			EventType: string(events.EventTypeURLDeleted),
			Payload:   payload,
			CreatedAt: time.Now(),
		}

		// Insert the event into the outbox in the same transaction
		return h.outboxStore.InsertEvent(r.Context(), tx, outbox)
	})

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// user_id didn't match, or shortcode doesn't exist/is already inactive -> 403
			writeError(w, http.StatusForbidden, "URL not found or you do not have permission")
		} else {
			writeError(w, http.StatusInternalServerError, "database error")
		}
		return
	}

	// 3. Redis Delete (invalidate cache immediately after successful commit)
	_ = h.cache.Delete(context.Background(), shortcode)

	// 4. Return 204 No Content
	w.WriteHeader(http.StatusNoContent)
}

// --- New Helper Method ---

func (h *HTTPHandler) writeAnalyticsEvent(r *http.Request, shortCode, ipHash string) {
	// Get User-Agent and Referer from request
	userAgent := r.Header.Get("User-Agent")
	referrer := r.Header.Get("Referer")

	// Create event (using the shared events package)
	event := events.URLClickedEvent{
		BaseEvent: events.NewBaseEvent(events.EventTypeURLClicked, ""),
		ShortCode: shortCode,
		IPHash:    ipHash,
		UserAgent: userAgent,
		Referer:   referrer,
		ClickedAt: time.Now(),
	}

	payload, _ := json.Marshal(event)

	outbox := &OutboxRecord{
		ID:        uuid.NewString(),
		EventType: string(events.EventTypeURLClicked),
		Payload:   payload,
		CreatedAt: time.Now(),
	}

	// We can ignore the error here - this is an analytics event,
	// the system should still function if analytics DB is down.
	// But for robustness, we could log it.
	_ = h.outboxStore.InsertEvent(context.Background(), nil, outbox)
}

// Hash IP helper - SHA256 of the IP address string
func hashIP(ipAddr string) string {
	// Remove port if present (e.g., "[IP_ADDRESS]:54321" -> "[IP_ADDRESS]")
	// This ensures [IP_ADDRESS] from different ports hashes to the same value
	host := ipAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}

	data := []byte(host)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]) // Return hex string for easy storage/comparison
}
