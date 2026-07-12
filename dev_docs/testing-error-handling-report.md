# BÁO CÁO CHI TIẾT: TESTING, ERROR HANDLING, EDGE CASES

**Môn học:** SE361.Q21 - Kiểm thử và Đảm bảo Chất lượng Phần mềm  
**Dự án:** URL Shortener Microservices  
**Công nghệ:** Go, PostgreSQL, Redis, RabbitMQ, Docker, k6, Prometheus, Grafana  

---

## MỤC LỤC

1. [TỔNG QUAN KIẾN TRÚC](#1-tổng-quan-kiến-trúc)
2. [UNIT TESTS](#2-unit-tests)
   - 2.1 [gateway/gateway_test.go](#21-gatewaygateway_testgo)
   - 2.2 [services/url-service/url_test.go](#22-servicesurl-serviceurl_testgo)
   - 2.3 [services/user-service/user_test.go](#23-servicesuser-serviceuser_testgo)
   - 2.4 [services/analytics-service/analytics_test.go](#24-servicesanalytics-serviceanalytics_testgo)
   - 2.5 [services/notification-service/notification_test.go](#25-servicesnotification-servicenotification_testgo)
   - 2.6 [shared/events/events_test.go](#26-sharedeventsevents_testgo)
3. [PHÂN TÍCH CHI TIẾT KEY TEST CASES](#3-phân-tích-chi-tiết-key-test-cases)
   - 3.1 [JWT Middleware](#31-jwt-middleware)
   - 3.2 [Rate Limit](#32-rate-limit)
   - 3.3 [Circuit Breaker](#33-circuit-breaker)
   - 3.4 [Invalid Event Handling](#34-invalid-event-handling)
   - 3.5 [Analytics Dedup](#35-analytics-dedup)
   - 3.6 [Milestone Uniqueness](#36-milestone-uniqueness)
   - 3.7 [Deactivated URL](#37-deactivated-url)
4. [SMOKE/E2E TESTS](#4-smokee2e-tests)
5. [LOAD TEST (k6)](#5-load-test-k6)
   - 5.1 [Happy Path Load Test](#51-happy-path-load-test)
   - 5.2 [Failure Path / Circuit Breaker Demo](#52-failure-path--circuit-breaker-demo)
6. [ERROR HANDLING MATRIX CHI TIẾT](#6-error-handling-matrix-chi-tiết)
   - 6.1 [URL Service Errors](#61-url-service-errors)
   - 6.2 [User Service Errors](#62-user-service-errors)
   - 6.3 [Analytics Service Errors](#63-analytics-service-errors)
   - 6.4 [Notification Service Errors](#64-notification-service-errors)
   - 6.5 [Gateway Errors](#65-gateway-errors)
7. [ERROR RESPONSE FORMAT](#7-error-response-format)
8. [LOGGING STRATEGY](#8-logging-strategy)
9. [DATABASE ERROR HANDLING](#9-database-error-handling)
10. [RABBITMQ ERROR HANDLING](#10-rabbitmq-error-handling)
11. [REDIS ERROR HANDLING](#11-redis-error-handling)
12. [EDGE CASES](#12-edge-cases)
    - 12.1 [Short Code Collision](#121-short-code-collision)
    - 12.2 [URL Expiry](#122-url-expiry)
    - 12.3 [Concurrent Deactivate + Redirect](#123-concurrent-deactivate--redirect)
    - 12.4 [Outbox Worker Concurrency](#124-outbox-worker-concurrency)
    - 12.5 [Timing Attack Protection](#125-timing-attack-protection)
    - 12.6 [Panic Recovery trong Consumer](#126-panic-recovery-trong-consumer)
    - 12.7 [Correlation ID Tracing](#127-correlation-id-tracing)
13. [HEALTH CHECK MATRIX](#13-health-check-matrix)
14. [PROMETHEUS METRICS](#14-prometheus-metrics)
15. [KẾT LUẬN](#15-kết-luận)

---

## 1. TỔNG QUAN KIẾN TRÚC

Dự án URL Shortener Microservices bao gồm **5 services** chính, mỗi service chạy trên một port riêng biệt:

| Service | Port | Mô tả |
|---------|------|-------|
| **Gateway** | 8080 | Reverse proxy, JWT auth, rate limiting, circuit breaker |
| **URL Service** | 8081 | Core business logic: shorten, redirect, CRUD URL |
| **Analytics Service** | 8082 | Click tracking, stats, milestones |
| **User Service** | 8083 | Authentication: register, login, JWT issuance |
| **Notification Service** | 8084 | User notifications via outbox pattern |

Kiến trúc tổng thể sử dụng:
- **PostgreSQL** (4 database instances riêng biệt) cho persistent storage
- **Redis** cho caching URL records và rate limiting counters
- **RabbitMQ** (topic exchange) cho event-driven communication giữa các services
- **Docker Compose** cho orchestration local development
- **Prometheus + Grafana** cho monitoring

---

## 2. UNIT TESTS

### 2.1 gateway/gateway_test.go

**File:** `gateway/gateway_test.go` — 241 dòng, 5 test functions chính

#### TestCircuitBreakerTransitions (dòng 28-59)

Test này kiểm tra toàn bộ vòng đời của Circuit Breaker pattern qua 5 giai đoạn:

```
CLOSED → (5 failures) → OPEN → (timeout) → HALF_OPEN → (success) → CLOSED
                                                      → (failure) → OPEN
```

**Phân tích chi tiết từng bước:**

1. **Pha 1: Accumulate failures → OPEN** (dòng 31-38)
   ```go
   cb := NewCircuitBreaker(2, time.Millisecond, time.Second)
   for i := 0; i < 2; i++ {
       cb.Do(ctx, func() error { return errors.New("upstream failed") })
   }
   ```
   - Khởi tạo CB với `maxFailures=2`, `openTimeout=1ms`, `failureWindow=1s`
   - Gửi 2 request đều fail → CB chuyển từ CLOSED → OPEN
   - Kiểm tra `State()` trả về `StateOpen`
   - **Ý nghĩa:** Đảm bảo cơ chế đếm failure hoạt động chính xác, đúng ngưỡng là OPEN

2. **Pha 2: OPEN rejects immediately** (dòng 40-42)
   ```go
   err := cb.Do(ctx, func() error { return nil })
   errors.Is(err, ErrCircuitOpen) // must be true
   ```
   - Khi OPEN, mọi request đều bị từ chối ngay lập tức mà không gọi upstream function
   - Trả về sentinel error `ErrCircuitOpen`
   - **Ý nghĩa:** Fail-fast, không waste resources gọi upstream đang chết

3. **Pha 3: After timeout → HALF_OPEN probe** (dòng 44-47)
   ```go
   time.Sleep(2 * time.Millisecond)
   err := cb.Do(ctx, func() error { return nil }) // probe success
   ```
   - Sleep qua `openTimeout` (1ms) → CB tự động chuyển HALF_OPEN
   - 1 request được phép đi qua (probe), thành công → CLOSED
   - **Ý nghĩa:** Self-healing mechanism hoạt động

4. **Pha 4: HALF_OPEN probe failure → back to OPEN** (dòng 52-58)
   ```go
   cb.state = StateHalfOpen // force state
   err := cb.Do(ctx, func() error { return errors.New("probe failed") })
   // cb.State() == StateOpen
   ```
   - Set manual state HALF_OPEN, probe fail
   - CB quay lại OPEN, reset timer
   - **Ý nghĩa:** Nếu service vẫn chết sau timeout, không để request đi qua

#### TestRateLimitRejectsAndFailOpen (dòng 61-94)

Test kiểm tra 2 kịch bản: rate limit bị vượt và fail-open khi Redis down.

1. **Over limit → 429** (dòng 62-79):
   ```go
   limiter := &fakeRateLimiter{allowed: false, retryAfter: 42}
   ```
   - Dùng `fakeRateLimiter` interface mock
   - Giả lập rate limit bị vượt với `retryAfter=42` giây
   - Request đến `/api/shorten` với IP `192.0.2.10`
   - Kiểm tra:
     - Status code: **429 Too Many Requests** ✅
     - Header `Retry-After: 42` ✅
     - Rate limit key format: `shorten:192.0.2.10` (route:IP) ✅

2. **Redis unavailable → fail-open → 201** (dòng 81-94):
   ```go
   limiter = &fakeRateLimiter{allowed: true, err: errors.New("redis down")}
   ```
   - `fakeRateLimiter` trả về `(true, 0, error)` — allowed=true despite error
   - Request vẫn được forward đến upstream, nhận 201 Created
   - **Fail-open pattern:** Khi Redis không available, rate limiter cho phép request đi qua, tránh false positive rate limiting

#### TestRouterAndPathRewrite (dòng 96-123)

Kiểm tra path rewriting cho redirect route.

- **Input:** GET `/r/abc1234`
- **Expected upstream path:** `/abc1234` (strip prefix `/r`)
- **Kiểm tra:** Proxy gửi request với path đã được rewrite
- **Ý nghĩa:** Gateway phải strip prefix để URL service nhận đúng short code

Chi tiết implementation từ `router.go`:
```go
{Method: "GET", PathPrefix: "/r/", Upstream: "url-service", StripPrefix: "/r", ...}
```

Và `handler.go`:
```go
if route.StripPrefix != "" && strings.HasPrefix(upstreamPath, route.StripPrefix) {
    upstreamPath = upstreamPath[len(route.StripPrefix):]
}
```

#### TestJWTMiddlewareProtectsPrivateRoutes (dòng 125-149)

Kiểm tra JWT middleware bảo vệ private routes.

1. **Missing Authorization header → 401:**
   - Request đến `/api/me` (requiresAuth=true) không có token
   - `jwtMiddleware` kiểm tra header → empty → 401

2. **Valid token → 204:**
   - Tạo JWT hợp lệ với `sub=user-1`
   - Request có `Authorization: Bearer <token>`
   - `auth.VerifyToken` parse thành công
   - Claims được inject vào context
   - Next handler gọi `auth.ClaimsFromContext` và verify `claims.Sub == "user-1"`

#### TestJWTMiddlewareSkipsPublicRoutes (dòng 151-168)

Kiểm tra public routes bypass JWT check.

- **Route:** POST `/api/auth/login` (requiresAuth=false)
- **Kết quả:** `next` handler được gọi, không kiểm tra token
- **Ý nghĩa:** Login/Register endpoints không yêu cầu authentication

#### TestCorsMiddleware (dòng 194-240)

Kiểm tra CORS headers.

1. **Preflight OPTIONS:** 
   - Status 204 No Content
   - Không gọi `next` handler
   - Headers: `Access-Control-Allow-Origin: *`

2. **Regular GET:**
   - Gọi `next` handler
   - Status 200 OK
   - CORS header vẫn được set

#### Test Doubles trong gateway_test.go

```go
type fakeRateLimiter struct {
    allowed    bool
    retryAfter int
    err        error
    key        string
}
```

Mock implementation của `rateLimiter` interface, cho phép test rate limiting behavior mà không cần Redis thật.

---

### 2.2 services/url-service/url_test.go

**File:** `services/url-service/url_test.go` — 779 dòng, 8 test functions chính

Đây là file test lớn nhất với coverage trải dài từ base62 encoding, code generation, caching, đến HTTP handler testing.

#### Base62 Tests

**TestBase62RoundTrip (dòng 22-55)**

Kiểm tra base62 encode/decode round-trip với các giá trị đặc biệt:

| Input | Expected Behavior |
|-------|------------------|
| `0` | Encode → "0000000", Decode → "0" |
| `1` | "0000001" |
| `61` | "000000z" (max 1-char) |
| `62` | "0000010" (carry-over) |
| `12345` | test value |
| `62^7 - 1 = 3521614606207` | Max representable 7-char base62 |
| `62^7 = 3521614606208` | Mod wraps to 0 |

Vì `Encode` thực hiện `n % 62^7`, decoded value phải bằng `val % 62^7`.

Implementation:
```go
func Encode(n *big.Int) string {
    n = new(big.Int).Abs(n)
    limit := new(big.Int).Exp(big.NewInt(62), big.NewInt(shortCodeLength), nil)
    n.Mod(n, limit)
    // build string from right to left
}
```

**TestBase62DecodeErrors (dòng 57-71)**

Kiểm tra decode với input không hợp lệ:

| Input | Lý do |
|-------|-------|
| `""` | Empty string |
| `"123456"` | Too short (6 chars, cần 7) |
| `"12345678"` | Too long (8 chars) |
| `"123456?"` | Invalid character `?` |
| `"abc-xyz"` | Invalid character `-` |

#### Codegen Tests

**TestCodegen (dòng 76-103)**

Kiểm tra short code generator sử dụng `crypto/rand`:

1. **Length:** Output phải đúng 7 ký tự
2. **Character set:** Mỗi ký tự trong base62 alphabet (0-9, A-Z, a-z)
3. **Uniqueness:** 2 lần generate liên tiếp phải khác nhau

Implementation note từ `codegen.go`:
```go
func (g *cryptoRandGenerator) Generate() string {
    b := make([]byte, 8)
    _, err := rand.Read(b)
    if err != nil {
        panic(err) // System entropy failure is unrecoverable
    }
    n := new(big.Int).SetBytes(b)
    return Encode(n)
}
```

**Probabilistic analysis:** 62^7 ≈ 3.5 trillion codes. Sau 1 million URLs, collision probability ≈ 1.4 × 10⁻⁷ per attempt.

#### Cache Tests

**TestRedisCache (dòng 108-189)**

Sử dụng **miniredis** — in-memory Redis implementation for testing.

1. **Cache Miss** (dòng 124-130):
   - `cache.Get(ctx, "miss123")` → `(nil, nil)`
   - Không có error, result nil

2. **Cache Hit** (dòng 132-161):
   - `cache.Set` → `cache.Get` → verify all fields
   - Truncate time để tránh subsecond serialization differences
   - Kiểm tra: OriginalURL, IsActive, ExpiresAt

3. **Cache Delete** (dòng 163-174):
   - Set → Delete → Get → nil
   - Verify deletion works correctly

4. **Error Fallback (non-fatal)** (dòng 176-188):
   - Tạo Redis client với non-existent port `localhost:9999`
   - `cache.Get` không trả về error (fail-open)
   - Trả về `nil` — treat as cache miss
   - **Ý nghĩa:** Redis unavailable không làm crash service

#### Mock Definitions (dòng 193-276)

Các mock implementations cho dependency injection:

- **mockTx:** Mock `pgx.Tx` với Commit/Rollback no-op
- **mockPool:** Mock `pgxPool` interface, trả về mockTx
- **mockURLStore:** Mock `URLStore` interface với các function hooks
- **mockOutboxStore:** Mock `OutboxStore` interface
- **mockCache:** Mock `Cache` interface
- **mockGenerator:** Mock `ShortCodeGenerator` interface

Mỗi mock sử dụng function fields để cho phép test-specific behavior.

#### TestHandlerShorten (dòng 278-425)

4 subtests cho shorten flow:

**Subtest 1: Success** (dòng 282-347)
- Mô phỏng shorten URL thành công
- Kiểm tra:
  - Status 201 Created
  - Response body: `short_code`, `short_url`
  - store.Insert được gọi
  - outbox.InsertEvent được gọi (cho event-driven notification)
  - cache.Set được gọi (async, sleep 10ms để chờ)

**Subtest 2: Collision Retry Success** (dòng 349-400)
```go
store.insertFn attempts: 1 → pgErr Code "23505" (unique violation)
                        2 → success
```
- Lần 1: collision → retry với backoff
- Lần 2: thành công
- Kiểm tra: `attempts == 2`, `ShortCode == "succ123"`

**Ý nghĩa:** Hệ thống tự động retry khi gặp short code collision, với backoff 50ms × attempt.

**Subtest 3: Unauthorized** (dòng 402-412)
- Request không có auth context (no claims)
- Status **401 Unauthorized**
- **Ý nghĩa:** Bảo vệ endpoint yêu cầu authentication

**Subtest 4: Invalid URL** (dòng 414-424)
- Input: `{"url":"invalid-url-no-scheme"}`
- Status **400 Bad Request**
- ValidateURL kiểm tra scheme http/https và host

#### TestHandlerRedirect (dòng 427-628)

5 subtests cho redirect flow:

**Subtest 1: Cache Hit** (dòng 428-481)
- Cache trả về URL hợp lệ, is_active=true
- **Không gọi** store.FindByCode (cache đã có)
- Status **308 Permanent Redirect**
- Location header đúng
- Outbox event được insert cho analytics

Chi tiết flow:
```
Request → Cache.Get("abc1234") → hit → check is_active → check expiry → 308
```

**Subtest 2: Cache Miss → DB Hit** (dòng 483-545)
- Cache miss → DB query → find record → set cache async → 308
- Kiểm tra: cache.Get, store.FindByCode, cache.Set, outbox.InsertEvent

Flow:
```
Request → Cache.Get → miss → DB.FindByCode → hit → Cache.Set(async) → 308
```

**Subtest 3: Deactivated URL** (dòng 547-573)
- Cache miss, DB trả về `is_active=false`
- Status **410 Gone**
- **Ý nghĩa:** Ngăn redirect đến URL đã bị deactivate

**Subtest 4: Expired URL** (dòng 575-603)
- DB trả về `ExpiresAt` trong quá khứ
- Status **410 Gone**
- **Ý nghĩa:** URL hết hạn không thể redirect

**Subtest 5: Not Found** (dòng 605-627)
- DB trả về `pgx.ErrNoRows`
- Status **404 Not Found**
- **Ý nghĩa:** Short code không tồn tại

#### TestHandlerList (dòng 630-706)

2 subtests cho list URLs với pagination.

**Subtest 1: Success** (dòng 634-674)
- Mock store trả về 2 records
- Kiểm tra:
  - Status 200 OK
  - Response có 2 URLs
  - `has_more = false`
  - `next_cursor = ""`

**Subtest 2: Pagination HasMore** (dòng 676-705)
- Mock store trả về 3 records (limit=2 → fetch 3 để detect has_more)
- Kiểm tra:
  - Response có 2 URLs (slice extra record)
  - `has_more = true`
  - `next_cursor = "id2"`

Implementation logic:
```go
hasMore := len(urls) > limit
if hasMore {
    urls = urls[:limit]
    nextCursor = urls[len(urls)-1].ID
}
```

#### TestHandlerDelete (dòng 708-779)

2 subtests cho deactivate URL.

**Subtest 1: Success** (dòng 712-758)
- store.Deactivate thành công
- outbox.InsertEvent được gọi (URLDeletedEvent)
- cache.Delete được gọi
- Status **204 No Content**

**Subtest 2: Forbidden** (dòng 761-778)
- store.Deactivate trả về `pgx.ErrNoRows` (user_id không match)
- Status **403 Forbidden**
- **Ý nghĩa:** Chỉ owner mới có thể deactivate URL

---

### 2.3 services/user-service/user_test.go

**File:** `services/user-service/user_test.go` — 372 dòng, 16 test functions

#### Input Validation Tests

**TestValidateEmail (dòng 44-65)**

| Input | Expected |
|-------|----------|
| `"user@example.com"` | Valid |
| `"user+tag@sub.domain.org"` | Valid (plus addressing) |
| `""` | Invalid (empty) |
| `"notanemail"` | Invalid (no @) |
| `"@nodomain.com"` | Invalid (no local part) |
| `"user@"` | Invalid (no domain) |
| `"user @example.com"` | Invalid (space) |

Regex pattern: `^[^@\s]+@[^@\s]+\.[^@\s]+$`

**TestValidatePassword (dòng 67-86)**

| Input | Expected |
|-------|----------|
| `"12345678"` | Valid (8 chars) |
| `"longerpassword"` | Valid |
| `"1234567"` | Invalid (7 chars) |
| `""` | Invalid (empty) |
| `"        7"` | Valid (8 chars với spaces) |

Rule: Minimum 8 characters.

#### Password Hashing Tests

**TestBcryptHasher_HashAndVerify (dòng 88-103)**
- Hash password → verify đúng → success
- Verify sai → `ErrPasswordMismatch`

**TestBcryptHasher_DifferentHashesSamePassword (dòng 105-118)**
- Cùng password hash 2 lần → khác nhau (vì bcrypt random salt)
- Cả 2 hash đều verify được với password gốc

#### JWT Token Tests

**TestJWTTokenIssuer_IssueAndVerify (dòng 120-145)**
- Issue token với `sub`, `email`, `iss`, `iat`, `exp`
- Verify token → claims đúng
- Kiểm tra `expiresAt` gần đúng (trong vòng 23-24h)

**TestJWTTokenIssuer_Verify_InvalidSignature (dòng 147-155)**
- Token ký với secret1 → verify với secret2 → `ErrTokenInvalid`
- **Ý nghĩa:** Wrong secret không thể verify

**TestJWTTokenIssuer_Verify_Malformed (dòng 157-163)**
- Input: `"not.a.jwt"` → `ErrTokenInvalid`
- **Ý nghĩa:** Graceful error handling cho malformed token

**TestJWTTokenIssuer_Verify_Expired (dòng 165-172)**
- Issue token với TTL = -1h (đã hết hạn)
- Verify → `ErrTokenInvalid`
- **Ý nghĩa:** Expired token bị reject

#### HTTP Handler Tests

**TestRegisterHandler_ShortPassword (dòng 174-193)**
- Input: password 7 ký tự
- Status **400 Bad Request**
- Response có field `"field": "password"`
- **Ý nghĩa:** Validation error trả về field-level error message

**TestRegisterHandler_Success (dòng 246-273)**
- Register thành công → Status **201 Created**
- Response: `user_id`, `email`
- **Không chứa** password/password_hash trong response

**TestRegisterHandler_DuplicateEmail (dòng 275-290)**
- Mock store.Insert trả về `ErrDuplicateEmail`
- Status **409 Conflict**
- Response: `"email already registered"`

**TestLoginHandler_UnknownEmail (dòng 195-218)**
- User không tồn tại → 401 "invalid credentials"
- **Timing attack protection:** Gọi `hasher.Verify(password, dummyBcryptHash)` để đồng bộ thời gian
- **Ý nghĩa:** Attacker không thể phân biệt email tồn tại hay không qua response time

**TestLoginHandler_WrongPassword (dòng 220-244)**
- Email tồn tại nhưng sai password → 401 "invalid credentials"
- Response message giống hệt unknown email

**TestLoginHandler_Success (dòng 292-320)**
- Login đúng → Status **200 OK**
- Response: `token`, `expires_at`

**TestLoginHandler_InvalidCredentials (dòng 322-339)**
- Sai credentials → Status **401 Unauthorized**

**TestMeHandler_ValidToken (dòng 341-361)**
- Có claims trong context → Status **200 OK**
- Response: `user_id`, `email`

**TestMeHandler_NoClaims (dòng 363-372)**
- Không có claims → Status **401 Unauthorized**

---

### 2.4 services/analytics-service/analytics_test.go

**File:** `services/analytics-service/analytics_test.go` — 376 dòng

#### Click Consumer Tests

**TestClickConsumer_ParseMalformedJSONAcks (dòng 20-31)**
- Input: `{bad-json` (malformed)
- `parseDelivery` → `ack` (không requeue)
- Kiểm tra: `acks=1, nacks=0`
- **Ý nghĩa:** Malformed JSON không làm hỏng queue, discard ngay

**TestClickConsumer_ParseMissingEventIDAcks (dòng 33-44)**
- Input: `{"short_code":"abc123"}` (không có EventID)
- `parseDelivery` → `ack`
- **Ý nghĩa:** Event thiếu EventID được xem là invalid, discard

**TestClickConsumer_ClickRecordUsesEventIPHash (dòng 46-57)**
- Kiểm tra `clickRecordFromEvent` giữ nguyên IPHash
- Các field khác: ShortCode, UserAgent, Referer

#### Stats Handler Tests

**TestStatsHandler_UnknownCodeReturnsZeros (dòng 59-81)**
- Short code không tồn tại → Status **200 OK**
- `TotalClicks=0, ClicksLast24h=0, ClicksLast7d=0`
- `TopReferers=[]` (empty slice, không phải null)

**TestStatsHandler_TopReferersLimitIsFive (dòng 83-98)**
- Kiểm tra `statsTopReferersLimit == 5`
- **Ý nghĩa:** Chỉ lấy top 5 referers

**TestStatsHandler_StatsDBErrorReturns500 (dòng 100-112)**
- DB error → Status **500 Internal Server Error**

**TestStatsHandler_TimeLineInvalidIntervalsReturn400 (dòng 114-130)**
- Test các interval không hợp lệ: `"week"`, `"month"`, `""`, `"DAY"`, `"Hour"`
- Tất cả đều trả về **400 Bad Request**
- **Ý nghĩa:** Chỉ chấp nhận `"day"` và `"hour"` (case-sensitive)

**TestStatsHandler_TimeLineValidIntervalsReturnEmptyPoints (dòng 132-161)**
- Test `"day"` và `"hour"` → Status **200 OK**
- Response có `Points=[]` (empty slice)

#### Milestone Tests

**TestMilestoneChecker_NoMilestoneBelow10 (dòng 163-175)**
- `totalClicks=9` → không có milestone nào
- `insertCount=0, events=0`

**TestMilestoneChecker_Threshold10Triggered (dòng 177-197)**
- `totalClicks=10` → milestone 10 được trigger
- Kiểm tra:
  - Milestone được insert (inserted[10]=1)
  - MilestoneReachedEvent được publish
  - Event payload đúng: ShortCode, UserID, UserEmail, MilestoneN, TotalClicks, CorrelationID

**TestMilestoneChecker_AlreadyRecorded_NoPublish (dòng 199-212)**
- Milestone 10 đã được ghi nhận trước đó
- `CheckAndPublish` không insert, không publish
- **Ý nghĩa:** Idempotent, không duplicate milestone

**TestMilestoneChecker_PublishFailureContinues (dòng 214-227)**
- Publisher trả về error (rabbitmq down)
- Milestone vẫn được insert (DB transaction)
- **Ý nghĩa:** Non-blocking failure — không để publish fail làm hỏng main flow

**TestMilestoneChecker_MultipleThresholdsAtOnce (dòng 229-246)**
- `totalClicks=1000` → cả 3 thresholds được trigger cùng lúc
- `insertCount=3, events=3`
- Các thresholds: 10, 100, 1000

---

### 2.5 services/notification-service/notification_test.go

**File:** `services/notification-service/notification_test.go` — 227 dòng

**TestNotificationConsumer_UsesRoutingKeyAsEventType (dòng 18-42)**
- Routing key `url.created` → EventType trong record là `url.created`
- Kiểm tra eventID không empty
- Chưa ack/nack trước khi insert (chờ processDelivery)

**TestNotificationConsumer_UnknownRoutingKeyAcksNoInsert (dòng 44-65)**
- Routing key `unknown.event` → ack + discard
- `insertCount=0`

**TestNotificationConsumer_MilestoneEmptyUserIDAcksNoInsert (dòng 67-90)**
- Milestone event với `UserID=""` → ack + discard
- **Ý nghĩa:** Milestone cần UserID để tạo notification

**TestNotificationHandler_InvalidAfterCursorReturns400 (dòng 92-106)**
- `after=not-a-uuid` → **400 Bad Request**
- `store.listCount=0` (không query DB)

**TestNotificationHandler_EmptyNotificationsResponse (dòng 108-129)**
- Không có notifications → Status **200 OK**
- `Notifications=[]` (empty slice)
- `NextCursor=nil`

**TestNotificationHandler_LimitDefaultAndMax (dòng 131-157)**
- Mặc định: 20
- Max: 100 (nếu request limit > 100)

---

### 2.6 shared/events/events_test.go

**File:** `shared/events/events_test.go` — 117 dòng

**TestJSONRoundTrip (dòng 9-117)**

Kiểm tra JSON serialization cho 4 event types:

1. **URLCreatedEvent:** EventID, ShortCode, OriginalURL, UserID, UserEmail, ExpiresAt
2. **URLClickedEvent:** IPHash, UserAgent, Referer, ClickedAt
3. **URLDeletedEvent:** UserID
4. **MilestoneReachedEvent:** MilestoneN, TotalClicks

Mỗi test:
- Marshal struct → JSON bytes
- Unmarshal JSON → struct
- Verify fields match

**BaseEvent structure:**
```go
type BaseEvent struct {
    EventType     string    `json:"event_type"`
    OccurredAt    time.Time `json:"occurred_at"`
    CorrelationID string    `json:"correlation_id"`
    EventID       string    `json:"event_id"`
}
```

Mỗi event được tạo với `NewBaseEvent(eventType, correlationID)`, tự động sinh UUID v4 cho EventID.

---

## 3. PHÂN TÍCH CHI TIẾT KEY TEST CASES

### 3.1 JWT Middleware

**Middleware implementation** (`gateway/jwtmiddleware.go` và `shared/auth/middleware.go`):
- Gateway sử dụng `jwtMiddleware` cho route-level JWT checking
- User service sử dụng `auth.JWTMiddleware` cho /me endpoint
- URL service sử dụng `auth.JWTMiddleware` cho protected endpoints

| Test Case | Input | Expected | Implementation |
|-----------|-------|----------|----------------|
| Missing Authorization header | GET /api/me, no headers | 401 | `authHeader == ""` → writeError |
| Invalid token format | `Authorization: Basic xyz` | 401 | `!strings.HasPrefix(authHeader, "Bearer ")` → 401 |
| Wrong signing algorithm | Token signed with HMAC-SHA512 | 401 | `token.Method.(*jwt.SigningMethodHMAC)` check fail |
| Expired token | Token with exp in past | 401 | `jwt.Parse` returns validation error |
| Valid token | Token with correct secret | pass to next | Claims injected to context |
| Public route bypass | POST /api/auth/login | pass without check | `route.RequiresAuth == false` |

**VerifyToken implementation** (`shared/auth/auth.go:60-123`):
1. Parse JWT với key function kiểm tra signing method
2. Chỉ chấp nhận HMAC (HS256, HS384, HS512)
3. Kiểm tra issuer là `"url-shortener"`
4. Kiểm tra `sub`, `email`, `iat`, `exp` claims tồn tại

### 3.2 Rate Limit

**Implementation** (`gateway/ratelimit.go`):

```
RateLimitConfig:
  Shorten:  10 req / 60s window
  Redirect: 300 req / 60s window
```

| Test Case | Input | Expected | Notes |
|-----------|-------|----------|-------|
| Under limit | 9 requests to /api/shorten | pass | INCR returns ≤ 10 |
| Over limit | 11th request | 429 Retry-After header | INCR returns > limit |
| Redis unavailable | Redis down | fail-open (pass) | err != nil → return (true, 0, err) |
| Per-IP isolation | Different IPs | separate counters | Key format: `"shorten:<client_ip>"` |

**Rate limit key generation:**
```go
func rateLimitKey(routeKey, ip string) string {
    return fmt.Sprintf("%s:%s", routeKey, ip)
}
```

**Client IP resolution** (ưu tiên):
1. `X-Forwarded-For` header (first IP)
2. `X-Real-IP` header
3. `RemoteAddr` parsed from `host:port`

**100ms timeout cho Redis operations** để không block request lâu.

### 3.3 Circuit Breaker

**Implementation** (`gateway/circuitbreaker.go`):

**State machine:**

```
                    +-----------+
                    |  CLOSED   |
                    +-----+-----+
                          |
                5 failures trong 10s
                          |
                    +-----v-----+
                    |    OPEN   |  ← reject all requests immediately
                    +-----+-----+
                          |
                30s timeout elapsed
                          |
                    +-----v-----+
                    | HALF_OPEN |  ← probe 1 request
                    +-----+-----+
                     /         \
               success         failure
                   /               \
          +------v------+     +-----v-----+
          |   CLOSED    |     |   OPEN    |
          +-------------+     +-----------+
```

| Test Case | Action | Expected State | Expected Error |
|-----------|--------|----------------|----------------|
| Normal operation | 4 failures | CLOSED | upstream error |
| Open threshold | 5th failure | OPEN | upstream error |
| Request during OPEN | any request | OPEN | `ErrCircuitOpen` (503) |
| After 30s | probe request | HALF_OPEN | no error |
| Probe success | probe returns nil | CLOSED | no error |
| Probe failure | probe returns error | OPEN | upstream error |

**Concurrency safety:** `sync.Mutex` bảo vệ state transitions.

**State change callback:** `WithStateChange(fn)` cho phép ghi metrics (Prometheus gauge).

### 3.4 Invalid Event Handling

**Click Consumer** (`services/analytics-service/consumer.go`):

```go
func (c *ClickConsumer) parseDelivery(delivery amqp.Delivery) (*events.URLClickedEvent, bool) {
    // 1. Try JSON unmarshal
    //    fail → ack + return false
    // 2. Check EventID != ""
    //    empty → ack + return false
    // 3. Check ShortCode != ""
    //    empty → ack + return false
    // 4. Return event
}
```

| Scenario | Action | Reason |
|----------|--------|--------|
| Malformed JSON | ack + discard | Parse error → không recover được |
| Missing EventID | ack + discard | Không thể dedup |
| Missing ShortCode | ack + discard | Không thể xác định URL |
| Valid event | process normally | |

**Notification Consumer** (`services/notification-service/consumer.go`):

```go
func (c *NotificationConsumer) notificationFromDelivery(delivery amqp.Delivery) (*NotificationRecord, string, bool) {
    // 1. Parse BaseEvent → check EventID
    // 2. Switch on RoutingKey:
    //    - url.created → parse URLCreatedEvent
    //    - url.deleted → parse URLDeletedEvent
    //    - milestone.reached → parse MilestoneReachedEvent
    //    - default → ack + discard (unsupported routing key)
    // 3. Validate fields based on event type
    // 4. Return record or nil
}
```

| Scenario | Action | Reason |
|----------|--------|--------|
| Unsupported routing key | ack + discard | Unknown event type |
| Missing EventID | ack + discard | Invalid base structure |
| Missing UserID (milestone) | ack + discard | Không thể tạo notification |
| Missing UserEmail | ack + discard | Không thể gửi email |
| Valid event | insert notification + ack | |

### 3.5 Analytics Dedup

**Implementation** (`services/analytics-service/consumer.go:95-111` và `store.go`):

Dedup sử dụng `processed_events` table với `ON CONFLICT DO NOTHING`.

**Flow per click event:**
1. Parse delivery → get EventID
2. `dedupStore.Exists(ctx, tx, eventID)` — check trong transaction
3. If exists → ack + discard (duplicate)
4. If not exists → `dedupStore.Insert(ctx, tx, eventID)` — insert
5. Proceed with click insert + milestone check
6. Commit transaction

**SQL:**
```sql
SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)
INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING
```

| Test Case | Event ID | Expected |
|-----------|----------|----------|
| Same event_id arrives twice | "abc-123" | First: processed. Second: discard |
| Different event_id | "abc-123" vs "def-456" | Both processed normally |

### 3.6 Milestone Uniqueness

**Implementation** (`services/analytics-service/milestone.go`):

**MilestoneChecker** kiểm tra và publish milestone events:
1. Count total clicks trong transaction (`COUNT(*) FROM clicks WHERE short_code = $1`)
2. Với mỗi threshold (10, 100, 1000):
   - Nếu `totalClicks >= threshold`:
     - `milestoneStore.HasMilestone` — check đã tồn tại chưa
     - Nếu đã tồn tại → skip
     - Nếu chưa → insert + publish event

**Milestones table constraint:**
```sql
INSERT INTO milestones (short_code, milestone) VALUES ($1, $2)
ON CONFLICT (short_code, milestone) DO NOTHING
```

| Test Case | Scenario | Expected |
|-----------|----------|----------|
| First time reaching 100 | totalClicks=100, no milestone exists | Insert milestone + publish event |
| Click 101 | totalClicks=101, milestone exists | No duplicate insert/publish |
| Multiple thresholds at once | totalClicks=1000 | All 3 thresholds triggered |
| Publish failure | RabbitMQ down | Milestone still inserted (non-blocking) |

### 3.7 Deactivated URL

**Flow** (`services/url-service/service.go:RedirectToURL`):

```
Redirect Request
    │
    ├── Cache.Get(shortCode)
    │   ├── Hit → check is_active → false → 410 Gone
    │   └── Miss/Error
    │       └── DB.FindByCode(shortCode)
    │           ├── Not found → 404
    │           ├── is_active=false → 410 Gone
    │           ├── Expired → 410 Gone
    │           └── Valid → 308 Redirect + Cache.Set(async)
    │
    └── Response
```

| Test Case | Cache | DB | Expected |
|-----------|-------|-----|----------|
| Cache has is_active=false | Return inactive CachedURL | — | 410 Gone immediately |
| DB has is_active=false | Miss | Return inactive URLRecord | 410 Gone |
| Cache has is_active=true | Return active CachedURL | — | 308 Redirect (no DB call) |
| Expired in cache | ExpiresAt in past | — | 410 Gone |
| Expired in DB | Miss | ExpiresAt in past | 410 Gone |

---

## 4. SMOKE/E2E TESTS

### Smoke Test (`scripts/smoke_test.sh`)

Kiểm tra tất cả 5 services đều healthy qua HTTP health endpoint.

```bash
PORTS=(8080 8081 8082 8083 8084)
for port in "${PORTS[@]}"; do
    curl -s http://localhost:$port/health | grep -q '"status":"ok"'
done
```

- Max 30 retries, sleep 2s giữa mỗi lần
- Sử dụng trong CI/CD pipeline
- Kiểm tra kết nối giữa các container

### E2E Test (`scripts/e2e_test.sh`)

End-to-end flow test comprehensive với 11 bước:

| Step | Action | Verification |
|------|--------|-------------|
| 1. Register | POST /api/auth/register | Status 201 |
| 2. Login | POST /api/auth/login | Status 200, có token |
| 3. Shorten | POST /api/shorten | Status 201, có short_code |
| 4. Redirect x15 | GET /r/{code} × 15 lần | Status 301 hoặc 308 |
| 5. Wait | Sleep 5s | Chờ outbox + consumer |
| 6. Stats | GET /api/stats/{code} | total_clicks ≥ 15 |
| 7. Notifications | GET /api/notifications | Có milestone.reached |
| 8. Delete | DELETE /api/urls/{code} | Status 204 |
| 9. Deleted redirect | GET /r/{code} | Status 410 Gone |
| 10. Rate limit | POST /api/shorten × 11 | Ít nhất 1 lần 429 |
| 11. Correlation header | GET /health | X-Correlation-ID present |

**Edge case testing trong E2E:**
- 15 redirects để trigger milestone (threshold 10)
- Deleted URL trả về 410 (không redirect)
- Rate limit verification (dùng SHORTEN_RATE_LIMIT=10)
- Correlation ID tracing

---

## 5. LOAD TEST (k6)

### 5.1 Happy Path Load Test

**File:** `scripts/load_test.js` — 218 dòng

**Configuration:**
```javascript
export const options = {
  scenarios: {
    circuit_breaker_stress: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "20s", target: 200 },   // Warm-up
        { duration: "20s", target: 500 },   // Build up
        { duration: "20s", target: 1000 },  // Peak: ~10k req/s
        { duration: "60s", target: 1000 },  // Hold at peak
        { duration: "20s", target: 0 },     // Ramp down
      ],
    },
  },
};
```

**Workload pattern:**
1. **Setup phase:** Đăng ký user → login → shorten URL → get short code
2. **Main phase:** 1000 VUs gửi redirect requests liên tục
3. **Metrics tracking:**
   - `circuit_breaker_open_responses` (Counter) — đếm 503 responses
   - `error_rate` (Rate) — tỷ lệ error
   - `redirect_duration_ms` (Trend) — phân phối latency

**Custom metrics:**
```javascript
const cbOpenResponses = new Counter("circuit_breaker_open_responses");
const errorRate = new Rate("error_rate");
const redirectDuration = new Trend("redirect_duration_ms", true);
```

**Thresholds:**
```javascript
http_req_duration: ["p(95)<2000"],    // 95% under 2s
error_rate: ["rate<0.95"],             // Allow 95% errors during CB OPEN
```

### 5.2 Failure Path / Circuit Breaker Demo

Kịch bản test circuit breaker behavior:

1. **Start load test:** k6 ramp đến 1000 VUs (~10k req/s)
2. **Kill url-service:** `docker compose stop url-service`
3. **Gateway nhận connection errors** → CB đếm failures
4. **Sau 5 failures/10s** → CB OPEN
5. **All requests trả về 503** ngay lập tức (không gọi upstream)
6. **Restart url-service:** `docker compose start url-service`
7. **Sau 30s** → CB HALF_OPEN, probe request đi qua
8. **Probe success** → CB CLOSED, service khôi phục

**Phân tích metrics:**
- Khi CB OPEN: latency giảm mạnh (request không đến upstream), throughput vẫn cao (503 trả về ngay)
- Khi CB CLOSED lại: latency về normal, error rate về 0

---

## 6. ERROR HANDLING MATRIX CHI TIẾT

### 6.1 URL Service Errors

**Định nghĩa** (`services/url-service/errors.go`):

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

**Error → HTTP Status mapping:**

| Error | Status Code | Điều kiện | Nguồn code |
|-------|-------------|-----------|------------|
| `ErrInvalidURL` | 400 | URL validation thất bại (no scheme, no host, empty) | `validate.go:ValidateURL` |
| `ErrNotFound` | 404 | short_code không tồn tại trong DB | `service.go:RedirectToURL` |
| `ErrExpired` | 410 | ExpiresAt trong quá khứ (cache hoặc DB) | `service.go:RedirectToURL` |
| `ErrDeactivated` | 410 | is_active=false (cache hoặc DB) | `service.go:RedirectToURL` |
| `ErrForbidden` | 403 | user_id không match khi deactivate | `service.go:DeactivateURL` |
| `ErrAlreadyExists` | 409 | Short code collision sau 3 retries | `service.go:ShortenURL` |
| `ErrDatabaseError` | 500 | Database query/ping thất bại | `service.go:*` |

**Detailed handler mapping:**

```go
func (s *URLService) ShortenURL(...) (ShortenResponse, *HTTPError) {
    // ValidateURL fail → 400
    // Collision after 3 retries → 409
    // DB error → 500
}

func (s *URLService) RedirectToURL(...) (*RedirectInfo, *HTTPError) {
    // Cache inactive → 410
    // Cache expired → 410
    // DB not found → 404
    // DB inactive → 410
    // DB expired → 410
    // DB error → 500
}

func (s *URLService) DeactivateURL(...) *HTTPError {
    // pgx.ErrNoRows → 403 (not owner / already inactive)
    // DB error → 500
}
```

### 6.2 User Service Errors

**Định nghĩa** (`services/user-service/errors.go`):

```go
var (
    ErrDuplicateEmail   = errors.New("duplicate email")
    ErrUserNotFound     = errors.New("user not found")
    ErrPasswordMismatch = errors.New("password mismatch")
    ErrTokenInvalid    = errors.New("invalid token")
    ErrInvalidEmail     = errors.New("invalid email")
    ErrInvalidPassword  = errors.New("invalid password")
)
```

**Error → HTTP Status mapping:**

| Error | Status | Điều kiện | Ghi chú |
|-------|--------|-----------|---------|
| `ErrInvalidEmail` | 400 | Email format invalid | field=email |
| `ErrInvalidPassword` | 400 | Password < 8 ký tự | field=password |
| `ErrDuplicateEmail` | 409 | Email đã tồn tại | DB unique violation |
| `ErrUserNotFound` | 401 (generic) | Email không tồn tại | "invalid credentials" |
| `ErrPasswordMismatch` | 401 (generic) | Sai password | "invalid credentials" |
| `ErrTokenInvalid` | 401 | JWT expired/invalid signature/malformed | "unauthorized" |
| Invalid JSON body | 400 | Malformed request body | |
| Wrong Content-Type | 415 | Content-Type không phải application/json | |

**Timing attack protection:**
```go
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
    user, err := h.store.FindByEmail(r.Context(), req.Email)
    if user == nil {
        _ = h.hasher.Verify(req.Password, dummyBcryptHash) // dummy verify for timing
        writeError(w, http.StatusUnauthorized, "invalid credentials")
        return
    }
    if err := h.hasher.Verify(req.Password, user.PasswordHash); err != nil {
        writeError(w, http.StatusUnauthorized, "invalid credentials")
        return
    }
    // ... issue token
}
```

`dummyBcryptHash` = `$2a$12$MB4lTvA5UVWJU8GPtVFSne/kMHaXBSz45DWvIl/4AS9NLnz7tavNm`

Dù email có tồn tại hay không, server luôn chạy bcrypt verify → thời gian phản hồi tương đương nhau.

### 6.3 Analytics Service Errors

**Định nghĩa** (`services/analytics-service/errors.go`):

| Error | Status | Điều kiện |
|-------|--------|-----------|
| Missing short_code | 400 | Path value empty |
| Invalid timeline interval | 400 | interval != "day" && interval != "hour" |
| DB query error | 500 | count/referers/timeline query fail |
| Malformed click event | ack + discard | JSON parse fail |
| Missing EventID | ack + discard | Không thể dedup |
| Missing ShortCode | ack + discard | Không thể xác định URL |

**Error handling trong ClickConsumer:**

```go
// Transient errors → nack + requeue
if err := c.pool.Begin(ctx); err != nil {
    nackRequeue(delivery, c.log) // Retry later
}
if err := c.dedupStore.Exists(ctx, tx, evt.EventID); err != nil {
    nackRequeue(delivery, c.log) // Retry later
}

// Invalid events → ack + discard
if err := json.Unmarshal(delivery.Body, &evt); err != nil {
    ack(delivery, c.log) // Discard, cannot recover
}
if evt.EventID == "" || evt.ShortCode == "" {
    ack(delivery, c.log) // Discard, cannot recover
}
```

### 6.4 Notification Service Errors

**Error handling trong NotificationConsumer:**

| Scenario | Action | Reason |
|----------|--------|--------|
| Malformed JSON (parseEventID fail) | ack + discard | Cannot recover |
| Missing EventID | ack + discard | Invalid event |
| Unknown routing key | ack + discard | Unsupported event type |
| Missing UserID in milestone | ack + discard | Cannot create notification |
| Missing UserEmail | ack + discard | Cannot send email |
| DB insert fail | nack + requeue | Transient error, retry |
| Valid event | insert + ack | Normal flow |

### 6.5 Gateway Errors

**Định nghĩa** (`gateway/errors.go`):

| Error | Status | Điều kiện |
|-------|--------|-----------|
| Circuit open | 503 | `ErrCircuitOpen` → "url-service unavailable" |
| Rate exceeded | 429 + Retry-After | "rate limit exceeded" |
| Unauthorized | 401 | JWT missing/invalid → "unauthorized" |
| Not found | 404 | Route không match → "not found" |
| Bad gateway | 502 | Upstream không có trong config proxy |
| Upstream 5xx | 503 (via CB) | "bad gateway" từ ReverseProxy ErrorHandler |

**Handler flow** (`gateway/handler.go`):

```
ServeHTTP:
  1. matchRoute(r) → nil? → 404
  2. RateLimit check → not allowed? → 429
  3. Path rewrite (strip prefix)
  4. Circuit Breaker.Do():
     - OPEN → 503 immediately
     - Run upstream proxy
     - Upstream returns 5xx → treat as failure
  5. Record metrics (status class, duration)
```

**CORS error handling:**
- OPTIONS preflight → 204 No Content (no auth required)
- Các headers: Access-Control-Allow-Origin, Methods, Headers, Expose-Headers

---

## 7. ERROR RESPONSE FORMAT

**Standard JSON error format:**

```json
{"error": "message"}
```

**Validation error (user service):**

```json
{"error": "invalid email format", "field": "email"}
```

**All services thống nhất format:**

| Service | Struct | Implementation |
|---------|--------|----------------|
| Gateway | `{"error": "..."}` | `gateway/errors.go:writeError` |
| URL Service | `{"error": "..."}` | `services/url-service/errors.go:writeError` |
| User Service | `{"error": "...", "field": "..."}` | `services/user-service/errors.go:writeFieldError` |
| Analytics Service | `{"error": "..."}` | `services/analytics-service/errors.go:writeError` |
| Notification Service | `{"error": "..."}` | `services/notification-service/errors.go:writeError` |

**Gateway thêm headers:**
- `Content-Type: application/json` — tất cả responses
- `X-Correlation-ID` — tracing header (sinh random 16-byte hex)
- `Retry-After` — rate limit exceeded
- `Access-Control-Allow-Origin: *` — CORS

---

## 8. LOGGING STRATEGY

**Shared logger** (`shared/logger/logger.go`):

```go
func New(serviceName string) *slog.Logger {
    handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    })
    logger := slog.New(handler)
    return logger.With(slog.String("service", serviceName))
}
```

**Structured JSON logging** — mỗi log line là JSON:

```json
{"time":"2024-01-01T12:00:00Z","level":"INFO","msg":"http request",
 "service":"gateway","method":"POST","path":"/api/shorten",
 "status":201,"duration_ms":42,"correlation_id":"a1b2c3..."}
```

**Log levels:**
| HTTP Status | Log Level | Notes |
|-------------|-----------|-------|
| 1xx, 2xx, 3xx | INFO | Successful requests |
| 4xx | WARN | Client errors (validation, auth, rate limit) |
| 5xx | ERROR | Server errors (DB, upstream) |

**Request logging middleware:**

```go
func RequestLogger(log *slog.Logger, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
        next.ServeHTTP(recorder, r)
        
        logWithID := WithCorrelationID(log, correlationID)
        msg := "http request"
        fields := []any{
            "method", r.Method, "path", r.URL.Path,
            "status", recorder.status, "duration_ms", time.Since(start).Milliseconds(),
        }
        
        if recorder.status >= 500 {
            logWithID.Error(msg, fields...)
        } else if recorder.status >= 400 {
            logWithID.Warn(msg, fields...)
        } else {
            logWithID.Info(msg, fields...)
        }
    })
}
```

**Log format per service startup:**
```
url-service:    "connected to DB" max_conns=10 min_conns=2
url-service:    "connected to Redis cache" addr=redis:6379
url-service:    "connected to RabbitMQ" exchange=url-shortener
url-service:    "database migrations applied successfully"
url-service:    "server listening" port=8080
```

---

## 9. DATABASE ERROR HANDLING

### Connection Pool Configuration

Mỗi service dùng pgxpool với settings đồng nhất:

```go
cfg, _ := pgxpool.ParseConfig(databaseURL)
cfg.MaxConns = 10
cfg.MinConns = 2
pool, _ := pgxpool.NewWithConfig(ctx, cfg)

// Ping with 10s timeout
pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()
if err := pool.Ping(pingCtx); err != nil {
    pool.Close()
    return nil, fmt.Errorf("ping db: %w", err)
}
```

**Pool settings:**
- `MaxConns=10` — maximum connections
- `MinConns=2` — minimum idle connections
- Ping timeout: 10s → fatal if fail → `os.Exit(1)`

### Error Handling Patterns

**1. Unique Violation Detection:**
```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    // Handle unique constraint violation
}
```

**2. Not Found Detection (pgx):**
```go
if errors.Is(err, pgx.ErrNoRows) {
    // Handle not found
}
```

**3. Context Cancellation:**
```go
select {
case <-ctx.Done():
    return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
default:
}
```

**4. Transaction Management:**
```go
err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
    // Multiple operations trong 1 transaction
    return nil // Auto-commit
})
// Auto-rollback on error
```

**5. RowsAffected = 0:**
```go
if cmdTag.RowsAffected() == 0 {
    return pgx.ErrNoRows
}
```

### Error Source Matrix

| Error | Detection | Action |
|-------|-----------|--------|
| Unique violation | `pgErr.Code == "23505"` | Retry (short code) or 409 (duplicate email) |
| Not found | `errors.Is(err, pgx.ErrNoRows)` | 404 or 403 |
| Connection timeout | Ping fail | `os.Exit(1)` — FATAL |
| Migration fail | Exec fail | `os.Exit(1)` — FATAL |
| Transaction conflict | Serialization error | Nack + requeue (consumer) |

---

## 10. RABBITMQ ERROR HANDLING

### Connection Retry với Exponential Backoff

**Implementation** (`services/url-service/rabbitmq.go`):

```go
func NewRabbitMQConn(ctx context.Context, amqpURL string, log *slog.Logger, maxAttempts int) (*RabbitMQConn, error) {
    backoff := time.Second
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        conn, err = amqp.DialConfig(amqpURL, config)
        if err == nil {
            break  // Success
        }
        // Log warning + sleep with backoff
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(backoff):
        }
        backoff = min(backoff * 2, 30 * time.Second)
    }
}
```

**Backoff sequence:** 1s → 2s → 4s → 8s → 16s → 30s → 30s → ... (max 10 attempts)

**Max total wait:** ~2 minutes (1+2+4+8+16+30+30+30+30+30 ≈ 181s)

| Condition | Action | Message |
|-----------|--------|---------|
| Connection fail (transient) | Retry với backoff | "rabbitmq connection attempt failed" |
| Connection fail (exhausted) | FATAL → `os.Exit(1)` | "failed to connect to RabbitMQ" |
| Context cancelled during retry | Return error | "context cancelled during rabbitmq connect" |
| Channel open fail | Return error | "open rabbitmq channel" |
| Exchange declare fail | Return error | "declare exchange" |

### Publish Error Handling

**URL Service Publisher** (`services/url-service/publisher.go`):

```go
func (p *amqpPublisher) Publish(ctx context.Context, routingKey string, body []byte) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    return p.ch.PublishWithContext(ctx, exchangeName, routingKey, false, false, amqp.Publishing{
        ContentType:  "application/json",
        DeliveryMode: amqp.Persistent,
        Body:         body,
    })
}
```

- `sync.Mutex` bảo vệ AMQP channel (not safe for concurrent use)
- Persistent delivery mode (messages survive broker restart)
- Publish failure → Warn log + retry via next outbox poll

### Consumer Error Handling

**Click Consumer** (`services/analytics-service/consumer.go`):

| Condition | Action | Rationale |
|-----------|--------|-----------|
| Invalid JSON | ack + discard | Cannot recover, malformed data |
| Missing EventID/ShortCode | ack + discard | Missing required fields |
| DB error (begin) | nack + requeue | Transient, retry later |
| DB error (dedup check) | nack + requeue | Transient, retry later |
| DB error (insert click) | nack + requeue | Transient, retry later |
| Milestone publish fail | Continue (log warn) | Non-blocking, milestone still inserted |
| Transaction commit fail | nack + requeue | Actual DB error |
| Runtime panic | recover + ack | Critical but isolated |

**Notification Consumer** (`services/notification-service/consumer.go`):

| Condition | Action |
|-----------|--------|
| Invalid JSON | ack + discard |
| Missing EventID | ack + discard |
| Unknown routing key | ack + discard |
| Missing UserID (milestone) | ack + discard |
| DB insert fail | nack + requeue |
| Runtime panic | recover + ack |

### Delivery Acknowledgement Functions

```go
func ack(delivery amqp.Delivery, log *slog.Logger) {
    if err := delivery.Ack(false); err != nil {
        log.Warn("ack delivery failed", "error", err)
    }
}

func nackRequeue(delivery amqp.Delivery, log *slog.Logger) {
    if err := delivery.Nack(false, true); err != nil {
        log.Warn("nack delivery failed", "error", err)
    }
}
```

- `Ack(false)`: Acknowledge single message (not multiple)
- `Nack(false, true)`: Reject + requeue (retry later)
- `prefetch=1` (QoS): Process one message at a time per consumer

---

## 11. REDIS ERROR HANDLING

### Connection Error — Non-Fatal

**Implementation** (`services/url-service/redis.go`):

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
        log.Warn("redis unreachable on startup, cache disabled", "error", err)
        return client, false
    }
    return client, true
}
```

**Key design decision:** Redis failure is **NON-FATAL**. Service vẫn start được, cache temporarily disabled.

### Error Handling Matrix

| Operation | Failure Mode | Behavior | Rationale |
|-----------|-------------|----------|-----------|
| Cache Get | Connection fail | Return nil (cache miss), no error | Serve stale is better than error |
| Cache Get | Timeout (50ms) | Return nil (cache miss) | Short timeout để không block |
| Cache Set | Connection fail | Log warn, no action | Async fire-and-forget |
| Cache Delete | Connection fail | Log warn, no action | Non-critical |
| Rate Limit INCR | Connection fail | `return (true, 0, err)` — fail-open | Allow request through |
| Rate Limit EXPIRE | Connection fail | `return (true, 0, err)` — fail-open | Allow request through |
| Rate Limit TTL | Connection fail | `return (false, 0, nil)` — treat as limited | Conservative: assume exceeded |

**Cache Get timeout:** 50ms
```go
timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
defer cancel()
data, err := c.client.Get(timeoutCtx, code).Result()
if err != nil {
    return nil, nil // Treat as cache miss
}
```

**Fail-open vs Fail-closed decision:**
- **Cache:** fail-open (serve from DB)
- **Rate Limiter:** fail-open (allow request)
- **Rationale:** Better to serve slightly degraded than reject legitimate requests

---

## 12. EDGE CASES

### 12.1 Short Code Collision

**Xác suất:**
- 62^7 = 3,521,614,606,208 (~3.5 trillion) unique codes
- Sau 1 triệu URLs, collision probability ≈ 1.4 × 10⁻⁷ per attempt

**Retry strategy** (`services/url-service/service.go:92-161`):

```go
for attempt := 0; attempt < 3; attempt++ {
    shortCode = s.cgen.Generate()
    err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
        // Insert URL + Insert outbox in transaction
    })
    if err == nil {
        success = true
        break
    }
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23505" {
        time.Sleep(time.Duration(attempt*50) * time.Millisecond) // backoff
        continue
    }
    return 500 (non-collision error)
}
if !success {
    return 409 Conflict (all 3 retries exhausted)
}
```

**Backoff:** 0ms, 50ms, 100ms (attempt × 50ms)

**Edge case analysis:**
- Attempt 1: Generate code A → collision → backoff 0ms
- Attempt 2: Generate code B → collision → backoff 50ms
- Attempt 3: Generate code C → collision → return 409 Conflict
- Xác suất 3 lần liên tiếp collision: ~(1.4×10⁻⁷)³ ≈ 2.7×10⁻²¹ (negligible)

### 12.2 URL Expiry

**Implementation** (`services/url-service/service.go`):

**Validation:**
```go
if expiresInHours <= 0 || expiresInHours > 24*365 {
    expiresInHours = 24 // Default: 1 day
}
```

**Range:** 1-8760 hours (1 year max)

**Double-check strategy:**
1. **Cache check:** Nếu cache hit, check `ExpiresAt` vs `time.Now()`
2. **DB check:** Nếu cache miss, check `ExpiresAt` từ DB

**Cache TTL:**
- Default: `time.Hour` nếu URL không có expiry
- Nếu có expiry: `time.Until(*expiresAt)` — remaining time
- Nếu remaining < 0: TTL = 0 (immediate expiry)

**Edge case:** Nếu cache set ngay trước khi URL hết hạn, cache TTL sẽ rất ngắn → tự động expire sau khi URL hết hạn.

### 12.3 Concurrent Deactivate + Redirect

**Race condition scenario:**
1. User A gửi DELETE /api/urls/{code}
2. Cùng lúc, User B click redirect /r/{code}
3. Cache có thể stale (vẫn còn record cũ với is_active=true)

**Mitigation:**
1. **Cache delete sau deactivate:**
   ```go
   func (s *URLService) DeactivateURL(...) *HTTPError {
       // DB transaction: UPDATE is_active = false
       // ... commit ...
       _ = s.cache.Delete(context.Background(), shortCode) // cache invalidation
   }
   ```
2. **Tiny race window:** Giữa cache delete và lần check tiếp theo
3. **Last defense:** DB check (is_active=false) sau cache miss

**Flow deactivate + concurrent redirect:**
```
Deactivate: UPDATE DB → delete cache
Redirect:   Cache hit (stale is_active=true) → 301 redirect (rare race)
            Cache miss → DB check is_active=false → 410 Gone
```

**Kết luận:** Race condition vẫn tồn tại với xác suất rất nhỏ, DB là source of truth cuối cùng.

### 12.4 Outbox Worker Concurrency

**Implementation** (`services/url-service/outbox.go` và `outbox_store.go`):

**Configuration:**
```go
const (
    outboxBatchSize   = 50
    outboxWorkerCount = 3
    outboxPollEvery   = 2 * time.Second
)
```

**Claim mechanism với FOR UPDATE SKIP LOCKED:**
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

**Why FOR UPDATE SKIP LOCKED:**
- Không block nhau giữa các workers
- Mỗi worker claim các records khác nhau
- Lock lease 30s: nếu worker crash, records tự động unlocked sau 30s
- `locked_until < now()` check cho phép reclaim expired locks

**AMQP Channel Protection:**
```go
type amqpPublisher struct {
    ch *amqp.Channel
    mu sync.Mutex
}
func (p *amqpPublisher) Publish(ctx context.Context, ...) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    // AMQP channel not safe for concurrent use → mutex required
}
```

**Worker lifecycle:**
```
OutboxCoordinator.Run():
  1. Create 3 workers, each reading from jobs channel
  2. Ticker polls DB every 2s
  3. poll(): FetchUnpublished(50) → send to jobs channel
  4. worker(): read from jobs → publish → MarkPublished
  5. On ctx.Done(): close jobs, wait for workers
```

### 12.5 Timing Attack Protection

**Implementation** (`services/user-service/handler.go`):

```go
const dummyBcryptHash = "$2a$12$MB4lTvA5UVWJU8GPtVFSne/kMHaXBSz45DWvIl/4AS9NLnz7tavNm"

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
    user, err := h.store.FindByEmail(r.Context(), req.Email)
    
    if user == nil {
        // Email không tồn tại → vẫn chạy bcrypt verify để đồng bộ thời gian
        _ = h.hasher.Verify(req.Password, dummyBcryptHash)
        writeError(w, http.StatusUnauthorized, "invalid credentials")
        return
    }
    
    if err := h.hasher.Verify(req.Password, user.PasswordHash); err != nil {
        // Email tồn tại nhưng sai password
        writeError(w, http.StatusUnauthorized, "invalid credentials")
        return
    }
    
    // Login thành công
}
```

**Tại sao bcrypt cost 12?**
- bcrypt cost 12 ≈ 250ms on modern hardware
- Đủ chậm để làm mờ timing differences
- Cost range: 4-12 (có thể config qua BCRYPT_COST env)

**Response message luôn giống nhau:**
- Email không tồn tại: "invalid credentials"
- Sai password: "invalid credentials"
- Attacker không thể distinguish qua message content

### 12.6 Panic Recovery trong Consumer

**Click Consumer:**
```go
func (c *ClickConsumer) processDelivery(ctx context.Context, delivery amqp.Delivery) {
    started := time.Now()
    defer c.recoverDeliveryPanic(delivery)
    // ... processing ...
}

func (c *ClickConsumer) recoverDeliveryPanic(delivery amqp.Delivery) {
    if recovered := recover(); recovered != nil {
        c.log.Error("panic processing click event", "panic", recovered, ...)
        ack(delivery, c.log) // Acknowledge để không requeue message lỗi
    }
}
```

**Notification Consumer:**
```go
func (c *NotificationConsumer) recoverDeliveryPanic(delivery amqp.Delivery) {
    if recovered := recover(); recovered != nil {
        c.log.Error("panic processing notification event", ...)
        ack(delivery, c.log)
    }
}
```

**Ý nghĩa:** Panic trong consumer không làm crash toàn bộ service, chỉ discard message hiện tại.

### 12.7 Correlation ID Tracing

**Implementation** (`gateway/middleware.go`):

```go
func correlationIDMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        correlationID := r.Header.Get("X-Correlation-ID")
        if correlationID == "" {
            correlationID = newCorrelationID() // 16-byte random hex
            r.Header.Set("X-Correlation-ID", correlationID)
        }
        w.Header().Set("X-Correlation-ID", correlationID)
        ctx := logger.ContextWithCorrelationID(r.Context(), correlationID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

**Propagation:**
1. Gateway tạo correlation ID (16 bytes random, hex-encoded → 32 chars)
2. Set trong response header `X-Correlation-ID`
3. Forward đến upstream services qua reverse proxy
4. Các services có thể đọc từ context: `logger.CorrelationIDFromContext(ctx)`
5. Structured logs include `correlation_id` field

**Benefits:**
- Trace request across service boundaries
- Debug distributed transactions
- Correlate logs between gateway → url-service → analytics-service

---

## 13. HEALTH CHECK MATRIX

**Standard format** — tất cả services:

```json
{"status":"ok","service":"<service-name>"}
```

| Service | Endpoint | Port | Checks | Implementation |
|---------|----------|------|--------|----------------|
| Gateway | GET /health | 8080 | Lazy (no downstream check) | Pre-encoded JSON response |
| URL Service | GET /health | 8081 | Startup: DB ping + Redis + RabbitMQ | Pre-encoded, startup validation |
| Analytics Service | GET /health | 8082 | Startup: DB ping + RabbitMQ | Pre-encoded |
| User Service | GET /health | 8083 | Startup: DB ping | Pre-encoded |
| Notification Service | GET /health | 8084 | Startup: DB ping + RabbitMQ | Pre-encoded |

**Health handler implementation:**
```go
func NewHealthHandler(serviceName string) http.HandlerFunc {
    resp := HealthResponse{Status: "ok", Service: serviceName}
    body, _ := json.Marshal(resp) // Pre-encoded once
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write(body) // No encoding overhead per request
    }
}
```

**Design decisions:**
- Pre-encode response body (zero-allocation per request)
- Không check DB connectivity mỗi request (too expensive)
- Startup checks đảm bảo dependencies available trước khi service start
- Docker healthcheck sử dụng `wget -qO- http://localhost:8080/health`

---

## 14. PROMETHEUS METRICS

**Gateway metrics** (`gateway/metrics.go`):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `gateway_circuit_breaker_state` | Gauge | service | 0=CLOSED, 1=HALF_OPEN, 2=OPEN |
| `gateway_circuit_breaker_trips_total` | Counter | service | CB OPEN transitions |
| `gateway_circuit_breaker_rejected_total` | Counter | service | Requests rejected by OPEN CB |
| `gateway_requests_total` | Counter | service, status_class | All upstream requests |
| `gateway_request_duration_seconds` | Histogram | service | Upstream latency |

**Metric recording points:**
1. `recordCBState()` — when CB state changes (via `WithStateChange` callback)
2. `recordCBTrip()` — when CB transitions to OPEN
3. `recordCBRejected()` — when request returns 503 due to OPEN CB
4. `recordUpstreamMetrics()` — after each upstream request (status class + duration)

---

## 15. KẾT LUẬN

### Test Coverage Summary

| Test File | Tests | Lines | Focus Areas |
|-----------|-------|-------|-------------|
| gateway_test.go | 6 | 241 | CB state machine, rate limiting, JWT, CORS, path rewrite |
| url_test.go | 8 | 779 | Base62, codegen, cache, shorten/redirect/list/delete handlers |
| user_test.go | 16 | 372 | Validation, bcrypt, JWT, register/login/me handlers |
| analytics_test.go | 11 | 376 | JSON parsing, dedup, milestone, stats handlers |
| notification_test.go | 6 | 227 | Routing key dispatch, validation, list handler |
| events_test.go | 4 | 117 | JSON serialization round-trip |

**Total: ~51 test functions, ~2,112 lines of test code**

### Error Handling Philosophy

1. **Fail-open > Fail-closed** cho non-critical dependencies (Redis cache, rate limiter)
2. **Fail-fast** cho startup dependencies (DB, RabbitMQ) — `os.Exit(1)`
3. **ack + discard** cho invalid events (malformed JSON, missing fields)
4. **nack + requeue** cho transient errors (DB connection issues)
5. **Panic recovery** prevents single bad message from crashing consumer
6. **Exponential backoff** cho connection retries (RabbitMQ: 1s-30s)
7. **Timing attack protection** via dummy bcrypt hash

### Key Design Patterns

- **Circuit Breaker:** Prevents cascading failures, self-healing via HALF_OPEN probes
- **Outbox Pattern:** Ensures exactly-once event delivery via transactional outbox
- **Deduplication:** `processed_events` table prevents duplicate click processing
- **Idempotent Milestones:** `ON CONFLICT DO NOTHING` prevents duplicate milestone events
- **Correlation ID:** End-to-end tracing across microservices
- **Structured Logging:** JSON slog with service name and correlation ID

---

*Báo cáo được tạo dựa trên mã nguồn thực tế của dự án URL Shortener Microservices.*  
*Môn học: SE361.Q21 — Kiểm thử và Đảm bảo Chất lượng Phần mềm*
