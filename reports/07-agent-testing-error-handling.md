# Phân Tích Chi Tiết Về Kiểm Thử (Testing), Xử Lý Lỗi (Error Handling) và Các Trường Hợp Biên (Edge Cases)

**Dự án:** URL Shortener Microservices  
**Tác giả:** Agent phân tích  
**Ngày:** 2026-07-11  
**Phiên bản:** 1.0  

---

## Mục Lục

1. [Tổng Quan Kiến Trúc](#1-tổng-quan-kiến-trúc)
2. [Unit Tests — Phân Tích Chi Tiết](#2-unit-tests--phân-tích-chi-tiết)
3. [Test Patterns](#3-test-patterns)
4. [Smoke Test và E2E Test Scripts](#4-smoke-test-và-e2e-test-scripts)
5. [K6 Load Testing](#5-k6-load-testing)
6. [Error Types — 25+ Sentinel Errors](#6-error-types--25-sentinel-errors)
7. [HTTP Status Code Mapping](#7-http-status-code-mapping)
8. [Error Handling Patterns](#8-error-handling-patterns)
9. [Phân Tích Edge Cases](#9-phân-tích-edge-cases)
10. [Đánh Giá Testing Coverage](#10-đánh-giá-testing-coverage)
11. [Gaps và Khuyến Nghị](#11-gaps-và-khuyến-nghị)
12. [Kết Luận](#12-kết-luận)

---

## 1. Tổng Quan Kiến Trúc

URL Shortener Microservices bao gồm 5 services chính, mỗi service chạy trên một cổng riêng biệt:

| Service | Port | Package | Mô tả |
|---------|------|---------|-------|
| **gateway** | 8080 | `main` | API Gateway — routing, rate limiting, circuit breaker, JWT middleware, CORS, correlation ID |
| **url-service** | 8081 | `main` | Business logic — tạo short code, redirect, cache Redis, outbox pattern |
| **analytics-service** | 8082 | `main` | Thống kê click, milestones (10, 100, 1000), RabbitMQ consumer |
| **notification-service** | 8083 | `main` | Thông báo cho user, lưu notification records |
| **user-service** | 8084 | `main` | Đăng ký, đăng nhập, JWT issuance, bcrypt password hashing |

**Shared packages:**
- `shared/auth` — JWT verification, Claims struct, middleware, context utilities
- `shared/events` — Event types (`URLCreatedEvent`, `URLClickedEvent`, `URLDeletedEvent`, `MilestoneReachedEvent`), JSON round-trip
- `shared/logger` — Correlation ID logging

---

## 2. Unit Tests — Phân Tích Chi Tiết

### 2.1 Gateway Tests (`gateway/gateway_test.go`) — 241 dòng

#### 2.1.1 `TestCircuitBreakerTransitions` (hàng 28–59)

Kiểm thử state machine của circuit breaker với 5 phase:

```
Phase 1: CLOSED → tích lũy failures
  - Gọi cb.Do() với upstream error × 2 (maxFailures = 2)
  - Assert: state == OPEN

Phase 2: OPEN → từ chối requests
  - Gọi cb.Do() với thành công
  - Assert: errors.Is(err, ErrCircuitOpen)
  - assert: state == OPEN (không đổi)

Phase 3: OPEN → HALF_OPEN (sau timeout)
  - Sleep 2ms (vượt openTimeout = 1ms)
  - Gọi cb.Do() với thành công
  - Assert: err == nil (probe thành công)
  - Assert: state == CLOSED

Phase 4: HALF_OPEN → CLOSED (probe thành công)
  - State được set về CLOSED sau phase 3
  - Assert: state == CLOSED

Phase 5: HALF_OPEN → OPEN (probe thất bại)
  - Set cb.state = StateHalfOpen (giả lập)
  - Gọi cb.Do() với upstream error
  - Assert: err != nil (lỗi probe)
  - Assert: state == OPEN
```

**Sentinel errors được kiểm tra:**
- `ErrCircuitOpen` — kiểm tra bằng `errors.Is`

**Các trạng thái (State) trong Circuit Breaker:**
- `StateClosed` (0) — hoạt động bình thường
- `StateOpen` (1) — từ chối request ngay lập tức
- `StateHalfOpen` (2) — cho phép 1 probe request để kiểm tra

**Cơ chế hoạt động của `Do()` method:**
```
Do(ctx, upstream func):
  Lock → check state:
    OPEN:
      if time since last failure > openTimeout → chuyển HALF_OPEN, set halfOpenProbe=true
      else → return ErrCircuitOpen
    HALF_OPEN:
      if halfOpenProbe=true → return ErrCircuitOpen (đã có probe đang chạy)
      else → set halfOpenProbe=true (cho phép probe)
    CLOSED: proceed
  Unlock
  
  if context cancelled → cleanup, return ctx.Err()
  
  err = upstream()
  
  if err:
    HALF_OPEN → quay lại OPEN, return err
    CLOSED → tăng failures counter, nếu >= maxFailures → OPEN
  else:
    HALF_OPEN → CLOSED, reset failures
    CLOSED → reset failures
```

#### 2.1.2 `TestRateLimitRejectsAndFailOpen` (hàng 61–94)

Kiểm thử rate limiter với 2 scenario:

**Scenario A: Rate limit exceeded (429 Too Many Requests)**
```
Input:
  - fakeRateLimiter{allowed: false, retryAfter: 42}
  - Request POST /api/shorten từ IP 192.0.2.10

Assert:
  - Status == 429
  - Retry-After header == "42"
  - Rate limit key == "shorten:192.0.2.10"
```

**Scenario B: Fail-open khi Redis down**
```
Input:
  - fakeRateLimiter{allowed: true, err: errors.New("redis down")}
  - Upstream trả về 201 Created

Assert:
  - Status == 201 (fail-open, request vẫn được xử lý)
  - Log warning "rate limiter failed open"
```

**Fail-open pattern:** Khi rate limiter trả về lỗi (Redis không khả dụng), gateway vẫn cho phép request đi qua thay vì từ chối. Điều này đảm bảo availability hơn consistency.

#### 2.1.3 `TestRouterAndPathRewrite` (hàng 96–123)

Kiểm thử path rewriting cho route `/r/`:

```
Input:
  - Request GET /r/abc1234
  - Upstream URL service

Assert:
  - Status == 200
  - Upstream nhận được path == "/abc1234" (đã strip prefix /r/)
```

**Route matching logic trong `matchRoute()`:**
```
GET  /api/auth/register   → user-service (strip /api/auth)
POST /api/auth/login      → user-service (strip /api/auth)
GET  /api/me              → user-service (strip /api)
POST /api/shorten         → url-service (strip /api, rate limited)
GET  /api/urls            → url-service (strip /api)
DELETE /api/urls/         → url-service (strip /api)
GET  /r/                  → url-service (strip /r, rate limited)
GET  /api/stats/          → analytics-service (strip /api)
GET  /api/notifications   → notification-service (strip /api)
```

#### 2.1.4 `TestJWTMiddlewareProtectsPrivateRoutes` (hàng 125–149)

```
Scenario A: Không có token → 401
  - Request GET /api/me không có Authorization header
  - Assert: Status == 401

Scenario B: Token hợp lệ → 204
  - Tạo JWT với jwt.MapClaims{sub: "user-1", email: "user@example.com", iss: "url-shortener"}
  - Request với Bearer token
  - Assert: Status == 204
  - Assert: claims.Sub == "user-1" (có trong context)
```

#### 2.1.5 `TestJWTMiddlewareSkipsPublicRoutes` (hàng 151–168)

```
Scenario: Route public (/api/auth/login) không cần auth
  - Request POST /api/auth/login không có token
  - Assert: next handler được gọi
  - Assert: Status == 204
```

#### 2.1.6 `TestCorsMiddleware` (hàng 194–240)

```
Scenario A: Preflight OPTIONS request
  - Request OPTIONS /api/auth/login
  - Assert: next KHÔNG được gọi (intercepted)
  - Assert: Status == 204 (No Content)
  - Assert: Access-Control-Allow-Origin == "*"
  - Assert: Access-Control-Allow-Methods tồn tại
  - Assert: Access-Control-Allow-Headers tồn tại

Scenario B: Regular GET request
  - Request GET /api/health
  - Assert: next được gọi
  - Assert: Status == 200
  - Assert: Access-Control-Allow-Origin == "*"
```

#### 2.1.7 Fake Objects trong Gateway Tests

```go
type fakeRateLimiter struct {
    allowed    bool
    retryAfter int
    err        error
    key        string  // ghi lại key được gọi
}
```

---

### 2.2 URL Service Tests (`services/url-service/url_test.go`) — 779 dòng

#### 2.2.1 `TestBase62RoundTrip` (hàng 22–55)

Kiểm thử encode/decode Base62 với 7 test cases:

| Input | Expected Behavior |
|-------|-------------------|
| `0` | Encode → "0000000", Decode → 0 |
| `1` | Encode → "0000001", Decode → 1 |
| `61` | Encode → "000000z" (61 = 'z' trong alphabet) |
| `62` | Encode → "0000010" (62 = 1*62 + 0) |
| `12345` | Round-trip: encode → decode → giá trị gốc |
| `62^7 - 1 = 3521614606207` | Giá trị max cho 7 ký tự base62 |
| `62^7 = 3521614606208` | Mod về 0 (wrap around) |
| `9999999999999` | Số lớn, vẫn encode được và decode về giá trị % 62^7 |

**Công thức:**
```
shortCodeLength = 7
base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
limit = 62^7
Encode(n) = base62(n % limit)  // luôn trả về đúng 7 ký tự
```

**Đặc điểm kỹ thuật:**
- Sử dụng `math/big.Int` để xử lý số nguyên lớn (UUID là 128-bit)
- `new(big.Int).Abs(n)` để xử lý số âm an toàn
- Modulo `n.Mod(n, limit)` để đảm bảo output luôn trong khoảng 0..62^7-1
- Padding với '0' (alphabet[0]) bên trái

#### 2.2.2 `TestBase62DecodeErrors` (hàng 57–72)

5 trường hợp input không hợp lệ:

| Input | Lý do |
|-------|-------|
| `""` | Empty string, không đúng độ dài 7 |
| `"123456"` | Quá ngắn (6 ký tự) |
| `"12345678"` | Quá dài (8 ký tự) |
| `"123456?"` | Ký tự '?' không nằm trong base62 alphabet |
| `"abc-xyz"` | Ký tự '-' không hợp lệ |

#### 2.2.3 `TestCodegen` (hàng 76–104)

Kiểm thử `ShortCodeGenerator`:
```
Assert:
  - code1 có độ dài == 7
  - Tất cả ký tự trong code1 đều thuộc base62Alphabet
  - code2 có độ dài == 7
  - Tất cả ký tự trong code2 đều thuộc base62Alphabet
  - code1 != code2 (tính uniqueness)
```

#### 2.2.4 `TestRedisCache` (hàng 108–189)

Kiểm thử Redis cache với `miniredis` (in-memory Redis mock):

**Cache Miss:**
```
Input: cache.Get(ctx, "miss123")
Assert: err == nil, result == nil
```

**Cache Hit:**
```
Input: cache.Set(ctx, "hitcode", cachedURL, 1h) → cache.Get(ctx, "hitcode")
Assert: err == nil, result != nil
Assert: result.OriginalURL == cachedURL.OriginalURL
Assert: result.IsActive == cachedURL.IsActive
Assert: result.ExpiresAt.Equal(*cachedURL.ExpiresAt)  // dùng Equal do time precision
```

**Cache Delete:**
```
Input: cache.Delete(ctx, "hitcode") → cache.Get(ctx, "hitcode")
Assert: err == nil trên delete
Assert: result == nil sau delete
```

**Error Fallback (Redis down):**
```
Input: badClient kết nối đến localhost:9999 (port sai)
Assert: err == nil (non-fatal, log warning)
Assert: result == nil
```

**Cache Error Handling Pattern:** Cache errors được coi là non-fatal. Khi Redis không khả dụng, service fallback về database và log warning thay vì trả về lỗi cho client.

#### 2.2.5 `TestHandlerShorten` (hàng 278–425)

**Subtest: Success (hàng 282–347)**
```
Input:
  - claims: {Sub: "user-123", Email: "user@example.com"}
  - Body: {"url":"https://example.com/test","expires_in_hours":12}
  - codegen trả về "abc1234"

Assert:
  - Status == 201 Created
  - resp.ShortCode == "abc1234"
  - resp.ShortURL == "http://localhost/abc1234"
  - store.Insert được gọi với đúng ShortCode
  - outbox.InsertEvent được gọi
  - cache.Set được gọi (async, sleep 10ms)
```

**Subtest: Collision Retry Success (hàng 349–400)**
```
Input:
  - Lần 1: store.Insert trả về pgError{Code: "23505"} (UNIQUE_VIOLATION)
  - Lần 2: store.Insert thành công
  - codegen lần lượt trả về "coll123", "succ123"

Assert:
  - Status == 201
  - resp.ShortCode == "succ123" (code thứ 2)
  - attempts == 2
```

**Collision handling logic:**
```go
for attempt := 0; attempt < 3; attempt++ {
    shortCode = cgen.Generate()
    err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
        store.Insert(ctx, tx, record)
        outboxStore.InsertEvent(ctx, tx, outbox)
        return nil
    })
    if err == nil { success = true; break }
    
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23505" {
        time.Sleep(time.Duration(attempt*50) * time.Millisecond) // backoff
        continue
    }
    return HTTPError{Status: 500, Err: ErrDatabaseError}
}
```

**Subtest: Unauthorized (hàng 402–412)**
```
Input: Request không có auth context
Assert: Status == 401
```

**Subtest: Invalid URL (hàng 414–424)**
```
Input: {"url":"invalid-url-no-scheme"}
Assert: Status == 400 Bad Request
```

#### 2.2.6 `TestHandlerRedirect` (hàng 427–628)

**Subtest: Cache Hit (hàng 428–481)**
```
Input:
  - cache.Get trả về CachedURL{OriginalURL: "https://cachehit.com", IsActive: true}
  - Short code: "abc1234"

Assert:
  - Status == 308 Permanent Redirect
  - Location header == "https://cachehit.com"
  - cache.Get được gọi
  - store.FindByCode KHÔNG được gọi (cache bypass)
  - outbox event cho analytics được insert (async)
```

**Subtest: Cache Miss — DB Hit (hàng 483–545)**
```
Input:
  - cache.Get trả về nil (miss)
  - store.FindByCode trả về URLRecord{OriginalURL: "https://dbhit.com", IsActive: true}

Assert:
  - Status == 308
  - Location == "https://dbhit.com"
  - cache.Get được gọi
  - store.FindByCode được gọi
  - cache.Set được gọi (async, populate cache)
  - outbox event cho analytics được insert
```

**Subtest: Deactivated URL (hàng 547–573)**
```
Input:
  - cache miss
  - store.FindByCode trả về URLRecord{IsActive: false}

Assert:
  - Status == 410 Gone
```

**Subtest: Expired URL (hàng 575–603)**
```
Input:
  - cache miss
  - store.FindByCode trả về URLRecord{ExpiresAt: past time, IsActive: true}

Assert:
  - Status == 410 Gone
```

**Subtest: Not Found (hàng 605–627)**
```
Input:
  - cache miss
  - store.FindByCode trả về nil, pgx.ErrNoRows

Assert:
  - Status == 404 Not Found
```

#### 2.2.7 `TestHandlerList` (hàng 630–706)

**Subtest: Success (hàng 634–674)**
```
Input:
  - claims: {Sub: "user-123"}
  - Query: ?limit=20
  - store.FindByUserID trả về 2 records

Assert:
  - Status == 200
  - len(URLs) == 2
  - HasMore == false
  - NextCursor == ""
```

**Subtest: Pagination HasMore (hàng 676–705)**
```
Input:
  - limit=2, after="id0"
  - store trả về 3 records (nhiều hơn limit)

Assert:
  - len(URLs) == 2 (chỉ lấy limit records)
  - HasMore == true
  - NextCursor == "id2" (ID của record cuối trong page)
```

#### 2.2.8 `TestHandlerDelete` (hàng 708–779)

**Subtest: Success (hàng 712–759)**
```
Input:
  - claims: {Sub: "user-123"}
  - DELETE /urls/del123
  - store.Deactivate thành công

Assert:
  - Status == 204 No Content
  - store.Deactivate được gọi với (ctx, tx, "del123", "user-123")
  - outbox.InsertEvent được gọi
  - cache.Delete được gọi với "del123"
```

**Subtest: Forbidden — Not Owner (hàng 761–778)**
```
Input:
  - store.Deactivate trả về pgx.ErrNoRows (không tìm thấy hoặc không phải owner)

Assert:
  - Status == 403 Forbidden
```

#### 2.2.9 Mock Definitions

```go
type mockTx struct { pgx.Tx }       // mock transaction
type mockPool struct {}              // mock connection pool
type mockURLStore struct { ... }     // 4 function fields
type mockOutboxStore struct { ... }  // 3 function fields
type mockCache struct { ... }        // 3 function fields
type mockGenerator struct { ... }    // 1 function field
```

**Design pattern:** Sử dụng function fields cho phép mỗi test định nghĩa behavior riêng mà không cần nhiều mock classes.

---

### 2.3 User Service Tests (`services/user-service/user_test.go`) — 372 dòng

#### 2.3.1 `TestValidateEmail` (hàng 44–65)

Table-driven test với 6 cases:

| Input | wantErr | Lý do |
|-------|---------|-------|
| `"user@example.com"` | false | Email hợp lệ cơ bản |
| `"user+tag@sub.domain.org"` | false | Email có +tag và subdomain |
| `""` | true | Empty string |
| `"notanemail"` | true | Thiếu @ và domain |
| `"@nodomain.com"` | true | Thiếu local part |
| `"user@"` | true | Thiếu domain |
| `"user @example.com"` | true | Có space |

#### 2.3.2 `TestValidatePassword` (hàng 67–86)

Table-driven test với 5 cases:

| Input | wantErr | Lý do |
|-------|---------|-------|
| `"12345678"` | false | Đúng 8 ký tự (min length) |
| `"longerpassword"` | false | Dài hơn 8 |
| `"1234567"` | false | ***BUG*** — chỉ 7 ký tự nhưng expected `wantErr` = true? |
| `""` | true | Empty string |
| `"        7"` | false | 8 ký tự (kể cả space) |

**⚠️ Lưu ý:** Dòng 75 có `{"1234567", true}` — test này pass vì `validatePassword("1234567")` trả về error (password quá ngắn). Tuy nhiên cần kiểm tra lại logic threshold — 7 ký tự có thực sự là invalid không dựa trên implementation.

#### 2.3.3 `TestBcryptHasher_HashAndVerify` (hàng 88–103)

```
Input:
  - Hash("mypassword") → hash string
  - Verify("mypassword", hash) → nil
  - Verify("wrongpassword", hash) → ErrPasswordMismatch

Assert:
  - hash != ""
  - Verify đúng password: err == nil
  - Verify sai password: errors.Is(err, ErrPasswordMismatch)
```

#### 2.3.4 `TestBcryptHasher_DifferentHashesSamePassword` (hàng 105–118)

```
Input:
  - Hash("samepassword") → hash1
  - Hash("samepassword") → hash2

Assert:
  - hash1 != hash2 (bcrypt dùng salt ngẫu nhiên)
  - Verify("samepassword", hash1) == nil
  - Verify("samepassword", hash2) == nil
```

#### 2.3.5 `TestJWTTokenIssuer_IssueAndVerify` (hàng 120–145)

```
Input:
  - Issue("user-uuid-123", "user@example.com") → token, expiresAt, err
  - Verify(token) → claims, err

Assert:
  - token != ""
  - time.Until(expiresAt) > 23h (24h TTL)
  - claims.Sub == "user-uuid-123"
  - claims.Email == "user@example.com"
  - claims.Iss == "url-shortener"
```

#### 2.3.6 `TestJWTTokenIssuer_Verify_InvalidSignature` (hàng 147–155)

```
Input:
  - issuer1: secret "secret-one-32-chars-long-exactly"
  - issuer2: secret "secret-two-32-chars-long-exactly"
  - Token từ issuer1, verify bằng issuer2

Assert:
  - errors.Is(err, ErrTokenInvalid)
```

#### 2.3.7 `TestJWTTokenIssuer_Verify_Malformed` (hàng 157–163)

```
Input:
  - Verify("not.a.jwt")

Assert:
  - errors.Is(err, ErrTokenInvalid)
```

#### 2.3.8 `TestJWTTokenIssuer_Verify_Expired` (hàng 165–172)

```
Input:
  - issuer với TTL = -1h (token hết hạn ngay)
  - Issue("uid", "u@e.com") → token
  - Verify(token)

Assert:
  - errors.Is(err, ErrTokenInvalid)
```

#### 2.3.9 `TestRegisterHandler_ShortPassword` (hàng 174–193)

```
Input:
  - Body: {"email":"test@example.com","password":"1234567"}

Assert:
  - Status == 400 Bad Request
  - resp.Field == "password"
  - resp.Error tồn tại
```

#### 2.3.10 `TestLoginHandler_UnknownEmail` (hàng 195–218)

```
Input:
  - store.FindByEmail trả về nil, ErrUserNotFound

Assert:
  - Status == 401 Unauthorized
  - resp.Error == "invalid credentials"
  - **Không tiết lộ thông tin** (không nói "email not found")
```

#### 2.3.11 `TestLoginHandler_WrongPassword` (hàng 220–244)

```
Input:
  - Email tồn tại, hash bcrypt cho "correctpassword"
  - Body: {"password":"wrongpassword"}

Assert:
  - Status == 401 Unauthorized
  - resp.Error == "invalid credentials" (generic message, không tiết lộ)
```

#### 2.3.12 `TestRegisterHandler_Success` (hàng 246–273)

```
Input:
  - Body: {"email":"test@example.com","password":"securepass"}
  - store.Insert trả về User{ID: "test-uuid"}

Assert:
  - Status == 201 Created
  - resp.UserID == "test-uuid"
  - resp.Email == "test@example.com"
  - **Response KHÔNG chứa "password"** (security check)
```

#### 2.3.13 `TestRegisterHandler_DuplicateEmail` (hàng 275–290)

```
Input:
  - store.Insert trả về nil, ErrDuplicateEmail

Assert:
  - Status == 409 Conflict
```

#### 2.3.14 `TestLoginHandler_Success` (hàng 292–320)

```
Input:
  - Email tồn tại, password đúng
  - Body: {"email":"user@example.com","password":"correctpassword"}

Assert:
  - Status == 200 OK
  - resp.Token != ""
  - resp.ExpiresAt != ""
```

#### 2.3.15 `TestMeHandler_ValidToken` (hàng 341–361)

```
Input:
  - claims: {Sub: "user-uuid", Email: "user@example.com"}
  - Request GET /me

Assert:
  - Status == 200
  - resp.UserID == "user-uuid"
```

#### 2.3.16 `TestMeHandler_NoClaims` (hàng 363–372)

```
Input:
  - Request GET /me không có auth context

Assert:
  - Status == 401 Unauthorized
```

---

### 2.4 Analytics Service Tests (`services/analytics-service/analytics_test.go`) — 376 dòng

#### 2.4.1 `TestClickConsumer_ParseMalformedJSONAcks` (hàng 20–31)

```
Input:
  - Body: `{bad-json` (malformed JSON)
  - fakeAcknowledger

Assert:
  - parseDelivery ok == false
  - acks == 1 (message được ack, không nack)
  - nacks == 0
```

**Important pattern:** Malformed messages được **ack** thay vì nack để tránh vòng lặp vô hạn trên RabbitMQ.

#### 2.4.2 `TestClickConsumer_ParseMissingEventIDAcks` (hàng 33–44)

```
Input:
  - Body: {"short_code":"abc123"} (thiếu event_id)
  
Assert:
  - ok == false
  - acks == 1
```

#### 2.4.3 `TestClickConsumer_ClickRecordUsesEventIPHash` (hàng 46–57)

```
Input: URLClickedEvent{IPHash: "already-hashed"}
Assert: rec.IPHash == "already-hashed"
```

#### 2.4.4 `TestStatsHandler_UnknownCodeReturnsZeros` (hàng 59–81)

```
Input:
  - GET /stats/missing
  - store.CountByCode = 0

Assert:
  - Status == 200 (không phải 404!)
  - ShortCode == "missing"
  - TotalClicks == 0
  - ClicksLast24h == 0
  - ClicksLast7d == 0
  - TopReferers là empty slice (không phải nil)
```

**Important design decision:** Stats endpoint trả về zeros thay vì 404 cho short code không tồn tại. Điều này tránh information leak (không tiết lộ code nào tồn tại hay không).

#### 2.4.5 `TestStatsHandler_TopReferersLimitIsFive` (hàng 83–98)

```
Input: GET /stats/abc123
Assert: store.topReferersLimit == statsTopReferersLimit (5)
```

#### 2.4.6 `TestStatsHandler_StatsDBErrorReturns500` (hàng 100–112)

```
Input:
  - store.countErr = errors.New("db down")
  - GET /stats/abc123

Assert: Status == 500 Internal Server Error
```

#### 2.4.7 `TestStatsHandler_TimeLineInvalidIntervalsReturn400` (hàng 114–130)

Kiểm thử 5 intervals không hợp lệ:

| Interval | Expected |
|----------|----------|
| `"week"` | 400 |
| `"month"` | 400 |
| `""` | 400 |
| `"DAY"` | 400 (case-sensitive) |
| `"Hour"` | 400 (case-sensitive) |

**Valid intervals:** Chỉ có `"day"` và `"hour"` (lowercase).

#### 2.4.8 `TestStatsHandler_TimeLineValidIntervalsReturnEmptyPoints` (hàng 132–161)

```
Input:
  - interval = "day" → 200
  - interval = "hour" → 200

Assert (cho cả 2):
  - Status == 200
  - ShortCode == "abc123"
  - Interval được truyền đúng
  - Points là empty slice
```

#### 2.4.9 `TestMilestoneChecker_NoMilestoneBelow10` (hàng 163–175)

```
Input: totalClicks = 9

Assert:
  - Không insert milestone nào
  - Không publish event nào
```

**Logic:** Milestone thresholds là [10, 100, 1000]. Với 9 clicks, không threshold nào được vượt qua.

#### 2.4.10 `TestMilestoneChecker_Threshold10Triggered` (hàng 177–197)

```
Input: totalClicks = 10

Assert:
  - milestone 10 được insert 1 lần
  - 1 event được publish
  - evt.ShortCode == "abc123"
  - evt.UserID == "user-1"
  - evt.MilestoneN == 10
  - evt.TotalClicks == 10
  - evt.CorrelationID == "corr-1"
```

#### 2.4.11 `TestMilestoneChecker_AlreadyRecorded_NoPublish` (hàng 199–212)

```
Input: totalClicks = 10, milestone 10 đã được record

Assert:
  - insertCount == 0 (không insert lại)
  - Không publish event (dedup)
```

#### 2.4.12 `TestMilestoneChecker_PublishFailureContinues` (hàng 214–227)

```
Input:
  - totalClicks = 10
  - publisher.err = errors.New("rabbitmq down")

Assert:
  - milestone vẫn được insert vào DB (không rollback)
  - publish vẫn được gọi (attempt)
  - Hàm không trả về error (fail-open)
```

**Pattern:** Milestone checker không fail khi RabbitMQ down. Nó insert milestone vào DB và cố gắng publish, nhưng nếu publish thất bại thì chỉ log warning.

#### 2.4.13 `TestMilestoneChecker_MultipleThresholdsAtOnce` (hàng 229–246)

```
Input: totalClicks = 1000

Assert:
  - insertCount == 3 (ba thresholds: 10, 100, 1000)
  - 3 events được publish
  - Cả 3 thresholds đều được record
```

#### 2.4.14 Fake Objects

```go
type fakeClickStore struct {
    countErr         error
    topReferersLimit int
    timelineInterval string
}

type fakeMilestoneStore struct {
    recorded    map[int]bool       // which milestones already exist
    inserted    map[int]int        // insert count per milestone
    insertCount int
}

type fakePublisher struct {
    events []*events.MilestoneReachedEvent
    err    error
}

type fakeTx struct {
    pgx.Tx
    count int64  // simulated click count
}

type fakeRow struct {
    value int64
}

type fakeAcknowledger struct {
    acks    int
    nacks   int
    rejects int
}
```

**Design pattern:** `fakeTx` implement `pgx.Tx` interface với tất cả methods không dùng đến trả về error. Chỉ `QueryRow` và `Commit`/`Rollback` được implement đầy đủ.

---

### 2.5 Notification Service Tests (`services/notification-service/notification_test.go`) — 227 dòng

#### 2.5.1 `TestNotificationConsumer_UsesRoutingKeyAsEventType` (hàng 18–42)

```
Input:
  - RoutingKey = "url.created"
  - Body: URLCreatedEvent JSON

Assert:
  - notificationFromDelivery ok == true
  - eventID != ""
  - rec.EventType == "url.created" (dùng routing key)
  - Chưa ack/nack (ack=0, nack=0) — ack sẽ xảy ra sau khi insert
```

#### 2.5.2 `TestNotificationConsumer_UnknownRoutingKeyAcksNoInsert` (hàng 44–65)

```
Input:
  - RoutingKey = "unknown.event"
  - Body: URLCreatedEvent JSON

Assert:
  - acks == 1 (event được ack)
  - nacks == 0
  - store.insertCount == 0
```

**Pattern:** Unknown event types được ack bỏ qua, không nack để tránh requeue loop.

#### 2.5.3 `TestNotificationConsumer_MilestoneEmptyUserIDAcksNoInsert` (hàng 67–90)

```
Input:
  - EventType = "milestone.reached"
  - MilestoneReachedEvent{UserID: ""}
  - Consumer type: notification

Assert:
  - acks == 1
  - insertCount == 0 (không insert notification cho milestone không có user)
```

**Edge case:** Milestone events có thể không có UserID (anonymous clicks). Notification consumer phải bỏ qua những event này.

#### 2.5.4 `TestNotificationHandler_InvalidAfterCursorReturns400` (hàng 92–106)

```
Input:
  - Query: /notifications?after=not-a-uuid
  - store.listCount tracking

Assert:
  - Status == 400
  - store.listCount == 0 (không gọi DB)
```

#### 2.5.5 `TestNotificationHandler_EmptyNotificationsResponse` (hàng 108–129)

```
Input:
  - store trả về empty slice

Assert:
  - Status == 200
  - notifications != nil (empty slice, not nil)
  - len(notifications) == 0
  - NextCursor == nil
```

**Important:** Response luôn trả về `"notifications": []` thay vì `"notifications": null` (JSON consistency).

#### 2.5.6 `TestNotificationHandler_LimitDefaultAndMax` (hàng 131–157)

Table-driven test với 2 cases:

| Test name | URL | Expected limit |
|-----------|-----|----------------|
| `"default"` | `/notifications` | `defaultNotificationLimit` |
| `"max"` | `/notifications?limit=999` | `maxNotificationLimit` |

---

### 2.6 Shared Events Tests (`shared/events/events_test.go`) — 117 dòng

#### 2.6.1 `TestJSONRoundTrip` (hàng 9–117)

4 subtests cho mỗi event type:

**URLCreatedEvent** (hàng 12–41):
```
Input: URLCreatedEvent với ExpiresAt time
Assert: EventID, ShortCode preserved sau JSON round-trip
```

**URLClickedEvent** (hàng 43–68):
```
Input: URLClickedEvent với IPHash, UserAgent, Referer, ClickedAt
Assert: IPHash preserved
```

**URLDeletedEvent** (hàng 70–91):
```
Input: URLDeletedEvent với UserID
Assert: UserID preserved
```

**MilestoneReachedEvent** (hàng 93–116):
```
Input: MilestoneReachedEvent với MilestoneN=10, TotalClicks=15
Assert: MilestoneN preserved
```

**Lưu ý về time precision:** JSON marshaling của `time.Time` có precision khác nhau giữa các môi trường, nên test dùng field-by-field comparison thay vì `reflect.DeepEqual`.

---

## 3. Test Patterns

### 3.1 Table-Driven Tests

Được sử dụng trong:
- `services/user-service/user_test.go:TestValidateEmail` — 6 test cases inline
- `services/user-service/user_test.go:TestValidatePassword` — 5 test cases inline
- `services/analytics-service/analytics_test.go:TestStatsHandler_TimeLineInvalidIntervalsReturn400` — 5 intervals
- `services/analytics-service/analytics_test.go:TestStatsHandler_TimeLineValidIntervalsReturnEmptyPoints` — 2 intervals
- `services/notification-service/notification_test.go:TestNotificationHandler_LimitDefaultAndMax` — 2 test cases

Template:
```go
cases := []struct {
    input   string
    wantErr bool
}{
    {"valid@example.com", false},
    {"invalid", true},
}
for _, tc := range cases {
    t.Run(tc.input, func(t *testing.T) {
        err := validateEmail(tc.input)
        if (err != nil) != tc.wantErr {
            t.Errorf("validateEmail(%q) err=%v, wantErr=%v", tc.input, err, tc.wantErr)
        }
    })
}
```

### 3.2 Subtests (`t.Run`)

51 subtests tổng cộng across all test files:
- `services/url-service/url_test.go` — 18 subtests
- `services/user-service/user_test.go` — 8 subtests (trong table-driven) + handlers
- `services/analytics-service/analytics_test.go` — 2 subtests groups × multiple cases
- `services/notification-service/notification_test.go` — 2 subtests
- `shared/events/events_test.go` — 4 subtests
- `gateway/gateway_test.go` — 0 subtests (top-level functions)

### 3.3 Error Assertion Patterns

**Pattern 1: `errors.Is` — Sentinel error checking**
```go
if !errors.Is(err, ErrCircuitOpen) {
    t.Fatalf("expected ErrCircuitOpen, got %v", err)
}

if !errors.Is(err, ErrPasswordMismatch) {
    t.Errorf("want ErrPasswordMismatch, got %v", err)
}

if !errors.Is(err, ErrTokenInvalid) {
    t.Errorf("want ErrTokenInvalid, got %v", err)
}
```

**Pattern 2: Direct error comparison (không wrapping)**
```go
if err == nil { t.Fatal("expected error") }        // chỉ check có error
if rec.Code != http.StatusNotFound { ... }          // check status code
```

**Pattern 3: `errors.As` type assertion (trong implementation)**
```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    // UNIQUE_VIOLATION — retry with new code
}
```

### 3.4 Mock Objects

5 loại mock objects được sử dụng:

| Mock Object | Package | Interface | Fields |
|-------------|---------|-----------|--------|
| `fakeRateLimiter` | gateway | `rateLimiter` interface | allowed, retryAfter, err, key |
| `fakeClickStore` | analytics | implicit | countErr, topReferersLimit, timelineInterval |
| `fakeMilestoneStore` | analytics | implicit | recorded, inserted, insertCount |
| `fakePublisher` | analytics | implicit | events, err |
| `fakeTx` | analytics | `pgx.Tx` | count |
| `fakeRow` | analytics | `pgx.Row` | value |
| `fakeAcknowledger` | analytics + notification | `amqp.Acknowledger` | acks, nacks, rejects |
| `fakeNotificationStore` | notification | implicit | insertCount, inserted, listCount, notifications, nextCursor |
| `mockURLStore` | url-service | `URLStore` | insertFn, findByCodeFn, findByUserIDFn, deactivateFn |
| `mockOutboxStore` | url-service | `OutboxStore` | insertEventFn, fetchUnpublishedFn, markPublishedFn |
| `mockCache` | url-service | `Cache` | getFn, setFn, deleteFn |
| `mockGenerator` | url-service | `ShortCodeGenerator` | generateFn |
| `mockPool` | url-service | `pgxPool` | Begin |
| `mockTx` | url-service | `pgx.Tx` | Commit, Rollback |
| `mockStore` | user-service | implicit | insertFn, findByEmailFn |
| `mockIssuer` | user-service | `TokenIssuer` | issueFn, verifyFn |

---

## 4. Smoke Test và E2E Test Scripts

### 4.1 Smoke Test (`scripts/smoke_test.sh`) — 24 dòng

**Mục đích:** Kiểm tra tất cả services đều healthy trước khi chạy E2E.

**Logic:**
```bash
PORTS=(8080 8081 8082 8083 8084)  # gateway, url, analytics, notification, user
MAX_RETRIES=30                     # ~60 giây timeout
SLEEP_SECS=2

for port in PORTS:
  count = 0
  until curl http://localhost:port/health có "status":"ok":
    count++
    if count >= MAX_RETRIES: exit 1
    sleep 2
```

**Health check endpoints:**
- `localhost:8080/health` — Gateway health
- `localhost:8081/health` — URL service health
- `localhost:8082/health` — Analytics service health
- `localhost:8083/health` — Notification service health
- `localhost:8084/health` — User service health

### 4.2 E2E Test (`scripts/e2e_test.sh`) — 123 dòng

**Flow 11 bước:**

```
Bước 1: Register
  POST /api/auth/register
  Body: {email, password}
  Expected: 201 Created

Bước 2: Login
  POST /api/auth/login
  Body: {email, password}
  Expected: 200 OK, extract token

Bước 3: Shorten
  POST /api/shorten
  Header: Authorization: Bearer <token>
  Body: {url, expires_in_hours: 24}
  Expected: 201 Created, extract short_code

Bước 4: Redirect × 15 lần
  GET /r/<short_code>
  Expected: 301 hoặc 308 (15 lần liên tiếp)

Bước 5: Wait
  sleep 5 (chờ outbox consumer và analytics processing)

Bước 6: Stats
  GET /api/stats/<short_code>
  Expected: 200, total_clicks >= 15

Bước 7: Notifications
  GET /api/notifications
  Header: Authorization: Bearer <token>
  Expected: 200, có ít nhất 1 notification
  Expected: Có event_type == "milestone.reached"

Bước 8: Delete
  DELETE /api/urls/<short_code>
  Header: Authorization: Bearer <token>
  Expected: 204 No Content

Bước 9: Deleted Redirect → 410
  GET /r/<short_code>
  Expected: 410 Gone

Bước 10: Rate Limit
  POST /api/shorten × 11 lần
  Expected: Ít nhất 1 lần trả về 429 Too Many Requests

Bước 11: Correlation Header
  GET /health hoặc GET /api/stats/<code>
  Expected: X-Correlation-ID header tồn tại
```

**Edge case testing trong E2E:**
- **Expired URL**: Không test trực tiếp (cần tạo URL với expires ngắn)
- **Invalid URL**: Không test
- **Unauthorized access**: Không test (route protection)
- **Circuit breaker**: Không test (cần stop upstream service)

**Gaps trong E2E:**
- Không test URL với expires_in_hours = 0 hoặc giá trị âm
- Không test pagination
- Không test concurrent access
- Không test database failure scenarios
- Không test malformed request bodies

### 4.3 Seed Script (`scripts/seed_demo.sh`) — 161 dòng

Tạo dữ liệu demo với 6 URLs, generate clicks ngẫu nhiên trong 30 ngày (≈150 clicks/code), milestones, và notifications.

**Các User Agents mô phỏng:**
- Chrome 120 (Linux)
- Firefox 121 (Windows)
- Safari 604.1 (Mac)
- Mobile Safari (iOS 17.1)

**Các Referers mô phỏng:**
- Google Search
- Twitter share
- Reddit /r/programming
- GitHub trending
- Facebook
- Direct (empty)

---

## 5. K6 Load Testing

### 5.1 Load Test Chính (`scripts/load_test.js`) — 218 dòng

**Cấu hình:**
```javascript
export const options = {
  scenarios: {
    circuit_breaker_stress: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "20s", target: 200 },   // Warm-up: ramp to 200 VUs
        { duration: "20s", target: 500 },   // Build up
        { duration: "20s", target: 1000 },  // Target: ~10k req/s
        { duration: "60s", target: 1000 },  // Hold at peak – watch CB trip!
        { duration: "20s", target: 0 },     // Ramp down
      ],
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<2000"],    // 95% dưới 2s
    error_rate: ["rate<0.95"],            // Cho phép 95% lỗi khi CB OPEN
  },
};
```

**Custom Metrics:**
- `circuit_breaker_open_responses` (Counter) — đếm response 503
- `error_rate` (Rate) — tỷ lệ lỗi
- `redirect_duration_ms` (Trend) — phân phối latency

**Setup function:**
1. Nếu `SHORT_CODE` env được set → dùng luôn
2. Nếu không → register user mới → login → tạo short URL → trả về short code

**Main VU function:**
```javascript
export default function (data) {
  const url = `${BASE_URL}/r/${shortCode}`;
  const res = http.get(url, { redirects: 0, timeout: "5s" });
  
  // Track CB open responses
  if (res.status === 503) cbOpenResponses.add(1);
  
  // Track errors (status 0 = timeout, >= 500 except 503)
  const isError = res.status === 0 || (res.status >= 500 && res.status !== 503) || res.status === 503;
  errorRate.add(isError ? 1 : 0);
  
  check(res, {
    "status is 2xx, 3xx, or 503 (CB open)": (r) =>
      (r.status >= 200 && r.status < 400) || r.status === 503,
  });
  
  sleep(0.001);  // 1ms think time
}
```

**VUs → Throughput estimate:**
- 1000 VUs × ~10 requests/s per VU ≈ 10,000 req/s
- Mỗi VU sleep 1ms giữa các requests

### 5.2 Load Test 1M RPS (`scripts/load_test_1m_rps.js`) — 153 dòng

**Cấu hình:**
```javascript
export const options = {
  scenarios: {
    max_throughput: {
      executor: "constant-arrival-rate",
      rate: TARGET_RATE,           // mặc định 100,000
      timeUnit: "1s",
      duration: TEST_DURATION,     // mặc định 60s
      preAllocatedVUs: 2000,
      maxVUs: 50000,
    },
  },
  thresholds: {
    errors: ["rate<0.99"],
    http_req_duration: ["p(95)<10000"],
  },
};
```

**Mixed workload:**
- `CREATE_RATIO = 0.3` (30% requests là tạo URL mới)
- `POOL_SIZE = 10000` (pool codes từ setup)
- 70% requests là GET redirect

**Setup tạo pool codes:**
```javascript
for (let attempt = 0; attempt < POOL_SIZE && !rateLimited; attempt++) {
  const res = http.post(`${BASE_URL}/api/shorten`, ...);
  if (res.status === 201 || res.status === 200) {
    pool.push(code);
  } else if (res.status === 429) {
    rateLimited = true;  // rate limit gateway (10 shortens/60s/IP)
  }
}
```

**WARNING:** Test này có thể bị rate limit ở gateway vì chỉ 10 shortens/60s/IP. Cần tăng `SHORTEN_RATE_LIMIT=10000` khi chạy.

### 5.3 Summary và Reporting

Cả 2 load tests đều có `handleSummary` function xuất báo cáo dạng:
```
╔══════════════════════════════════════════════════════╗
║           k6 Load Test Summary                       ║
╠══════════════════════════════════════════════════════╣
║  Total Requests   : 123456                           ║
║  Duration         : 140.0s                           ║
║  Avg Throughput   : 882 req/s                        ║
║  CB Open (503)    : 50000                            ║
║  P95 Latency      : 1500ms                           ║
╚══════════════════════════════════════════════════════╝
```

---

## 6. Error Types — 25+ Sentinel Errors

### 6.1 Gateway (`gateway/circuitbreaker.go`)

| Error | Value | Usage |
|-------|-------|-------|
| `ErrCircuitOpen` | `"circuit open"` | Circuit breaker từ chối request |

### 6.2 URL Service (`services/url-service/errors.go`)

| Error | Value | HTTP Status |
|-------|-------|-------------|
| `ErrNotFound` | `"url not found"` | 404 |
| `ErrInvalidURL` | `"invalid URL"` | 400 |
| `ErrAlreadyExists` | `"short code already exists"` | 409 |
| `ErrForbidden` | `"forbidden"` | 403 |
| `ErrExpired` | `"url has expired"` | 410 |
| `ErrDeactivated` | `"url has been deactivated"` | 410 |
| `ErrDatabaseError` | `"database error"` | 500 |
| `ErrCacheError` | `"cache error"` | 500 (log only, non-fatal) |

### 6.3 User Service (`services/user-service/errors.go`)

| Error | Value | HTTP Status |
|-------|-------|-------------|
| `ErrDuplicateEmail` | `"duplicate email"` | 409 |
| `ErrUserNotFound` | `"user not found"` | 401 (generic) |
| `ErrPasswordMismatch` | `"password mismatch"` | 401 (generic) |
| `ErrTokenInvalid` | `"invalid token"` | 401 |
| `ErrInvalidEmail` | `"invalid email"` | 400 |
| `ErrInvalidPassword` | `"invalid password"` | 400 |

### 6.4 Shared Auth (`shared/auth/auth.go`)

| Error | Value | Usage |
|-------|-------|-------|
| `ErrTokenInvalid` | `"invalid token"` | JWT verification failure |

### 6.5 HTTPError Wrapper (`services/url-service/service.go`)

```go
type HTTPError struct {
    Status int   // HTTP status code
    Err    error // wrapped sentinel error
}
```

### 6.6 Internal Errors (không exported)

**URL Service (`validate.go`):**
- `"URL cannot be empty"` — raw string error
- `"invalid URL format"` — raw string error  
- `"URL must start with http or https"` — raw string error
- `"URL must have a host"` — raw string error

**Base62 (`base62.go`):**
- `"invalid short code length"` — raw string error
- `"invalid character in short code"` — raw string error

### 6.7 Response Error Types (User Service)

```go
type errorResponse struct {
    Error string `json:"error"`
}

type fieldErrorResponse struct {
    Error string `json:"error"`
    Field string `json:"field,omitempty"`
}
```

**JSON Error Response Examples:**
```json
// User Service — validation error
{"error":"invalid password", "field":"password"}

// User Service — generic error
{"error":"invalid credentials"}

// Gateway — rate limit
{"error":"rate limit exceeded"}

// All services — generic
{"error":"<message>"}
```

---

## 7. HTTP Status Code Mapping

### 7.1 Successful Responses

| HTTP Status | Ý nghĩa | Sử dụng tại |
|-------------|---------|-------------|
| **200 OK** | Thành công | Login, Me, Stats, List URLs, Notifications |
| **201 Created** | Tạo thành công | Register, Shorten |
| **204 No Content** | Xóa thành công | Delete URL, CORS preflight |
| **308 Permanent Redirect** | Redirect | Redirect endpoint |

### 7.2 Client Error Responses

| HTTP Status | Ý nghĩa | Sentinel Error | Service |
|-------------|---------|----------------|---------|
| **400 Bad Request** | Input không hợp lệ | `ErrInvalidURL`, email/password validation | url-service, user-service |
| **401 Unauthorized** | Không có token hoặc token không hợp lệ | JWT middleware, `ErrUserNotFound` | gateway, user-service |
| **403 Forbidden** | Không phải owner hoặc URL đã inactive | `ErrForbidden`, `pgx.ErrNoRows` | url-service |
| **404 Not Found** | Short code không tồn tại | `ErrNotFound`, `pgx.ErrNoRows` | url-service (redirect) |
| **409 Conflict** | Email hoặc short code đã tồn tại | `ErrDuplicateEmail`, `ErrAlreadyExists` | user-service, url-service |
| **410 Gone** | URL đã hết hạn hoặc bị deactivate | `ErrExpired`, `ErrDeactivated` | url-service (redirect) |
| **429 Too Many Requests** | Rate limit exceeded | Rate limiter | gateway |

### 7.3 Server Error Responses

| HTTP Status | Ý nghĩa | Service |
|-------------|---------|---------|
| **500 Internal Server Error** | Lỗi database hoặc unexpected error | url-service, analytics-service |
| **503 Service Unavailable** | Circuit breaker OPEN | gateway |

### 7.4 Status Code Flow Diagrams

**Shorten Flow:**
```
POST /api/shorten
  → [400] invalid request body
  → [401] user not authenticated / invalid user token
  → [400] invalid URL (validation)
  → [500] database error
  → [409] already exists (after 3 retries)
  → [201] success
```

**Redirect Flow:**
```
GET /r/<code>
  → [400] missing short code
  → [404] URL not found
  → [410] URL expired
  → [410] URL deactivated
  → [500] database error
  → [308] success
```

**Auth Flow:**
```
POST /api/auth/register
  → [400] invalid email
  → [400] invalid password
  → [409] duplicate email
  → [201] success

POST /api/auth/login
  → [401] user not found (generic "invalid credentials")
  → [401] password mismatch (generic "invalid credentials")
  → [200] success
```

**Gateway Flow:**
```
Any request
  → [429] rate limit exceeded
  → [503] circuit breaker open (url-service only)
  → [404] route not found

Protected route (requires auth)
  → [401] missing/invalid Authorization header
```

---

## 8. Error Handling Patterns

### 8.1 Sentinel Error Pattern

Definition: Errors defined as package-level variables for comparison with `errors.Is`.

```go
// services/url-service/errors.go
var ErrNotFound = errors.New("url not found")
var ErrInvalidURL = errors.New("invalid URL")
var ErrExpired = errors.New("url has expired")

// services/user-service/errors.go
var ErrDuplicateEmail = errors.New("duplicate email")
var ErrPasswordMismatch = errors.New("password mismatch")
```

### 8.2 Error Wrapping Pattern

Sentinel errors được wrap trong `HTTPError` struct để mang thông tin HTTP status:

```go
type HTTPError struct {
    Status int
    Err    error
}

func (e *HTTPError) Error() string {
    return e.Err.Error()
}

// Usage:
return nil, &HTTPError{
    Status: http.StatusGone,
    Err:    ErrExpired,
}
```

**So sánh với `fmt.Errorf(": %w"):`** Project này chọn struct wrapping thay vì `%w` vì cần mang thêm HTTP status. `errors.Is` vẫn hoạt động được với struct wrapping nếu struct implement `Unwrap()` hoặc nếu so sánh trực tiếp field `Err`.

### 8.3 Fail-Open Pattern

Khi một dependency không khả dụng, hệ thống vẫn hoạt động với degraded functionality:

```go
// Rate limiter fail-open
allowed, retryAfter, err := h.checkRateLimit(r, route.RateLimitKey)
if err != nil {
    h.log.Warn("rate limiter failed open", "route", route.RateLimitKey, "error", err)
} else if !allowed {
    // only reject when rate limiter explicitly says "not allowed"
    writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
    return
}
```

```go
// Cache fail-open
cached, err := s.cache.Get(ctx, shortCode)
if err == nil && cached != nil {
    // cache hit — use it
}
// cache miss or error — fallback to DB
```

```go
// Analytics publish fail-open
go func() {
    _ = s.cache.Set(context.Background(), shortCode, cached, ttl)
}()
// Cache set failure is silently ignored
```

### 8.4 Middleware Chain Error Handling

Gateway middleware chain:
```
Request → correlationIDMiddleware → corsMiddleware → jwtMiddleware → handler.ServeHTTP
                                    ↓
                            rate limit check
                                    ↓
                        circuit breaker check (url-service only)
                                    ↓
                            proxy.ServeHTTP (upstream)
```

**Mỗi middleware có trách nhiệm:**
- `correlationIDMiddleware` — inject/generate correlation ID
- `corsMiddleware` — set CORS headers, intercept OPTIONS
- `jwtMiddleware` — validate JWT, add claims to context, skip public routes
- `handler.ServeHTTP` — rate limit, circuit breaker, route to upstream

### 8.5 Database Error Handling

**Unique constraint violation (PostgreSQL code 23505):**
```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    time.Sleep(time.Duration(attempt*50) * time.Millisecond)
    continue  // retry with new code
}
```

**Not found (pgx.ErrNoRows):**
```go
if errors.Is(err, pgx.ErrNoRows) {
    return nil, &HTTPError{Status: http.StatusNotFound, Err: ErrNotFound}
}
```

**Forbidden (pgx.ErrNoRows trong UPDATE):**
```go
// Khi UPDATE WHERE user_id = ? AND short_code = ? trả về 0 rows
if errors.Is(err, pgx.ErrNoRows) {
    return &HTTPError{Status: http.StatusForbidden, Err: ErrForbidden}
}
```

### 8.6 Outbox Pattern Error Handling

Transactional outbox đảm bảo event không bị mất:

```go
pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
    s.store.Insert(ctx, tx, record)      // INSERT URL
    s.outboxStore.InsertEvent(ctx, tx, outbox)  // INSERT outbox event
    return nil
})  // Atomic commit or rollback
```

Nếu `InsertEvent` fail, cả transaction rollback, URL cũng không được insert.

### 8.7 Async Error Handling

Các goroutines async không được kiểm soát lỗi:
```go
go func() {
    _ = s.cache.Set(context.Background(), shortCode, cached, ttl)
}()

go h.writeAnalyticsEvent(r, shortcode, ...)
```

**Rủi ro:** Nếu cache hoặc outbox liên tục fail, goroutines sẽ leak.

---

## 9. Phân Tích Edge Cases

### 9.1 Implemented Edge Cases

| # | Edge Case | Implemented | Test Coverage |
|---|-----------|-------------|---------------|
| 1 | **Empty URL** | ✅ `ValidateURL("")` → error | ❌ Không có unit test riêng |
| 2 | **URL without scheme** | ✅ "invalid-url-no-scheme" → 400 | ✅ `TestHandlerShorten/Invalid_URL` |
| 3 | **URL with wrong scheme** | ✅ http/https only | ❌ Không test riêng |
| 4 | **URL without host** | ✅ `u.Host == ""` → error | ❌ Không test riêng |
| 5 | **Empty short code** | ✅ `shortcode == ""` → 400 | ❌ Không test |
| 6 | **Invalid short code (bad chars)** | ✅ Base62 decode error | ✅ `TestBase62DecodeErrors` |
| 7 | **Short code wrong length** | ✅ 7 chars required | ✅ `TestBase62DecodeErrors` |
| 8 | **Short code empty string** | ✅ decode error | ✅ `TestBase62DecodeErrors` |
| 9 | **URL expired (past ExpiresAt)** | ✅ 410 Gone | ✅ `TestHandlerRedirect/Expired_URL` |
| 10 | **URL deactivated (IsActive=false)** | ✅ 410 Gone | ✅ `TestHandlerRedirect/Deactivated_URL` |
| 11 | **URL not found** | ✅ 404 via pgx.ErrNoRows | ✅ `TestHandlerRedirect/Not_Found` |
| 12 | **Unique collision retry** | ✅ 3 attempts + backoff | ✅ `TestHandlerShorten/Collision_Retry_Success` |
| 13 | **Duplicate email register** | ✅ 409 Conflict | ✅ `TestRegisterHandler_DuplicateEmail` |
| 14 | **Wrong password login** | ✅ 401 generic message | ✅ `TestLoginHandler_WrongPassword` |
| 15 | **Unknown email login** | ✅ 401 generic message | ✅ `TestLoginHandler_UnknownEmail` |
| 16 | **Rate limit exceeded** | ✅ 429 Too Many Requests | ✅ `TestRateLimitRejectsAndFailOpen` |
| 17 | **Circuit breaker OPEN** | ✅ 503 Service Unavailable | ✅ `TestCircuitBreakerTransitions` |
| 18 | **Missing auth token** | ✅ 401 Unauthorized | ✅ `TestJWTMiddlewareProtectsPrivateRoutes` |
| 19 | **Invalid JWT signature** | ✅ 401 Unauthorized | ✅ `TestJWTTokenIssuer_Verify_InvalidSignature` |
| 20 | **Expired JWT** | ✅ 401 Unauthorized | ✅ `TestJWTTokenIssuer_Verify_Expired` |
| 21 | **Malformed JWT** | ✅ 401 Unauthorized | ✅ `TestJWTTokenIssuer_Verify_Malformed` |
| 22 | **Redis cache miss** | ✅ nil result | ✅ `TestRedisCache` |
| 23 | **Redis cache hit** | ✅ CachedURL returned | ✅ `TestRedisCache` |
| 24 | **Redis connection error** | ✅ non-fatal fallback | ✅ `TestRedisCache` |
| 25 | **Rate limiter Redis error** | ✅ fail open | ✅ `TestRateLimitRejectsAndFailOpen` |
| 26 | **Malformed JSON in request** | ✅ 400 Bad Request | ❌ Không có unit test |
| 27 | **Invalid email format** | ✅ validation | ✅ `TestValidateEmail` |
| 28 | **Short password (< 8 chars)** | ✅ 400 Bad Request | ✅ `TestRegisterHandler_ShortPassword` |
| 29 | **Deactivate not-owner URL** | ✅ 403 Forbidden | ✅ `TestHandlerDelete/Forbidden` |
| 30 | **Unknown routing key (RabbitMQ)** | ✅ ack + skip | ✅ `TestNotificationConsumer_UnknownRoutingKeyAcksNoInsert` |
| 31 | **Malformed JSON (RabbitMQ)** | ✅ ack + skip | ✅ `TestClickConsumer_ParseMalformedJSONAcks` |
| 32 | **Empty milestone UserID** | ✅ ack + skip | ✅ `TestNotificationConsumer_MilestoneEmptyUserIDAcksNoInsert` |
| 33 | **Milestone already recorded** | ✅ dedup by DB | ✅ `TestMilestoneChecker_AlreadyRecorded_NoPublish` |
| 34 | **Multiple milestones at once** | ✅ all thresholds | ✅ `TestMilestoneChecker_MultipleThresholdsAtOnce` |
| 35 | **Pagination hasMore** | ✅ cursor-based | ✅ `TestHandlerList/Pagination_HasMore` |
| 36 | **Empty list response** | ✅ empty slice, not nil | ✅ `TestNotificationHandler_EmptyNotificationsResponse` |
| 37 | **Invalid after cursor** | ✅ 400 Bad Request | ✅ `TestNotificationHandler_InvalidAfterCursorReturns400` |
| 38 | **CORS preflight OPTIONS** | ✅ 204 intercepted | ✅ `TestCorsMiddleware` |
| 39 | **Public routes bypass JWT** | ✅ next handler called | ✅ `TestJWTMiddlewareSkipsPublicRoutes` |
| 40 | **Bcrypt different salts** | ✅ hashes differ | ✅ `TestBcryptHasher_DifferentHashesSamePassword` |

### 9.2 Edge Cases Chưa Được Implement

| # | Edge Case | Risk | Priority |
|---|-----------|------|----------|
| 1 | **expires_in_hours = 0** | Mặc định 24h, nhưng có thể gây nhầm lẫn | Low |
| 2 | **expires_in_hours = negative** | Mặc định 24h | Low |
| 3 | **expires_in_hours > 8760 (365 days)** | Bị clamp về 24h | Low |
| 4 | **Concurrent duplicate short code** | 3 retries, nhưng 2 goroutines cùng lúc có thể cùng code | Medium |
| 5 | **SQL injection** | ORM (pgx) prevents, nhưng cần verify | Low |
| 6 | **Large request body** | Không có size limit | Medium |
| 7 | **Binary data in URL field** | `url.Parse` xử lý được, nhưng có thể gây lỗi | Low |
| 8 | **Unicode/emoji in URL** | `url.Parse` hỗ trợ, nhưng chưa được test | Low |
| 9 | **Very long URL (>2048 chars)** | Chưa có validation | Medium |
| 10 | **Concurrent URL deactivation** | Race condition có thể xảy ra | Medium |
| 11 | **Zero-length short code pool** | Generator không kiểm tra uniqueness global | Medium |
| 12 | **JWT token with alg: none** | Cần verify `token.Method` là HMAC (đã làm trong VerifyToken) | ✅ Fixed |
| 13 | **Database connection pool exhaustion** | Không có circuit breaker cho DB | High |
| 14 | **RabbitMQ connection lost** | Không có reconnect logic test | High |
| 15 | **Memory leak in goroutines** | Async cache set không có timeout | Medium |
| 16 | **Panic recovery in HTTP handlers** | Không có `recover()` middleware | High |
| 17 | **Time zone handling** | `time.Now()` dùng local time, có thể gây issue | Low |
| 18 | **Negative limit in pagination** | `math.Max(1, ...)` protects, nhưng chưa test | Low |
| 19 | **Very large limit (>100)** | Clamp to 100 | ❌ Không test |
| 20 | **Delete already-deleted URL** | Trả về 403 thay vì 404 | Low |
| 21 | **Stats for non-existent code** | Trả về zeros (design decision) | ✅ Implemented |
| 22 | **Notification list for non-existent user** | Empty list (no error) | ❌ Không test |
| 23 | **Gateway upstream timeout** | `context.WithTimeout` chỉ 50ms cho cache | Medium |
| 24 | **Metric label explosion** | `class := fmt.Sprintf("%dxx", status/100)` — unlimited cardinality | High |
| 25 | **Race condition in circuit breaker** | sync.Mutex protects state, but halfOpenProbe có race | ✅ sync.Mutex |

### 9.3 Analysis: Race Conditions

**Circuit Breaker State Machine Race:**
```go
// Potential race (dù có mutex):
cb.mu.Lock()
switch cb.state {
case StateHalfOpen:
    if cb.halfOpenProbe {
        cb.mu.Unlock()
        return ErrCircuitOpen  // ← unlock trước khi return
    }
    cb.halfOpenProbe = true
case StateOpen:
    if time.Since(cb.lastFailureTime) <= cb.openTimeout {
        cb.mu.Unlock()
        return ErrCircuitOpen  // ← unlock trước khi return
    }
}
cb.mu.Unlock()
// ← gap: state có thể thay đổi giữa unlock và upstream call
err := upstream()
```

**Mitigation:** Mutex được lock/unlock đúng cách, nhưng có TOC/TOU (time-of-check-time-of-use) window. Tuy nhiên với circuit breaker pattern, đây là compromise chấp nhận được.

---

## 10. Đánh Giá Testing Coverage

### 10.1 Lines of Test Code

| File | Lines Code | Lines Test | Ratio |
|------|-----------|------------|-------|
| `gateway/gateway_test.go` | ~700 | 241 | 34% |
| `services/url-service/url_test.go` | ~900 | 779 | 87% |
| `services/user-service/user_test.go` | ~400 | 372 | 93% |
| `services/analytics-service/analytics_test.go` | ~500 | 376 | 75% |
| `services/notification-service/notification_test.go` | ~300 | 227 | 76% |
| `shared/events/events_test.go` | ~100 | 117 | 117% (extra test utilities) |
| **Total** | **~2900** | **2112** | **73%** |

### 10.2 Test Distribution by Category

| Category | Count | Files |
|----------|-------|-------|
| **Handler/HTTP tests** | 19 functions | gateway_test, url_test, user_test, analytics_test, notification_test |
| **Business logic tests** | 10 functions | base62, codegen, validate, bcrypt, JWT |
| **Cache tests** | 1 function (comprehensive) | url_test |
| **Consumer/Event tests** | 6 functions | analytics_test, notification_test, events_test |
| **Middleware tests** | 4 functions | gateway_test |
| **Table-driven tests** | 4 functions | user_test (email, password), analytics_test (intervals), notification_test (limit) |
| **Subtest count** | ~51 | All test files |

### 10.3 Coverage by Package (ước lượng)

| Package | Estimated Coverage | Gaps |
|---------|-------------------|------|
| `gateway` | **~60%** | Circuit breaker: ✅ đầy đủ. Rate limiter: ✅. Router: ✅. JWT middleware: ✅. CORS: ✅. Proxy: ❌ không test. Config: ❌ không test. |
| `url-service` | **~80%** | Base62: ✅ đầy đủ. Codegen: ✅. Service: ✅ (Shorten, Redirect, List, Deactivate). Cache: ✅. Store: ❌ integration test. Outbox: ❌ không test. |
| `user-service` | **~85%** | Validation: ✅. Bcrypt: ✅. JWT: ✅. Handlers: ✅. Store: ❌ integration test. |
| `analytics-service` | **~60%** | Stats handler: ✅. Milestone checker: ✅. Click consumer: ✅. Store: ❌ integration test. |
| `notification-service` | **~55%** | Consumer: ✅. Handler: ✅. Store: ❌ integration test. |
| `shared/events` | **~90%** | JSON round-trip: ✅. |
| `shared/auth` | **~40%** | VerifyToken được test qua user-service, nhưng không có unit test riêng cho `ClaimsFromContext`, `IsExpired`. |
| `shared/logger` | **~0%** | Không có test. |

### 10.4 Integration Test Coverage

| Component | Integration Test | Notes |
|-----------|----------------|-------|
| PostgreSQL | ❌ | Tests dùng mocks, không có testcontainers |
| Redis | ✅ | `miniredis` in-memory mock |
| RabbitMQ | ❌ | Tests dùng fakeAcknowledger |
| JWT | ✅ | Pure Go, no external dependencies |
| Bcrypt | ✅ | Pure Go |
| HTTP/gRPC | ✅ | `httptest.NewServer` + `httptest.NewRecorder` |

### 10.5 What is NOT Tested

**Critical paths không có test:**
1. **Gateway proxy** (`proxy.go`) — không test reverse proxy logic
2. **Outbox publisher** (`rabbitmq.go`, `publisher.go`) — không test RabbitMQ publish
3. **Outbox relay** (`outbox.go`) — không test background outbox processing
4. **Database store** (`store.go`, `outbox_store.go`) — không test SQL queries
5. **Metrics** (`metrics.go`) — không test Prometheus metrics registration
6. **Health endpoints** (`health.go`) — không test health check logic
7. **Config parsing** (`config.go`) — không test environment variable parsing
8. **Seed demo script** — integration với Docker, khó test tự động
9. **Panic recovery** — không có middleware recovery
10. **Graceful shutdown** — không test signal handling

---

## 11. Gaps và Khuyến Nghị

### 11.1 Critical Gaps

| # | Gap | Mức độ | Khuyến Nghị |
|---|-----|--------|-------------|
| 1 | **Không có integration test với DB** | 🔴 Critical | Sử dụng `testcontainers-go` hoặc `database/sql` mock với pgx |
| 2 | **Không có RabbitMQ integration test** | 🔴 Critical | Sử dụng RabbitMQ test container |
| 3 | **Không có panic recovery middleware** | 🔴 Critical | Thêm recover middleware ở gateway |
| 4 | **Async goroutines không có timeout** | 🟡 Medium | Thêm context với timeout cho goroutines |
| 5 | **Không test circuit breaker với real upstream** | 🟡 Medium | Thêm integration test với mock upstream server |
| 6 | **Statscard (Prometheus) label explosion** | 🟡 Medium | Fix metric labels để tránh high cardinality |
| 7 | **Không có test cho outbox relay** | 🟡 Medium | Thêm unit test cho outbox processing |
| 8 | **Concurrent short code generation race** | 🟡 Medium | Thêm unique constraint + retry (đã có 1 phần) |

### 11.2 Recommended Test Additions

**Priority 1 — High Impact:**
```go
// 1. Integration test với PostgreSQL + Redis
func TestURLServiceIntegration(t *testing.T) {
    // Sử dụng testcontainers hoặc docker-compose test setup
    db := setupTestDB(t)
    redis := setupTestRedis(t)
    svc := NewURLService(db, store, outbox, cache, codegen, "http://localhost")
    // Test full flow: Shorten → Redirect → Delete
}

// 2. Test panic recovery
func TestPanicRecoveryMiddleware(t *testing.T) {
    handler := recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        panic("something went wrong")
    }))
    req := httptest.NewRequest("GET", "/", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusInternalServerError {
        t.Fatalf("expected 500 after panic, got %d", rec.Code)
    }
}

// 3. Test concurrent short code collision
func TestConcurrentShortenCollision(t *testing.T) {
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            // Gọi Shorten đồng thời
        }()
    }
    wg.Wait()
    // Verify: không có duplicate codes
}
```

**Priority 2 — Medium Impact:**
```go
// 4. Test rate limiter with Redis mock
func TestRateLimiterWindowExpiry(t *testing.T) {
    mr := miniredis.RunT(t)
    rl := NewRateLimiter(mr.Addr())
    // Test window reset
}

// 5. Test CORS with various origins
func TestCORSMultipleOrigins(t *testing.T) {
    // Không dùng * mà dùng specific origins
}

// 6. Test JWT middleware with alg: none
func TestJWTAlgNone(t *testing.T) {
    token := "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyLTEifQ."
    // Expect: 401
}
```

**Priority 3 — Low Impact:**
```go
// 7. Test expires_in_hours = 0, negative, >365
func TestExpiresInHoursEdgeCases(t *testing.T) {}

// 8. Test very long URL (>2048 chars)
func TestLongURLValidation(t *testing.T) {}

// 9. Test Unicode/emoji in URL
func TestUnicodeURL(t *testing.T) {}

// 10. Test concurrent deactivation
func TestConcurrentDeactivation(t *testing.T) {}
```

### 11.3 Testing Infrastructure Improvements

| Cải Tiến | Chi Phí | Lợi Ích |
|----------|---------|----------|
| **Testcontainers for PostgreSQL** | Medium | Tăng coverage cho store layer đáng kể |
| **Testcontainers for RabbitMQ** | Medium | Kiểm tra outbox → consumer flow |
| **Makefile test targets** | Low | `make test`, `make test-integration`, `make test-e2e` |
| **Test coverage reports** | Low | `go test -coverprofile=coverage.out` |
| **Fuzz testing** | Medium | Cho Base62 decode, JWT verify, URL validation |
| **Benchmark tests** | Low | Cho Base62 encode/decode, bcrypt, rate limiter |
| **Race detector in CI** | Low | `go test -race` phát hiện race conditions |

### 11.4 Security Testing Gaps

| # | Security Concern | Status | Khuyến Nghị |
|---|-----------------|--------|-------------|
| 1 | **JWT alg: none attack** | ✅ Fixed | Kiểm tra `token.Method.(*jwt.SigningMethodHMAC)` |
| 2 | **Password in response** | ✅ Fixed | `strings.Contains(body, "password")` trong test |
| 3 | **Information leak (401 message)** | ✅ Fixed | "invalid credentials" generic |
| 4 | **SQL injection** | ✅ Mitigated | pgx parameterized queries |
| 5 | **XSS via error messages** | ⚠️ Partial | JSON encoding prevents injection |
| 6 | **Rate limit bypass (X-Forwarded-For)** | ⚠️ Partial | `clientIP()` parse first IP |
| 7 | **CORS overly permissive** | ⚠️ Partial | `Access-Control-Allow-Origin: *` |
| 8 | **No request body size limit** | ❌ Missing | Thêm `http.MaxBytesReader` |
| 9 | **No TLS** | ❌ Missing | Development only |
| 10 | **No CSRF protection** | ⚠️ N/A | API service, not browser-based |

---

## 12. Kết Luận

### 12.1 Strengths

1. **Comprehensive handler testing:** HTTP handlers được test kỹ với mock objects và httptest, coverage tốt cho business logic layer.
2. **State machine testing:** Circuit breaker transitions được test exhaustively (CLOSED → OPEN → HALF_OPEN → CLOSED/OPEN).
3. **Edge case awareness:** Nhiều edge cases được xử lý (expired URLs, deactivated, collision retry, etc.).
4. **Error handling patterns:** Sentinel errors, fail-open, HTTPError wrapping — các patterns được áp dụng nhất quán.
5. **Security awareness:** Generic error messages (không leak thông tin), bcrypt salts, JWT signature verification.
6. **Load testing:** k6 scripts with ramping VUs, custom metrics, và circuit breaker stress testing.
7. **E2E testing:** 11-step script covering happy path, rate limiting, deletion, và correlation headers.

### 12.2 Weaknesses

1. **Missing integration tests:** Database, message queue, và full service integration không được test.
2. **Goroutine management:** Async operations (cache set, analytics write) không có error handling hoặc timeout.
3. **Panic recovery:** Không có middleware để recover từ panic, có thể crash toàn bộ service.
4. **Limited negative testing:** Một số edge cases validation không có unit test riêng (empty URL, URL không có host).
5. **Metric label cardinality:** `fmt.Sprintf("%dxx", status/100)` tạo label unbounded.
6. **Config parsing:** Config struct không được test với các environment variable combinations.

### 12.3 Overall Assessment

| Aspect | Score (1-10) | Notes |
|--------|--------------|-------|
| **Unit test coverage** | 7.5/10 | Handler layer tốt, store layer yếu |
| **Error handling** | 8/10 | Sentinel errors, wrapping, fail-open |
| **Edge case coverage** | 7/10 | 40 edge cases implemented, ~10 gaps |
| **Integration test** | 3/10 | Chỉ có miniredis, không có DB/RabbitMQ |
| **E2E test** | 7/10 | 11-step flow, nhưng không test concurrent |
| **Security testing** | 6/10 | JWT, bcrypt, rate limit tested; size limit, TLS missing |
| **Load testing** | 8/10 | k6 script với realistic profile và metrics |
| **Test patterns** | 8/10 | Table-driven, subtests, errors.Is, mocks |
| **Async testing** | 4/10 | Goroutines sleep-based, không deterministic |
| **Documentation** | 6/10 | Test functions có tên rõ ràng, nhưng thiếu comments |

**Overall: 6.4/10** — Cơ sở testing tốt với unit tests và E2E, nhưng cần integration tests và async testing improvements.

---

*Tài liệu này được tạo tự động bởi agent phân tích. Tất cả thông tin dựa trên source code của project tại thời điểm 2026-07-11.*
