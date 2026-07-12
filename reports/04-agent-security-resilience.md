# Báo Cáo Phân Tích Chi Tiết: Security & Resilience Patterns

**Dự án:** URL Shortener Microservices  
**Tác vụ:** 04 — Agent Security & Resilience  
**Ngôn ngữ:** Go  
**Ngày:** 2026-07-11  

---

## Mục Lục

1. [Tổng Quan](#1-tổng-quan)
2. [User Service Authentication](#2-user-service-authentication)
   - 2.1. [Register Flow](#21-register-flow)
   - 2.2. [Login Flow](#22-login-flow)
   - 2.3. [Password Hashing với BCrypt Cost 12](#23-password-hashing-với-bcrypt-cost-12)
   - 2.4. [Input Validation](#24-input-validation)
3. [Timing Attack Mitigation](#3-timing-attack-mitigation)
   - 3.1. [Cơ Chế Dummy BCrypt Hash](#31-cơ-chế-dummy-bcrypt-hash)
   - 3.2. [Phân Tích Hiệu Quả](#32-phân-tích-hiệu-quả)
   - 3.3. [Hạn Chế và Lỗ Hổng Còn Tồn Tại](#33-hạn-chế-và-lỗ-hổng-còn-tồn-tại)
4. [JWT Implementation](#4-jwt-implementation)
   - 4.1. [Cấu Trúc Claims](#41-cấu-trúc-claims)
   - 4.2. [HS256 Signing](#42-hs256-signing)
   - 4.3. [GenerateToken — Quy Trình Phát Hành](#43-generatetoken--quy-trình-phát-hành)
   - 4.4. [VerifyToken — Quy Trình Xác Thực](#44-verifytoken--quy-trình-xác-thực)
   - 4.5. [JWTMiddleware — Tích Hợp vào HTTP](#45-jwtmiddleware--tích-hợp-vào-http)
   - 4.6. [Security Concerns với JWT](#46-security-concerns-với-jwt)
5. [Circuit Breaker](#5-circuit-breaker)
   - 5.1. [Kiến Trúc 3-State Machine](#51-kiến-trúc-3-state-machine)
   - 5.2. [State Transitions Chi Tiết](#52-state-transitions-chi-tiết)
   - 5.3. [Cơ Chế Đồng Bộ RWMutex](#53-cơ-chế-đồng-bộ-rwmutext)
   - 5.4. [Half-Open Probe Mechanism](#54-half-open-probe-mechanism)
   - 5.5. [Metrics và Monitoring](#55-metrics-và-monitoring)
   - 5.6. [Weaknesses và Hạn Chế](#56-weaknesses-và-hạn-chế)
6. [Rate Limiter](#6-rate-limiter)
   - 6.1. [Token Bucket Algorithm](#61-token-bucket-algorithm)
   - 6.2. [Redis INCR và Expire](#62-redis-incr-và-expire)
   - 6.3. [Fail-Open Behavior](#63-fail-open-behavior)
   - 6.4. [Redis Key Management](#64-redis-key-management)
   - 6.5. [Client IP Detection](#65-client-ip-detection)
   - 6.6. [Thiếu Atomicity với Lua Script](#66-thiếu-atomicity-với-lua-script)
7. [Security Analysis Tổng Hợp](#7-security-analysis-tổng-hợp)
   - 7.1. [Token Revocation — Missing](#71-token-revocation--missing)
   - 7.2. [Refresh Tokens — Missing](#72-refresh-tokens--missing)
   - 7.3. [Per-Endpoint Rate Limiting](#73-per-endpoint-rate-limiting)
   - 7.4. [Shared Secret Risk](#74-shared-secret-risk)
   - 7.5. [CORS Configuration](#75-cors-configuration)
   - 7.6. [Correlation ID — Truy Vết Request](#76-correlation-id--truy-vết-request)
   - 7.7. [Content-Type Enforcement](#77-content-type-enforcement)
   - 7.8. [Body Size Limiting](#78-body-size-limiting)
   - 7.9. [Error Message Leakage](#79-error-message-leakage)
   - 7.10. [Database Credential Masking](#710-database-credential-masking)
   - 7.11. [Go Module Isolation](#711-go-module-isolation)
8. [Resilience Analysis Tổng Hợp](#8-resilience-analysis-tổng-hợp)
   - 8.1. [Fail-Open vs Fail-Closed Decisions](#81-fail-open-vs-fail-closed-decisions)
   - 8.2. [Retry Mechanisms — Thiếu Vắng](#82-retry-mechanisms--thiếu-vắng)
   - 8.3. [Graceful Shutdown](#83-graceful-shutdown)
   - 8.4. [Database Connection Pooling](#84-database-connection-pooling)
   - 8.5. [Context Propagation và Timeout](#85-context-propagation-và-timeout)
   - 8.6. [Health Checks](#86-health-checks)
   - 8.7. [Signal Handling](#87-signal-handling)
   - 8.8. [Logging và Observability](#88-logging-và-observability)
9. [Kết Luận và Khuyến Nghị](#9-kết-luận-và-khuyến-nghị)

---

## 1. Tổng Quan

Báo cáo này phân tích chi tiết các mẫu hình **Security** và **Resilience** được triển khai trong hệ thống URL Shortener Microservices. Hệ thống bao gồm nhiều service riêng biệt (user-service, url-service, analytics-service, notification-service) giao tiếp thông qua một API Gateway tập trung. Các cơ chế bảo mật chính bao gồm xác thực người dùng qua JWT, bảo vệ mật khẩu bằng bcrypt, và phòng chống tấn công timing. Về mặt resilience, hệ thống triển khai circuit breaker cho upstream calls, rate limiter dùng Redis, và graceful shutdown.

Mỗi thành phần được phân tích từ góc độ thiết kế, triển khai, điểm mạnh, điểm yếu, và các rủi ro bảo mật tiềm ẩn. Mục tiêu là cung cấp một bức tranh toàn diện về mức độ an toàn và khả năng chống chịu lỗi của hệ thống, đồng thời đề xuất các cải tiến cụ thể.

---

## 2. User Service Authentication

### 2.1. Register Flow

**File:** `services/user-service/handler.go:55-103`

Register flow là quy trình tạo tài khoản người dùng mới. Luồng xử lý được thiết kế như sau:

**Bước 1 — Kiểm tra Content-Type:**  
Handler kiểm tra header `Content-Type` phải là `application/json`. Nếu không đúng, trả về HTTP 415 Unsupported Media Type. Đây là một biện pháp bảo vệ cơ bản chống lại CSRF-based attacks sử dụng form encoding và đảm bảo dữ liệu đến được parse đúng cấu trúc.

**Bước 2 — Giới hạn kích thước body:**  
`r.Body = http.MaxBytesReader(w, r.Body, 1<<20)` — giới hạn body ở 1MB (1048576 bytes). Đây là biện pháp chống DoS qua large payload bombs. Nếu một attacker gửi JSON hợp lệ nhưng có kích thước lớn, Go's JSON decoder sẽ fail khi đọc quá giới hạn. Tuy nhiên, 1MB có thể được coi là quá lớn cho một request register — một email và password thường chỉ vài trăm bytes. Giá trị này có thể giảm xuống 1<<10 (1KB) để an toàn hơn.

**Bước 3 — Parse JSON body:**  
Sử dụng `json.NewDecoder(r.Body).Decode(&req)`. Nếu JSON malformed (syntax error, unexpected EOF, v.v.), handler trả về 400 Bad Request. Một điểm đáng chú ý là `json.NewDecoder` mặc định không từ chối các trường không xác định (unknown fields). Một attacker có thể gửi thêm trường và chúng sẽ bị silently ignored. Tuy không gây hại trực tiếp, nhưng điều này có thể che giấu các cuộc tấn công prototype pollution hoặc mass assignment trong tương lai nếu struct được mở rộng. Nên sử dụng `json.NewDecoder(r.Body).DisallowUnknownFields()`.

**Bước 4 — Validate email:**  
Hàm `validateEmail` sử dụng regex `^[^@\s]+@[^@\s]+\.[^@\s]+$`. Regex này kiểm tra sự tồn tại của ký tự `@` và một dấu chấm sau `@` với ít nhất một ký tự không phải khoảng trắng. Đây là một validation cơ bản, không tuân theo chuẩn RFC 5321/5322 đầy đủ. Ví dụ, email như `"user@[IPv6:2001:db8::1]"` (dạng bracket cho IP) sẽ bị từ chối, nhưng đây là trường hợp hiếm. Quan trọng hơn, regex này không kiểm tra độ dài tối đa (RFC 5321 giới hạn địa chỉ email ở 254 ký tự). Một email cực dài hợp lệ về mặt regex có thể gây ra vấn đề về lưu trữ và indexing trong database.

**Bước 5 — Validate password:**  
Kiểm tra `len(password) < 8`. Chỉ yêu cầu tối thiểu 8 ký tự, không yêu cầu chữ hoa, chữ thường, số, hoặc ký tự đặc biệt. Đây là mức tối thiểu theo khuyến nghị NIST SP 800-63B, nhưng có thể chưa đủ mạnh trong một số ngữ cảnh yêu cầu bảo mật cao. Hệ thống nên bổ sung thêm các rule kiểm tra entropy hoặc sử dụng một thư viện như `go-crack` để phát hiện mật khẩu yếu.

**Bước 6 — Hash password:**  
`h.hasher.Hash(req.Password)` gọi đến `bcrypt.GenerateFromPassword` với cost parameter. Kết quả là một bcrypt hash dạng `$2a$12$...`. Chi phí tính toán là đáng kể — cost 12 mất khoảng 250-400ms trên CPU hiện đại (xem phân tích ở phần 2.3).

**Bước 7 — Insert vào database:**  
`h.store.Insert(r.Context(), req.Email, hash)` thực hiện SQL INSERT. Nếu email đã tồn tại, PostgreSQL trả về unique constraint violation (error code 23505) và handler chuyển thành HTTP 409 Conflict. Thông báo lỗi là "email already registered" — không tiết lộ thông tin về user nào đã tồn tại.

**Bước 8 — Trả về response:**  
`writeJSON(w, http.StatusCreated, registerResponse{UserID: user.ID, Email: user.Email})`. Response chỉ chứa user_id và email, KHÔNG chứa password hash. Một test cụ thể (user_test.go:270-272) kiểm tra điều này: `if strings.Contains(rec.Body.String(), "password") { t.Error(...) }`.

### 2.2. Login Flow

**File:** `services/user-service/handler.go:105-148`

Login flow xác thực thông tin đăng nhập và trả về JWT token:

**Bước 1 — Kiểm tra Content-Type:**  
Giống register, kiểm tra `Content-Type: application/json`. Trả về 415 nếu sai.

**Bước 2 — Giới hạn kích thước body:**  
Giống register, áp dụng `MaxBytesReader` 1MB.

**Bước 3 — Parse JSON body:**  
Giống register, parse `loginRequest` struct chứa `Email` và `Password`.

**Bước 4 — Tìm user theo email:**  
`h.store.FindByEmail(r.Context(), req.Email)` — truy vấn database. Nếu user không tồn tại (`ErrUserNotFound` hoặc `user == nil`), handler thực thi **dummy bcrypt verify** (xem phân tích Section 3) và trả về "invalid credentials". Cả hai trường hợp — email không tồn tại và sai password — đều trả về cùng một thông báo lỗi để tránh tiết lộ thông tin về user.

**Điểm đáng chú ý trong code:**

```go
user, err := h.store.FindByEmail(r.Context(), req.Email)
if user == nil {
    _ = h.hasher.Verify(req.Password, dummyBcryptHash)
    writeError(w, http.StatusUnauthorized, "invalid credentials")
    return
}
```

Thay vì kiểm tra `err != nil`, code kiểm tra `user == nil`. Điều này có nghĩa là nếu `FindByEmail` trả về cả `nil` user và `nil` error (trường hợp không tìm thấy), dummy verify vẫn được thực thi. Tuy nhiên, nếu có database error (connection timeout, v.v.) và trả về `nil` user kèm error, code sẽ rơi vào nhánh này và thực thi dummy verify, dẫn đến false negative — user hợp lệ không thể login do lỗi database, nhưng nhận được "invalid credentials" thay vì "internal server error". Đây là một **bug tiềm ẩn**: dòng 121 nên kiểm tra lỗi trước:

```go
user, err := h.store.FindByEmail(r.Context(), req.Email)
if err != nil {
    if errors.Is(err, ErrUserNotFound) {
        _ = h.hasher.Verify(req.Password, dummyBcryptHash)
        writeError(w, http.StatusUnauthorized, "invalid credentials")
        return
    }
    h.log.Error("find by email failed", "error", err)
    writeError(w, http.StatusInternalServerError, "internal server error")
    return
}
```

Tuy nhiên, trong implementation hiện tại của `FindByEmail` tại store.go:57-73, khi không tìm thấy user thì trả về `(nil, ErrUserNotFound)` và khi có lỗi database thì trả về `(nil, error)` — cả hai đều có user == nil. Điều này có nghĩa là một database error (ví dụ: connection pool exhaustion) sẽ dẫn đến việc user bị từ chối với "invalid credentials" thay vì 500 Internal Server Error. Đây là một thiết kế không lý tưởng.

**Bước 5 — Verify password hash:**  
Nếu user tồn tại, handler gọi `h.hasher.Verify(req.Password, user.PasswordHash)`. Nếu `bcrypt.CompareHashAndPassword` trả về `ErrMismatchedHashAndPassword`, handler trả về 401 "invalid credentials". Các lỗi khác (ví dụ: hash malformed) cũng trả về 401, log lỗi ở server side.

**Bước 6 — Issue JWT:**  
`h.issuer.Issue(user.ID, user.Email)` tạo JWT token với HS256 signature. Trả về token string và thời gian hết hạn.

**Bước 7 — Trả về response:**  
`writeJSON(w, http.StatusOK, loginResponse{Token: tokenStr, ExpiresAt: ...})`. Response chứa token và thời gian hết hạn theo định dạng ISO 8601.

### 2.3. Password Hashing với BCrypt Cost 12

**File:** `services/user-service/password.go:14-26`

**Cấu hình BCrypt:**
```go
cost := 12 // mặc định
type bcryptHasher struct {
    cost int
}
```

Cost parameter trong bcrypt quyết định số vòng lặp của Blowfish key schedule: số vòng = 2^cost. Với cost = 12, có 4096 vòng lặp. Việc này khiến cho mỗi lần hash hoặc verify mật khẩu mất khoảng 250ms trên CPU đơn luồng hiện đại.

**Ưu điểm:**
- **Chống brute-force mạnh mẽ:** Mỗi lần thử mật khẩu tốn ~250ms, giới hạn attacker ở ~4 passwords/second/core. Với 10 cores, attacker chỉ thử được ~40 passwords/second — không khả thi cho online brute-force.
- **Chống GPU/ASIC:** bcrypt yêu cầu nhiều bộ nhớ (4KB cho Blowfish state), không thể tăng tốc hiệu quả trên GPU. So sánh: MD5 có throughput hàng tỷ hash/giây trên GPU, bcrypt chỉ vài nghìn hash/giây.
- **Salt tự động:** bcrypt tự động tạo salt 16-byte ngẫu nhiên, salt được lưu trong chính hash string. Không cần quản lý salt riêng.
- **Hash format chuẩn:** `$2a$12$...` với $2a$ là version identifier, 12 là cost, và phần còn lại là salt + hash base64-encoded.

**Nhược điểm:**
- **DoS tiềm ẩn:** Mỗi request login tốn ~250ms CPU time. Attacker có thể gửi nhiều request login với password dài và gây tốn tài nguyên server. Với cost = 12, 100 request login song song có thể làm satu một CPU core trong 25 giây.
- **Không thể điều chỉnh động:** Cost parameter cố định tại thời điểm hash. Khi CPU trở nên nhanh hơn, các hash cũ vẫn dùng cost 12. Cần cơ chế re-hash password khi user login để nâng cost.
- **Không support parallel processing:** bcrypt không thể tận dụng multiple cores cho một hash duy nhất.
- **Phiên bản $2a$:** Mã định danh $2a$ có một lỗi nhỏ trong xử lý non-ASCII password (UTF-8 characters > 255). Nên sử dụng $2b$ nếu library hỗ trợ. Go's x/crypto/bcrypt hỗ trợ $2a$ và $2b$.

**So sánh các mức cost:**

| Cost | Vòng lặp | Thời gian (CPU 2024) | Bảo mật |
|------|----------|---------------------|---------|
| 10   | 1024     | ~80ms               | Cơ bản   |
| 12   | 4096     | ~250-350ms          | Tốt      |
| 14   | 16384    | ~1-1.5s             | Rất tốt  |
| 16   | 65536    | ~4-6s               | Có thể quá chậm cho production |

**Implementación:**

Trong `password.go:18-26`, constructor `NewPasswordHasher` có validation:
```go
if cost < bcrypt.MinCost {
    cost = bcrypt.MinCost
}
if cost > bcrypt.MaxCost {
    cost = bcrypt.MaxCost
}
```

`bcrypt.MinCost` = 4, `bcrypt.MaxCost` = 31. Tuy nhiên, trong config.go:20-25, giá trị BCryptCost mặc định là 12 và validate range 4-12:
```go
if n, err := strconv.Atoi(v); err == nil && n >= 4 && n <= 12 {
    bcryptCost = n
}
```

Giới hạn trên 12 là một biện pháp an toàn — cost > 12 có thể gây ra DoS (cost 31 sẽ mất hàng giờ cho một hash). Tuy nhiên, validation này nằm ở config layer, không phải ở password.go. Nếu ai đó khởi tạo `NewPasswordHasher(20)` trực tiếp (ví dụ từ unit test), password.go sẽ accept và gây ra hiệu năng tồi tệ.

### 2.4. Input Validation

**File:** `services/user-service/validate.go`

**Email validation:**
```go
var emailRegex = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
```

Regex này:
- Cho phép ký tự `@` ở bất kỳ vị trí miễn là không có khoảng trắng
- Yêu cầu có ít nhất một dấu chấm sau `@` với ký tự không khoảng trắng
- Không giới hạn độ dài tối đa
- Cho phép các ký tự đặc biệt như `+`, `-`, `_`, `.` ở local part
- Không kiểm tra TLD hợp lệ

Các trường hợp không được xử lý:
- `user@.com` (không có domain name) — regex từ chối vì sau `@` phải có ký tự không space trước dấu chấm
- `user@com` (không có dấu chấm) — regex từ chối vì yêu cầu dấu chấm
- `user+tag@example.com` (plus addressing) — OK
- `" ab c"@example.com` (quoted local part with spaces) — bị từ chối mặc dù RFC 5321 cho phép

**Password validation:**
```go
func validatePassword(password string) error {
    if len(password) < 8 {
        return ErrInvalidPassword
    }
    return nil
}
```

Validation chỉ yêu cầu tối thiểu 8 ký tự. Theo khuyến nghị hiện tại:
- **NIST SP 800-63B:** Không yêu cầu composition rules (chữ hoa, số, ký tự đặc biệt) vì người dùng thường chọn pattern dễ đoán như `Password1!`. NIST khuyến nghị tối thiểu 8 ký tự, kiểm tra với danh sách password phổ biến.
- **OWASP:** Khuyến nghị tối thiểu 10 ký tự, support tối đa 128 ký tự, không truncation.
- **PCI DSS 4.0:** Yêu cầu mật khẩu có độ phức tạp phù hợp.

Hệ thống hiện tại **thiếu**:
- Kiểm tra password với common password list (rockyou.txt, HaveIBeenPwned API)
- Giới hạn độ dài tối đa (bcrypt có giới hạn 72 bytes, password dài hơn sẽ bị truncation silently)
- Unicode normalization (NFD/NFC) cho password quốc tế

---

## 3. Timing Attack Mitigation

### 3.1. Cơ Chế Dummy BCrypt Hash

**File:** `services/user-service/handler.go:12`

```go
const dummyBcryptHash = "$2a$12$MB4lTvA5UVWJU8GPtVFSne/kMHaXBSz45DWvIl/4AS9NLnz7tavNm"
```

Khi một email không tồn tại trong database, thay vì trả về lỗi ngay lập tức, hệ thống thực thi một bcrypt verify với dummy hash:

```go
if user == nil {
    _ = h.hasher.Verify(req.Password, dummyBcryptHash)
    writeError(w, http.StatusUnauthorized, "invalid credentials")
    return
}
```

**Mục đích:** Đảm bảo thời gian xử lý cho cả hai trường hợp (email tồn tại + sai password, và email không tồn tại) là xấp xỉ bằng nhau. Nếu không có dummy verify, trường hợp email không tồn tại sẽ trả về ngay lập tức (chỉ tốn thời gian database query, thường <5ms), trong khi trường hợp email tồn tại sẽ tốn thêm ~250ms để verify bcrypt. Attacker có thể đo thời gian response để suy ra email nào tồn tại.

**Phân tích chi tiết dummy hash:**
```
$2a$12$MB4lTvA5UVWJU8GPtVFSne/kMHaXBSz45DWvIl/4AS9NLnz7tavNm
├┴┴┤├┴┴┤├┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┤├┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┴┤
 1   2    salt (22 chars base64)        hash (31 chars base64)
```

1. Prefix `$2a$` — xác định phiên bản bcrypt
2. Cost `12` — tương đương với cost hiện tại của hệ thống
3. Salt `MB4lTvA5UVWJU8GPtVFSne` — 16 bytes ngẫu nhiên, base64-encoded
4. Hash `/kMHaXBSz45DWvIl/4AS9NLnz7tavNm` — 23 bytes hash, base64-encoded

### 3.2. Phân Tích Hiệu Quả

**Về mặt lý thuyết, cơ chế này rất hiệu quả:**

1. **Thời gian xử lý đồng nhất:** Cả hai path đều thực hiện `bcrypt.CompareHashAndPassword` với cost = 12. Thời gian BCrypt verify chỉ phụ thuộc vào cost và độ dài input, không phụ thuộc vào nội dung hash hay password. Do đó, thời gian xử lý giữa hai path là gần như bằng nhau.

2. **Không thể phân biệt qua error response:** Cả hai path đều trả về cùng HTTP 401 với message "invalid credentials". Không có sự khác biệt trong response body hay status code.

**Tuy nhiên, phân tích chi tiết cho thấy các yếu tố sau có thể làm giảm hiệu quả:**

**A. Database query timing khác biệt:**
- Email tồn tại: `FindByEmail` thực hiện `QueryRow` + `Scan` — trả về 4 fields
- Email không tồn tại: `FindByEmail` thực hiện `QueryRow` + `Scan` — trả về `pgx.ErrNoRows`

Mặc dù cả hai đều là query giống nhau, có thể có khác biệt nhỏ trong thời gian xử lý do PostgreSQL phải đọc page từ disk/cache. Tuy nhiên, sự khác biệt này (thường <1ms) rất nhỏ so với bcrypt verify (~250ms) và bị che lấp hoàn toàn.

**B. Password độ dài khác nhau:**
BCrypt compare time phụ thuộc nhẹ vào độ dài password (longer password = nhiều vòng Blowfish hơn). Nếu attacker gửi password với độ dài khác nhau cho cùng email, và biết được password thật có độ dài cụ thể, họ có thể khai thác timing này. Nhưng:
- Attacker không biết password đúng
- Sự khác biệt về thời gian giữa password 8 ký tự và 32 ký tự là không đáng kể (< 5%)
- Với dummy hash, password length effect là như nhau

**C. Memory/cache effects:**
Sau lần bcrypt verify đầu tiên, Blowfish key schedule tables có thể được cache trong CPU L1/L2. Nếu attacker gửi request liên tiếp cho cùng email (tồn tại), table đã được cache, verify nhanh hơn. Cho email không tồn tại, mỗi request là một BCrypt verify mới với table mới (dummy hash). Tuy nhiên:
- Dummy hash được verify với password của request, tạo table mới
- Ở cả hai path, table đều được tính mới cho mỗi request
- Sự khác biệt về cache hit ratio là tối thiểu

**D. Kết nối mạng và network latency:**
Trong môi trường production, network jitter thường lớn hơn nhiều so với timing differences có thể khai thác. Ngay cả trong cùng datacenter, network latency thường là 0.5-5ms với jitter 0.1-1ms. Điều này làm cho timing attack qua mạng hầu như không khả thi khi có dummy bảo vệ.

**E. So sánh với constant-time comparison:**
BCrypt implementation trong Go (`x/crypto/bcrypt`) sử dụng Blowfish cipher, vốn được thiết kế với constant-time S-box lookups (trên CPU có data cache timing không đổi). Tuy nhiên, trong thực tế, CPU có thể có cache timing side channels (tấn công Prime+Probe, Flush+Reload). Các tấn công này yêu cầu:
- Shared CPU cache (cùng physical core, SMT/HyperThreading)
- Instruction-level precision (rdtsc)
- Hàng nghìn mẫu thử

Đây là các tấn công nâng cao, không khả thi trong môi trường cloud/datacenter thông thường.

### 3.3. Hạn Chế và Lỗ Hổng Còn Tồn Tại

**1. Password reuse detection qua timing:**
Nếu attacker biết email A tồn tại và biết User A có password "P@ssw0rd", họ có thể:
- Gửi request login với email A, password "P@ssw0rd" → mất ~250ms (bcrypt verify) + thời gian issue JWT
- Gửi request login với email B (không tồn tại), password "P@ssw0rd" → mất ~250ms (dummy verify)

Thời gian ở đây là **như nhau**, cơ chế hoạt động đúng.

**2. Email enumeration qua registration:**
Dummy hash chỉ bảo vệ login endpoint. Register endpoint trả về HTTP 409 "email already registered" khi email đã tồn tại, và HTTP 201 khi tạo thành công. Attacker có thể dễ dàng brute-force danh sách email qua register endpoint. Đây là một lỗ hổng email enumeration nghiêm trọng hơn timing attack.

**3. Timing difference từ error handling path khác:**
Trong login handler, có 3 path trả về 401:
- Path A: user == nil → dummy verify → response (dòng 121-124)
- Path B: user != nil, sai password → `bcrypt.ErrMismatchedHashAndPassword` → response (dòng 127-130)
- Path C: user != nil, lỗi hash → log error + response (dòng 131-134)

Path A và B đều thực hiện bcrypt verify, nên thời gian tương đương. Path C xảy ra khi hash bị lỗi (corrupted) — thời gian ngắn hơn vì bcrypt CompareHashAndPassword sẽ fail sớm ở bước parse hash. Tuy nhiên, trường hợp này hiếm và không thể khai thác để enum email vì nó đòi hỏi email phải tồn tại.

**4. Constant-time so sánh response:**
Tất cả error path đều trả về JSON `{"error":"invalid credentials"}`. Response có cùng kích thước và cấu trúc. Không thể phân biệt qua content length.

---

## 4. JWT Implementation

### 4.1. Cấu Trúc Claims

**File:** `shared/auth/auth.go:34-46`

```go
type Claims struct {
    Sub   string `json:"sub"`
    Email string `json:"email"`
    Iss   string `json:"iss"`
    Iat   int64  `json:"iat"`
    Exp   int64  `json:"exp"`
}
```

**Phân tích từng claim:**

- **`sub`** (Subject): UUID của user dạng string. Theo JWT RFC 7519 §4.1.2, sub phải là unique identifier, không chứa thông tin nhạy cảm. UUID đáp ứng yêu cầu này.

- **`email`** (Custom claim): Email của user được denormalized vào token. Mục đích: "Avoids downstream services needing a DB lookup to display user context" (comment trong auth.go:39). Điều này tạo ra một **vấn đề bảo mật**: email là PII (Personally Identifiable Information), và việc đưa nó vào JWT (một stateless token) có nghĩa là:
  - Email được base64url-encode (không mã hóa) trong payload
  - Bất kỳ ai có token đều đọc được email
  - Nếu token bị leak, email bị lộ
  - Nếu user đổi email, JWT cũ vẫn chứa email cũ

- **`iss`** (Issuer): Luôn là "url-shortener". Được kiểm tra trong VerifyToken — nếu khác, token bị reject. Đây là biện pháp chống cross-service token usage.

- **`iat`** (Issued At): Unix timestamp. Không được sử dụng trong bất kỳ logic kiểm tra nào (không có kiểm tra "token không được sử dụng trước iat").

- **`exp`** (Expiration): Unix timestamp. Được jwt library tự động kiểm tra khi parse token với `jwt.Parse`. Nếu token hết hạn, parse trả về error.

**Thiếu sót quan trọng:**
- Không có `jti` (JWT ID) — unique identifier cho mỗi token. Nếu có jti, có thể implement token revocation.
- Không có `nbf` (Not Before) — thời gian token bắt đầu có hiệu lực.
- Không có `aud` (Audience) — xác định token được thiết kế cho service nào.
- Email claim không có expiry synchronization — user có thể đổi email trong DB nhưng JWT cũ vẫn valid.

### 4.2. HS256 Signing

**File:** `services/user-service/token.go:26-28`

```go
token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{...})
```

**HS256 (HMAC-SHA256):**
- **Thuật toán:** HMAC với SHA-256 hash function
- **Key:** Symmetric secret (cùng secret cho signing và verification)
- **Output size:** 256 bits (32 bytes)
- **Security level:** 128 bits (against collision attacks)

**Phân tích:**
- **Tốc độ:** Nhanh hơn RS256 (RSA) khoảng 100-1000x, không yêu cầu public key infrastructure
- **An toàn:** HMAC-SHA256 được coi là an toàn (không có practical attack năm 2026)
- **Nhược điểm lớn:** Symmetric key — cả gateway và user-service đều phải biết secret. Nếu một service bị compromised, JWT signing key bị lộ. Với RS256 (RSA), user-service có private key, gateway chỉ có public key để verify.
- **Key size:** Trong config, secret là `JWT_SECRET` environment variable. jwt library yêu cầu key size >= 32 bytes cho HS256. Nếu secret ngắn hơn, vẫn hoạt động nhưng giảm bảo mật. Code không kiểm tra độ dài secret.

**Signing method validation trong VerifyToken:**
```go
if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
    return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
}
```

Đây là biện pháp chống **algorithm confusion attack**:
- Attacker thay đổi header `alg` từ `HS256` thành `none` → không có signature verification
- Attacker sử dụng public key (trong RS256) để verify HMAC-signed token
- Code kiểm tra method là `*jwt.SigningMethodHMAC`, reject tất cả method khác

**Tuy nhiên:** jwt library mặc định cũng đã kiểm tra điều này nếu dùng `jwt.Parse` với key function. Việc kiểm tra thủ công là một lớp bảo vệ bổ sung.

### 4.3. GenerateToken — Quy Trình Phát Hành

**File:** `services/user-service/token.go:22-40`

```go
func (i *jwtTokenIssuer) Issue(userID, email string) (string, time.Time, error) {
    now := time.Now()
    expiresAt := now.Add(i.ttl)
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
        "sub":   userID,
        "email": email,
        "iss":   "url-shortener",
        "iat":   now.Unix(),
        "exp":   expiresAt.Unix(),
    })
    signed, err := token.SignedString(i.secret)
    return signed, expiresAt, nil
}
```

**Chi tiết kỹ thuật:**
1. `now := time.Now()` — lấy thời gian hiện tại (server clock)
2. `expiresAt := now.Add(i.ttl)` — tính thời gian hết hạn. Mặc định TTL = 24 giờ (config.go:27)
3. `jwt.NewWithClaims` — tạo token object với signing method HS256
4. `jwt.MapClaims` — claims được lưu dưới dạng map, cho phép flexibility nhưng không có type safety
5. `token.SignedString(i.secret)` — thực hiện:
   - Base64url-encode header: `{"alg":"HS256","typ":"JWT"}`
   - Base64url-encode payload (claims)
   - Tạo signature: `HMAC-SHA256(base64url(header) + "." + base64url(payload), secret)`
   - Kết hợp: `header.payload.signature`

**Vấn đề thời gian (clock drift):**
- `time.Now()` sử dụng system clock. Nếu clock bị sai (NTP chưa sync, server bị tấn công), token có thể có iat/exp sai.
- Khi server clock chạy nhanh, token exp có thể đã hết hạn ngay khi vừa được tạo.
- Khi server clock chạy chậm, token exp thực tế kéo dài hơn mong muốn.
- Giải pháp: sử dụng monotonic clock cho tính toán, nhưng không khả thi vì iat/exp là unix timestamps.

**TTL 24 giờ:**
- Mặc định 24 giờ (có thể cấu hình qua TOKEN_TTL_HOURS)
- 24 giờ là khoảng thời gian dài cho JWT, đặc biệt là không có refresh token mechanism
- Nếu token bị đánh cắp, attacker có thể sử dụng trong 24 giờ
- OWASP khuyến nghị: access token TTL 15-60 phút, kết hợp với refresh token

### 4.4. VerifyToken — Quy Trình Xác Thực

**File:** `shared/auth/auth.go:60-123`

```go
func VerifyToken(tokenString, secret string) (*Claims, error) {
    token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
        if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
            return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
        }
        return []byte(secret), nil
    })
    if err != nil || !token.Valid {
        return nil, ErrTokenInvalid
    }
    // ... extract claims
}
```

**Quy trình xác thực 7 bước:**

**Bước 1 — Parse JWT:**
`jwt.Parse` thực hiện:
- Tách token thành 3 phần: header.payload.signature
- Base64url-decode header
- Gọi key function (anonymous function) để lấy secret key
- Check algorithm matching
- Base64url-decode payload
- Verify signature
- Check expiration (exp)
- Trả về token và nil error, hoặc nil và error

**Bước 2 — Kiểm tra signing method:**
Algorithm confusion protection (đã phân tích ở 4.2).

**Bước 3 — Kiểm tra token valid:**
`jwt.Parse` đã kiểm tra:
- Signature match
- Token không hết hạn (exp)
- (Không kiểm tra nbf, iat)

**Bước 4 — Type assertion sang MapClaims:**
`mapClaims, ok := token.Claims.(jwt.MapClaims)`. Chỉ chấp nhận MapClaims.

**Bước 5 — Extract từng claim:**
Hàm helper `extractString` kiểm tra:
- Key tồn tại trong map
- Value có type string

**Bước 6 — Extract sub, email, iss:**
Mỗi claim đều được kiểm tra tồn tại. Nếu thiếu, trả về ErrTokenInvalid.

**Bước 7 — Extract iat, exp:**
Sử dụng type assertion `mapClaims["iat"].(float64)`. JSON số được decode thành float64. Chuyển đổi sang int64.

**Kiểm tra bổ sung:**
```go
if claims.Iss != "url-shortener" {
    return nil, ErrTokenInvalid
}
```

Đảm bảo issuer là chính xác. Đây là biện pháp chống attacker tạo token với iss khác.

**Vấn đề với extractString:**

```go
extractString := func(m jwt.MapClaims, key string) (string, bool) {
    v, exists := m[key]
    if !exists {
        return "", false
    }
    s, ok := v.(string)
    return s, ok
}
```

- Nếu key tồn tại nhưng giá trị không phải string (ví dụ: số, null, array), function trả về `("", false)`, dẫn đến ErrTokenInvalid.
- Nếu key không tồn tại, function trả về `("", false)`, dẫn đến ErrTokenInvalid.
- Nếu giá trị là empty string `""`, function trả về `("", true)` — OK.

**Vấn đề với float64 conversion:**
```go
if iat, ok := mapClaims["iat"].(float64); ok {
    claims.Iat = int64(iat)
}
```

- JSON numbers > 2^53 mất precision khi chuyển qua float64
- Nếu iat là số nguyên lớn (ví dụ: timestamp trong tương lai xa), conversion có thể sai
- jwt library thường set iat/exp là số nguyên, không phải float. Tuy nhiên, Go's encoding/json decode JSON numbers thành float64

### 4.5. JWTMiddleware — Tích Hợp vào HTTP

**File:** `shared/auth/middleware.go:10-47`

JWTMiddleware là middleware HTTP có nhiệm vụ:
1. Trích xuất Authorization header
2. Parse Bearer token
3. Verify token
4. Inject claims vào request context

**Chi tiết:**

```go
func JWTMiddleware(secret string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

**Xử lý Authorization header:**
- Header trống → 401 "authorization header required"
- Header không bắt đầu bằng "Bearer " → 401 "invalid authorization header format"
- Token empty sau "Bearer " → 401 "token is required"
- TrimPrefix xử lý đúng, không bị ảnh hưởng bởi extra spaces

**Verify và inject claims:**
```go
claims, err := VerifyToken(tokenString, secret)
if err != nil {
    writeJSONError(w, http.StatusUnauthorized, "unauthorized")
    return
}
ctx := context.WithValue(r.Context(), claimsKey{}, claims)
next.ServeHTTP(w, r.WithContext(ctx))
```

**Điểm đáng chú ý:**
- Error message "unauthorized" là generic, không tiết lộ lý do (hết hạn, sai signature, malformed)
- Claims được inject vào context với key là `claimsKey{}` (unexported type) — tránh collision
- Có `TestClaimsKey` (exported type alias) cho phép unit test inject claims mà không cần real token

**Sự khác biệt với gateway jwtMiddleware:**

Gateway có JWTMiddleware riêng tại `gateway/jwtmiddleware.go`:
```go
func jwtMiddleware(secret string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        route := matchRoute(r)
        if route == nil || !route.RequiresAuth {
            next.ServeHTTP(w, r)
            return
        }
        // ... verify token
    })
}
```

Gateway middleware kiểm tra route's RequiresAuth flag. Nếu route public (register, login, redirect), middleware bypass authentication. Đây là **duplication** — cả user-service và gateway đều verify JWT token. User-service verify lại token trong `GET /me` handler thông qua `auth.JWTMiddleware(cfg.JWTSecret)`. Gateway cũng verify token trước khi proxy request. Điều này:
- Lãng phí CPU (double verification)
- Tăng latency
- Tạo ra hai điểm cần quản lý secret

### 4.6. Security Concerns với JWT

**1. Không có Token Revocation:**
Đây là vấn đề bảo mật **lớn nhất**. Một khi JWT được phát hành, nó valid cho đến khi hết hạn (24 giờ). Không có cách nào để:
- Thu hồi token khi user logout
- Thu hồi token khi user đổi password
- Thu hồi token khi user bị ban/suspend
- Thu hồi token bị đánh cắp (nếu attacker đã lấy được token)

**Giải pháp phổ biến:**
- **Blacklist:** Lưu các token bị thu hồi trong Redis/Memcached với TTL. Middleware kiểm tra blacklist trước khi accept.
- **Token versioning:** Lưu `token_version` trong database user record. JWT chứa `tver` claim. Nếu version mismatch, token bị reject. Cho phép invalidate tất cả token của user bằng cách increment version.
- **Short TTL + Refresh Tokens:** Access token 15 phút, refresh token 7 ngày. Refresh token có thể bị thu hồi.

**2. Thiếu Refresh Tokens:**
Với TTL 24 giờ, user phải login lại mỗi ngày. Trải nghiệm kém. Refresh token với TTL dài hơn (30 ngày) cho phép:
- Silent refresh (không cần user nhập lại password)
- Thu hồi refresh token khi cần
- Access token ngắn hạn giảm thiểu rủi ro khi token bị đánh cắp

**3. Shared Secret Risk:**
HS256 sử dụng symmetric key. Cả user-service và gateway đều cần secret. Nếu:
- gateway bị compromised → attacker có signing key → tạo token giả
- Một service developer lỡ commit secret lên Git → leak
- Không thể rotate secret mà không deploy lại cả hai service

**4. Email trong token:**
Email (PII) được đưa vào JWT payload. JWT payload được base64url-encode (không encrypt). Bất kỳ ai có token đều đọc được email. Nếu token được dùng trong URL query parameter (bad practice), email có thể bị leak trong server logs, referer headers.

**5. Thiếu JWT ID (jti):**
`jti` là unique identifier cho mỗi token. Nếu có jti:
- Có thể track token usage
- Dễ dàng blacklist token cụ thể
- Phát hiện token reuse
- Audit logging

**6. Payload size có thể lớn:**
Nếu có nhiều custom claims, JWT size tăng lên. Với những hạn chế của HTTP headers (nginx 8KB default, AWS ELB 16KB), JWT size có thể gây vấn đề. Tuy nhiên, với 5 claims hiện tại, JWT size khoảng 200-300 bytes.

**7. Token không được checksum trong database:**
Không có cách nào xác định token nào đã được cấp cho user nào (trừ khi parse token để lấy sub). Điều này gây khó khăn cho audit và debugging.

---

## 5. Circuit Breaker

### 5.1. Kiến Trúc 3-State Machine

**File:** `gateway/circuitbreaker.go`

Circuit breaker là một state machine với 3 trạng thái:

```go
type State int

const (
    StateClosed State = iota   // 0
    StateOpen                  // 1
    StateHalfOpen              // 2
)
```

**Cấu trúc CircuitBreaker:**
```go
type CircuitBreaker struct {
    mu              sync.Mutex
    state           State
    failures        int
    lastFailureTime time.Time
    maxFailures     int
    openTimeout     time.Duration
    failureWindow   time.Duration
    windowStart     time.Time
    onStateChange   func(from, to State)
    halfOpenProbe   bool
}
```

**Giải thích các field:**

| Field | Kiểu | Mục đích |
|-------|------|----------|
| `mu` | `sync.Mutex` | Đồng bộ hóa truy cập, bảo vệ tất cả fields khác |
| `state` | `State` | Trạng thái hiện tại: CLOSED, OPEN, HALF_OPEN |
| `failures` | `int` | Số lần fail liên tiếp trong failure window |
| `lastFailureTime` | `time.Time` | Thời điểm của lần fail gần nhất (dùng cho open timeout) |
| `maxFailures` | `int` | Ngưỡng fail để chuyển từ CLOSED → OPEN |
| `openTimeout` | `time.Duration` | Thời gian chờ trước khi chuyển OPEN → HALF_OPEN |
| `failureWindow` | `time.Duration` | Cửa sổ thời gian reset failure counter |
| `windowStart` | `time.Time` | Thời điểm bắt đầu của failure window hiện tại |
| `onStateChange` | `func(from, to State)` | Callback được gọi khi state thay đổi |
| `halfOpenProbe` | `bool` | Cờ đánh dấu probe request trong HALF_OPEN |

### 5.2. State Transitions Chi Tiết

**State diagram:**

```
    ┌─────────┐  failures >= maxFailures  ┌─────────┐
    │ CLOSED  │ ────────────────────────▶ │  OPEN   │
    └────┬────┘                           └────┬────┘
         │ ▲                                   │
         │ │               openTimeout expired │
         │ │   ┌───────────────────────────────┘
         │ │   │
         │ │   ▼
         │ │  ┌───────────┐
         │ └──│ HALF_OPEN │
         │    └─────┬─────┘
         │          │
         └──────────┘
       probe success
         (reset failures)
```

#### Transition 1: CLOSED → OPEN

**Điều kiện:** `cb.failures >= cb.maxFailures && cb.state != StateOpen`

**Trigger:** Trong `Do()` sau khi upstream trả về error (dòng 138-143):
```go
if cb.failures >= cb.maxFailures && cb.state != StateOpen {
    previousState = cb.state
    cb.state = StateOpen
    cb.mu.Unlock()
    cb.notifyStateChange(previousState, StateOpen)
    return err
}
```

**Chi tiết:**
- `cb.failures` được reset sau mỗi lần thành công (dòng 163) hoặc khi failure window hết hạn (dòng 130-132)
- `cb.lastFailureTime` được set ở mỗi failure (dòng 136)
- Default `maxFailures` = 5 (config.go:39)
- Transition chỉ xảy ra khi state != StateOpen (tránh double trigger)

#### Transition 2: OPEN → HALF_OPEN

**Điều kiện:** `time.Since(cb.lastFailureTime) > cb.openTimeout`

**Trigger:** Trong `Do()` khi state == StateOpen và open timeout đã hết (dòng 80-89):
```go
case StateOpen:
    if time.Since(cb.lastFailureTime) <= cb.openTimeout {
        cb.mu.Unlock()
        return ErrCircuitOpen
    }
    previousState = cb.state
    cb.state = StateHalfOpen
    cb.halfOpenProbe = true
    stateChanged = true
    newState = StateHalfOpen
```

**Chi tiết:**
- Nếu openTimeout chưa hết, reject request ngay với `ErrCircuitOpen`
- Nếu openTimeout đã hết, chuyển sang HALF_OPEN và set `halfOpenProbe = true`
- `halfOpenProbe = true` có nghĩa là request này sẽ được dùng làm probe
- Default `openTimeout` = 30 giây (config.go:40)

#### Transition 3: HALF_OPEN → CLOSED

**Điều kiện:** Upstream call thành công (err == nil) khi state == HALF_OPEN

**Trigger:** Trong `Do()` sau khi upstream trả về success (dòng 150-159):
```go
if cb.state == StateHalfOpen {
    previousState = cb.state
    cb.state = StateClosed
    cb.halfOpenProbe = false
    cb.failures = 0
    cb.windowStart = time.Now()
    cb.mu.Unlock()
    cb.notifyStateChange(previousState, StateClosed)
    return nil
}
```

**Chi tiết:**
- Reset `failures` về 0
- Reset `windowStart` về hiện tại
- `halfOpenProbe` được set về false

#### Transition 4: HALF_OPEN → OPEN

**Điều kiện:** Upstream call thất bại khi state == HALF_OPEN

**Trigger:** Trong `Do()` sau khi upstream trả về error (dòng 118-126):
```go
if cb.state == StateHalfOpen {
    cb.halfOpenProbe = false
    previousState = cb.state
    cb.state = StateOpen
    cb.lastFailureTime = time.Now()
    cb.mu.Unlock()
    cb.notifyStateChange(previousState, StateOpen)
    return err
}
```

**Chi tiết:**
- Chuyển ngay về OPEN sau một failure
- Reset `lastFailureTime` để bắt đầu open timeout mới
- `halfOpenProbe` được set về false để cho phép request tiếp theo làm probe

#### Edge Case: HALF_OPEN với concurrent requests

Khi state == HALF_OPEN:
1. Request đầu tiên vào: `halfOpenProbe == false` → set `halfOpenProbe = true` (dòng 95-96), cho phép request đi qua
2. Các request tiếp theo: `halfOpenProbe == true` → reject với `ErrCircuitOpen` (dòng 91-93)
3. Request đầu tiên thành công → state → CLOSED, reset halfOpenProbe
4. Request đầu tiên thất bại → state → OPEN, reset halfOpenProbe

Cơ chế này đảm bảo chỉ **một request** được gửi upstream trong trạng thái HALF_OPEN. Tuy nhiên có **race condition**: nếu hai request vào HALF_OPEN cùng lúc, cả hai đều thấy `halfOpenProbe == false`, và cả hai đều được phép đi qua. Mutex chỉ bảo vệ việc đọc/ghi state, không bảo vệ logic "đọc-trạng thái-rồi-quyết-định" một cách nguyên tử. Đây là một lỗ hổng có thể dẫn đến nhiều probe requests đồng thời.

### 5.3. Cơ Chế Đồng Bộ RWMutex

Circuit breaker sử dụng `sync.Mutex` (không phải RWMutex như tên section này). Tuy nhiên, việc sử dụng mutex đơn giản là phù hợp vì:

1. **Write nhiều hơn Read:** Mỗi request gọi `Do()` đều cần write state (update failure count, lastFailureTime, v.v.)
2. **Thời gian ngắn:** Mutex chỉ được giữ trong thời gian rất ngắn để đọc/ghi fields
3. **Không I/O trong lock:** Không có network call, database query, hoặc I/O operation trong critical section
4. **Không gọi callback khi đang giữ lock:** `notifyStateChange` được gọi sau khi unlock (dòng 101-102)

**Pattern: Lock → Modify → Unlock → Notify:**

```go
cb.mu.Lock()
// ... modify state
cb.mu.Unlock()

if stateChanged {
    cb.notifyStateChange(previousState, newState)
}
```

Callback "onStateChange" gọi Prometheus metric updates, không gây deadlock.

**Race condition analysis:**

**Potential race 1 — windowStart reset:**
```go
// Goroutine A (failure)
cb.failures++
// Goroutine B (success) interleaves
cb.failures = 0
cb.windowStart = time.Now()
// Goroutine A continues
cb.lastFailureTime = time.Now()
if cb.failures >= cb.maxFailures && cb.state != StateOpen { // cb.failures = 1, không trigger
```

Không gây hại nghiêm trọng — chỉ làm chậm transition.

**Potential race 2 — halfOpenProbe double entry:**
Đã phân tích ở 5.2. Hai goroutine cùng thấy `halfOpenProbe == false`. Cần atomic compare-and-swap để khắc phục.

### 5.4. Half-Open Probe Mechanism

Probe là cơ chế cho phép circuit breaker kiểm tra xem upstream service đã phục hồi chưa. Trong implementation này, **request đầu tiên** sau open timeout được dùng làm probe.

**Hoạt động:**
1. State OPEN, openTimeout hết → state chuyển HALF_OPEN
2. `halfOpenProbe = true` — đánh dấu request sắp tới là probe
3. Request vào `Do()` với state HALF_OPEN:
   - Nếu `halfOpenProbe == true`, reject request (đã có probe đang chạy)
   - Nếu `halfOpenProbe == false`, cho phép request đi qua và set `halfOpenProbe = true`
4. Request probe chạy upstream
5. Kết quả:
   - Thành công → CLOSED
   - Thất bại → OPEN

**Cơ chế ngăn chặn flood trong HALF_OPEN:**
- Chỉ một probe được phép mỗi lần
- Các request khác bị reject ngay
- Nếu probe mất quá nhiều thời gian (timeout), các request khác vẫn bị từ chối

**Vấn đề:** Nếu probe timeout (context deadline), code ở dòng 104-112:
```go
case <-ctx.Done():
    cb.mu.Lock()
    if cb.state == StateHalfOpen {
        cb.halfOpenProbe = false
    }
    cb.mu.Unlock()
    return ctx.Err()
```

Khi context timeout, `halfOpenProbe` được reset về false. Điều này cho phép request tiếp theo thử probe mới. Tuy nhiên, upstream call (`err := upstream()`) đã được gọi trước khi context check này. Nếu `upstream()` vẫn đang chạy (goroutine leak), probe result sẽ được xử lý sau khi state đã được thay đổi bởi request khác.

### 5.5. Metrics và Monitoring

**File:** `gateway/metrics.go`

Circuit breaker exposed 3 Prometheus metrics:

**1. `gateway_circuit_breaker_state`:**
- Type: Gauge
- Labels: `service` (chỉ có "url-service" hiện tại)
- Values: 0=CLOSED, 1=HALF_OPEN, 2=OPEN
- Cho phép alerting khi state = OPEN kéo dài

**2. `gateway_circuit_breaker_trips_total`:**
- Type: Counter
- Labels: `service`
- Incremented mỗi khi state chuyển từ CLOSED/CLOSED sang OPEN
- Cho phép tính trip rate, phát hiện degradation patterns

**3. `gateway_circuit_breaker_rejected_total`:**
- Type: Counter
- Labels: `service`
- Incremented mỗi khi request bị reject do circuit open
- Cho phép đánh giá impact trên user experience

**Additional metrics:**

**4. `gateway_requests_total`:**
- Type: Counter
- Labels: `service`, `status_class` (2xx, 3xx, 4xx, 5xx)
- Track tất cả upstream requests

**5. `gateway_request_duration_seconds`:**
- Type: Histogram
- Labels: `service`
- Track upstream response latency
- Default buckets: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10

**Registration trong main.go:**
```go
cb := NewCircuitBreaker(...).WithStateChange(func(from, to State) {
    recordCBState("url-service", to)
    if to == StateOpen {
        recordCBTrip("url-service")
    }
})
recordCBState("url-service", StateClosed)
```

### 5.6. Weaknesses và Hạn Chế

**1. Chỉ áp dụng cho url-service:**

```go
if route.Upstream == "url-service" && h.circuitBreaker != nil {
    // circuit breaker logic
}
```

Chỉ url-service được bảo vệ bởi circuit breaker. Các service khác (analytics, notification, user) không có protection. Nếu analytics-service bị lỗi, gateway vẫn forward requests và nhận timeout.

**2. Thiếu per-route circuit breaker:**
Một circuit breaker cho tất cả endpoints của url-service. Nếu một endpoint cụ thể bị lỗi (ví dụ: `POST /api/shorten`), tất cả requests đến url-service (kể cả `GET /api/urls`) đều bị reject. Cần circuit breaker riêng cho mỗi endpoint hoặc sử dụng failure types để quyết định.

**3. Failure counting đơn giản:**
- Mọi error đều được đếm như nhau (timeout, connection refused, 500, 503)
- Không phân biệt transient vs permanent errors
- Không có exponential backoff
- Có thể trip quá sớm nếu network có transient glitch

**4. Không có retry mechanism:**
Khi circuit breaker reject request, gateway trả về 503 ngay lập tức. Không có:
- Automatic retry sau backoff
- Retry với different upstream instance (nếu có multiple instances)
- Stale cache fallback

**5. Mutex contention dưới high load:**
Mỗi request đều acquire/release mutex ít nhất 1-2 lần. Dưới 10K+ RPS, mutex contention có thể trở thành bottleneck. Có thể sử dụng atomic operations cho counter và state.

**6. Half-open probe nguy hiểm với slow upstream:**
Nếu upstream phản hồi chậm nhưng vẫn thành công, probe request giữ circuit ở HALF_OPEN trong thời gian dài, trong khi các request khác bị reject. Upstream thực sự có thể đã recover nhưng probe timeout setting không phù hợp.

**7. Không có circuit breaker health endpoint:**
Không có cách nào để monitoring tool query trạng thái circuit breaker qua HTTP (ngoại trừ Prometheus metrics). Có thể thêm `/circuitbreaker` endpoint cho operational visibility.

**8. Thread safety của halfOpenProbe:**
Race condition khi multiple goroutines cùng thấy `halfOpenProbe == false`. Nên sử dụng `atomic.CompareAndSwapInt32` hoặc một mutex riêng cho probe flag.

---

## 6. Rate Limiter

### 6.1. Token Bucket Algorithm

**File:** `gateway/ratelimit.go`

Implementation sử dụng **Fixed Window Counter** (không phải Token Bucket như tên gọi). Mỗi key có một counter trong Redis với TTL tương đương window. Khi counter vượt quá limit, request bị từ chối.

**Thuật toán:**
1. Generate key: `rl:<routeKey>:<clientIP>`
2. INCR key trong Redis
3. Nếu kết quả là 1 (key mới được tạo), set EXPIRE với window duration
4. Nếu count > limit, từ chối request
5. Trả về TTL còn lại cho Retry-After header

**So sánh với Token Bucket thực sự:**

| Feature | Fixed Window (hiện tại) | Token Bucket |
|---------|------------------------|--------------|
| Smoothing | Không — tất cả quota reset cùng lúc | Có — tokens được thêm đều đặn |
| Burst handling | Kém — burst ở đầu window sau đó cạn kiệt | Tốt — burst có thể được hấp thụ nếu accumulated tokens |
| Implementation | Đơn giản (INCR + EXPIRE) | Phức tạp hơn (cần lưu last refill time + token count) |
| Redis storage | 1 key/user | 1 key/user |
| Boundary behavior | Có thể 2x request ở biên giữa 2 windows | Smooth transition |

**Vấn đề Fixed Window:** Với limit 10 requests/60 giây, một attacker có thể gửi 10 requests ở giây 59 và 10 requests ở giây 61 — tổng cộng 20 requests trong 2 giây. Token Bucket giới hạn rate thực tế hơn.

**Rate limit config:**
```go
ShortenRateLimit:  RateLimitConfig{Limit: 10, WindowSecs: 60},   // 10 requests/phút
RedirectRateLimit: RateLimitConfig{Limit: 300, WindowSecs: 60},  // 300 requests/phút
```

Shorten: 10 URLs/phút — phù hợp.  
Redirect: 300 redirects/phút — với fixed window, attacker có thể redirect 600 lần trong 2 giây (cuối window + đầu window mới). Có thể chưa đủ cho use case có traffic cao.

### 6.2. Redis INCR và Expire

**Code:**
```go
count, err := rl.client.Incr(ctx, fullKey).Result()
if err != nil {
    return true, 0, err
}
if count == 1 {
    if err := rl.client.Expire(ctx, fullKey, time.Duration(windowSecs)*time.Second).Err(); err != nil {
        return true, 0, err
    }
}
if count > int64(limit) {
    ttl, err := rl.client.TTL(ctx, fullKey).Result()
    if err != nil {
        return false, 0, nil
    }
    return false, int(ttl.Seconds()), nil
}
return true, 0, nil
```

**Phân tích:**
1. `INCR` tạo key với value 1 nếu chưa tồn tại, increment nếu đã tồn tại
2. `EXPIRE` chỉ được gọi khi `count == 1` (key mới). Nếu key đã tồn tại nhưng chưa có TTL (trường hợp Redis crashed và phục hồi từ RDB), EXPIRE không được gọi lại → key tồn tại vĩnh viễn
3. Race condition: `INCR` trả về 1, nhưng trước khi `EXPIRE` được gọi, key bị expire bởi chính nó (nếu đã có TTL từ trước)

**Race condition analysis:**

**Timeline:**
```
T1: Request A INCR key → 1
T2: Request B INCR key → 2 (key vừa được A tạo, chưa có TTL)
T3: Request A EXPIRE key 60s (OK, nhưng A nghĩ key mới)
T4: Request B kiểm tra count > limit → false (2 <= 10)
```

Trong kịch bản này, cả A và B đều được phép (không có vấn đề). Tuy nhiên:

```
T1: Request A INCR key → 1
T2: Key bị expire (Redis policy, memory pressure, master/slave failover)
T3: Request B INCR key → 1 (lại tạo mới)
T4: Request A EXPIRE key (thành công, TTL mới)
```

Ở đây, cả A và B đều thấy count = 1 và đều đặt EXPIRE. Không có hậu quả nghiêm trọng.

**Vấn đề nghiêm trọng hơn:**
```
T1: INCR key → count = 11 (đã vượt limit)
T2: EXPIRE thất bại (network error, Redis down)
T3: count=11, > limit 10 → deny
T4: Không có EXPIRE, key tồn tại vĩnh viễn
T5: Tất cả request tiếp theo đều thấy count=11 → deny vĩnh viễn
```

Đây là **lỗi fail-closed không mong muốn**: khi EXPIRE thất bại nhưng INCR thành công, key tồn tại mãi mãi và rate limiter từ chối tất cả requests. Fix: sử dụng Lua script để đảm bảo atomicity.

### 6.3. Fail-Open Behavior

Khi Redis có lỗi, rate limiter **fail-open**: cho phép request đi qua:

```go
count, err := rl.client.Incr(ctx, fullKey).Result()
if err != nil {
    return true, 0, err  // fail-open: cho phép request
}
```

**Lý do:** Rate limiter không nên block user khi Redis (infrastructure) gặp vấn đề. Fail-open là lựa chọn đúng đắn vì:
- Availability > strict rate limiting
- Redis outage không làm sập toàn bộ hệ thống
- DDoS protection không nên phụ thuộc vào single point of failure

**Tuy nhiên:**
- Fail-open + Redis outage = không rate limit = có thể bị DDoS
- Có thể implement degraded mode: local rate limiting (in-memory map với TTL) khi Redis unavailable
- Main.go không có health check cho Redis — nếu Redis URL sai ở startup, ứng dụng exit. Nếu Redis die sau startup, không có cảnh báo.

**Ở gateway handler:** Khi rate limit error (fail-open), log warning:
```go
h.log.Warn("rate limiter failed open", "route", route.RateLimitKey, "error", err)
```

Log level WARN, không phải ERROR — thể hiện đây là hành vi expected, không phải lỗi.

### 6.4. Redis Key Management

**Key format:** `rl:<routeKey>:<clientIP>`

Ví dụ: `rl:shorten:192.168.1.100`, `rl:redirect:10.0.0.1`

**Memory estimation:**
- Key: ~15-25 bytes (`rl:shorten:192.168.1.100`)
- Value: 8 bytes (Redis integer encoding)
- TTL metadata: ~20 bytes
- Redis overhead: ~32 bytes per key
- Total per key: ~80 bytes
- Với 10000 unique IPs: ~800KB
- Với 1M unique IPs: ~80MB

Redis tự động xóa key khi TTL hết. Không cần cleanup job.

**Key scope:**
- Mỗi route có key riêng (shorten, redirect)
- Mỗi IP có key riêng trong mỗi route
- Tổng số key = số unique IPs * số routes được rate limit

**Vấn đề security với key:**
- Key bao gồm IP, có thể ghi vào Redis logs
- Nếu Redis cluster lớn, key distribution dựa trên hash slot, không optimized cho IP-based lookup
- Có thể dùng Redis Cluster với key hash tags: `rl:{shorten}:192.168.1.100` để đảm bảo cùng slot

### 6.5. Client IP Detection

**File:** `gateway/ratelimit.go:68-83`

```go
func clientIP(r *http.Request) string {
    if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
        ip := strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
        if ip != "" {
            return ip
        }
    }
    if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
        return realIP
    }
    host, _, err := net.SplitHostPort(r.RemoteAddr)
    if err == nil && host != "" {
        return host
    }
    return r.RemoteAddr
}
```

**Thứ tự ưu tiên:**
1. `X-Forwarded-For` header (lấy IP đầu tiên)
2. `X-Real-IP` header
3. `RemoteAddr` (split host:port)
4. Raw `RemoteAddr` nếu split fail

**Vấn đề:**
- **X-Forwarded-For spoofing:** Client có thể gửi fake `X-Forwarded-For` header để bypass rate limit. Ví dụ: gửi `X-Forwarded-For: 10.0.0.1` và rate limiter rate-limit 10.0.0.1, nhưng IP thật là attacker.
- **Thiếu trusted proxy list:** Mọi `X-Forwarded-For` đều được trust. Production deployment nên chỉ trust proxy IPs (gateway's upstream proxies như nginx, ELB).
- **IPv6 normalization:** `2001:db8::1` và `2001:0db8:0000:0000:0000:0000:0000:0001` là cùng một địa chỉ. Code không normalize, dẫn đến rate limit bypass qua different representations.
- **Private IP bypass:** Nếu attacker ở trong internal network, `X-Forwarded-For` có thể là private IP. Không issue nhưng cần lưu ý.

**Proxy trust model:**
- Gateway nhận request từ nginx
- Nginx set `X-Real-IP` từ client IP thật
- Gateway nên chỉ đọc `X-Real-IP` và ignore `X-Forwarded-For` (hoặc strip external X-Forwarded-For)

### 6.6. Thiếu Atomicity với Lua Script

Hiện tại, rate limiter sử dụng 2-3 Redis commands riêng biệt (INCR, EXPIRE, TTL). Atomicity không được đảm bảo. **Lua script** có thể giải quyết:

```lua
-- Lua script (atomic rate limit)
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local count = redis.call("INCR", key)
if count == 1 then
    redis.call("EXPIRE", key, window)
end

if count > limit then
    local ttl = redis.call("TTL", key)
    return {0, ttl}  -- denied
end

return {1, 0}  -- allowed
```

**Lợi ích:**
- Atomic: INCR + EXPIRE + TTL trong một round trip
- Không race condition
- Giảm network overhead
- Cache script trên Redis (EVALSHA)

**Implementation bằng Go:**
```go
const rateLimitScript = `
local count = redis.call("INCR", KEYS[1])
if count == 1 then
    redis.call("EXPIRE", KEYS[1], ARGV[1])
end
if count > tonumber(ARGV[2]) then
    return {0, redis.call("TTL", KEYS[1])}
end
return {1, 0}
`

func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, windowSecs int) (bool, int, error) {
    result, err := rl.client.Eval(ctx, rateLimitScript, []string{"rl:" + key}, windowSecs, limit).Result()
    if err != nil {
        return true, 0, err  // fail-open
    }
    arr := result.([]interface{})
    allowed := arr[0].(int64) == 1
    retryAfter := int(arr[1].(int64))
    return allowed, retryAfter, nil
}
```

---

## 7. Security Analysis Tổng Hợp

### 7.1. Token Revocation — Missing

Như đã phân tích ở 4.6, hệ thống hoàn toàn không có cơ chế thu hồi token. Đây là lỗ hổng bảo mật nghiêm trọng.

**Tác động:**
- User logout không invalidate token → token vẫn dùng được cho đến khi hết hạn (24h)
- Password change không invalidate existing tokens → attacker với token cũ vẫn truy cập
- Account suspension (user vi phạm terms) không thể enforce ngay lập tức
- Token leak không thể mitigate (phải chờ 24h)

**Giải pháp khuyến nghị:**
- Thêm `token_version` integer column trong users table
- JWT claims thêm `tvr` (token version)
- Middleware kiểm tra `claims.tvr == user.token_version`
- Khi user logout/change password, increment token_version trong database
- Tất cả token cũ tự động invalidate

### 7.2. Refresh Tokens — Missing

Không có refresh token flow. User phải login lại mỗi 24 giờ.

**Tác động:**
- UX kém — mobile app yêu cầu login hàng ngày
- Không thể có "remember me" functionality
- TTL dài (24h) tăng rủi ro bảo mật

**Giải pháp khuyến nghị:**
- Access token TTL: 15 phút
- Refresh token TTL: 7-30 ngày
- Refresh token lưu trong database hoặc Redis
- `/api/auth/refresh` endpoint nhận refresh token, trả về access token mới
- Refresh token có thể bị thu hồi

### 7.3. Per-Endpoint Rate Limiting

Rate limiting hiện tại chỉ có 2 cấu hình:
- `shorten` — 10 requests/60s
- `redirect` — 300 requests/60s

Các endpoints khác (register, login, analytics) **không có rate limit**.

**Vấn đề:**
- Login endpoint không rate limit → brute-force attack
- Register endpoint không rate limit → account creation flood
- Analytics endpoint không rate limit → resource exhaustion

**Login brute-force protection:**
Không có account lockout, không có rate limit per IP, không có rate limit per email. Attacker có thể thử hàng triệu password cho một email.

**Giải pháp:**
- Rate limit login: 5 attempts/IP/minute, 3 attempts/email/minute
- Progressive delay: tăng delay sau mỗi lần fail
- CAPTCHA integration cho repeated failures
- Account lockout sau N failures (với auto-unlock sau thời gian)

### 7.4. Shared Secret Risk

JWT secret được share giữa:
1. `user-service` — tạo và verify token
2. `gateway` — verify token

Cả hai đều đọc `JWT_SECRET` từ environment variable.

**Vấn đề:**
- Một service compromised → JWT signing key compromised → attacker tạo token cho bất kỳ user nào
- Key rotation requires coordinated deployment
- Secret trong memory có thể bị dump (core dump, /proc/PID/mem)

**Giải pháp:**
- **RS256 (RSA):** User-service có private key (sign), gateway có public key (verify). Nếu gateway bị compromised, attacker chỉ có public key, không thể sign token.
- **Vault/Secrets Manager:** Lưu secret trong HashiCorp Vault, AWS Secrets Manager, hoặc Kubernetes Secrets. Secret được mount vào pod/filesystem.
- **Key rotation:** Support multiple active keys (kid header), cho phép rotate key mà không downtime.

### 7.5. CORS Configuration

**File:** `gateway/middleware.go:34-48`

```go
w.Header().Set("Access-Control-Allow-Origin", "*")
```

CORS được cấu hình với `*` origin — cho phép tất cả origins truy cập API.

**Vấn đề:**
- Bất kỳ website nào cũng có thể gửi request từ browser của user (CORS không phải là security mechanism, nhưng kết hợp với credentials có thể gây vấn đề)
- Với `Access-Control-Allow-Origin: *`, browser không gửi credentials (cookies, Authorization header) trong cross-origin requests. Đây là behavior của Fetch Standard — `*` + credentials = error.
- Vì JWT được gửi qua `Authorization` header, browser sẽ không gửi kèm với wildcard origin. Điều này vô tình bảo vệ API khỏi cross-origin requests có credentials.

**Khuyến nghị:**
- Nếu API public, `*` là acceptable
- Nếu có specific frontend domains, nên restrict: `https://app.example.com`
- Không dùng `*` với `Access-Control-Allow-Credentials: true`

### 7.6. Correlation ID — Truy Vết Request

**File:** `gateway/middleware.go:13-32`

Mỗi request được gán một correlation ID:
```go
func newCorrelationID() string {
    var b [16]byte
    if _, err := rand.Read(b[:]); err != nil {
        return "unknown"
    }
    return hex.EncodeToString(b[:])
}
```

**Phân tích:**
- 16 bytes từ `crypto/rand` (128-bit entropy) — không thể đoán trước
- Nếu `crypto/rand` fail, fallback về "unknown" — multiple requests có cùng ID
- Correlation ID được propagate qua HTTP headers cho upstream services
- Hữu ích cho debugging distributed transactions
- Có thể dùng CSRF protection (synchronizer token pattern) nhưng không thay thế CSRF token

### 7.7. Content-Type Enforcement

Register và login endpoints yêu cầu `Content-Type: application/json`. Đây là biện pháp bảo vệ:

**Tác dụng:**
- Chống CSRF sử dụng form encoding (không thể tạo `<form>` submission với JSON)
- Chống MIME sniffing attacks (browser không thể interpret response khác)
- Đảm bảo request body được parse đúng

**Thiếu sót:**
- Các endpoints khác trong hệ thống (url-service, analytics-service) không enforce Content-Type (kiểm tra qua gateway proxy)
- `Content-Type: application/json; charset=utf-8` sẽ pass (trailing charset) — OK
- Nếu client gửi `Content-Type: application/json` nhưng body không phải JSON, `Decode` sẽ fail với lỗi parse — handler trả về 400

### 7.8. Body Size Limiting

`http.MaxBytesReader(w, r.Body, 1<<20)` giới hạn kích thước body ở 1MB.

**Phân tích:**
- 1MB = 1048576 bytes
- Kích thước JSON điển hình cho register/login: 50-100 bytes
- Tỷ lệ 10000:1 giữa giới hạn và kích thước thực tế — có thể giảm
- Giới hạn này ngăn chặn resource exhaustion qua large body
- Go's `json.Decoder` tự động fail khi đọc quá giới hạn

**Cải tiến:**
- Giảm còn 1KB (1024 bytes) cho register/login
- 64KB cho endpoints có thể nhận dữ liệu lớn hơn (ví dụ: create URL với metadata)

### 7.9. Error Message Leakage

**User Service errors:**
- 400: "invalid request body", "invalid email format", "password must be at least 8 characters"
- 401: "invalid credentials"
- 409: "email already registered"
- 415: "content-type must be application/json"
- 500: "internal server error"

**Gateway errors:**
- 401: "unauthorized"
- 404: "not found"
- 429: "rate limit exceeded"
- 502: "bad gateway", "upstream not found"
- 503: "url-service unavailable"

**Phân tích:**
- Error messages không leak internal details (no stack traces, no SQL queries, no file paths)
- 500 luôn trả về "internal server error" — không tiết lộ nguyên nhân
- 409 "email already registered" — có thể dùng để enum email qua register endpoint
- 401 "invalid credentials" — generic, không phân biệt email không tồn tại vs sai password

**Leakage qua status code:**
- Register: 201 = tạo thành công, 409 = email đã tồn tại. Attacker có thể enum email
- Login: luôn 401 cho failure. Không phân biệt. Tốt.

### 7.10. Database Credential Masking

**File:** `services/user-service/db.go:37-39`

```go
func maskDBSecret(url string) string {
    return "[REDACTED]"
}
```

Khi logging database URL, credential được mask hoàn toàn. Đây là best practice. Tuy nhiên, hàm này không thực sự parse URL để mask password — nó trả về `[REDACTED]` cho tất cả. Hữu ích khi log connection URL, nhưng mất thông tin debugging (không biết host, database name).

**Cải tiến:** Parse URL, replace password với `***`:
```go
func maskDSN(dsn string) string {
    parsed, err := url.Parse(dsn)
    if err != nil {
        return "[REDACTED]"
    }
    parsed.User = url.UserPassword(parsed.User.Username(), "***")
    return parsed.String()
}
```

### 7.11. Go Module Isolation

Mỗi service có `go.mod` riêng, workspace root có `go.work`. Điều này:
- Giảm blast radius của dependency vulnerability
- Cho phép mỗi service sử dụng version khác nhau của cùng thư viện
- Tách biệt shared packages (`shared/auth`, `shared/logger`, `shared/events`)

**Shared module auth:**
- `shared/auth/go.mod` riêng
- Các service tham chiếu qua replace directive: `replace github.com/ikniz/url-shortener/shared/auth => ../../shared/auth`
- Cho phép unit test với auth package mà không cần import toàn bộ service

---

## 8. Resilience Analysis Tổng Hợp

### 8.1. Fail-Open vs Fail-Closed Decisions

Hệ thống có nhiều quyết định fail-open/fail-closed khác nhau:

**Rate Limiter — Fail-Open:**
- Khi Redis error, rate limiter cho phép request đi qua
- Lý do: Availability > strict rate limiting
- Rủi ro: Mất rate limit protection khi Redis down

**Circuit Breaker — Fail-Open (khi OPEN):**
- Request bị reject với ErrCircuitOpen, không retry
- Server trả về 503 Service Unavailable
- Lý do: Bảo vệ upstream khỏi overload
- Đây là fail-closed từ góc độ client (request không được phục vụ) nhưng fail-open từ góc độ upstream (không gửi request đến upstream đang lỗi)

**Database Connection Pool — Fail-Closed:**
- Nếu `pool.Ping()` thất bại, service exit
- Lý do: Service không thể hoạt động không có database
- Có thể cải tiến: health check + graceful degradation (read from cache)

**Redis Connection — Fail-Closed (startup):**
- Nếu `redis.ParseURL` thất bại, service exit
- Sau startup, Redis error → fail-open (rate limiter)

**JWT Verification — Fail-Closed:**
- Nếu token invalid hoặc secret sai, request bị từ chối
- Không có fallback authentication method

**Proxy Upstream — Fail-Open (với circuit breaker):**
- Nếu upstream trả về 5xx, error được trả về cho client
- Circuit breaker mở → request không được gửi upstream

**So sánh các quyết định:**

| Component | Failure | Behavior | Đánh giá |
|-----------|---------|----------|----------|
| Rate Limiter | Redis error | Fail-open | ✅ Đúng — không block user vì infrastructure issue |
| DB Connection | Ping fail | Fail-closed (exit) | ✅ Đúng — không thể hoạt động không có DB |
| JWT | Signature invalid | Fail-closed (401) | ✅ Đúng — security không thể fail-open |
| Proxy | Upstream timeout | Fail-closed (502) | ✅ Đúng — không thể trả về garbage data |
| Circuit Breaker | Circuit open | Fail-closed (503) | ✅ Đúng — bảo vệ upstream |

### 8.2. Retry Mechanisms — Thiếu Vắng

Hệ thống **không có retry mechanism** ở bất kỳ cấp độ nào:

**Thiếu:**
1. **Gateway upstream retry:** Khi proxy gửi request đến upstream và nhận timeout, không có retry. Client nhận 502 Bad Gateway.
2. **Circuit breaker retry:** Khi request thất bại (5xx), không có automatic retry sau backoff.
3. **Database retry:** Khi `pgxpool.QueryRow` thất bại (connection error, deadlock detected), không có retry.
4. **Redis retry:** go-redis có sẵn retry mechanism (có thể cấu hình). Mặc định có retry.
5. **Message queue retry:** RabbitMQ publishers (analytics-service, url-service) không có retry logic trong publisher code.

**Impact:**
- Transient database deadlock (PostgreSQL error 40P01) → user nhận 500 thay vì retry
- Transient network partition → request thất bại thay vì retry
- Service restart (rolling update) → request gửi đến đúng lúc restart bị mất

**Khuyến nghị:**
- **Exponential backoff cho database:** retry với jitter cho transient errors
- **Circuit breaker với retry:** HAPPROXY pattern — retry 2-3 lần trước khi trip circuit breaker
- **Idempotency key:** Cho phép client retry an toàn (POST /shorten với idempotency key)
- **Kubernetes liveness/readiness probes:** Đảm bảo service không nhận traffic khi đang restart

### 8.3. Graceful Shutdown

**User Service (`user-service/main.go:57-63`):**
```go
go func() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    cancel()
    srv.Shutdown(ctx)
}()
```

**Phân tích:**
- Signal handler trong goroutine riêng
- Khi nhận signal: cancel context (cho in-flight requests biết shutdown) và shutdown HTTP server
- `srv.Shutdown(ctx)` — graceful shutdown, chờ in-flight requests hoàn thành
- `defer pool.Close()` — đóng database pool sau khi server shutdown

**Vấn đề:**
- `cancel()` gọi trước `srv.Shutdown()` — có thể cancel context quá sớm, gây ra in-flight requests fail
- `ctx` được tạo ở đầu main, cancel ở đây có thể ảnh hưởng đến database operations còn đang chạy
- Không có timeout cho shutdown (srv.Shutdown dùng context.Background())

**Gateway (`gateway/main.go`):**
```go
go func() {
    log.Info("server listening", "port", cfg.Port)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Error("server error", "error", err)
        os.Exit(1)
    }
}()

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
<-quit

log.Info("shutting down gateway")
srv.Shutdown(context.Background())
```

**Phân tích:**
- Gateway sử dụng pattern khác: goroutine cho ListenAndServe, main goroutine block on signal
- `srv.Shutdown(context.Background())` — không có timeout, có thể block vô hạn
- Không close Redis connection pool (rate limiter) khi shutdown — `defer limiter.Close()` không được gọi nếu os.Exit ở goroutine

### 8.4. Database Connection Pooling

**User Service (`services/user-service/db.go:15-35`):**
```go
cfg.MaxConns = 10
cfg.MinConns = 2
```

**Phân tích:**
- MinConns=2: duy trì 2 kết nối luôn sẵn sàng, giảm latency cho request đầu tiên
- MaxConns=10: tối đa 10 concurrent database connections
- Pool được tạo thông qua pgxpool
- `pool.Ping(ctx)` kiểm tra kết nối ngay sau khi khởi tạo
- Nếu pool creation thất bại, service exit (fail-closed)

**Đánh giá sizing:**
- User service chỉ có 3 endpoints (register, login, me) + health
- Load dự kiến: vài trăm RPS
- 10 connections có thể đủ, nhưng thiếu connection pooling cho heavy load
- Không có connection pool cho url-service (cần kiểm tra)

**Thiếu:**
- MaxConnLifetime — giới hạn thời gian sống của connection (tránh stale connections với database proxy như PgBouncer, HAProxy)
- MaxConnIdleTime — đóng kết nối idle, giải phóng tài nguyên
- Health check interval — kiểm tra kết nối định kỳ

### 8.5. Context Propagation và Timeout

**User Service:**
- `r.Context()` được truyền vào database operations
- Khi context cancel (shutdown), database queries fail nhanh
- Không có request timeout — nếu client không đóng kết nối, handler chờ database vô hạn

**Gateway:**
- `h.circuitBreaker.Do(r.Context(), func() error { ... })` — context từ HTTP request
- Rate limiter có timeout 100ms: `context.WithTimeout(ctx, 100*time.Millisecond)`
- Proxy không có timeout — có thể chờ upstream response vô hạn

**Thiếu:**
- Request timeout middleware (ví dụ: 30s timeout cho tất cả requests)
- Context deadline propagation: nếu request timeout, proxy nên cancel upstream request upstream
- Database query timeout: context nên có deadline để tránh long-running queries

### 8.6. Health Checks

Mỗi service có health endpoint:
- `GET /user-service/health`
- `GET /gateway/health`
- `GET /url-service/health`
- `GET /analytics-service/health`
- `GET /notification-service/health`

**User Service health:**
```go
func NewHealthHandler(serviceName string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, map[string]string{
            "service": serviceName,
            "status":  "ok",
        })
    }
}
```

**Phân tích:**
- Health check chỉ trả về status "ok"
- Không kiểm tra database connectivity
- Không kiểm tra upstream dependencies
- Liveness probe: OK (service còn chạy)
- Readiness probe: KHÔNG OK (không biết database có hoạt động không)
- Không có dependency check: nếu database die, health vẫn trả về 200

**Cải tiến:**
- Readiness probe: kiểm tra database ping, Redis ping, upstream health
- Liveness probe: chỉ kiểm tra process alive
- Separate endpoints: `/health/live`, `/health/ready`

### 8.7. Signal Handling

**User Service:**
```go
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
```

**Gateway:**
```go
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
```

**Phân tích:**
- Cả hai đều handle SIGINT (Ctrl+C) và SIGTERM (Kubernetes, docker stop)
- Không handle SIGHUP (reload config)
- Không handle SIGQUIT (stack trace dump)
- Signal channel có buffer = 1, nhưng signal.Notify không block — signals có thể bị mất nếu channel full
- Không ignore unnecessary signals (SIGPIPE, SIGCHLD)

**Cải tiến:**
- Thêm SIGHUP cho config reload
- Handle SIGQUIT để dump goroutine stack vào log (debug)
- Channel có buffer > 1

### 8.8. Logging và Observability

**Logger setup:**
```go
log := logger.New(cfg.ServiceName)
```

**Structured logging với slog:**
- Service name tag trên mọi log entry
- Correlation ID context propagation
- Error logging với stack trace context

**Log coverage:**
- User service: request validation failure, duplicate email, store insert failure, token verification failure, server start/shutdown
- Gateway: rate limit failure, circuit breaker state changes, upstream errors

**Thiếu:**
- Request logging middleware (method, path, status, duration)
- Slow query detection
- Panic recovery middleware (hiện tại không có recover)
- Audit log (login success/failure with IP, user agent)

**Metrics coverage:**
- Circuit breaker state (gauge)
- Circuit breaker trips (counter)
- Circuit breaker rejections (counter)
- Request count per service per status class (counter)
- Request duration per service (histogram)
- User service có Prometheus metrics endpoint (`GET /metrics`)
- Gateway có Prometheus metrics endpoint (`GET /metrics`)

**Thiếu metrics:**
- Rate limit hits/misses
- Database query duration
- JWT verification duration
- Active connections (goroutine count, memory usage)
- Error rate per endpoint (currently only per status class: 4xx, 5xx)

---

## 9. Kết Luận và Khuyến Nghị

### Tổng Kết

Hệ thống URL Shortener Microservices triển khai một số patterns bảo mật và resilience đáng chú ý:

**Điểm mạnh:**
1. **Bảo vệ password mạnh:** BCrypt cost 12, salt tự động, hash format chuẩn
2. **Timing attack mitigation:** Dummy bcrypt verify cho non-existent emails
3. **JWT verification an toàn:** Algorithm confusion protection, claim validation, issuer check
4. **Circuit breaker:** 3-state machine với half-open probe, metrics integration
5. **Rate limiter:** Redis-based, configurable per route, fail-open behavior
6. **Graceful shutdown:** Signal handling, context cancellation
7. **Correlation ID:** Distributed tracing support
8. **Structured logging:** slog với service name, context propagation
9. **Body size limiting:** MaxBytesReader protection
10. **Content-Type enforcement:** JSON-only API endpoints
11. **Module isolation:** Separate go.mod per service, shared packages

**Điểm yếu cần cải thiện:**

| Priority | Issue | Impact | Effort |
|----------|-------|--------|--------|
| CRITICAL | Token revocation missing | Attacker với token bị đánh cắp có thể truy cập 24h | Medium |
| CRITICAL | Login rate limiting missing | Brute-force attack | Low |
| HIGH | Refresh tokens missing | UX kém, TTL dài | Medium |
| HIGH | Shared JWT secret (HS256) | Một service compromised = signing key lộ | Medium |
| HIGH | Circuit breaker chỉ cho url-service | Các service khác không được bảo vệ | Low |
| HIGH | Half-open probe race condition | Multiple probes có thể overload upstream | Low |
| MEDIUM | Email enumeration qua register endpoint | 201 vs 409 status code | Low |
| MEDIUM | Fixed window rate limit | Burst at window boundaries | Medium |
| MEDIUM | Thiếu retry mechanisms | Transient errors gây failure | Medium |
| MEDIUM | Database connection pool config | Thiếu MaxConnLifetime, health check | Low |
| MEDIUM | No request timeout middleware | Request có thể chờ vô hạn | Low |
| LOW | CORS wildcard origin | Không restrict được specific domains | Low |
| LOW | JWT payload chứa email (PII) | Email exposed trong token | Low |
| LOW | Password validation yếu | Chỉ check độ dài | Low |
| LOW | Health check không kiểm tra dependencies | False positive readiness | Low |
| LOW | Lua script atomicity cho rate limiter | Race condition ở EXPIRE | Low |

### Khuyến Nghị Chi Tiết

**P0 (Critical — cần triển khai ngay):**

1. **Token revocation:** Thêm `token_version` trong users table. JWT chứa version claim. Khi logout, increment version. Middleware kiểm tra version match.

2. **Login rate limiting:** Thêm rate limit cho `POST /api/auth/login` — 5 attempts/IP/minute, 3 attempts/email/minute. Sử dụng Redis fixed window (giống rate limiter hiện tại).

**P1 (High — triển khai trong 1-2 tuần):**

3. **Refresh tokens:** Triển khai JWT access token (15 phút) + refresh token (7 ngày). Refresh token endpoint có rate limit riêng. Refresh token lưu trong Redis.

4. **RS256 signing:** Thay thế HS256 bằng RS256. User-service có RSA key pair. Gateway chỉ có public key. Key rotation support.

5. **Circuit breaker cho tất cả services:** Mở rộng CB cho analytics, notification, user services.

**P2 (Medium — triển khai trong 1 tháng):**

6. **Authentication bypass trong login flow:** Fix bug khi `FindByEmail` trả về error và nil user.

7. **Lua script cho rate limiter:** Đảm bảo atomicity, eliminate race conditions.

8. **Exponential backoff retry:** Cho database queries, upstream proxy calls.

**P3 (Low — cải tiến dần):**

9. **Email validation nâng cao:** Kiểm tra độ dài, DNS MX record check.

10. **Password policy:** Thêm yêu cầu complexity, check common password list.

11. **Health check nâng cao:** Readiness probe kiểm tra database, Redis connectivity.

---

*Báo cáo hoàn thành ngày 2026-07-11. Phân tích dựa trên source code tại thời điểm viết báo cáo.*
