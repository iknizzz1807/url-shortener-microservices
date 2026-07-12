# Phân Tích Chi Tiết URL Service — Microservice Rút Gọn URL

> **Tác giả:** Agent AI  
> **Dự án:** url-shortener-microservices  
> **Ngày:** 2026-07-11  
> **Phiên bản phân tích:** v1.0  
> **Tổng số dòng code phân tích:** ~1.500 dòng Go + SQL

---

## Mục Lục

1. [Tổng Quan Kiến Trúc](#1-tổng-quan-kiến-trúc)
2. [Cấu Hình (config.go)](#2-cấu-hình-configgo)
3. [Kết Nối Database (db.go)](#3-kết-nối-database-dbgo)
4. [Kết Nối Redis (redis.go)](#4-kết-nối-redis-redisgo)
5. [Kết Nối RabbitMQ (rabbitmq.go)](#5-kết-nối-rabbitmq-rabbitmqgo)
6. [Schema Database (migration.sql)](#6-schema-database-migrationsql)
7. [Base62 Encoding (base62.go)](#7-base62-encoding-base62go)
8. [Sinh Mã Short Code (codegen.go)](#8-sinh-mã-short-code-codegengo)
9. [Validation URL (validate.go)](#9-validation-url-validatego)
10. [Error Types & HTTP Mapping (errors.go)](#10-error-types--http-mapping-errorsgo)
11. [URL Store Layer (store.go)](#11-url-store-layer-storego)
12. [Outbox Store Layer (outbox_store.go)](#12-outbox-store-layer-outbox_storego)
13. [Cache Layer (cache.go)](#13-cache-layer-cachego)
14. [Publisher Layer (publisher.go)](#14-publisher-layer-publishergo)
15. [Outbox Coordinator (outbox.go)](#15-outbox-coordinator-outboxgo)
16. [Service Layer (service.go)](#16-service-layer-servicego)
17. [HTTP Handler Layer (handler.go)](#17-http-handler-layer-handlergo)
18. [Entry Point (main.go)](#18-entry-point-maingo)
19. [Tổng Kết Luồng Hoạt Động](#19-tổng-kết-luồng-hoạt-động)
20. [Phân Tích Bảo Mật & Độ Tin Cậy](#20-phân-tích-bảo-mật--độ-tin-cậy)

---

## 1. Tổng Quan Kiến Trúc

URL Service là một microservice Go thuần (không framework) chịu trách nhiệm xử lý toàn bộ vòng đời của URL rút gọn: tạo mới, redirect, liệt kê, và vô hiệu hóa. Service này nằm trong hệ thống `url-shortener-microservices` gồm nhiều service nhỏ giao tiếp qua RabbitMQ.

### 1.1. Các Thành Phần Chính

| Thành phần | File | Vai trò |
|---|---|---|
| `Config` | `config.go` | Load biến môi trường, kiểm tra required fields |
| `NewDBPool` | `db.go` | Khởi tạo connection pool PostgreSQL (pgxpool) |
| `NewRedisClient` | `redis.go` | Khởi tạo Redis client (non-fatal khi lỗi) |
| `NewRabbitMQConn` | `rabbitmq.go` | Kết nối RabbitMQ với exponential backoff |
| `pgxURLStore` | `store.go` | CRUD URLs trên PostgreSQL |
| `pgxOutboxStore` | `outbox_store.go` | CRUD outbox events trên PostgreSQL |
| `redisCache` | `cache.go` | Cache layer dùng Redis |
| `cryptoRandGenerator` | `codegen.go` | Sinh short code ngẫu nhiên bằng crypto/rand |
| `Encode/Decode` | `base62.go` | Mã hóa Base62 |
| `OutboxCoordinator` | `outbox.go` | Worker pool xử lý outbox events |
| `amqpPublisher` | `publisher.go` | Publish message lên RabbitMQ |
| `URLService` | `service.go` | Business logic layer |
| `HTTPHandler` | `handler.go` | HTTP handlers, routing, auth middleware |
| `main` | `main.go` | Dependency injection, graceful shutdown |

### 1.2. Nguyên Lý Thiết Kế Cốt Lõi

1. **Transactional Outbox Pattern**: Đảm bảo tính nhất quán giữa database state và message publishing. Mỗi mutation (create URL, deactivate URL) đều ghi event vào bảng `outbox` trong cùng một transaction với mutation chính. Một coordinator riêng biệt poll bảng outbox và publish lên RabbitMQ.

2. **Cache-Aside Pattern**: Redis được dùng làm L1 cache. Cache được populate sau khi write (không phải trong transaction) và được đọc trước khi query DB. Cache miss dẫn đến fallback về DB.

3. **Fail-Open cho Redis**: Redis không phải là critical path. Nếu Redis chết, service vẫn hoạt động bằng cách query trực tiếp DB.

4. **Cryptographic Randomness cho Short Code**: Dùng `crypto/rand` thay vì `math/rand` để tránh predictability.

5. **Graceful Shutdown**: Bắt SIGTERM/SIGINT, drain connections, shutdown HTTP server.

---

## 2. Cấu Hình (config.go)

### 2.1. Cấu Trúc Config

```go
type Config struct {
    DatabaseURL  string
    RedisURL     string
    RabbitMQURL  string
    JWTSecret    string
    ShortURLBase string
    IPHashSalt   string
    Port         string
    ServiceName  string
}
```

**Phân tích từng field:**

| Field | Bắt buộc? | Mặc định | Mục đích |
|---|---|---|---|
| `DatabaseURL` | **Fatal** nếu thiếu | — | DSN PostgreSQL: `postgres://user:pass@host:5432/db` |
| `RedisURL` | **Fatal** nếu thiếu | — | DSN Redis: `redis://host:6379/0` |
| `RabbitMQURL` | **Fatal** nếu thiếu | — | AMQP URL: `amqp://user:pass@host:5672/` |
| `JWTSecret` | **Fatal** nếu thiếu | — | Secret key xác thực JWT |
| `ShortURLBase` | Optional | `http://localhost:8080` | Base URL cho short URL response |
| `IPHashSalt` | Optional | `default-salt` | Salt cho SHA-256 hash IP |
| `Port` | Optional | `8080` | Cổng HTTP server |
| `ServiceName` | Hardcode | `url-service` | Dùng cho logging |

**Phân tích thiết kế:**
- `LoadConfig()` gọi `os.Exit(1)` gián tiếp qua `main()` — nó trả về error, main quyết định exit.
- `envOrDefault()` pattern đơn giản, không dùng library config nào (Viper, envconfig, v.v.).
- `ShortURLBase` có fallback `http://localhost:8080` — phù hợp cho development, cần override trong production.
- `IPHashSalt` mặc định `default-salt` — **cảnh báo bảo mật**: cần override trong production để tránh rainbow table attack.
- `ServiceName` hardcode — không linh hoạt cho multi-environment deployment.

---

## 3. Kết Nối Database (db.go)

### 3.1. Chi Tiết Implementation

```go
func NewDBPool(ctx context.Context, databaseURL string, log *slog.Logger) (*pgxpool.Pool, error) {
    cfg, err := pgxpool.ParseConfig(databaseURL)
    // ...
    cfg.MaxConns = 10
    cfg.MinConns = 2
    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    // ...
    pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    if err := pool.Ping(pingCtx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("ping db: %w", err)
    }
    // ...
}
```

### 3.1. Pool Configuration

| Tham số | Giá trị | Ý nghĩa |
|---|---|---|
| `MaxConns` | 10 | Số kết nối tối đa tới PostgreSQL |
| `MinConns` | 2 | Số kết nối tối thiểu duy trì trong pool |

**Phân tích:**
- `MaxConns = 10` là con số hợp lý cho một microservice. Với 3 worker outbox + HTTP handler goroutines, 10 connections đủ để tránh contention mà không gây áp lực lên database.
- `MinConns = 2` giữ 2 kết nối luôn sẵn sàng, tránh cold start khi có request đột biến.
- `pgxpool.NewWithConfig` được dùng thay vì `pgxpool.Connect` để có control flow rõ ràng hơn.
- Timeout ping là 10 giây — nếu DB không reachable trong 10s, service crash (fatal).

### 3.2. Error Handling

```go
if err := pool.Ping(pingCtx); err != nil {
    pool.Close()
    return nil, fmt.Errorf("ping db: %w", err)
}
```

- `pool.Close()` được gọi trước khi return error để tránh rò rỉ tài nguyên.
- Error wrapping dùng `%w` để caller có thể dùng `errors.Is`/`errors.As`.
- Không có retry logic cho DB connection — đây là **fail-fast** design. Nếu DB không reachable, service crash và orchestration tool (Kubernetes, Docker Compose) sẽ restart.

---

## 4. Kết Nối Redis (redis.go)

### 4.1. Chi Tiết Implementation

```go
func NewRedisClient(ctx context.Context, redisURL string, log *slog.Logger) (*redis.Client, bool) {
    opts, err := redis.ParseURL(redisURL)
    if err != nil {
        log.Warn("redis URL parse failed, cache disabled", "error", err)
        return redis.NewClient(&redis.Options{Addr: "localhost:6379"}), false
    }
    client := redis.NewClient(opts)
    pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
    defer cancel()
    if err := client.Ping(pingCtx).Err(); err != nil {
        log.Warn("redis unreachable on startup, cache will be disabled until available", "error", err)
        return client, false
    }
    log.Info("connected to Redis cache", "addr", opts.Addr)
    return client, true
}
```

### 4.1. Phân Tích Chi Tiết

**Điểm khác biệt quan trọng so với DB: Redis là NON-FATAL.**

| Khía cạnh | DB | Redis |
|---|---|---|
| Hành vi khi lỗi | `os.Exit(1)` | Warn log + tiếp tục |
| Retry | Không | Không |
| Graceful degradation | Không thể | Cache-Aside fallback về DB |

**Cơ chế fail-open:**
- Nếu `redis.ParseURL` fail → tạo client với `Addr: "localhost:6379"` (fallback address) + return `false`.
- Nếu ping fail → vẫn return client (có thể reconnect sau) + `false`.
- `main.go` kiểm tra `redisOK` và log warning.
- `redisCache.Get` bắt mọi lỗi Redis và trả về `nil, nil` — coi như cache miss, fallback về DB.

**Thiếu sót:** Không có reconnect mechanism. Nếu Redis die sau startup, client sẽ không tự động reconnect. Tuy nhiên, `go-redis` có built-in connection recovery ở transport level, nên khi Redis quay lại, các command mới sẽ tự động kết nối lại.

---

## 5. Kết Nối RabbitMQ (rabbitmq.go)

### 5.1. Chi Tiết Implementation

```go
const (
    exchangeName = "url-shortener"
    exchangeType = "topic"
)
```

**Exchange `url-shortener` kiểu `topic`:**
- Cho phép routing linh hoạt dựa trên routing key.
- Các consumer có thể subscribe với pattern như `url.created`, `url.deleted`, `url.clicked`, hoặc `url.#` để nhận tất cả.

### 5.2. Exponential Backoff

```go
backoff := time.Second
for attempt := 1; attempt <= maxAttempts; attempt++ {
    conn, err = amqp.DialConfig(amqpURL, config)
    if err == nil {
        break
    }
    // log warning
    select {
    case <-ctx.Done():
        return nil, fmt.Errorf("context cancelled during rabbitmq connect: %w", ctx.Err())
    case <-time.After(backoff):
    }
    backoff = min(backoff*2, 30*time.Second)
}
```

**Phân tích backoff:**
- Attempt 1: 1s
- Attempt 2: 2s
- Attempt 3: 4s
- Attempt 4: 8s
- Attempt 5: 16s
- Attempt 6-10: 30s (capped)
- Tổng thời gian tối đa: ~107s (gần 2 phút)

**Tham số AMQP Config:**
```go
config := amqp.Config{
    Properties: amqp.Table{
        "connection_name": "url-service",
    },
}
```
- `connection_name` giúp nhận diện connection trong RabbitMQ Management UI.

### 5.3. Exchange Declaration

```go
func declareExchange(ch *amqp.Channel) error {
    return ch.ExchangeDeclare(
        exchangeName, // "url-shortener"
        exchangeType, // "topic"
        true,         // durable — exchange tồn tại qua restart
        false,        // autoDelete — không tự xóa khi không còn queue bind
        false,        // internal — consumer có thể publish trực tiếp
        false,        // noWait — chờ server xác nhận
        nil,          // args
    )
}
```

**Phân tích:**
- `durable = true`: Exchange tồn tại qua RabbitMQ restart.
- `autoDelete = false`: Exchange không tự biến mất khi không còn queue nào bind.
- `internal = false`: Cho phép service publish message trực tiếp (không qua exchange khác).
- Kiểu `topic` cho phép routing key linh hoạt: `url.created`, `url.deleted`, `url.clicked`.

### 5.3. Graceful Close

```go
func (r *RabbitMQConn) Close() {
    if r.Channel != nil {
        r.Channel.Close()
    }
    if r.Conn != nil {
        r.Conn.Close()
    }
}
```

- Đóng channel trước, connection sau — đúng thứ tự AMQP protocol.
- Nil-check để tránh panic nếu connection chưa được thiết lập hoàn chỉnh.

---

## 6. Schema Database (migration.sql)

### 6.1. Bảng `urls`

```sql
CREATE TABLE IF NOT EXISTS urls (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code   VARCHAR(10)  UNIQUE NOT NULL,
    original_url TEXT         NOT NULL,
    user_id      UUID         NOT NULL,
    user_email   TEXT         NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ  NULL,
    is_active    BOOLEAN      NOT NULL DEFAULT true
);
```

**Phân tích từng cột:**

| Cột | Kiểu | Ràng buộc | Ý nghĩa |
|---|---|---|---|
| `id` | `UUID` | `PRIMARY KEY DEFAULT gen_random_uuid()` | Khóa chính, sinh tự động bằng `gen_random_uuid()` (PostgreSQL built-in) |
| `short_code` | `VARCHAR(10)` | `UNIQUE NOT NULL` | Mã rút gọn 7 ký tự, unique |
| `original_url` | `TEXT` | `NOT NULL` | URL gốc, TEXT vì URL có thể rất dài |
| `user_id` | `UUID` | `NOT NULL` | UUID của user sở hữu |
| `user_email` | `TEXT` | `NOT NULL DEFAULT ''` | Email user, có default empty string |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT now()` | Thời gian tạo, dùng timezone-aware |
| `expires_at` | `TIMESTAMPTZ` | `NULL` | NULL = không hết hạn |
| `is_active` | `BOOLEAN` | `NOT NULL DEFAULT true` | Soft-delete flag |

**Phân tích thiết kế schema:**

1. **`VARCHAR(10)` cho short_code**: Code thực tế chỉ dài 7 ký tự, nhưng VARCHAR(10) cho phép linh hoạt nếu cần tăng độ dài sau này mà không cần migration.

2. **`gen_random_uuid()`**: Dùng PostgreSQL built-in UUID generation thay vì sinh từ ứng dụng. Tuy nhiên, code Go lại tự sinh UUID bằng `uuid.NewString()` — có sự không nhất quán giữa schema default và application logic.

3. **`user_email TEXT NOT NULL DEFAULT ''`**: Có `ALTER TABLE ADD COLUMN IF NOT EXISTS` — đây là migration cho việc thêm column sau. Cho thấy schema đã tiến hóa qua thời gian.

4. **`expires_at TIMESTAMPTZ NULL`**: NULL = không hết hạn. Dùng `TIMESTAMPTZ` (timezone-aware) thay vì `TIMESTAMP` để tránh issues với timezone.

5. **`is_active`**: Soft-delete. Khi URL bị deactivate, record không bị xóa mà chỉ set `is_active = false`.

### 6.2. Indexes

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_urls_short_code ON urls(short_code);
CREATE INDEX IF NOT EXISTS idx_urls_user_id_created ON urls(user_id, created_at DESC);
```

**Phân tích index:**

1. **`idx_urls_short_code`** (UNIQUE B-tree):
   - Hỗ trợ `WHERE short_code = $1` trong `FindByCode`.
   - UNIQUE constraint đảm bảo không có hai URL cùng short_code.
   - `VARCHAR(10)` là key ngắn, index rất hiệu quả.

2. **`idx_urls_user_id_created`** (Composite B-tree):
   - Hỗ trợ `WHERE user_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`.
   - Cột `user_id` đứng trước vì equality, `created_at DESC` đứng sau cho ordering.
   - Index này hỗ trợ cursor-based pagination.

### 6.2. Bảng `outbox`

```sql
CREATE TABLE IF NOT EXISTS outbox (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   TEXT         NOT NULL,
    payload      JSONB        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ  NULL,
    published_at TIMESTAMPTZ  NULL
);
```

**Phân tích từng cột:**

| Cột | Kiểu | Ý nghĩa |
|---|---|---|
| `id` | `UUID` | Khóa chính, sinh tự động |
| `event_type` | `TEXT` | Routing key cho RabbitMQ, VD: `url.created`, `url.deleted`, `url.clicked` |
| `payload` | `JSONB` | Toàn bộ event struct serialized dạng JSON |
| `created_at` | `TIMESTAMPTZ` | Thời gian tạo event |
| `locked_until` | `TIMESTAMPTZ` | NULL = chưa bị claim; có giá trị = đang được xử lý bởi worker |
| `published_at` | `TIMESTAMPTZ` | NULL = chưa publish; có giá trị = đã publish thành công |

**Indexes cho outbox:**

```sql
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox(created_at ASC)
    WHERE published_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished_unlocked
    ON outbox(created_at ASC)
    WHERE published_at IS NULL AND locked_until IS NULL;
```

**Phân tích index:**

1. **`idx_outbox_unpublished`** (partial index):
   - Chỉ index các row có `published_at IS NULL`.
   - Partial index = nhỏ hơn, nhanh hơn full-table index.
   - `created_at ASC` cho phép lấy các event cũ nhất trước (FIFO).

2. **`idx_outbox_unpublished_unlocked`** (partial index chi tiết hơn):
   - Index các row chưa publish VÀ không bị lock.
   - Hỗ trợ query `WHERE published_at IS NULL AND (locked_until IS NULL OR locked_until < now())`.
   - Tuy nhiên, query thực tế trong `FetchUnpublished` dùng `FOR UPDATE SKIP LOCKED` nên index thứ hai có thể không cần thiết — `FOR UPDATE SKIP LOCKED` tự động bỏ qua các row đã bị lock.

**Lưu ý:** Có `ALTER TABLE outbox ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ NULL` — đây là migration cho việc thêm `locked_until` sau. Ban đầu schema có thể không có cột này.

---

## 7. Base62 Encoding (base62.go)

### 7.1. Bảng Chữ Cái

```go
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
```

**Phân tích bảng chữ cái:**
- 10 chữ số: `0-9`
- 26 chữ hoa: `A-Z`
- 26 chữ thường: `a-z`
- Tổng: 62 ký tự
- Thứ tự: số → chữ hoa → chữ thường (ASCII order)

**Tại sao không dùng Base64?**
- Base64 dùng `+`, `/`, `=` gây vấn đề trong URL.
- Base62 chỉ dùng alphanumeric, an toàn trong mọi context URL.

### 7.2. Thuật Toán Encode

```go
func Encode(n *big.Int) string {
    n = new(big.Int).Abs(n)
    limit := new(big.Int).Exp(big.NewInt(62), big.NewInt(shortCodeLength), nil)
    n.Mod(n, limit)
    buf := make([]byte, shortCodeLength)
    for i := shortCodeLength - 1; i >= 0; i-- {
        rem := new(big.Int).Mod(n, big.NewInt(62))
        n.Div(n, big.NewInt(62))
        buf[i] = base62Alphabet[rem.Int64()]
    }
    return string(buf)
}
```

**Phân tích từng bước:**

1. **`Abs(n)`**: Xử lý số âm (dù không mong đợi) bằng cách lấy giá trị tuyệt đối.
2. **`n.Mod(n, limit)`**: Giới hạn giá trị trong khoảng `[0, 62^7 - 1]`. Đây là bước critical — nếu không mod, số lớn hơn `62^7` sẽ encode ra hơn 7 ký tự.
3. **Vòng lặp từ phải sang trái**: Xây dựng string từ ký tự cuối cùng (least significant digit) lên đầu.
4. **`buf[i] = base62Alphabet[rem.Int64()]`**: Mỗi remainder (0-61) map trực tiếp vào bảng chữ cái.

### 7.3. Capacity Analysis

```
62^7 = 62 × 62 × 62 × 62 × 62 × 62 × 62
     = 3,521,614,606,208
     ≈ 3.5 nghìn tỷ unique codes
```

**So sánh capacity:**
- 7 ký tự Base62: ~3.5 nghìn tỷ codes
- 6 ký tự Base62: ~56.8 tỷ codes
- 8 ký tự Base62: ~218 nghìn tỷ codes

Với 3.5 nghìn tỷ codes, collision probability là cực kỳ thấp. Xem phân tích chi tiết ở phần codegen.

### 7.4. Thuật Toán Decode

```go
func Decode(s string) (*big.Int, error) {
    if len(s) != shortCodeLength {
        return nil, errors.New("invalid short code length")
    }
    result := big.NewInt(0)
    base := big.NewInt(62)
    for i := 0; i < len(s); i++ {
        idx := strings.IndexByte(base62Alphabet, s[i])
        if idx == -1 {
            return nil, errors.New("invalid character in short code")
        }
        result.Mul(result, base)
        result.Add(result, big.NewInt(int64(idx)))
    }
    return result, nil
}
```

**Phân tích:**
- `strings.IndexByte` O(n) mỗi ký tự → O(7×62) = O(434) operations. Có thể tối ưu bằng map lookup nhưng với 7 ký tự thì không đáng.
- `Decode` hiện không được dùng trong codebase (chỉ `Encode` được gọi từ `codegen.go`). Có thể dùng cho reverse lookup hoặc analytics.

---

## 8. Sinh Mã Short Code (codegen.go)

### 8.1. Interface

```go
type ShortCodeGenerator interface {
    Generate() string
}
```

Interface cho phép mock trong unit test. Implementation duy nhất là `cryptoRandGenerator`.

### 8.2. Implementation

```go
func (g *cryptoRandGenerator) Generate() string {
    b := make([]byte, 8)
    _, err := rand.Read(b)
    if err != nil {
        panic(err)
    }
    n := new(big.Int).SetBytes(b)
    return Encode(n)
}
```

**Phân tích từng dòng:**

1. **`make([]byte, 8)`**: 8 bytes = 64 bits. `crypto/rand.Read` fill đầy 8 bytes với entropy từ OS ( `/dev/urandom` trên Linux).

2. **`rand.Read(b)`**: Đọc 8 bytes ngẫu nhiên từ nguồn entropy hệ thống. Trên Linux, đây là `getrandom(2)` syscall.

3. **`panic(err)`**: Nếu `crypto/rand.Read` fail, hệ thống đang trong trạng thái không có entropy — không thể recover. `panic` là hành vi đúng đắn.

4. **`new(big.Int).SetBytes(b)`**: Chuyển 8 bytes thành big integer (0 đến 2^64 - 1 = ~1.8 × 10^19).

5. **`Encode(n)`**: Base62 encode với modulo `62^7`.

### 8.3. Phân Tích Xác Suất Collision

```
62^7 = 3,521,614,606,208 ≈ 3.5 × 10^12

Sau 1 triệu URLs:
  P(collision per attempt) ≈ n / N = 10^6 / 3.5×10^12 ≈ 2.86 × 10^-7

Sau 10 triệu URLs:
  P(collision per attempt) ≈ 10^7 / 3.5×10^12 ≈ 2.86 × 10^-6

Sau 100 triệu URLs:
  P(collision per attempt) ≈ 10^8 / 3.5×10^12 ≈ 2.86 × 10^-5
```

**Với 3 attempts retry:**
```
P(all 3 fail) ≈ (2.86 × 10^-7)^3 ≈ 2.34 × 10^-20 (sau 1M URLs)
```

Con số này cực kỳ nhỏ. Trên thực tế, collision gần như không xảy ra.

### 8.4. Tại Sao Dùng `crypto/rand` Thay Vì `math/rand`?

| Tiêu chí | `math/rand` | `crypto/rand` |
|---|---|---|
| Nguồn entropy | Seed cố định / time | OS kernel entropy pool |
| Predictable? | Có (nếu biết seed) | Không |
| Performance | Rất nhanh | Chậm hơn |
| Use case | Simulation, game | Security-sensitive |

Với short code, predictability có thể dẫn đến:
- **URL enumeration**: Kẻ tấn công có thể dự đoán short code của URL người khác.
- **Competitive analysis**: Đối thủ có thể ước lượng số lượng URL được tạo.

### 8.4. Padding và Truncation

`Encode` function tự động pad với '0' bên trái nếu số nhỏ hơn `62^6`. Ví dụ:
- `n = 1` → `0000001` (6 số 0 + 1)
- `n = 62` → `0000010` (5 số 0 + 10)
- `n = 62^6` → `1000000`

Điều này đảm bảo mọi short code đều có độ dài chính xác 7 ký tự.

---

## 9. Validation URL (validate.go)

### 9.1. Implementation

```go
func ValidateURL(rawURL string) error {
    if rawURL == "" {
        return errors.New("URL cannot be empty")
    }
    u, err := url.Parse(rawURL)
    if err != nil {
        return errors.New("invalid URL format")
    }
    if u.Scheme != "http" && u.SScheme != "https" {
        return errors.New("URL must start with http or https")
    }
    if u.Host == "" {
        return errors.New("URL must have a host")
    }
    return nil
}
```

**Phân tích validation rules:**

1. **Empty check**: URL không được rỗng.
2. **`url.Parse`**: Go's `net/url.Parse` rất "tha thứ" — nó parse hầu hết mọi string. Ví dụ `"abc"` sẽ parse thành `Scheme:""`, `Host:""`, `Path:"abc"`. Do đó cần kiểm tra thêm scheme và host.
3. **Scheme check**: Chỉ chấp nhận `http` hoặc `https`. Từ chối `ftp://`, `file://`, `javascript:`, v.v.
4. **Host check**: URL phải có host. Ngăn các input như `http://` (thiếu host).

**Thiếu sót:**
- Không validate độ dài URL tối đa.
- Không kiểm tra URL có chứa ký tự nguy hiểm (XSS, injection).
- Không normalize URL (loại bỏ fragment, trailing slash, v.v.).
- Không kiểm tra URL có reachable không (chỉ validate format).

---

## 10. Error Types & HTTP Mapping (errors.go)

### 10.1. Sentinel Errors

```go
var (
    ErrNotFound      = errors.New("url not found")
    ErrInvalidURL    = errors.New("invalid URL")
    ErrAlreadyExists = errors.New("short code already exists")
    ErrForbidden     = errors.New("forbidden")
    ErrExpired       = errors.New("url has expired")
    ErrDeactivated   = errors.New("url has been deactivated")
    ErrDatabaseError = errors.New("database error")
    ErrCacheError    = errors.New("cache error")
)
```

### 10.2. HTTP Status Mapping

| Error | HTTP Status | Ý nghĩa |
|---|---|---|
| `ErrInvalidURL` | 400 Bad Request | URL không hợp lệ |
| `ErrAlreadyExists` | 409 Conflict | Short code collision (sau 3 retry) |
| `ErrNotFound` | 404 Not Found | Short code không tồn tại |
| `ErrForbidden` | 403 Forbidden | User không sở hữu URL |
| `ErrExpired` | 410 Gone | URL đã hết hạn |
| `ErrDeactivated` | 410 Gone | URL đã bị vô hiệu hóa |
| `ErrDatabaseError` | 500 Internal Server Error | Lỗi database không xác định |

**Phân tích HTTP status codes:**
- **410 Gone** được dùng cho cả expired và deactivated — đây là lựa chọn đúng đắn vì cả hai trạng thái đều là vĩnh viễn (resource không còn available).
- **409 Conflict** cho collision — đúng theo REST convention.
- **403 Forbidden** khi user cố deactivate URL không thuộc sở hữu — đây là authorization failure.

### 10.1. Helper Functions

```go
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
```

Cả hai function đều set `Content-Type: application/json` và dùng `json.NewEncoder` (streaming) thay vì `json.Marshal` (buffer). `json.NewEncoder` ghi trực tiếp vào `ResponseWriter`, tiết kiệm memory allocation.

---

## 11. URL Store Layer (store.go)

### 11.1. Domain Model

```go
type URLRecord struct {
    ID          string
    ShortCode   string
    OriginalURL string
    UserID      string
    UserEmail   string
    CreatedAt   time.Time
    ExpiresAt   *time.Time
    IsActive    bool
}
```

**Lưu ý:** `ExpiresAt` là `*time.Time` (pointer) để phân biệt "không có expiry" (nil) với "expiry tại thời điểm zero" (time.Time{}).

### 11.2. Interface

```go
type URLStore interface {
    Insert(ctx context.Context, tx pgx.Tx, record *URLRecord) error
    FindByCode(ctx context.Context, shortCode string) (*URLRecord, error)
    FindByUserID(ctx context.Context, userID string, afterID string, limit int) ([]URLRecord, error)
    Deactivate(ctx context.Context, tx pgx.Tx, shortCode, userID string) error
}
```

**Phân tích interface design:**
- `Insert` và `Deactivate` nhận `pgx.Tx` — bắt buộc phải dùng trong transaction.
- `FindByCode` và `FindByUserID` không nhận tx — đây là read operations, không cần transaction.
- Interface design cho phép mock trong unit test.

### 11.3. Insert

```go
func (s *pgxURLStore) Insert(ctx context.Context, tx pgx.Tx, record *URLRecord) error {
    const query = `INSERT INTO urls (id, short_code, original_url, user_id, user_email, created_at, expires_at, is_active) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
    _, err := tx.Exec(ctx, query, record.ID, record.ShortCode, record.OriginalURL, record.UserID, record.UserEmail, record.CreatedAt, record.ExpiresAt, record.IsActive)
    return err
}
```

- Dùng `tx.Exec` thay vì `pool.Exec` — bắt buộc phải gọi trong transaction.
- Error trả về trực tiếp — caller xử lý unique violation (23505).

### 11.4. FindByCode

```go
func (s *pgxURLStore) FindByCode(ctx context.Context, shortCode string) (*URLRecord, error) {
    const query = `SELECT id, short_code, original_url, user_id, user_email, created_at, expires_at, is_active FROM urls
    WHERE short_code = $1`
    var r URLRecord
    err := s.pool.QueryRow(ctx, query, shortCode).Scan(&r.ID, &r.ShortCode, &r.OriginalURL, &r.UserID, &r.UserEmail, &r.CreatedAt, &r.ExpiresAt, &r.IsActive)
    if err != nil {
        if err == pgx.ErrNoRows {
            return nil, nil
        }
        return nil, err
    }
    return &r, nil
}
```

**Phân tích:**
- Dùng `pool.QueryRow` (không transaction) — read operation.
- `pgx.ErrNoRows` được convert thành `nil, nil` — service layer sẽ map thành `ErrNotFound`.
- **Lưu ý:** `FindByCode` không kiểm tra `is_active` hay `expires_at`. Việc này được xử lý ở service layer. Đây là thiết kế có chủ đích — store layer chỉ làm nhiệm vụ CRUD thuần túy.

### 11.5. FindByUserID (Cursor-based Pagination)

```go
func (s *pgxURLStore) FindByUserID(ctx context.Context, userID string, afterID string, limit int) ([]URLRecord, error) {
    fetchLimit := limit + 1
    if afterID != "" {
        query = `SELECT ... FROM urls 
        WHERE user_id = $1 AND id < $2 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW()) 
        ORDER BY id DESC LIMIT $3`
        args = []any{userID, afterID, fetchLimit}
    } else {
        query = `SELECT ... FROM urls 
        WHERE user_id = $1 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW())
        ORDER BY id DESC LIMIT $2`
        args = []any{userID, fetchLimit}
    }
}
```

**Phân tích cursor-based pagination:**

1. **`fetchLimit = limit + 1`**: Fetch thêm 1 record để xác định có page tiếp theo không.
2. **Cursor = `id`**: Dùng UUID làm cursor. `id < $2` cho DESC ordering (newest first).
3. **Filter `is_active = true`**: Chỉ hiển thị URL đang active.
4. **Filter `expires_at IS NULL OR expires_at > NOW()`**: Chỉ hiển thị URL chưa hết hạn.
5. **`ORDER BY id DESC`**: Mới nhất trước. UUIDv7 (time-ordered) sẽ cho kết quả đúng, nhưng UUIDv4 (random) có thể không đúng thứ tự thời gian.

**Vấn đề tiềm ẩn:** Nếu `id` là UUIDv4 (random), `ORDER BY id DESC` không đảm bảo thứ tự thời gian. Nên dùng `created_at DESC` thay vì `id DESC`, hoặc dùng UUIDv7.

### 11.6. Deactivate

```go
func (s *pgxURLStore) Deactivate(ctx context.Context, tx pgx.Tx, shortCode, userID string) error {
    const query = `UPDATE urls SET is_active = false WHERE short_code = $1 AND user_id = $2 AND is_active = true`
    cmdTag, err := tx.Exec(ctx, query, shortCode, userID)
    if err != nil {
        return err
    }
    if cmdTag.RowsAffected() == 0 {
        return pgx.ErrNoRows
    }
    return nil
}
```

**Phân tích:**
- `WHERE short_code = $1 AND user_id = $2 AND is_active = true`: Chỉ deactivate nếu:
  - Short code tồn tại
  - User là chủ sở hữu
  - URL chưa bị deactivate trước đó
- `cmdTag.RowsAffected() == 0` → `pgx.ErrNoRows` → service layer trả về 403 Forbidden.
- Đây là **optimistic locking** — không cần SELECT trước, UPDATE trực tiếp và kiểm tra rows affected.

---

## 12. Outbox Store Layer (outbox_store.go)

### 12.1. Domain Model

```go
type OutboxRecord struct {
    ID          string
    EventType   string
    Payload     []byte
    CreatedAt   time.Time
    LockedUntil *time.Time
    PublishedAt *time.Time
}
```

### 12.2. Interface

```go
type OutboxStore interface {
    InsertEvent(ctx context.Context, tx pgx.Tx, outbox *OutboxRecord) error
    FetchUnpublished(ctx context.Context, limit int) ([]*OutboxRecord, error)
    MarkPublished(ctx context.Context, id string) error
}
```

### 12.3. InsertEvent

```go
func (s *pgxOutboxStore) InsertEvent(ctx context.Context, tx pgx.Tx, outbox *OutboxRecord) error {
    const query = `INSERT INTO outbox (id, event_type, payload, created_at) VALUES ($1, $2, $3, $4)`
    if tx != nil {
        _, err := tx.Exec(ctx, query, outbox.ID, outbox.EventType, outbox.Payload, outbox.CreatedAt)
        return err
    }
    _, err := s.pool.Exec(ctx, query, outbox.ID, outbox.EventType, outbox.Payload, outbox.CreatedAt)
    return err
}
```

**Phân tích:**
- `InsertEvent` hỗ trợ cả transactional (có tx) và non-transactional (không tx).
- Non-transactional mode được dùng trong `writeAnalyticsEvent` (handler.go:208) — click events được insert không transaction vì không cần atomicity với mutation khác.
- Transactional mode được dùng trong `ShortenURL` và `DeactivateURL`.

### 12.2. FetchUnpublished — CTE với FOR UPDATE SKIP LOCKED

```sql
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
```

**Phân tích chi tiết từng phần:**

1. **CTE `claimed`**:
   - Chọn các row chưa publish (`published_at IS NULL`).
   - Chỉ chọn row không bị lock hoặc lock đã hết hạn (`locked_until IS NULL OR locked_until < now()`).
   - `ORDER BY created_at ASC` — FIFO, xử lý event cũ nhất trước.
   - `LIMIT $1` — giới hạn batch size (50).
   - `FOR UPDATE SKIP LOCKED` — **critical**: khóa các row được chọn, bỏ qua row đã bị khóa bởi worker khác.

2. **`FOR UPDATE SKIP LOCKED`**:
   - `FOR UPDATE`: Khóa row để các transaction khác không thể claim.
   - `SKIP LOCKED` (PostgreSQL 9.5+): Bỏ qua các row đã bị khóa, thay vì chờ.
   - Cho phép nhiều replica chạy đồng thời mà không conflict.

3. **`SET locked_until = now() + interval '30 seconds'`**:
   - Claim lease: worker này tuyên bố độc quyền xử lý event trong 30 giây.
   - Nếu worker crash, lease tự động hết hạn sau 30s, worker khác có thể claim lại.

4. **`RETURNING`**: Trả về toàn bộ row đã update, tránh cần SELECT riêng.

### 12.3. MarkPublished

```go
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
```

- `WHERE published_at IS NULL` — idempotency guard: nếu event đã được mark published trước đó, UPDATE không ảnh hưởng.
- `locked_until = NULL` — giải phóng lock sau khi publish thành công.
- `pgx.ErrNoRows` được log warning trong outbox coordinator.

---

## 13. Cache Layer (cache.go)

### 13.1. CachedURL Struct

```go
type CachedURL struct {
    OriginalURL string     `json:"original_url"`
    UserID      string     `json:"user_id"`
    UserEmail   string     `json:"user_email"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
    IsActive    bool       `json:"is_active"`
}
```

**Tại sao cache cả `IsActive`?**
- Nếu URL bị deactivate, cache vẫn có thể chứa bản cũ. Bằng cách cache `IsActive`, redirect handler có thể trả về 410 Gone ngay từ cache mà không cần query DB.
- Tuy nhiên, có race condition: URL bị deactivate sau khi cache được set. Giải pháp là `Delete` cache khi deactivate (xem `DeactivateURL`).

### 13.2. Cache Interface

```go
type Cache interface {
    Get(ctx context.Context, code string) (*CachedURL, error)
    Set(ctx context.Context, code string, cached *CachedURL, ttl time.Duration) error
    Delete(ctx context.Context, code string) error
}
```

### 13.3. Get — Cache-Aside Read

```go
func (c *redisCache) Get(ctx context.Context, code string) (*CachedURL, error) {
    timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
    defer cancel()
    data, err := c.client.Get(timeoutCtx, code).Result()
    if err != nil {
        return nil, nil
    }
    var cached CachedURL
    if err := json.Unmarshal([]byte(data), &cached); err != nil {
        return nil, nil
    }
    return &cached, nil
}
```

**Phân tích fail-open behavior:**
- Redis error (connection refused, timeout, etc.) → `return nil, nil` → service coi như cache miss.
- JSON unmarshal error → `return nil, nil` → cache miss.
- **Không có cách phân biệt** giữa "cache miss thật" và "Redis lỗi". Cả hai đều trả về `nil, nil`.

**Timeout 50ms:**
- `context.WithTimeout(ctx, 50*time.Millisecond)` — nếu Redis không response trong 50ms, context timeout, `Get` trả về error, service fallback về DB.
- Đây là **circuit breaker nhẹ**: không để Redis chậm làm ảnh hưởng toàn bộ response time.

### 13.4. Set — Cache-Aside Write

```go
func (c *redisCache) Set(ctx context.Context, code string, cached *CachedURL, ttl time.Duration) error {
    data, err := json.Marshal(cached)
    if err != nil {
        return err
    }
    return c.client.Set(ctx, code, data, ttl).Err()
}
```

- `ttl` được tính từ `expiresInHours` (shorten) hoặc `time.Until(expiresAt)` (redirect).
- Nếu `ttl` là 0, Redis sẽ không expire key (có thể gây memory leak nếu URL không có expiry).

### 13.4. Delete — Cache Invalidation

```go
func (c *redisCache) Delete(ctx context.Context, code string) error {
    return c.client.Del(ctx, code).Err()
}
```

- Được gọi trong `DeactivateURL` (service.go:345) — cache invalidation ngay lập tức.
- `_ = s.cache.Delete(...)` — lỗi bị bỏ qua (fire-and-forget).

### 13.5. Cache-Aside Pattern Analysis

```
Shorten Flow:
  1. INSERT url + outbox (DB transaction)
  2. go cache.Set() ← fire-and-forget, không blocking

Redirect Flow:
  1. cache.Get(code) ← 50ms timeout
  2. Nếu HIT → kiểm tra is_active + expires_at → redirect
  3. Nếu MISS → DB query → cache.Set() (fire-and-forget) → redirect

Deactivate Flow:
  1. UPDATE url (DB transaction)
  2. cache.Delete(code) ← fire-and-forget
```

**Vấn đề tiềm ẩn:**
- **Race condition**: Giữa `ShortenURL` (set cache) và `RedirectToURL` (get cache). Nếu cache set chậm hơn redirect request, có thể dẫn đến cache miss ngay sau khi tạo URL. Tuy nhiên, đây chỉ là performance issue, không phải correctness issue.
- **Stale cache**: Nếu `DeactivateURL` thất bại (Redis error), cache vẫn chứa URL active. Lần redirect tiếp theo sẽ thấy cache HIT và redirect thành công, dù URL đã bị deactivate trong DB. Đây là **temporary inconsistency** — eventual consistency.

---

## 14. Publisher Layer (publisher.go)

### 14.1. Interface

```go
type RabbitMQPublisher interface {
    Publish(ctx context.Context, routingKey string, body []byte) error
}
```

### 14.2. Implementation

```go
type amqpPublisher struct {
    ch *amqp.Channel
    mu sync.Mutex
}

func (p *amqpPublisher) Publish(ctx context.Context, routingKey string, body []byte) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    return p.ch.PublishWithContext(ctx,
        exchangeName,  // "url-shortener"
        routingKey,    // event_type
        false,         // mandatory
        false,         // immediate
        amqp.Publishing{
            ContentType:  "application/json",
            DeliveryMode: amqp.Persistent,  // 2 = Persistent
            Body:         body,
        },
    )
}
```

**Phân tích từng tham số Publish:**

| Tham số | Giá trị | Ý nghĩa |
|---|---|---|
| `exchange` | `"url-shortener"` | Topic exchange |
| `routingKey` | `event_type` | VD: `url.created`, `url.deleted`, `url.clicked` |
| `mandatory` | `false` | Không yêu cầu queue tồn tại — nếu không có queue bind, message bị drop |
| `immediate` | `false` | Không yêu cầu consumer sẵn sàng |
| `ContentType` | `application/json` | Body là JSON |
| `DeliveryMode` | `amqp.Persistent` (2) | Message được persist to disk, sống sót qua RabbitMQ restart |

**Tại sao `mandatory = false`?**
- Nếu không có queue nào bind với routing key tương ứng, message bị drop silently.
- Đây là thiết kế có chủ đích: nếu consumer chưa sẵn sàng, không cần giữ message.
- Tuy nhiên, với Transactional Outbox pattern, message đã được persist trong DB, nên nếu bị drop, outbox coordinator sẽ retry ở lần poll tiếp theo.

**Tại sao `mu sync.Mutex`?**
- `amqp.Channel` không thread-safe. Mutex đảm bảo chỉ một goroutine publish tại một thời điểm.
- Đây là bottleneck tiềm ẩn — nếu publish rate cao, mutex có thể gây contention. Giải pháp: dùng channel pool hoặc connection pool.

---

## 15. Outbox Coordinator (outbox.go)

### 15.1. Constants

```go
const (
    outboxBatchSize   = 50
    outboxWorkerCount = 3
    outboxPollEvery   = 2 * time.Second
)
```

**Phân tích tuning parameters:**

| Parameter | Giá trị | Cơ sở |
|---|---|---|
| `outboxBatchSize` | 50 | Batch size vừa phải, không gây áp lực lớn lên DB |
| `outboxWorkerCount` | 3 | 3 worker goroutines xử lý song song |
| `outboxPollEvery` | 2s | Poll interval — cân bằng giữa latency và DB load |

**Tại sao 3 workers?**
- Với `MaxConns = 10`, 3 workers chiếm 3 connections (qua pool).
- Mỗi worker publish tuần tự (do mutex trong publisher), nên nhiều worker giúp tăng throughput.
- 3 là con số hợp lý cho hệ thống vừa phải.

### 15.2. OutboxCoordinator Struct

```go
type OutboxCoordinator struct {
    store     OutboxStore
    publisher RabbitMQPublisher
    log       *slog.Logger
}
```

### 15.3. Run — Main Loop

```go
func (c *OutboxCoordinator) Run(ctx context.Context) {
    jobs := make(chan *OutboxRecord, outboxBatchSize)
    var workers sync.WaitGroup

    for i := 0; i < outboxWorkerCount; i++ {
        workers.Add(1)
        go func(workerID int) {
            defer workers.Done()
            c.worker(ctx, workerID, jobs)
        }(i + 1)
    }

    ticker := time.NewTicker(outboxPollEvery)
    defer ticker.Stop()
    defer func() {
        close(jobs)
        workers.Wait()
    }()

    for {
        c.poll(ctx, jobs)
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
        }
    }
}
```

**Phân tích flow:**

1. **Khởi tạo workers**: 3 goroutines, mỗi worker đọc từ channel `jobs`.
2. **Channel buffered**: `make(chan *OutboxRecord, outboxBatchSize)` — buffer 50 items.
3. **Poll loop**: Mỗi 2 giây, poll DB → gửi records vào channel.
4. **Graceful shutdown**: `defer close(jobs)` + `workers.Wait()` — đợi tất cả workers hoàn thành.

### 15.4. Worker Pattern

```go
func (c *OutboxCoordinator) worker(ctx context.Context, workerID int, jobs <-chan *OutboxRecord) {
    for {
        select {
        case <-ctx.Done():
            return
        case record, ok := <-jobs:
            if !ok {
                return
            }
            c.publish(ctx, workerID, record)
        }
    }
}
```

- Worker thoát khi channel đóng (`ok == false`) hoặc context cancelled.
- Mỗi worker xử lý một record tại một thời điểm.

### 15.5. Publish Flow

```go
func (c *OutboxCoordinator) publish(ctx context.Context, workerID int, record *OutboxRecord) {
    if err := c.publisher.Publish(ctx, record.EventType, record.Payload); err != nil {
        c.log.Warn("outbox publish failed", "worker", workerID, "outbox_id", record.ID, "event_type", record.EventType, "error", err)
        return
    }
    if err := c.store.MarkPublished(ctx, record.ID); err != nil {
        c.log.Warn("outbox mark published failed", "worker", workerID, "outbox_id", record.ID, "error", err)
    }
}
```

**Phân tích:**
1. **Publish to RabbitMQ**: Nếu fail → log warning + return (không mark published). Ở lần poll tiếp theo, record sẽ được fetch lại (vì `locked_until` vẫn còn hiệu lực, nhưng sẽ hết hạn sau 30s).
2. **MarkPublished**: Nếu fail → log warning. Record đã được publish nhưng chưa được mark — ở lần poll tiếp theo, nó sẽ được publish lại (duplicate). Đây là **at-least-once delivery**.

**Vấn đề duplicate:**
- Nếu `MarkPublished` fail sau khi publish thành công, record sẽ được publish lại ở lần poll sau.
- Consumer phải xử lý idempotency (dùng `event.ID` hoặc `short_code` để deduplicate).

---

## 16. Service Layer (service.go)

### 16.1. URLService Struct

```go
type URLService struct {
    pool         pgxPool
    store        URLStore
    outboxStore  OutboxStore
    cache        Cache
    cgen         ShortCodeGenerator
    shortURLBase string
}
```

`pgxPool` interface:
```go
type pgxPool interface {
    Begin(ctx context.Context) (pgx.Tx, error)
}
```

Interface này cho phép mock transaction trong test. `pgxpool.Pool` implement interface này.

### 16.2. ShortenURL Flow (Chi Tiết)

```
Request: POST /shorten { url, expires_in_hours }
  │
  ├─ 1. ValidateURL(url)
  │     ├─ Empty check
  │     ├─ url.Parse
  │     ├─ Scheme check (http/https)
  │     └─ Host check
  │
  ├─ 2. Normalize expiresInHours
  │     ├─ ≤ 0 hoặc > 8760 (24*365) → 24 (1 ngày)
  │     └─ expiresAt = now + expiresInHours
  │
  ├─ 3. Loop (max 3 attempts):
  │     ├─ Generate short code (crypto/rand + base62)
  │     ├─ BEGIN transaction
  │     │   ├─ INSERT url
  │     │   └─ INSERT outbox event
  │     └─ COMMIT
  │     ├─ Nếu UNIQUE_VIOLATION (23505) → sleep + retry
  │     └─ Nếu lỗi khác → return 500
  │
  ├─ 4. Nếu success = false sau 3 attempts → return 409 Conflict
  │
  └─ 5. go cache.Set() ← fire-and-forget
```

**Phân tích chi tiết từng bước:**

#### Bước 1: Validate

```go
if err := ValidateURL(url); err != nil {
    return ShortenResponse{}, &HTTPError{
        Status: http.StatusBadRequest,
        Err:    ErrInvalidURL,
    }
}
```

- Validation xảy ra trước khi bất kỳ operation nào khác.
- Error mapping: `ErrInvalidURL` → 400 Bad Request.

#### Bước 2: Normalize Expiry

```go
if expiresInHours <= 0 || expiresInHours > 24*365 {
    expiresInHours = 24
}
expiresAt := time.Now().Add(time.Duration(expiresInHours) * time.Hour)
```

- Default: 24 giờ nếu không hợp lệ.
- Max: 1 năm (8760 giờ).
- `expiresAt` được tính bằng `time.Now()` — lưu ý: đây là server time, không phải client time.

#### Bước 3: Transaction với Retry

```go
for attempt := 0; attempt < 3; attempt++ {
    shortCode = s.cgen.Generate()
    err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
        // INSERT url
        // INSERT outbox
        return nil
    })
    if err == nil {
        success = true
        break
    }
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23505" {
        time.Sleep(time.Duration(attempt*50) * time.Millisecond)
        continue
    }
    return ShortenResponse{}, &HTTPError{Status: http.StatusInternalServerError, Err: ErrDatabaseError}
}
```

**Phân tích retry logic:**

1. **`pgx.BeginFunc`**: Tự động quản lý transaction — Commit nếu function return nil, Rollback nếu panic hoặc return error.
2. **Error code `23505`**: PostgreSQL unique violation. Chỉ có short_code là UNIQUE, nên đây chắc chắn là collision.
3. **Backoff**: `attempt * 50ms` → 0ms, 50ms, 100ms. Backoff nhẹ để giảm collision rate khi nhiều request đồng thời.
4. **Sau 3 attempts**: Trả về `409 Conflict` với `ErrAlreadyExists`.

**Tại sao không dùng `ON CONFLICT DO NOTHING`?**
- Vì cần insert cả url và outbox trong cùng transaction. Nếu dùng `ON CONFLICT DO NOTHING`, cần kiểm tra `RowsAffected()`.
- Retry với short code mới là approach sạch hơn.

### 16.3. RedirectToURL Flow (Chi Tiết)

```
Request: GET /{code}
  │
  ├─ 1. context.WithTimeout(ctx, 50ms)
  │
  ├─ 2. cache.Get(shortCode)
  │     ├─ HIT + active + chưa expired → return RedirectInfo
  │     ├─ HIT + !is_active → 410 Gone
  │     ├─ HIT + expired → 410 Gone
  │     └─ MISS → tiếp tục
  │
  ├─ 3. store.FindByCode(shortCode)
  │     ├─ pgx.ErrNoRows → 404 Not Found
  │     ├─ Lỗi khác → 500 Internal Server Error
  │     └─ Success → kiểm tra is_active + expires_at
  │
  ├─ 4. Kiểm tra is_active
  │     └─ false → 410 Gone
  │
  ├─ 5. Kiểm tra expires_at
  │     └─ expired → 410 Gone
  │
  ├─ 6. go cache.Set() ← fire-and-forget
  │
  └─ 7. Return RedirectInfo { OriginalURL, UserID, UserEmail, IpHash }
```

**Phân tích TTL cho cache set sau redirect:**

```go
ttl := time.Hour
if urlRecord.ExpiresAt != nil {
    ttl = time.Until(*urlRecord.ExpiresAt)
    if ttl < 0 {
        ttl = 0
    }
}
```

- Nếu URL không có expiry → cache 1 giờ.
- Nếu URL có expiry → cache đến lúc hết hạn.
- Nếu URL đã hết hạn (ttl < 0) → cache 0 giây (không cache).

### 16.4. GetUserUrls

```go
func (s *URLService) GetUserUrls(ctx context.Context, userID, afterID string, limit int) (*ListURLsResponse, *HTTPError) {
    urls, err := s.store.FindByUserID(ctx, userID, afterID, limit)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, &HTTPError{Status: http.StatusOK, Err: ErrNotFound}
        }
        return nil, &HTTPError{Status: http.StatusInternalServerError, Err: ErrDatabaseError}
    }
    // Determine next cursor and hasMore
    var nextCursor string
    hasMore := len(urls) > limit
    if hasMore {
        urls = urls[:limit]
        nextCursor = urls[len(urls)-1].ID
    }
    return &ListURLsResponse{URLs: urls, NextCursor: nextCursor, HasMore: hasMore}, nil
}
```

**Phân tích pagination:**
- `pgx.ErrNoRows` → trả về 200 OK với empty list (không phải 404). Đây là design choice: "user không có URL" là trạng thái hợp lệ.
- `len(urls) > limit` → có page tiếp theo. Cắt bỏ record thừa.
- `nextCursor = urls[len(urls)-1].ID` — cursor là ID của record cuối cùng trong page hiện tại.

### 16.5. DeactivateURL Flow

```
Request: DELETE /urls/{code}
  │
  ├─ 1. BEGIN transaction
  │     ├─ UPDATE urls SET is_active = false WHERE short_code = $1 AND user_id = $2
  │     └─ INSERT INTO outbox (url.deleted event)
  └─ COMMIT
  │
  ├─ Nếu pgx.ErrNoRows → 403 Forbidden
  ├─ Nếu lỗi khác → 500 Internal Server Error
  │
  ├─ cache.Delete(code) ← fire-and-forget
  │
  └─ Return 204 No Content
```

**Phân tích:**
- `WHERE short_code = $1 AND user_id = $2 AND is_active = true` — ownership check trong SQL.
- Nếu `RowsAffected() == 0` → hoặc short_code không tồn tại, hoặc user không sở hữu, hoặc đã inactive → 403 Forbidden.
- Cache invalidation sau transaction — eventual consistency.

---

## 17. HTTP Handler Layer (handler.go)

### 17.1. HTTPHandler Struct

```go
type HTTPHandler struct {
    pool         pgxPool
    store        URLStore
    outboxStore  OutboxStore
    cache        Cache
    codegen      ShortCodeGenerator
    shortURLBase string
    urlService   *URLService
}
```

**Lưu ý:** `HTTPHandler` giữ cả dependencies riêng lẻ lẫn `urlService`. Điều này tạo ra **duplicate references** — pool, store, outboxStore, cache, codegen, shortURLBase được lưu cả trong handler lẫn trong service. Đây là code smell nhẹ — có thể refactor để handler chỉ giữ `urlService`.

### 17.2. HandleShorten

```go
func (h *HTTPHandler) HandleShorten(w http.ResponseWriter, r *http.Request) {
    claims, ok := auth.ClaimsFromContext(r.Context())
    if !ok {
        writeError(w, http.StatusUnauthorized, "user not authenticated")
        return
    }
    userID := claims.Sub
    userEmail := claims.Email
    if userID == "" {
        writeError(w, http.StatusUnauthorized, "invalid user token")
        return
    }
    // ... decode body, call service, write response
}
```

**Phân tích:**
- JWT claims được lấy từ context (set bởi `auth.JWTMiddleware`).
- `claims.Sub` = user ID (UUID), `claims.Email` = email.
- Nếu `userID == ""` → token không hợp lệ.
- Response status: `201 Created` — đúng REST convention cho resource creation.

### 17.3. HandleRedirect

```go
func (h *HTTPHandler) HandleRedirect(w http.ResponseWriter, r *http.Request) {
    shortcode := r.PathValue("code")
    // ...
    redirectInfo, httpError := h.urlService.RedirectToURL(r.Context(), shortcode, r.RemoteAddr)
    // ...
    go h.writeAnalyticsEvent(r, shortcode, redirectInfo.UserID, redirectInfo.UserEmail, redirectInfo.IpHash)
    http.Redirect(w, r, redirectInfo.OriginalURL, http.StatusPermanentRedirect)
}
```

**Phân tích:**
- `r.PathValue("code")` — Go 1.22 routing với `GET /{code}`.
- `http.StatusPermanentRedirect` (308) — client/browser nên cache redirect và tiếp tục dùng GET method.
- `go h.writeAnalyticsEvent(...)` — fire-and-forget, không block redirect response.

### 17.3. HandleShortenAnon

```go
func (h *HTTPHandler) HandleShortenAnon(w http.ResponseWriter, r *http.Request) {
    // ...
    urlRecord, httpError := h.urlService.ShortenURL(r.Context(), req.URL, uuid.NewString(), uuid.NewString(), req.ExpiresInHours)
    // ...
}
```

- Anonymous users được gán UUID ngẫu nhiên cho cả `userID` và `userEmail`.
- Không có JWT authentication — endpoint public.
- **Hạn chế**: Anonymous URLs không thể liệt kê hoặc deactivate (vì không biết UUID).

### 17.4. writeAnalyticsEvent

```go
func (h *HTTPHandler) writeAnalyticsEvent(r *http.Request, shortCode, userID, userEmail, ipHash string) {
    userAgent := r.Header.Get("User-Agent")
    referrer := r.Header.Get("Referer")
    event := events.URLClickedEvent{
        BaseEvent: events.NewBaseEvent(events.EventTypeURLClicked, ""),
        ShortCode: shortCode,
        UserID:    userID,
        UserEmail: userEmail,
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
    _ = h.outboxStore.InsertEvent(context.Background(), nil, outbox)
}
```

**Phân tích:**
- `go h.writeAnalyticsEvent(...)` — chạy trong goroutine riêng, không block redirect response.
- `context.Background()` — không dùng request context vì request có thể kết thúc trước khi goroutine hoàn thành.
- `InsertEvent` với `tx = nil` — insert trực tiếp vào DB (không transaction).
- **Vấn đề tiềm ẩn**: Nếu DB connection pool đầy, goroutine này có thể block. Tuy nhiên, vì là fire-and-forget, nó không ảnh hưởng đến response time.

---

## 18. Entry Point (main.go)

### 18.1. Dependency Injection

```
main()
  │
  ├─ LoadConfig()
  ├─ NewDBPool()          → pool
  ├─ pool.Exec(migrationSQL)
  ├─ NewRedisClient()     → redisClient, redisOK
  ├─ NewRabbitMQConn()    → rmqConn
  │
  ├─ NewURLStore(pool)
  ├─ NewOutboxStore(pool)
  ├─ NewAMQPPublisher(rmqConn.Channel)
  ├─ NewOutboxCoordinator(outboxStore, publisher, log)
  ├─ NewRedisCache(redisClient)
  ├─ NewShortCodeGenerator()
  └─ NewHTTPHandler(pool, urlStore, outboxStore, cache, cgen, shortURLBase)
```

### 18.1. Migration

```go
//go:embed migration.sql
var migrationSQL string

if _, err := pool.Exec(context.Background(), migrationSQL); err != nil {
    log.Error("failed to run database migrations", "error", err)
    os.Exit(1)
}
```

- `//go:embed migration.sql` — Go 1.16 embed, nhúng file SQL vào binary.
- Migration chạy mỗi lần service start.
- `IF NOT EXISTS` và `ALTER TABLE ADD COLUMN IF NOT EXISTS` đảm bảo idempotency.

**Hạn chế:** Không có migration versioning. Nếu schema thay đổi, cần update migration.sql và deploy lại. Không support rollback.

### 18.2. HTTP Server Configuration

```go
srv := &http.Server{
    Addr:         ":" + cfg.Port,
    Handler:      mux,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  60 * time.Second,
}
```

| Timeout | Giá trị | Mục đích |
|---|---|---|
| `ReadTimeout` | 10s | Ngăn slow client gửi request chậm |
| `WriteTimeout` | 10s | Ngăn response bị treo |
| `IdleTimeout` | 60s | Đóng kết nối idle (Keep-Alive) |

### 18.2. Route Table

| Method | Path | Handler | Auth | Mô tả |
|---|---|---|---|---|
| `GET` | `/health` | `NewHealthHandler` | Không | Health check |
| `GET` | `/metrics` | `promhttp.Handler()` | Không | Prometheus metrics |
| `POST` | `/shorten` | `HandleShorten` | JWT | Tạo URL (authenticated) |
| `GET` | `/{code}` | `HandleRedirect` | Không | Redirect |
| `POST` | `/shorten-anon` | `HandleShortenAnon` | Không | Tạo URL (anonymous) |
| `GET` | `/redirect-anon/{code}` | `HandleRedirectAnon` | Không | Redirect (anonymous) |
| `GET` | `/urls` | `HandleGetUrls` | JWT | Liệt kê URLs |
| `DELETE` | `/urls/{code}` | `HandleDeactivateUrl` | JWT | Vô hiệu hóa URL |

### 18.2. Graceful Shutdown

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

// ... start server + outbox coordinator ...

<-quit
cancel()  // Cancel context → outbox coordinator stops
log.Info("shutdown signal received, draining connections…")

shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
defer shutdownCancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    log.Error("graceful shutdown failed", "error", err)
}
```

**Graceful shutdown sequence:**
1. Nhận SIGTERM/SIGINT.
2. `cancel()` — cancel root context → outbox coordinator dừng.
3. `srv.Shutdown(shutdownCtx)` — HTTP server ngừng nhận request mới, drain existing connections (10s timeout).
4. `defer pool.Close()`, `defer rmqConn.Close()`, `defer redisClient.Close()` — dọn dẹp connections.

---

## 19. Tổng Kết Luồng Hoạt Động

### 19.1. Shorten URL — Sequence Diagram (Text)

```
Client                  HTTP Handler             URL Service              DB (PostgreSQL)          Redis              RabbitMQ
  │                         │                        │                        │                    │                    │
  │  POST /shorten          │                        │                        │                    │                    │
  │────────────────────────>│                        │                        │                    │                    │
  │                         │  ValidateURL()         │                        │                    │                    │
  │                         │───────────────────────>│                        │                    │                    │
  │                         │                        │                        │                    │                    │
  │                         │  ShortenURL()          │                        │                    │                    │
  │                         │───────────────────────>│                        │                    │                    │
  │                         │                        │  Generate()            │                    │                    │
  │                         │                        │───────────────────────>│                    │                    │
  │                         │                        │  crypto/rand → 8 bytes │                    │                    │
  │                         │                        │  Base62 Encode         │                    │                    │
  │                         │                        │<───────────────────────│                    │                    │
  │                         │                        │                        │                    │                    │
  │                         │                        │  pgx.BeginFunc()       │                    │                    │
  │                         │                        │────────────────────────>│                    │                    │
  │                         │                        │  INSERT url            │                    │                    │
  │                         │                        │────────────────────────>│                    │                    │
  │                         │                        │  INSERT outbox         │                    │                    │
  │                         │                        │────────────────────────>│                    │                    │
  │                         │                        │  COMMIT                │                    │                    │
  │                         │                        │<────────────────────────│                    │                    │
  │                         │                        │                        │                    │                    │
  │                         │                        │  go cache.Set()        │                    │                    │
  │                         │                        │─────────────────────────────────────────────>│                    │
  │                         │                        │                        │                    │                    │
  │  HTTP 201 Created       │                        │                        │                    │                    │
  │<────────────────────────│                        │                        │                    │                    │
```

### 19.2. Redirect Flow — Sequence Diagram

```
Client                  HTTP Handler             URL Service              Redis                DB                 Outbox Coordinator
  │                         │                        │                    │                    │                    │
  │  GET /{code}            │                        │                    │                    │                    │
  │────────────────────────>│                        │                    │                    │                    │
  │                         │  RedirectToURL()        │                    │                    │                    │
  │                         │───────────────────────>│                    │                    │                    │
  │                         │                        │  cache.Get(code)    │                    │                    │
  │                         │                        │───────────────────>│                    │                    │
  │                         │                        │<──── (HIT/MISS) ───│                    │                    │
  │                         │                        │                    │                    │                    │
  │                         │  [Nếu MISS]            │                    │                    │                    │
  │                         │                        │  store.FindByCode() │                    │                    │
  │                         │                        │─────────────────────────────────────────>│                    │
  │                         │                        │<──────────────────────────────────────────│                    │
  │                         │                        │                    │                    │                    │
  │                         │                        │  go cache.Set()    │                    │                    │
  │                         │                        │───────────────────>│                    │                    │
  │                         │                        │                    │                    │                    │
  │                         │  go writeAnalyticsEvent()                  │                    │                    │
  │                         │───────────────────────>│                    │                    │                    │
  │                         │                        │  InsertEvent()     │                    │                    │
  │                         │                        │─────────────────────────────────────────>│                    │
  │                         │                        │                    │                    │                    │
  │  HTTP 308 Redirect      │                        │                    │                    │                    │
  │<────────────────────────│                        │                    │                    │                    │
```

### 19.3. Outbox Processing Flow

```
OutboxCoordinator.Run()
  │
  ├─ Ticker (2s)
  │     │
  │     └─ poll()
  │           │
  │           └─ store.FetchUnpublished(50)
  │                 │
  │                 ├─ CTE: SELECT ... FOR UPDATE SKIP LOCKED
  │                 ├─ UPDATE locked_until = now() + 30s
  │                 └─ RETURNING records
  │
  ├─ jobs channel (buffered 50)
  │     │
  │     ├─ Worker 1: publish() → MarkPublished()
  │     ├─ Worker 2: publish() → MarkPublished()
  │     └─ Worker 3: publish() → MarkPublished()
  │
  └─ [2 giây sau] poll lại
```

---

## 20. Phân Tích Bảo Mật & Độ Tin Cậy

### 20.1. Bảo Mật

| Khía cạnh | Phân tích |
|---|---|
| **Short code randomness** | `crypto/rand` — không thể dự đoán. An toàn trước enumeration attack. |
| **JWT authentication** | Middleware xác thực, claims chứa userID và email. |
| **Ownership check** | `WHERE short_code = $1 AND user_id = $2` — SQL-level authorization. |
| **IP hashing** | `hashIP(remoteAddr)` với salt — không lưu IP raw. |
| **SQL Injection** | Parameterized queries (`$1`, `$2`) — an toàn. |
| **URL validation** | Chỉ chấp nhận http/https scheme. |

### 20.1. Điểm Yếu Bảo Mật

1. **IPHashSalt mặc định**: `"default-salt"` trong code. Nếu không override bằng env, attacker có thể brute-force IP hash.
2. **Không rate limiting**: Endpoint `/shorten` và `/shorten-anon` không có rate limit — attacker có thể spam tạo URL.
3. **Không validation độ dài URL**: URL rất dài có thể gây issues ở DB (TEXT không giới hạn) và cache.
4. **Anonymous endpoint không giới hạn**: `/shorten-anon` cho phép bất kỳ ai tạo URL mà không cần auth.

### 20.2. Độ Tin Cậy

| Cơ chế | Mô tả |
|---|---|
| **Transactional Outbox** | Đảm bảo event không bị mất ngay cả khi RabbitMQ down |
| **FOR UPDATE SKIP LOCKED** | Cho phép nhiều replica xử lý outbox đồng thời |
| **Cache-Aside fail-open** | Redis lỗi → fallback về DB |
| **Exponential backoff (RabbitMQ)** | Retry kết nối với backoff 1s→2s→4s→...→30s |
| **Collision retry** | 3 attempts với backoff cho short code collision |
| **Graceful shutdown** | Drain connections, đợi workers hoàn thành |
| **Persistent messages** | `DeliveryMode: amqp.Persistent` — message sống sót qua RabbitMQ restart |

### 20.3. At-Least-Once Delivery Guarantee

Hệ thống đảm bảo **at-least-once delivery**:

1. Event được persist trong DB (bảng outbox) trước khi publish.
2. Outbox coordinator poll DB, publish lên RabbitMQ.
3. Nếu publish thành công nhưng `MarkPublished` thất bại → event được publish lại.
4. Consumer phải xử lý duplicate (idempotency).

**Không đảm bảo exactly-once** — consumer cần deduplication logic.

---

## 21. Phân Tích Chi Tiết Từng File

### 21.1. main.go — Entry Point

**Dòng 18-19: Embed migration SQL**
```go
//go:embed migration.sql
var migrationSQL string
```
- Go 1.16 embed directive.
- File `migration.sql` được nhúng vào binary tại compile time.
- Không cần file external khi deploy.

**Dòng 22-23: Context với Cancel**
```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
```
- Root context cho toàn bộ service.
- `cancel()` được defer để đảm bảo cleanup.

**Dòng 34-35: DB Connection với Timeout**
```go
dbCtx, dbCancel := context.WithTimeout(context.Background(), 15*time.Second)
defer dbCancel()
pool, err := NewDBPool(dbCtx, cfg.DatabaseURL, log)
```
- 15 giây cho toàn bộ quá trình connect + ping.
- `defer dbCancel()` đảm bảo context được giải phóng.

**Dòng 44: Migration**
```go
if _, err := pool.Exec(context.Background(), migrationSQL); err != nil {
    log.Error("failed to run database migrations", "error", err)
    os.Exit(1)
}
```
- Dùng `context.Background()` (không timeout) — migration có thể mất nhiều thời gian.
- `IF NOT EXISTS` đảm bảo idempotency.

**Dòng 51-55: Redis — Non-Fatal**
```go
redisClient, redisOK := NewRedisClient(context.Background(), cfg.RedisURL, log)
defer redisClient.Close()
if !redisOK {
    log.Warn("starting without Redis cache; cache will be unavailable until Redis recovers")
}
```
- Service vẫn start nếu Redis không available.
- Cache-Aside pattern đảm bảo fallback về DB.

**Dòng 58-65: RabbitMQ — Fatal**
```go
rmqCtx, rmqCancel := context.WithTimeout(context.Background(), 60*time.Second)
defer rmqCancel()
rmqConn, err := NewRabbitMQConn(rmqCtx, cfg.RabbitMQURL, log, 10)
if err != nil {
    log.Error("failed to connect to RabbitMQ", "error", err)
    os.Exit(1)
}
```
- 60 giây timeout cho toàn bộ quá trình connect.
- 10 attempts với exponential backoff.
- Nếu không connect được → fatal.

**Dòng 67-73: Dependency Injection**
```go
urlStore := NewURLStore(pool)
outboxStore := NewOutboxStore(pool)
publisher := NewAMQPPublisher(rmqConn.Channel)
outboxCoordinator := NewOutboxCoordinator(outboxStore, publisher, log)
cache := NewRedisCache(redisClient)
cgen := NewShortCodeGenerator()
handler := NewHTTPHandler(pool, urlStore, outboxStore, cache, cgen, cfg.ShortURLBase)
```

Đây là **manual dependency injection** — không dùng framework DI nào. Mỗi dependency được tạo explicit.

**Dòng 76: JWT Middleware**
```go
authMw := auth.JWTMiddleware(cfg.JWTSecret)
```
- Middleware pattern: `authMw(handler)` trả về `http.Handler`.
- JWT secret từ config.

**Dòng 106: Outbox Coordinator Goroutine**
```go
go outboxCoordinator.Run(ctx)
```
- Chạy trong goroutine riêng.
- `ctx` là root context — khi cancel, coordinator dừng.

---

## 22. Phân Tích Chi Tiết Các Design Pattern

### 22.1. Transactional Outbox Pattern

**Vấn đề:** Khi insert URL vào DB và publish message lên RabbitMQ, nếu publish thất bại sau khi DB insert thành công, hệ thống ở trạng thái inconsistent.

**Giải pháp:** Ghi event vào bảng `outbox` trong cùng transaction với mutation. Một background worker đọc từ bảng outbox và publish lên RabbitMQ.

```
┌─────────────────────────────────────────────────────────┐
│                     Transaction                          │
│  ┌─────────────────────┐    ┌────────────────────────┐  │
│  │ INSERT INTO urls    │    │ INSERT INTO outbox      │  │
│  │ (short_code, url,   │    │ (event_type, payload)   │  │
│  │  user_id, ...)      │    │                         │  │
│  └─────────────────────┘    └────────────────────────┘  │
│  │                        COMMIT                        │
└─────────────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌─────────────────────┐
                    │  OutboxCoordinator  │
                    │  (poll every 2s)    │
                    └─────────────────────┘
                              │
                              ▼
                    ┌─────────────────────┐
                    │  RabbitMQ Publisher │
                    └─────────────────────┘
```

### 21.2. Chi Tiết Về `hashIP`

Trong `service.go`, hàm `hashIP` được gọi nhưng không được định nghĩa trong file này. Nó có thể được định nghĩa trong một file khác trong cùng package. Hàm này:

- Nhận `remoteAddr` (dạng `ip:port`).
- Hash với salt (`IPHashSalt` từ config).
- Trả về hash string để lưu trong analytics event.

**Mục đích:** Không lưu IP raw vì lý do privacy (GDPR, CCPA). Chỉ lưu hash để phân tích mà không thể还原 IP gốc.

---

## 22. Phân Tích Hiệu Năng

### 22.1. Shorten Path

| Bước | Operations | Estimated Latency |
|---|---|---|
| Validate URL | String parsing | < 1µs |
| Generate code | crypto/rand (8 bytes) + big.Int Mod | ~10-50µs |
| DB Transaction | INSERT url + INSERT outbox | ~5-20ms |
| Cache Set | Redis SET (fire-and-forget) | ~1-5ms |
| **Total** | | **~5-25ms** |

### 22.2. Redirect Path (Cache HIT)

| Bước | Operations | Estimated Latency |
|---|---|---|
| Cache Get | Redis GET | ~1-5ms |
| Check is_active | In-memory | < 1µs |
| Check expires_at | In-memory | < 1µs |
| **Total** | | **~1-5ms** |

### 22.3. Redirect Path (Cache MISS)

| Bước | Operations | Estimated Latency |
|---|---|---|
| Cache Get | Redis GET (miss) | ~1-5ms |
| DB Query | PostgreSQL SELECT | ~5-20ms |
| Cache Set | Redis SET (fire-and-forget) | ~1-5ms |
| **Total** | | **~6-25ms** |

### 22.4. Outbox Processing

| Bước | Operations | Estimated Latency |
|---|---|---|
| Poll DB | CTE + FOR UPDATE SKIP LOCKED | ~5-20ms |
| Publish (per record) | AMQP Publish | ~1-5ms |
| MarkPublished (per record) | UPDATE outbox | ~2-10ms |
| **Total per batch (50 records)** | | **~150-750ms** |

Với 3 workers, throughput tối đa: ~150 records / 2s = 75 events/second.

---

## 22. Phân Tích Mở Rộng (Scalability)

### 22.1. Horizontal Scaling

| Component | Scaling Strategy |
|---|---|
| **URL Service** | Stateless HTTP — có thể chạy nhiều replicas. Cache-Aside + DB shared. |
| **Outbox Coordinator** | `FOR UPDATE SKIP LOCKED` cho phép nhiều replicas xử lý outbox đồng thời. |
| **PostgreSQL** | Read replicas cho redirect queries. Write master cho mutations. |
| **Redis** | Cluster mode cho high availability. |
| **RabbitMQ** | Clustered deployment cho HA. |

### 22.2. Bottlenecks

1. **AMQP Publisher Mutex**: `sync.Mutex` trên `amqpPublisher` — tất cả workers share cùng một channel. Với 3 workers, mutex contention có thể xảy ra.
2. **Single RabbitMQ Channel**: Một channel cho tất cả publishes. Nếu channel đóng (do lỗi network), tất cả publishes đều fail.
3. **Single Redis Client**: Một client cho tất cả cache operations. Redis client có connection pool internally, nhưng không configurable.
4. **DB Connection Pool (10)**: 10 connections shared giữa HTTP handlers + outbox workers. Nếu có nhiều concurrent requests, pool có thể cạn.

---

## 23. Kết Luận

URL Service là một microservice được thiết kế tốt với các pattern enterprise:

1. **Transactional Outbox** đảm bảo reliable event publishing.
2. **Cache-Aside** với Redis giảm latency cho redirect path.
3. **Cryptographic random** cho short code đảm bảo security.
4. **Graceful degradation** khi Redis không available.
5. **Cursor-based pagination** cho URL listing.
6. **Exponential backoff** cho RabbitMQ connection.

**Điểm mạnh:**
- Code sạch, dễ đọc, không framework.
- Error handling nhất quán.
- Graceful shutdown đầy đủ.
- Security best practices (crypto/rand, parameterized queries, IP hashing).

**Điểm yếu:**
- Không có rate limiting.
- Không có metrics cho outbox processing (số events pending, processing time).
- Không có health check cho Redis/RabbitMQ sau startup.
- `pgx.ErrNoRows` trong `FindByUserID` trả về 200 OK với empty list — inconsistent với error handling khác.
- Anonymous URLs không thể quản lý (list/deactivate).

**Khuyến nghị cải thiện:**
1. Thêm rate limiting (token bucket hoặc sliding window).
2. Thêm metrics cho outbox (pending count, processing duration).
3. Thêm Redis/RabbitMQ health checks.
4. Dùng UUIDv7 thay vì UUIDv4 cho cursor-based pagination.
5. Thêm connection recovery cho Redis.
6. Thêm channel pool cho RabbitMQ publisher.
7. Thêm validation độ dài URL tối đa.

---

## 24. Phân Tích Chi Tiết Các Shared Package

### 24.1. Shared Events Package

Service sử dụng các event types từ `github.com/ikniz/url-shortener/shared/events`:

| Event Type | Struct | Routing Key | Khi nào phát sinh |
|---|---|---|---|
| `url.created` | `URLCreatedEvent` | `url.created` | ShortenURL thành công |
| `url.deleted` | `URLDeletedEvent` | `url.deleted` | DeactivateURL thành công |
| `url.clicked` | `URLClickedEvent` | `url.clicked` | Mỗi lần redirect |

**Phân tích event structs:**

**URLCreatedEvent:**
```go
type URLCreatedEvent struct {
    BaseEvent
    ShortCode   string     `json:"short_code"`
    OriginalURL string     `json:"original_url"`
    UserID      string     `json:"user_id"`
    UserEmail   string     `json:"user_email"`
    ExpiresAt   *time.Time `json:"expires_at"`
}
```

**URLDeletedEvent:**
```go
type URLDeletedEvent struct {
    BaseEvent
    ShortCode string `json:"short_code"`
    UserID    string `json:"user_id"`
    UserEmail string `json:"user_email"`
}
```

**URLClickedEvent:**
```go
type URLClickedEvent struct {
    BaseEvent
    ShortCode string    `json:"short_code"`
    UserID    string    `json:"user_id"`
    UserEmail string    `json:"user_email"`
    IPHash    string    `json:"ip_hash"`
    UserAgent string    `json:"user_agent"`
    Referer   string    `json:"referer"`
    ClickedAt time.Time `json:"clicked_at"`
}
```

**BaseEvent:**
```go
type BaseEvent struct {
    ID        string    `json:"id"`
    EventType string    `json:"event_type"`
    Timestamp time.Time `json:"timestamp"`
}
```

- `NewBaseEvent(eventType, id)` sinh UUID nếu id rỗng.
- `Timestamp` được set tại thời điểm tạo event.

---

## 24. Phân Tích Chi Tiết Các Shared Package

### 24.1. Shared Auth Package

Service dùng `github.com/ikniz/url-shortener/shared/auth` cho JWT authentication:

- `auth.JWTMiddleware(secret)` — middleware trích xuất JWT từ `Authorization: Bearer <token>` header.
- `auth.ClaimsFromContext(ctx)` — lấy claims từ context (set bởi middleware).
- Claims chứa `Sub` (user ID) và `Email`.

### 24.2. Shared Logger Package

Service dùng `github.com/ikniz/url-shortener/shared/logger`:

- `logger.New(serviceName)` — tạo structured logger với service name.
- Dùng `slog.Logger` (Go 1.21+ structured logging).
- Service name được set là `"url-service"`.

### 24.3. Shared Events Package

Các event types:

```go
const (
    EventTypeURLCreated = "url.created"
    EventTypeURLDeleted = "url.deleted"
    EventTypeURLClicked = "url.clicked"
)
```

Mỗi event có `BaseEvent` chứa:
- `ID`: UUID duy nhất cho mỗi event (dùng cho deduplication).
- `EventType`: Routing key.
- `Timestamp`: Thời gian tạo event.

---

*Báo cáo được tạo bởi Agent AI — phân tích toàn bộ source code của URL Service.*
*Tổng cộng: ~2.500 dòng phân tích.*

