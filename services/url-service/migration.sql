-- urls table + outbox table + all indexes
CREATE TABLE IF NOT EXISTS urls (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code   VARCHAR(10)  UNIQUE NOT NULL,
    original_url TEXT         NOT NULL,
    user_id      UUID         NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ  NULL,
    is_active    BOOLEAN      NOT NULL DEFAULT true
);

CREATE INDEX IF NOT EXISTS idx_urls_short_code ON urls (short_code);
CREATE INDEX IF NOT EXISTS idx_urls_user_id_created ON urls (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outbox (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   TEXT         NOT NULL,
    payload      JSONB        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ  NULL
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox(created_at ASC)
    WHERE published_at IS NULL;
