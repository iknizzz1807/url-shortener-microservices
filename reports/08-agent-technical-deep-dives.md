# Phân Tích Kỹ Thuật Chuyên Sâu — URL Shortener Microservices

> **Tác giả:** Agent khảo sát tự động
> **Ngày:** 2026-07-11
> **Phiên bản:** v1.0
> **Phạm vi:** 5 deep dives về kiến trúc, bảo mật, đồng thời, và độ tin cậy của hệ thống

---

## Mục Lục

1. [Deep Dive 1: Timing Attack Mitigation trong Login Endpoint](#deep-dive-1-timing-attack-mitigation-trong-login-endpoint)
2. [Deep Dive 2: Outbox Pattern và Concurrent Safety](#deep-dive-2-outbox-pattern-và-concurrent-safety)
3. [Deep Dive 3: Cursor-based Pagination vs OFFSET](#deep-dive-3-cursor-based-pagination-vs-offset)
4. [Deep Dive 4: Circuit Breaker + Rate Limiter Interaction](#deep-dive-4-circuit-breaker--rate-limiter-interaction)
5. [Deep Dive 5: Graceful Shutdown Analysis](#deep-dive-5-graceful-shutdown-analysis)

---

## Deep Dive 1: Timing Attack Mitigation trong Login Endpoint

### 1.1. Giới thiệu về Timing Attack

Timing attack là một dạng tấn công side-channel mà kẻ tấn công dựa vào thời gian phản hồi của hệ thống để suy luận thông tin nhạy cảm. Trong bối cảnh xác thực người dùng, timing attack thường nhắm vào việc so sánh mật khẩu hoặc hash: nếu việc so sánh dừng lại ngay khi gặp ký tự/byte không khớp đầu tiên, thời gian xử lý sẽ tỷ lệ thuận với độ dài prefix đúng.

Với bcrypt, tình huống nguy hiểm hơn: nếu ứng dụng chỉ gọi `bcrypt.CompareHashAndPassword` khi user tồn tại trong database và return ngay lập tức (hoặc nhanh hơn đáng kể) khi user không tồn tại, kẻ tấn công có thể:
- Phân biệt email đã đăng ký vs email chưa đăng ký dựa trên thời gian phản hồi
- Bruteforce danh sách email hợp lệ mà không cần truy cập database
- Kết hợp với các attack vector khác (credential stuffing, password spraying)

### 1.2. Phân Tích Code — Cơ Chế Dummy Hash

Trong `services/user-service/handler.go`, chúng ta thấy implementation của login endpoint:

```go
const dummyBcryptHash = "$2a$12$MB4lTvA5UVWJU8GPtVFSne/kMHaXBSz45DWvIl/4AS9NLnz7tavNm"

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
    // ... parse request body ...

    user, err := h.store.FindByEmail(r.Context(), req.Email)
    if user == nil {
        _ = h.hasher.Verify(req.Password, dummyBcryptHash)
        writeError(w, http.StatusUnauthorized, "invalid credentials")
        return
    }

    if err := h.hasher.Verify(req.Password, user.PasswordHash); err != nil {
        // ... handle mismatch ...
    }
    // ... issue token ...
}
```

Cơ chế hoạt động:
1. Khi email không tồn tại trong database (`user == nil`), thay vì trả về ngay lập tức, hệ thống thực hiện `h.hasher.Verify(req.Password, dummyBcryptHash)` — một phép so sánh bcrypt giả với hash cố định.
2. Khi email tồn tại, hệ thống gọi `h.hasher.Verify(req.Password, user.PasswordHash)` — so sánh thật.
3. Trong cả hai trường hợp, message trả về giống hệt nhau: `"invalid credentials"` với status `401 Unauthorized`.

### 1.3. Hash cố định được tạo như thế nào?

Dummy hash có format: `$2a$12$MB4lTvA5UVWJU8GPtVFSne/kMHaXBSz45DWvIl/4AS9NLnz7tavNm`

Phân tích:
- `$2a$`: Thuật toán bcrypt (phiên bản 2a)
- `$12$`: Cost factor = 12 (2^12 = 4096 rounds)
- `MB4lTvA5UVWJU8GPtVFSne`: Salt 16-byte (22 ký tự base64)
- `/kMHaXBSz45DWvIl/4AS9NLnz7tavNm`: Hash 24-byte (31 ký tự base64)

Đây là bcrypt hash hợp lệ, được tạo từ một password bất kỳ (không liên quan đến hệ thống). Khi gọi `Verify` với password do attacker cung cấp, bcrypt sẽ thực hiện đúng 4096 rounds của Blowfish key schedule, tốn khoảng ~200ms (trên CPU hiện đại) — giống hệt thời gian xử lý của một hash thật.

### 1.4. Phân Tích Thời Gian Phản Hồi

Giả sử cost factor = 12, trên một CPU Intel Xeon hiện đại (2024):

| Kịch bản | User tồn tại, password đúng | User tồn tại, password sai | User không tồn tại (có mitigation) | User không tồn tại (không mitigation) |
|---|---|---|---|---|
| Thời gian | ~200ms | ~200ms | ~200ms | <1ms |
| DB query | ~1ms | ~1ms | ~1ms | ~1ms |
| So sánh bcrypt | Có | Có | Có (dummy) | Không |
| Tổng cộng | ~201ms | ~201ms | ~201ms | ~1-2ms |

**Không có mitigation:** Attacker gửi request với email ngẫu nhiên. Nếu email không tồn tại, response đến trong ~1ms. Nếu email tồn tại, response đến trong ~201ms. Sự khác biệt ~200ms cho phép attacker dễ dàng phân biệt hai trường hợp chỉ với một vài request.

**Có mitigation (dummy hash):** Cả ba trường hợp đều trả về trong ~201ms. Attacker không thể phân biệt email tồn tại hay không dựa trên thời gian.

### 1.5. Bcrypt Cost Factor và Tác Động

`handler.go` sử dụng `cost := 12` (được khởi tạo từ NewPasswordHasher). Hàm khởi tạo ở `password.go`:

```go
func NewPasswordHasher(cost int) PasswordHasher {
    if cost < bcrypt.MinCost {
        cost = bcrypt.MinCost
    }
    if cost > bcrypt.MaxCost {
        cost = bcrypt.MaxCost
    }
    return &bcryptHasher{cost: cost}
}
```

Phân tích cost:
- **Cost = 4 (MinCost):** ~8ms mỗi hash — quá nhanh, dễ bruteforce
- **Cost = 10 (Default):** ~100ms — cân bằng giữa bảo mật và hiệu năng
- **Cost = 12 (Hiện tại):** ~200ms — tốt cho bảo mật, chấp nhận được cho login
- **Cost = 14:** ~800ms — có thể gây trải nghiệm người dùng kém
- **Cost = 31 (MaxCost):** ~hàng nghìn tỷ năm — không khả thi

Cost = 12 là lựa chọn hợp lý. Nó đủ chậm để chống bruteforce (khoảng 5 hash/giây trên CPU đơn), nhưng đủ nhanh để không ảnh hưởng đến UX. Tuy nhiên, đi kèm với vấn đề CPU waste khi bị DDoS (xem mục 1.6).

### 1.6. CPU Waste và DDoS Concern

Một hệ quả không mong muốn: dummy hash biến mỗi request login với email không tồn tại thành một tác vụ tốn ~200ms CPU. Nếu attacker gửi 1000 request/giây với email không tồn tại:

- **Không có mitigation:** 1000 request × 1ms = 1 CPU-second/giây. Hầu như không tải.
- **Có mitigation:** 1000 request × 200ms = 200 CPU-seconds/giây. Cần ~200 CPU cores để xử lý.

**Phân tích rủi ro DDoS:**

| Tài nguyên | Không mitigation | Có mitigation |
|---|---|---|
| CPU cost/request | ~1ms | ~200ms |
| Requests/giây để saturation 1 core | ~1000 rps | ~5 rps |
| Requests/giây để saturation 16 cores | ~16000 rps | ~80 rps |
| Bandwidth cost | Thấp | Vừa phải |

Đây là một trade-off có chủ đích: đánh đổi CPU để lấy bảo mật. Trong hầu hết các hệ thống production, đây là quyết định đúng đắn vì:
1. Login endpoint thường được rate-limit nghiêm ngặt (xem [Deep Dive 4](#deep-dive-4-circuit-breaker--rate-limiter-interaction))
2. DDoS layer 7 có thể được mitigate bằng WAF/CDN
3. Rủi ro information disclosure (email enumeration) nghiêm trọng hơn CPU cost

### 1.7. Alternative Approaches

**1. Constant-time Comparison (với hash tự tạo)**

```go
func constantTimeVerify(plaintext, hash string) bool {
    // Luôn tạo hash từ plaintext, dù hash đầu vào không hợp lệ
    fakeHash, _ := bcrypt.GenerateFromPassword([]byte(plaintext), cost)
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}
```

Nhược điểm: vẫn gọi `GenerateFromPassword` (tốn ~200ms), nhưng dummy hash không cần precompute.

**2. Sleep-based Delay**

```go
if user == nil {
    time.Sleep(200 * time.Millisecond)
    writeError(w, http.StatusUnauthorized, "invalid credentials")
    return
}
```

Nhược điểm:
- Goroutine bị blocking 200ms — nếu có 10,000 concurrent requests, 10,000 goroutines bị giữ
- Dễ bị phát hiện qua pattern thời gian (luôn chính xác 200ms) — bcrypt hash có variance tự nhiên
- Sử dụng CPU hiệu quả hơn dummy hash (không tốn CPU), nhưng tốn goroutine

**3. Rate Limiting**

Sử dụng rate limiter (xem [Deep Dive 4](#deep-dive-4-circuit-breaker--rate-limiter-interaction)) để giới hạn số lần thử login từ một IP. Đây là lớp phòng thủ thứ hai, không thể thay thế dummy hash.

**4. CAPTCHA**

Thêm CAPTCHA sau N lần thử thất bại. Phòng thủ lớp ứng dụng.

### 1.8. Đánh Giá Tổng Thể

| Tiêu chí | Đánh giá |
|---|---|
| Hiệu quả timing protection | ✅ Tuyệt đối — thời gian phản hồi đồng nhất trong mọi trường hợp |
| Chi phí bảo trì | ✅ Thấp — một hằng số cố định, zero config |
| CPU overhead | ⚠️ Trung bình — ~200ms mỗi request không có user |
| DDoS resistance | ⚠️ Kém hơn — cần rate limiter bù đắp |
| Implementation complexity | ✅ Rất thấp — chỉ thêm 1 dòng code |

**Kết luận:** Dummy bcrypt hash là một mitigation hiệu quả, đơn giản, và đáng tin cậy. Trade-off về CPU là chấp nhận được khi kết hợp với rate limiting.

---

## Deep Dive 2: Outbox Pattern và Concurrent Safety

### 2.1. Tổng Quan về Outbox Pattern

Outbox pattern là một architectural pattern giải quyết bài toán "dual write" — ghi dữ liệu vào database đồng thời gửi message đến message queue. Nếu không có outbox, hệ thống phải đối mặt với:

1. **Ghi DB thành công, gửi message thất bại:** Mất event, dữ liệu không nhất quán
2. **Gửi message thành công, ghi DB thất bại:** Message orphan, không có dữ liệu tương ứng
3. **Distributed transaction (2PC):** Phức tạp, chậm, không được nhiều hệ thống hỗ trợ

Outbox pattern giải quyết bằng cách:
1. Ghi event vào bảng `outbox` trong **cùng transaction** với nghiệp vụ chính
2. Một tiến trình riêng (OutboxCoordinator) đọc từ bảng outbox và publish lên message queue
3. Đánh dấu event đã published sau khi publish thành công

### 2.2. Kiến Trúc Outbox trong URL Shortener

Hệ thống sử dụng outbox pattern qua ba thành phần chính:

**A. Transaction Layer** (`service.go`):

```go
err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
    // INSERT URL — nghiệp vụ chính
    if err := s.store.Insert(ctx, tx, ur); err != nil {
        return err
    }
    // INSERT OUTBOX — event trong cùng transaction
    if err := s.outboxStore.InsertEvent(ctx, tx, outbox); err != nil {
        return err
    }
    return nil // Commit
})
```

**B. OutboxCoordinator** (`outbox.go`):

```go
type OutboxCoordinator struct {
    store     OutboxStore
    publisher RabbitMQPublisher
    log       *slog.Logger
}
```

Coordinator chạy trong một goroutine riêng, định kỳ poll bảng outbox và publish event.

**C. OutboxStore** (`outbox_store.go`):

```go
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
```

### 2.3. FOR UPDATE SKIP LOCKED — Phân Tích Chi Tiết

`FOR UPDATE SKIP LOCKED` là một tính năng PostgreSQL (từ 9.5) cho phép transaction "claim" các hàng mà không bị blocking bởi các transaction khác đang lock hàng khác.

**Cơ chế hoạt động:**

1. **FOR UPDATE:** Khóa các hàng được SELECT, ngăn các transaction khác UPDATE/DELETE/SELECT FOR UPDATE các hàng này cho đến khi transaction hiện tại kết thúc (COMMIT/ROLLBACK).

2. **SKIP LOCKED:** Bỏ qua các hàng đã bị khóa bởi transaction khác. Nếu không có SKIP LOCKED, transaction sẽ **block** cho đến khi hàng được unlock.

**Ví dụ với 2 coordinator instances (C1, C2) cùng poll:**

```
Bảng outbox (ban đầu):
| id | published_at | locked_until | event_type |
|----|-------------|--------------|------------|
| 1  | NULL        | NULL         | url.created|
| 2  | NULL        | NULL         | url.created|
| 3  | NULL        | NULL         | url.created|

Bước 1: C1 và C2 cùng chạy query FetchUnpublished
C1: WITH claimed AS (SELECT id FROM outbox ... LIMIT 50 FOR UPDATE SKIP LOCKED)
    → Claim được id=1, id=2, id=3
    → Khóa 3 hàng này

C2: WITH claimed AS (SELECT id FROM outbox ... LIMIT 50 FOR UPDATE SKIP LOCKED)
    → SKIP LOCKED bỏ qua id=1,2,3 (đang bị C1 khóa)
    → Không claim được hàng nào
    → Trả về empty set

Bước 2: C1 UPDATE locked_until, RETURNING → có 3 records
C1: Bắt đầu xử lý publish từng record

Bước 3: C1 publish xong record 1, gọi MarkPublished(id=1)
    → UPDATE SET published_at = now(), locked_until = NULL

Bước 4: C1 gặp lỗi khi publish record 2 (RabbitMQ down)
    → Không gọi MarkPublished → record 2 vẫn có locked_until
    → Sau 30 giây, locked_until < now() → record 2 sẽ được các coordinator khác pick up
```

**Tại sao cần UPDATE locked_until trước?**

Query sử dụng CTE (Common Table Expression) với `UPDATE ... RETURNING` thay vì `SELECT ... FOR UPDATE SKIP LOCKED` riêng. Điều này đảm bảo:

1. **Atomic claim:** Việc chọn hàng và đánh dấu "đã được claim" xảy ra trong cùng một câu lệnh SQL
2. **Visibility:** Các coordinator khác thấy ngay locked_until đã được set
3. **No race condition:** Không có khoảng trống giữa SELECT và UPDATE

### 2.4. Transaction Isolation Levels

PostgreSQL có 4 isolation levels:
- **READ UNCOMMITTED:** Trong PostgreSQL, behaves giống READ COMMITTED
- **READ COMMITTED (Default):** Chỉ thấy dữ liệu đã commit. Mỗi câu lệnh trong transaction thấy snapshot mới nhất tại thời điểm câu lệnh bắt đầu.
- **REPEATABLE READ:** Transaction thấy một snapshot cố định tại thời điểm transaction bắt đầu.
- **SERIALIZABLE:** Các transaction được thực thi như thể chạy tuần tự.

Hệ thống dùng READ COMMITTED (default của PostgreSQL). Phân tích:

| Đặc tính | READ COMMITTED | REPEATABLE READ | SERIALIZABLE |
|---|---|---|---|
| Phantom read | ✅ Có thể xảy ra | ❌ Không | ❌ Không |
| Non-repeatable read | ✅ Có thể xảy ra | ❌ Không | ❌ Không |
| Dirty read | ❌ Không (PostgreSQL) | ❌ Không | ❌ Không |
| Performance | 🟢 Tốt nhất | 🟡 Trung bình | 🔟 Chậm nhất |
| Lock contention | 🟢 Thấp | 🟡 Trung bình | 🔴 Cao |

**Tại sao READ COMMITTED là đủ?**

Với `FOR UPDATE SKIP LOCKED`, READ COMMITTED là đủ vì:
- `FOR UPDATE` đảm bảo không có dirty write
- `SKIP LOCKED` xử lý contention mà không cần isolation cao hơn
- Phantom reads không thành vấn đề vì coordinator chỉ cần claim hàng có sẵn
- Nếu một hàng bị claim bởi coordinator khác, nó sẽ bị SKIP LOCKED — coordinator hiện tại không thấy nó

**Kịch bản lỗi với REPEATABLE READ:**

Nếu dùng REPEATABLE READ, coordinator C1 và C2 có thể thấy cùng một snapshot cũ, dẫn đến cả hai đều cố gắng claim cùng hàng. `FOR UPDATE SKIP LOCKED` vẫn xử lý được (một trong hai sẽ bỏ qua), nhưng READ COMMITTED đơn giản và hiệu quả hơn.

### 2.5. Race Condition Analysis — Nhiều Coordinator Instances

**Kịch bản 1: Cả hai coordinator cùng claim các hàng khác nhau**

```
C1 claim: id=1,2,3
C2 claim: id=4,5,6
→ Không race condition. Cả hai xử lý song song.
```

**Kịch bản 2: Cả hai coordinator cùng claim cùng một hàng (gần như không thể với SKIP LOCKED)**

```
Bảng outbox có 1 hàng duy nhất id=1

C1 chạy query → lock id=1
C2 chạy query → SKIP LOCKED bỏ qua id=1 → không claim được gì

→ Không race. Mỗi hàng chỉ được xử lý bởi một coordinator.
```

**Kịch bản 3: Coordinator xử lý chậm, lock hết hạn**

```
C1 claim id=1, locked_until = now + 30s
C1 bắt đầu publish (RabbitMQ hơi chậm)
... 35 giây trôi qua ...
locked_until < now() → hàng được release implicit

C2 poll → thấy id=1 (locked_until < now()) → claim id=1
C1 publish xong → gọi MarkPublished(id=1) → UPDATE SET published_at = now()

C2 cũng publish → publish lại event lần 2 → DUPLICATE EVENT
```

Đây là **vấn đề nghiêm trọng** — duplicate event. Phân tích:

- **MarkPublished** sử dụng:
  ```sql
  UPDATE outbox SET published_at = now(), locked_until = NULL WHERE id = $1 AND published_at IS NULL
  ```
  Nếu C1 đã gọi `MarkPublished` trước, `published_at` đã được set, C2 sẽ không UPDATE được gì (`RowsAffected() == 0`) → `pgx.ErrNoRows`.

- Tuy nhiên, nếu C1 chưa kịp gọi `MarkPublished` (vẫn đang publish), C2 sẽ publish và gọi MarkPublished — dẫn đến duplicate.

**Mitigation:** Consumer phải idempotent (ví dụ: dedup bằng event ID).

### 2.6. Failure Scenarios and Recovery

**Kịch bản A: Partial batch failure**

```
C1 claim 50 records: id=1..50
Publish thành công: id=1..40
Publish thất bại: id=41 (RabbitMQ timeout)
Publish không được thử lại: id=42..50

Kết quả:
- id=1..40: MarkPublished → published
- id=41..50: locked_until vẫn còn, chưa published
→ Sau 30s, coordinator (C1 hoặc C2) sẽ pick up lại id=41..50
→ id=41 được publish lại (duplicate, cần idempotency)
→ id=42..50 publish lần đầu
```

**Kịch bản B: Coordinator crash**

```
C1 claim id=1..50
C1 crash (OOM, SIGKILL, node failure)
Transaction của C1 bị rollback → FOR UPDATE lock được giải phóng
locked_until đã được UPDATE trước đó vẫn còn (UPDATE là committed, vì nó là statement riêng trong CTE)

Wait, thực ra locked_until UPDATE có thể đã được commit.
Nếu C1 crash sau CTE query nhưng trước khi publish:
- locked_until đã được set → sau 30s, coordinator khác pick up
- Các hàng này chưa được publish → publish bình thường
- Lock từ FOR UPDATE đã được giải phóng khi C1's connection đóng
```

**Kịch bản C: Coordinator crash trong khi đang publish**

```
C1 claim id=1, đã gọi Publish (RabbitMQ đã nhận), chưa kịp MarkPublished
C1 crash

Trạng thái:
- id=1 đã được publish lên RabbitMQ (consumer đã nhận)
- id=1 chưa được MarkPublished (published_at = NULL)
- locked_until vẫn còn

Sau 30s:
- Coordinator khác pick up id=1
- Publish lại id=1 → DUPLICATE
```

**Mitigation tầng consumer:**
- Consumer (analytics-service) sử dụng `DeduplicationStore` để tránh xử lý duplicate click events
- Cần idempotency key dựa trên event ID (UUID)

### 2.7. Worker Pool Architecture

OutboxCoordinator sử dụng worker pool với:

```go
const (
    outboxBatchSize   = 50
    outboxWorkerCount = 3
    outboxPollEvery   = 2 * time.Second
)
```

**Phân tích:**

| Tham số | Giá trị | Cơ sở |
|---|---|---|
| `outboxBatchSize` | 50 | Batch size vừa phải, tránh lock quá nhiều hàng |
| `outboxWorkerCount` | 3 | 3 worker xử lý song song, tận dụng CPU |
| `outboxPollEvery` | 2s | Poll interval, cân bằng giữa latency và DB load |

**Pipeline:**

```
[Ticker 2s] → [Poll DB: claim tối đa 50 records] → [Jobs channel] → [Worker 1]
                                                                  → [Worker 2]
                                                                  → [Worker 3]
```

Mỗi worker:
1. Nhận record từ channel (non-blocking với select)
2. Gọi `publisher.Publish(ctx, eventType, payload)` 
3. Nếu thành công: gọi `store.MarkPublished(ctx, id)`
4. Nếu thất bại: log warning, không MarkPublished → retry sau 30s

**Tại sao 3 workers?**

- CPU-bound task: bcrypt, JSON parsing — 3 workers cho phép parallelism
- I/O-bound task: RabbitMQ publish — 3 workers cho phép pipelining
- Không quá nhiều để tránh DB connection pool exhaustion

### 2.8. Đánh Giá Outbox Pattern

| Tiêu chí | Đánh giá |
|---|---|
| Data consistency | ✅ Đảm bảo — event luôn được ghi cùng transaction với dữ liệu |
| Fault tolerance | ✅ Cao — coordinator crash không mất dữ liệu |
| Duplicate handling | ⚠️ Cần consumer idempotency |
| Latency | ⚠️ Poll interval 2s + publish time ~200ms → max ~2.2s delay |
| DB load | 🟢 Thấp — mỗi 2s query một lần |
| Scalability | ✅ Multiple coordinator instances nhờ SKIP LOCKED |

---

## Deep Dive 3: Cursor-based Pagination vs OFFSET

### 3.1. Vấn Đề với OFFSET Pagination

OFFSET-based pagination là cách đơn giản nhất:

```sql
SELECT * FROM urls WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20 OFFSET 0;  -- Trang 1
SELECT * FROM urls WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20 OFFSET 20; -- Trang 2
SELECT * FROM urls WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20 OFFSET 40; -- Trang 3
```

**Vấn đề 1: O(n) Performance**

Mỗi lần gọi OFFSET, database phải:
1. Scan qua tất cả các hàng từ đầu
2. Skip OFFSET hàng
3. Trả về LIMIT hàng

Công thức: `T(offset) = O(offset + limit)`

Với OFFSET = 10000, LIMIT = 20, database phải scan 10020 hàng chỉ để trả về 20. Khi bảng có hàng triệu URLs, query trang cuối cùng có thể mất hàng giây.

**Vấn đề 2: Không nhất quán (Phantom Reads)**

Khi user đang xem danh sách URLs:
- Trang 1: trả về 20 URLs (id=100..81)
- User xóa URL id=85
- Trang 2: OFFSET 20 bây giờ trả về `id=80..61` (vì đã xóa một hàng, offset bị lệch)
- User thấy URL id=80 ở cả hai trang → duplicate

Hoặc ngược lại:
- Admin thêm URL mới (id=101)
- Trang 2: OFFSET 20 bây giờ trả về `id=81..62` thay vì `id=80..61`
- User bỏ lỡ URL id=81 → missing data

**Vấn đề 3: Không hiệu quả với soft delete**

Khi sử dụng `is_active` flag thay vì xóa vật lý, OFFSET càng trở nên không chính xác.

### 3.2. Cursor-based Pagination trong URL Shortener

Hệ thống sử dụng cursor-based pagination với keyset pagination dựa trên UUID:

**Store Layer** (`services/url-service/store.go`):

```go
func (s *pgxURLStore) FindByUserID(ctx context.Context, userID string, afterID string, limit int) ([]URLRecord, error) {
    fetchLimit := limit + 1

    if afterID != "" {
        query = `SELECT id, short_code, original_url, user_id, user_email, created_at, expires_at, is_active 
                 FROM urls 
                 WHERE user_id = $1 AND id < $2 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW())
                 ORDER BY id DESC LIMIT $3`
        args = []any{userID, afterID, fetchLimit}
    } else {
        query = `SELECT id, short_code, original_url, user_id, user_email, created_at, expires_at, is_active 
                 FROM urls 
                 WHERE user_id = $1 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW())
                 ORDER BY id DESC LIMIT $2`
        args = []any{userID, fetchLimit}
    }
    // ...
}
```

**Service Layer** (`services/url-service/service.go`):

```go
func (s *URLService) GetUserUrls(ctx context.Context, userID, afterID string, limit int) (*ListURLsResponse, *HTTPError) {
    urls, err := s.store.FindByUserID(ctx, userID, afterID, limit)

    var nextCursor string
    hasMore := len(urls) > limit

    if hasMore {
        urls = urls[:limit]
        nextCursor = urls[len(urls)-1].ID
    }

    return &ListURLsResponse{
        URLs:       urls,
        NextCursor: nextCursor,
        HasMore:    hasMore,
    }, nil
}
```

**Handler Layer** (`services/url-service/handler.go`):

```go
func (h *HTTPHandler) HandleGetUrls(w http.ResponseWriter, r *http.Request) {
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
    // ...
}
```

### 3.3. Phân Tích Keyset Pagination với UUID

Query sử dụng `WHERE user_id = $1 AND id < $2 ... ORDER BY id DESC`.

**Tại sao là `id < $2` thay vì `created_at < $last_created_at`?**

1. **UUID v1/v7 có thể sắp xếp theo thời gian:** UUID v1 chứa timestamp, UUID v7 (proposed) cũng vậy. Tuy nhiên, codebase dùng `github.com/google/uuid` — mặc định tạo UUID v4 (random), không sắp xếp được.

2. **Không đồng nhất về thời gian:** Nếu dùng `created_at`, hai URLs có thể có cùng created_at (nếu tạo trong cùng microsecond), dẫn đến cursor ambiguity.

3. **ID là UNIQUE + NOT NULL:** Đảm bảo mỗi cursor trỏ đến duy nhất một hàng.

**Tuy nhiên, có một vấn đề:**

UUID v4 là **random**, không đảm bảo thứ tự tăng dần theo thời gian:
```
id = "a1b2c3d4-..." (UUID v4, random)
id = "e5f6g7h8-..." (UUID v4, random)

So sánh chuỗi: "a1b2..." < "e5f6..." → OK
Nhưng "f1b2..." < "a1b2..." → CÓ THỂ XẢY RA
```

Điều này có nghĩa là:
- `ORDER BY id DESC` không đảm bảo thứ tự thời gian tạo
- Khi user xem "previous page" (page cũ hơn), cursor có thể bỏ lỡ URLs được tạo sau nhưng có ID "nhỏ hơn"

**Giải pháp:** Nên dùng `created_at DESC, id DESC` làm composite key cho cursor:

```sql
WHERE user_id = $1 
  AND (created_at, id) < ($2, $3) 
ORDER BY created_at DESC, id DESC
```

### 3.4. Performance Analysis

**Query Plan — OFFSET với user có 100,000 URLs:**

```sql
-- Trang 5000 (OFFSET 99980, LIMIT 20)
EXPLAIN ANALYZE
SELECT * FROM urls 
WHERE user_id = 'user-123' AND is_active = true 
ORDER BY id DESC 
LIMIT 20 OFFSET 99980;

-- Kết quả (ước lượng):
-- Sort: 100,000 rows
-- Limit: skip 99980 rows
-- Total: ~850ms
```

**Query Plan — Cursor với user có 100,000 URLs:**

```sql
-- Trang cuối (after = id của hàng thứ 99980)
EXPLAIN ANALYZE
SELECT * FROM urls 
WHERE user_id = 'user-123' AND id < 'last-uuid' AND is_active = true 
ORDER BY id DESC 
LIMIT 21;

-- Kết quả (ước lượng với index phù hợp):
-- Index Scan Backward: chỉ 21 rows
-- Total: ~0.5ms
```

**So sánh chi tiết:**

| Metric | OFFSET (trang 5000) | Cursor (trang 5000) |
|---|---|---|
| Rows scanned | 100,000 | 21 |
| Time | ~850ms | ~0.5ms |
| Memory | ~10MB (sort buffer) | ~16KB |
| Index usage | Index scan + sort | Index seek |
| Time complexity | O(n) | O(1) với index |
| Consistency | ❌ Phantom reads | ✅ Snapshot tại cursor |

### 3.5. Consistency Advantages

Cursor-based pagination vượt trội về consistency:

**Kịch bản: User xem danh sách URLs trong khi người dùng khác xóa URLs**

```
Thời điểm T0: Database có URLs id=100, 99, 98, ..., 1
User A request trang 1 → after="" → trả về [100, 99, ..., 81], nextCursor="81"

Thời điểm T1: User B xóa URLs 90 và 85

Thời điểm T2: User A request trang 2 → after="81"
Query với OFFSET: OFFSET 20 → [80, 79, ..., 61] (bỏ lỡ 2 URLs → thấy 2 URLs lạ)
Query với Cursor: id < "81" → [80, 79, ..., 61] (đúng 20 URLs, không skip)

Với OFFSET, User A thấy:
- Trang 1: [100, 99, 98, ..., 82, 81] (thiếu 90, 85)
- Trang 2: [80, 79, ..., 61] (vẫn đúng 20, nhưng 90 và 85 bị mất)

Nếu thêm URLs mới:
Với OFFSET:
- Trang 1: [101, 100, ..., 82] (URL 101 mới, OFFSET bị lệch)
- Trang 2: [81, 80, ..., 62] (trùng với trang 1?)

Với Cursor:
- Trang 1: [100, 99, ..., 81] (không đổi)
- Trang 2: [80, 79, ..., 61] (không đổi)
- URL 101 sẽ xuất hiện ở lần refresh đầu tiên (khi không có after)
```

### 3.6. Implementation Pattern — "Fetch Limit + 1" Technique

Code sử dụng `fetchLimit := limit + 1` để xác định còn trang sau hay không:

```go
fetchLimit := limit + 1
// ... query với fetchLimit thay vì limit ...
// Sau đó:
if len(results) > limit {
    hasMore = true
    nextCursor = results[limit-1].ID // ID của hàng cuối cùng trong limit
    results = results[:limit]         // Cắt bỏ hàng thừa
}
```

**Phân tích kỹ thuật:**

1. **Tại sao fetch thêm 1 hàng?** Thay vì chạy query count riêng (tốn thêm DB roundtrip), kỹ thuật này tận dụng query chính để xác định "có còn trang sau không".

2. **Chi phí:** Fetch thêm 1 hàng là không đáng kể (chỉ thêm vài bytes dữ liệu), đặc biệt khi so với một query COUNT riêng có thể tốn O(n).

3. **Độ chính xác:** Nếu số hàng > limit (tức là có ít nhất limit+1 hàng), chắc chắn có trang sau.

4. **Edge case:** Khi số hàng đúng bằng limit (không fetch thêm), `hasMore = false`, trang hiện tại là trang cuối. Khi số hàng < limit (ít hơn cả limit), cũng là trang cuối.

### 3.7. Design Decisions

| Quyết định | Lý do | Hệ quả |
|---|---|---|
| UUID làm cursor | UNIQUE, NOT NULL, luôn có sẵn | Không sắp xếp theo thời gian vì UUID v4 |
| `DESC` order | URLs mới nhất hiển thị trước | Cursor là ID của hàng cuối, dùng `id < cursor` |
| Fetch limit+1 | Tránh COUNT query | Thêm 1 hàng overhead, không đáng kể |
| Limit range [1, 100] | Kiểm soát resource usage | Default 20, query parameter `?limit=` |
| `after` query param | REST convention | `GET /urls?after=uuid&limit=20` |
| Soft delete filter | `is_active = true` | User không thấy URLs đã deactivate |
| Expiry filter | `expires_at IS NULL OR expires_at > NOW()` | Không hiển thị URLs hết hạn |

### 3.8. Notification Service — Cursor Pattern Mở Rộng

Notification service (`services/notification-service/`) sử dụng cursor pattern tương tự nhưng với nullable cursor:

```go
func nullableCursor(cursor string) *string {
    if cursor == "" {
        return nil
    }
    return &cursor
}
```

Điều này cho phép phân biệt "không có cursor" (trang đầu) với "cursor rỗng" (không hợp lệ). Response dùng `*string` để JSON serialization trả về `null` thay vì `""`:

```json
// Trang cuối
{
  "notifications": [...],
  "next_cursor": null
}
// Còn trang
{
  "notifications": [...],
  "next_cursor": "some-uuid"
}
```

### 3.9. Hạn Chế của Cursor-based Pagination

1. **Không random access:** User không thể nhảy đến trang 5. Chỉ có "next page" và "previous page".

2. **Không biết tổng số trang:** Không thể hiển thị "Page 3 of 20". Cần query COUNT riêng nếu muốn.

3. **Phụ thuộc vào index:** Hiệu quả khi có composite index trên `(user_id, id)`. Nếu không có index, cursor không tốt hơn OFFSET.

4. **Không thể sort tùy ý:** Chỉ sort theo một hoặc vài cột cố định. Nếu user muốn sort theo `created_at ASC` và `created_at DESC` trên cùng trang, phải implement hai cursor khác nhau.

### 3.10. Kết Luận

Cursor-based pagination là lựa chọn đúng đắn cho URL Shortener:
- **Phù hợp với use case:** User xem danh sách URLs của họ theo trình tự thời gian ("infinite scroll")
- **Hiệu năng vượt trội:** O(1) vs O(n) với OFFSET
- **Consistency:** Không bị ảnh hưởng bởi concurrent writes

Tuy nhiên, cần cân nhắc dùng UUID v7 (time-ordered) thay vì UUID v4 để đảm bảo thứ tự sắp xếp chính xác.

---

## Deep Dive 4: Circuit Breaker + Rate Limiter Interaction

### 4.1. Tổng Quan về Hai Pattern

**Rate Limiter (RL):** Bảo vệ backend khỏi quá tải request từ client. Hoạt động ở tầng gateway. Giới hạn số request/IP/thời gian.

**Circuit Breaker (CB):** Bảo vệ hệ thống khỏi cascading failures. Ngừng gửi request đến upstream đang lỗi, cho phép nó "hồi phục".

Cả hai đều nằm trong gateway (`/gateway/`):

```
Client → Rate Limiter → Circuit Breaker → Proxy → Upstream Service
                                      ↓
                               Prometheus Metrics
```

### 4.2. Rate Limiter Implementation

**File:** `gateway/ratelimit.go`

```go
type RateLimiter struct {
    client *redis.Client
}

func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, windowSecs int) (bool, int, error) {
    ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
    defer cancel()

    count, err := rl.client.Incr(ctx, fullKey).Result()
    if count == 1 {
        rl.client.Expire(ctx, fullKey, time.Duration(windowSecs)*time.Second)
    }

    if count > int64(limit) {
        ttl, err := rl.client.TTL(ctx, fullKey).Result()
        return false, int(ttl.Seconds()), nil
    }
    return true, 0, nil
}
```

**Cơ chế:** Sliding window đơn giản dùng Redis INCR + TTL.

- Mỗi key redis `rl:{route_key}:{ip}` có TTL = window
- INCR tăng counter, TTL tự động expire sau window
- Nếu counter > limit, reject request với thời gian còn lại (Retry-After)

**Cấu hình (từ handler):**
- Mỗi route có `RateLimit` và `RateLimitWindow` config riêng

**Context timeout 100ms:** Tránh goroutine leak khi Redis chậm hoặc down. Nếu Redis không phản hồi trong 100ms, rate limiter **allow by default** (fail open).

### 4.3. Circuit Breaker Implementation

**File:** `gateway/circuitbreaker.go`

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

**State Machine:**

```
     ┌──────────────────────────────┐
     │                              │
     ▼    failures ≥ maxFailures    │
  ┌──────┐    trong window       ┌──────┐
  │CLOSED│──────────────────────►│ OPEN │
  └──────┘                       └──────┘
     ▲                               │
     │    success (probe)            │ recovery timeout expired
     │    ┌──────────┐               │
     └────┤HALF_OPEN │◄──────────────┘
          └──────────┘
               │
               │ failure (probe)
               ▼
            ┌──────┐
            │ OPEN │
            └──────┘
```

**Chi tiết state transitions:**

**CLOSED → OPEN:**
```
1. upstream() return error
2. Kiểm tra failure window (reset failures nếu window expired)
3. failures++
4. Nếu failures >= maxFailures → chuyển sang OPEN
5. Ghi nhận lastFailureTime = now
```

**OPEN → HALF_OPEN:**
```
1. Do() được gọi, state == OPEN
2. Kiểm tra time.Since(lastFailureTime) > openTimeout
3. Nếu hết timeout → chuyển sang HALF_OPEN
4. Set halfOpenProbe = true (chặn request khác khi đang probe)
```

**HALF_OPEN → CLOSED:**
```
1. upstream() return nil (success)
2. Chuyển sang CLOSED
3. Reset failures = 0, windowStart = now
```

**HALF_OPEN → OPEN:**
```
1. upstream() return error
2. Chuyển sang OPEN
3. Ghi nhận lastFailureTime = now
```

**Cơ chế half-open probe lock:**

```go
case StateHalfOpen:
    if cb.halfOpenProbe {
        cb.mu.Unlock()
        return ErrCircuitOpen  // Chỉ cho phép 1 request probe
    }
    cb.halfOpenProbe = true
```

Điều này đảm bảo chỉ một request duy nhất được gửi khi circuit ở trạng thái HALF_OPEN. Nếu có 50 concurrent requests, 49 sẽ nhận `ErrCircuitOpen` ngay lập tức.

### 4.4. Cách RL và CB Hoạt Động Cùng Nhau

**Luồng request hoàn chỉnh:**

```
1. Request đến gateway
2. CORS middleware
3. Logger middleware
4. JWT middleware (nếu cần)
5. Rate Limiter:
   a. Parse client IP (X-Forwarded-For, X-Real-IP, RemoteAddr)
   b. Build Redis key = "rl:{route}:{ip}"
   c. INCR key, check limit
   d. Nếu exceeded → return 429 Too Many Requests (Retry-After header)
6. Circuit Breaker:
   a. Kiểm tra state
   b. OPEN → return 503 (nếu không trong recovery window)
   c. HALF_OPEN + probing → block request khác
   d. Gọi upstream proxy
   e. Nếu lỗi → track failure, có thể mở circuit
   f. Nếu thành công (HALF_OPEN) → close circuit
7. Proxy gửi request đến upstream service
8. Trả response về client
```

### 4.5. Failure Scenarios Phân Tích Chi Tiết

**Kịch bản 1: Redis Down**

```
Redis crash → Rate Limiter gọi INCR → timeout 100ms → "fail open" (allow request)
Redis crash → CB không dùng Redis (in-memory) → không bị ảnh hưởng

Hậu quả:
- Rate limiter không hoạt động (tất cả request được allow)
- CB vẫn hoạt động bình thường
- Upstream services có thể bị overload
```

**Mitigation:**
- Rate limiter fail open → cho phép request nhưng mất protection
- Cần Redis HA (sentinel/cluster)
- Có thể thêm local rate limiter (in-memory) làm fallback

**Kịch bản 2: Upstream timeout**

```
Client gửi request → RL: allow → CB: CLOSED → Proxy gửi đến user-service
User-service đang quá tải → response timeout 30s
Proxy thấy lỗi → CB ghi nhận failure

Sau 5 lần failure (maxFailures = 5):
CB chuyển sang OPEN → các request tiếp theo nhận 503 ngay lập tức
Rate limiter vẫn cho phép request (vì chỉ check IP, không biết CB state)

Vấn đề: Client vẫn tốn công gửi request, nhận 503.
Rate limiter đếm request này là "used" → client có thể hết quota.
```

**Mitigation tốt hơn:**
- Rate limiter nên biết CB state (hoặc ngược lại)
- Khi CB OPEN, rate limiter nên giảm limit hoặc từ chối request sớm hơn
- Hoặc gateway trả về 503 Retry-After để client backoff

**Kịch bản 3: Cascading failure — Notification Service chậm**

```
Notification service chậm do database lock
→ Các request đến notification service timeout
→ CB mở circuit cho notification-service

Tuy nhiên, gateway vẫn gửi request đến user-service và url-service (bình thường)
User-service vẫn hoạt động → authentication vẫn OK
URL-service vẫn hoạt động → short URLs vẫn redirect được

Vấn đề: Chỉ notification service bị ảnh hưởng. CB localizes failure.
```

Đây là use case **hoàn hảo** cho CB: ngăn một service chậm ảnh hưởng đến toàn hệ thống.

**Kịch bản 4: DDoS + CB Open**

```
Attacker gửi 10,000 rps đến url-service
RL bắt đầu từ chối request sau limit (ví dụ 100 rps/IP)
Tuy nhiên, attacker dùng 1000 IP khác nhau → mỗi IP được 10 rps → pass RL

url-service bắt đầu quá tải → response 500:
- CB ghi nhận failure
- maxFailures = 5 → sau 5 failures, CB mở circuit
- Tất cả request đến url-service đều 503 ngay lập tức
- url-service có thời gian recovery
- Sau openTimeout, CB cho 1 request probe
- Nếu thành công → close circuit, request đến url-service lại
- Nếu thất bại → OPEN lại, đợi thêm openTimeout
```

**Vấn đề với tham số mặc định:**
Nếu `maxFailures = 5`, chỉ sau 5 failures, CB mở circuit. Với 1000 IP, điều này xảy ra gần như ngay lập tức. Tuy nhiên, nếu `failureWindow = 60s`, 5 failures trong 60s có thể xảy ra do transient error, không phải DDoS.

### 4.6. State Machine Analysis với Failure Injection

**Scenario: Injection lỗi dần dần**

```
Time  T0: Khởi tạo CB: state=CLOSED, failures=0
Time  T1: upstream lỗi (500) → failures=1, state=CLOSED
Time  T2: upstream lỗi (500) → failures=2, state=CLOSED
Time  T3: upstream lỗi (500) → failures=3, state=CLOSED
Time  T4: upstream lỗi (500) → failures=4, state=CLOSED
Time  T5: upstream lỗi (500) → failures=5 ≥ maxFailures → state=OPEN, lastFailureTime=T5
Time  T6: Request đến → state=OPEN, time.Since(T5) < openTimeout → return ErrCircuitOpen
Time T15: Request đến → state=OPEN, time.Since(T5) = 10s > openTimeout → state=HALF_OPEN, probe=1
         upstream success → state=CLOSED, failures=0, windowStart=T15
Time T16: upstream lỗi (500) → failures=1, state=CLOSED
Time T17: upstream lỗi (500) → failures=2, state=CLOSED
... lại tiếp tục cycle
```

**Scenario: Transient error trong window**

```
failureWindow = 60s
T0: failures=0, windowStart=T0
T1: lỗi → failures=1
T2: lỗi → failures=2
T3: lỗi → failures=3
T4: lỗi → failures=4
T5: lỗi → failures=5 → OPEN

Giả sử T1..T5 trong vòng 10s, T5 vẫn trong failureWindow của T0 → OK

Giả sử T1..T4 trong 10s, T5 ở T+70s (sau 60s):
Khi T5 xảy ra: time.Since(windowStart) = 70s > 60s → reset failures=0, windowStart=T5
→ failures=1 → không mở circuit
```

**Scenario: Half-open probe success → recovery**

```
T0: OPEN (sau 5 failures)
T10: HALF_OPEN (sau 10s openTimeout)
     Request A: probe=true, gửi đến upstream → success
     Request B (concurrent): state=HALF_OPEN, halfOpenProbe=true → return ErrCircuitOpen
     → state=CLOSED, failures=0
```

**Scenario: Half-open probe failure → immediate reopen**

```
T0: OPEN
T10: HALF_OPEN
     Request probe → upstream 500
     → state=OPEN, lastFailureTime=T10
     Lưu ý: không cần đợi maxFailures lần nữa — chỉ cần 1 failure trong HALF_OPEN
```

### 4.7. Monitoring Integration (Prometheus Metrics)

Gateway tích hợp Prometheus metrics:

```go
mux.Handle("GET /metrics", promhttp.Handler())
```

Circuit breaker ghi metrics qua callback:

```go
cb := NewCircuitBreaker(...).WithStateChange(func(from, to State) {
    recordCBState("url-service", to)
    if to == StateOpen {
        recordCBTrip("url-service")
    }
})
```

**Metrics nên có:**

| Metric | Type | Labels | Mục đích |
|---|---|---|---|
| `gateway_cb_state` | Gauge | service, state | 1=CLOSED, 2=OPEN, 3=HALF_OPEN |
| `gateway_cb_trips_total` | Counter | service | Số lần circuit mở |
| `gateway_cb_requests_total` | Counter | service, result (allowed/rejected) | Số request/result |
| `gateway_rl_requests_total` | Counter | route, result (allowed/throttled) | Số request/result |
| `gateway_rl_redis_errors_total` | Counter | — | Số lỗi Redis |
| `gateway_upstream_latency` | Histogram | service | Latency upstream |

**Alerting rules:**

```yaml
# PrometheusRule
groups:
  - name: gateway
    rules:
      - alert: CircuitBreakerOpen
        expr: gateway_cb_state{state="open"} == 1
        for: 5m
        labels: { severity: critical }
        annotations:
          summary: "Circuit breaker OPEN for {{ $labels.service }}"

      - alert: HighRateLimitThrottle
        expr: rate(gateway_rl_requests_total{result="throttled"}[5m]) > 100
        labels: { severity: warning }
        annotations:
          summary: "High rate limiting for route {{ $labels.route }}"

      - alert: CircuitBreakerFlapping
        expr: rate(gateway_cb_trips_total[15m]) > 5
        labels: { severity: warning }
        annotations:
          summary: "Circuit breaker flapping for {{ $labels.service }}"
```

### 4.8. Tương Tác Phức Tạp — Race Conditions

**Kịch bản: CB vừa chuyển sang HALF_OPEN, nhiều request đến cùng lúc**

```
Goroutine A (request 1):     Goroutine B (request 2):     Goroutine C (request 3):
cb.Do()                      cb.Do()                      cb.Do()
  lock()                       lock()                       lock()
  state=OPEN → HALF_OPEN       state=HALF_OPEN              state=HALF_OPEN
  halfOpenProbe=true           halfOpenProbe=true           halfOpenProbe=true
  unlock()                     unlock()                     unlock()
  (A là request đầu)           halfOpenProbe=true           halfOpenProbe=true
                               → return ErrCircuitOpen      → return ErrCircuitOpen
  upstream() ← success
  lock()
  state=HALF_OPEN → CLOSED
  halfOpenProbe=false
  failures=0
  unlock()
```

Chỉ một request (A) thực sự được gửi đến upstream. Các request khác (B, C) bị từ chối ngay lập tức. Đây là behavior đúng đắn — tránh làm quá tải upstream khi đang phục hồi.

### 4.9. Tuning Parameters

| Tham số | Giá trị mặc định | Khuyến nghị | Cơ sở |
|---|---|---|---|
| `maxFailures` | 5 | 5-10 | Đủ để tránh transient errors, đủ ít để phản ứng nhanh |
| `openTimeout` | configurable (giây) | 30-60s | Thời gian để upstream recovery |
| `failureWindow` | configurable (giây) | 60-120s | Window để đếm failures |
| RL `limit` | configurable | 50-200 req/IP/phút | Tùy thuộc route |
| RL `timeout` | 100ms | 100ms | Fail open nếu Redis chậm |

### 4.10. Kết Luận

Circuit breaker + rate limiter là hai pattern bảo vệ hệ thống ở hai tầng khác nhau:
- Rate limiter: bảo vệ **trước** khi request đến upstream (tầng client)
- Circuit breaker: bảo vệ **sau** khi upstream có vấn đề (tầng service)

Kết hợp cả hai tạo thành defense-in-depth:
1. RL ngăn client lạm dụng
2. CB ngăn failure lan rộng
3. Prometheus metrics cho phép monitoring và alerting
4. half-open probe cho phép recovery tự động

---

## Deep Dive 5: Graceful Shutdown Analysis

### 5.1. Tổng Quan về Graceful Shutdown

Graceful shutdown là quá trình tắt ứng dụng một cách "có trật tự":
1. Ngừng nhận request mới
2. Xử lý các request đang pending
3. Đóng các kết nối (database, message queue, cache)
4. Flush dữ liệu còn tồn đọng
5. Exit với code 0

Nếu không có graceful shutdown:
- Request đang xử lý bị terminate giữa chừng → dữ liệu không nhất quán
- Database connections bị đột ngột đóng → connection pool bên DB bị rò rỉ
- RabbitMQ messages bị mất (nếu chưa được acknowledge)
- Outbox records bị bỏ dở

### 5.2. Signal Handling

**URL Service** (`services/url-service/main.go`):

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

// ...

<-quit           // Block until signal received
cancel()          // Cancel context → outbox coordinator dừng
log.Info("shutdown signal received, draining connections…")

shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
defer shutdownCancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    log.Error("graceful shutdown failed", "error", err)
}
```

**Phân tích signal handling:**

- `signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)`: Bắt cả SIGTERM (kill mặc định, systemd) và SIGINT (Ctrl+C).
- **Channel buffer = 1:** Nếu có nhiều signals, chỉ giữ lại một. Các signal sau bị mất.
- Sau khi nhận signal:
  1. Gọi `cancel()` — cancel context gốc
  2. Gọi `srv.Shutdown(shutdownCtx)` với timeout 10s

**Gateway** (`gateway/main.go`):

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
<-quit

log.Info("shutting down gateway")
srv.Shutdown(context.Background()) // Không có timeout
```

**Sự khác biệt quan trọng:**
- Gateway: `srv.Shutdown(context.Background())` — **không timeout**, chờ tất cả request hoàn tất
- URL Service: `srv.Shutdown(shutdownCtx)` với **10s timeout** — force close nếu quá lâu

### 5.3. Shutdown Sequence — Phân Tích Chi Tiết

**URL Service shutdown sequence:**

```
Signal (SIGTERM/SIGINT)
    │
    ├── 1. cancel() → OutboxCoordinator context cancelled
    │       │
    │       ├── outboxCoordinator.Run():
    │       │   ├── Ticker dừng
    │       │   ├── Poll hiện tại → ctx.Done() → return
    │       │   ├── Workers: select ctx.Done() → return
    │       │   └── defer: close(jobs), workers.Wait()
    │       │       └── Workers nhận jobs channel close → return
    │       │
    │       └── Worker publish hiện tại:
    │           ├── Nếu đang publish → context cancelled (KHÔNG, context.Background)
    │           └── Nếu đang MarkPublished → context.Background → vẫn hoàn tất
    │
    ├── 2. srv.Shutdown(shutdownCtx):
    │       ├── HTTP server: ngừng nhận request mới
    │       ├── Chờ request đang xử lý hoàn tất (IdleTimeout tracking)
    │       ├── Close idle connections
    │       └── Nếu > 10s → force close (shutdownCtx timeout)
    │
    ├── 3. defer pool.Close() (database pool):
    │       ├── Chờ transaction đang chạy
    │       └── Đóng tất cả connections
    │
    ├── 4. defer redisClient.Close():
    │       └── Đóng Redis connection
    │
    ├── 5. defer rmqConn.Close():
    │       ├── Close RabbitMQ connection
    │       └── Tất cả channels bị đóng
    │
    └── 6. main() kết thúc → os.Exit(0)
```

### 5.4. Outbox Coordinator Shutdown

```go
func (c *OutboxCoordinator) Run(ctx context.Context) {
    // ...
    defer func() {
        close(jobs)
        workers.Wait()
    }()

    for {
        c.poll(ctx, jobs)
        select {
        case <-ctx.Done():
            return    // Signal received → thoát vòng lặp
        case <-ticker.C:
        }
    }
}
```

**Vấn đề tiềm ẩn:** Khi `cancel()` được gọi:
1. `poll()` đang chạy — nếu `store.FetchUnpublished` đang query, nó sẽ:
   - Query thành công (không context check ở store layer)
   - Gửi records vào `jobs` channel
   - Worker nhận record và bắt đầu publish
   - Worker kiểm tra `ctx.Done()` khi publish xong → thoát
   - Record đã publish nhưng chưa MarkPublished → mất event? KHÔNG, vì MarkPublished dùng context.Background? KHÔNG, nó dùng ctx được truyền vào:

```go
func (c *OutboxCoordinator) publish(ctx context.Context, workerID int, record *OutboxRecord) {
    if err := c.publisher.Publish(ctx, record.EventType, record.Payload); err != nil {
        // Publish context đã cancelled → lỗi
        return
    }
    if err := c.store.MarkPublished(ctx, record.ID); err != nil {
        // MarkPublished context đã cancelled → lỗi
    }
}
```

**Phân tích:** Khi `cancel()` được gọi:
1. Ticker goroutine thấy `ctx.Done()` → return
2. Worker goroutine thấy `ctx.Done()` trong select → return
3. Tuy nhiên, `poll()` vẫn chạy xong query và gửi jobs trước khi select kiểm tra ctx

**Edge case chi tiết:**

```
Lần lặp cuối:
1. c.poll(ctx, jobs) chạy → FetchUnpublished query (mất 5ms)
   → Trong 5ms này, cancel() được gọi
   → poll() vẫn chạy xong query (context không được kiểm tra trong query)
   → poll() gửi 3 records vào jobs channel
2. select kiểm tra ctx.Done() → return
3. defer close(jobs), workers.Wait()
4. Worker còn 3 records trong jobs channel → xử lý
5. Worker publish record 1 → publish thành công
6. Worker MarkPublished record 1 → context.Background? KHÔNG, ctx đã cancelled
   → MarkPublished thất bại
   → record 1 published nhưng chưa mark → duplicate sau restart
```

**Giải pháp:** MarkPublished nên dùng `context.Background()` hoặc context riêng với timeout, không dùng ctx đã cancel:

```go
func (c *OutboxCoordinator) publish(ctx context.Context, workerID int, record *OutboxRecord) {
    if err := c.publisher.Publish(ctx, record.EventType, record.Payload); err != nil {
        return
    }
    // Dùng background context để đảm bảo MarkPublished luôn thành công
    markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := c.store.MarkPublished(markCtx, record.ID); err != nil {
        // ...
    }
}
```

### 5.5. HTTP Server Shutdown

`srv.Shutdown(ctx)` hoạt động như sau:
1. Đặt server vào trạng thái "closing" — không nhận request mới trên listener
2. `Listener.Close()` — ngừng accept connections
3. Chờ tất cả active connections hoàn tất
4. Close idle connections ngay lập tức

**Quan trọng:** `http.Server.Shutdown` chỉ chờ **in-flight requests** (request đã được ServeHTTP xử lý). Các connections đang trong quá trình TLS handshake hoặc mới connect chưa gửi request sẽ bị đóng ngay lập tức.

**URL Service — Read/Write/Idle Timeouts:**

```go
srv := &http.Server{
    Addr:         ":" + cfg.Port,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  60 * time.Second,
}
```

- **ReadTimeout:** Thời gian tối đa để đọc request body (bao gồm headers) — 10s
- **WriteTimeout:** Thời gian tối đa để ghi response — 10s
- **IdleTimeout:** Thời gian tối đa connection idle (keep-alive) — 60s

Các timeout này giúp:
1. Slow loris attack: ReadTimeout ngăn attacker gửi headers từ từ
2. Slow response: WriteTimeout đảm bảo không có response nào kéo dài >10s
3. Connection leak: IdleTimeout đóng connections không hoạt động

**Tác động đến Shutdown:**

- Request đang xử lý được timeout bởi WriteTimeout (10s)
- Shutdown timeout cũng 10s → nếu request mất 10s, shutdown timeout và WriteTimeout cùng lúc
- Nếu WriteTimeout > ShutdownTimeout, request có thể bị force close khi chưa hoàn tất

### 5.6. Gateway vs URL Service — So Sánh Shutdown

| Khía cạnh | Gateway | URL Service | Analytics Service |
|---|---|---|---|
| HTTP Shutdown timeout | `context.Background()` (vô hạn) | 10s | 10s |
| Outbox coordinator | Không có | ✅ Dừng qua context cancel | Không có (consumer-based) |
| RabbitMQ consumer | Không có | Không có | ✅ Được dừng qua context |
| DB pool close | Không có DB | `defer pool.Close()` | `defer pool.Close()` |
| Redis close | `defer limiter.Close()` | `defer redisClient.Close()` | Không có |
| Signal handlers | SIGTERM, SIGINT | SIGTERM, SIGINT | SIGTERM, SIGINT |
| Logger | slog | slog | slog |

**Gateway không có shutdown timeout** — điều này có thể dẫn đến:
- Nếu có request kéo dài vô hạn (WebSocket, streaming), server không bao giờ shutdown
- Cần timeout cứng để tránh trường hợp này

### 5.7. Timeout Handling Analysis

**context.Background() trong URL Service shutdown:**

```go
shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
```

- Dùng `context.Background()` làm parent — không bị ảnh hưởng bởi `cancel()` đã gọi
- Tự tạo timeout 10s riêng

**context.Background() trong Gateway shutdown:**

```go
srv.Shutdown(context.Background())
```

- Không timeout — chờ tất cả request hoàn tất
- Rủi ro: nếu có request treo (hang), gateway không bao giờ shutdown

**Phân tích context.Background() vs context khác:**

| Context | Behavior | Khi nào dùng |
|---|---|---|
| `context.Background()` | Không bao giờ cancelled, không timeout | Shutdown không timeout, background tasks |
| `context.TODO()` | Giống Background, signal "chưa quyết định" | Code chưa được refactor |
| `context.WithTimeout(parent, dur)` | Tự động cancel sau dur | Giới hạn thời gian chờ shutdown |
| context từ request | Bị cancel khi request kết thúc | Không dùng cho shutdown |

### 5.8. Deferred Operations — Thứ Tự và Rủi Ro

**URL Service defer order:**

```go
defer cancel()               // 1. (đầu tiên, chạy cuối cùng)
pool, err := NewDBPool(...)
defer pool.Close()           // 2. (chạy thứ 5)
redisClient, _ := NewRedisClient(...)
defer redisClient.Close()    // 3. (chạy thứ 4)
rmqConn, err := NewRabbitMQConn(...)
defer rmqConn.Close()        // 4. (chạy thứ 3)
```

Defer chạy theo LIFO (Last In, First Out):
1. `defer rmqConn.Close()` — chạy đầu tiên
2. `defer redisClient.Close()` — chạy thứ hai
3. `defer pool.Close()` — chạy thứ ba
4. `defer shutdownCancel()` — chạy thứ tư
5. `defer cancel()` — chạy cuối cùng

**Rủi ro:** Khi main() kết thúc, `cancel()` chạy cuối cùng, nhưng `srv.Shutdown()` đã chạy trước đó (trong luồng chính). Outbox coordinator goroutine có thể vẫn chạy khi pool.Close() được gọi (vì pool.Close là defer thứ 3, chạy sau rmqConn).

Thực tế trình tự đúng:
1. Nhận signal → cancel() context gốc
2. srv.Shutdown() → HTTP server dừng
3. main() kết thúc → defers chạy
4. defer cancel() — không làm gì vì đã cancel
5. defer shutdownCancel() — cancel shutdown context (đã timeout)
6. defer pool.Close() — đóng DB pool (có thể OutboxCoordinator vẫn chạy?)

Vấn đề: OutboxCoordinator là goroutine riêng. Khi main() kết thúc, goroutine vẫn chạy. pool.Close() đóng DB pool, nhưng coordinator goroutine có thể đang gọi FetchUnpublished/MarkPublished → panic vì pool đã đóng.

**Mitigation cần thêm:**
```go
// Trước khi main() kết thúc, chờ coordinator dừng hẳn
coordinatorWG.Wait()
```

### 5.9. What Happens if Shutdown Takes Too Long

**Scenario A: HTTP request kéo dài >10s**

```
Shutdown signal received
srv.Shutdown(shutdownCtx) bắt đầu (timeout 10s)
  Request A đang xử lý (shorten URL → bcrypt → DB insert → 2s)
  Request B đang xử lý (redirect → Redis cache miss → DB query → 3s)
  Request C vừa đến → bị từ chối (connection closed)

Sau 10s:
  shutdownCtx timeout → context deadline exceeded
  srv.Shutdown trả về error "context deadline exceeded"
  pool.Close() → đóng DB connections
  redisClient.Close() → đóng Redis
  rmqConn.Close() → đóng RabbitMQ
  main() kết thúc

Vẫn còn request đang xử lý (Request A, B)?
  srv.Shutdown chỉ chờ request hoàn tất. Nếu request hoàn tất trước timeout → OK.
  Nếu không → request bị force close (TCP RST đến client).
```

**Scenario B: Outbox coordinator đang publish**

```
Shutdown signal → cancel()
  Coordinator Run() thấy ctx.Done() → return
  Nhưng worker goroutine vẫn đang publish (Publish + MarkPublished)
  Publish dùng context.Background? Thực tế trong code hiện tại:
  
  func (c *OutboxCoordinator) publish(ctx context.Context, ...) {
      // Publish dùng ctx đã cancel → có thể thất bại
      if err := c.publisher.Publish(ctx, record.EventType, record.Payload); err != nil {
          return  // Bỏ qua record này!
      }
      // MarkPublished dùng ctx đã cancel → thất bại
      if err := c.store.MarkPublished(ctx, record.ID); err != nil {
          return  // Record published nhưng chưa marked!
      }
  }

Hậu quả:
  - Record đã publish lên RabbitMQ (consumer đã nhận)
  - Record chưa được MarkPublished
  - Sau restart, coordinator pick up record này → publish lại → duplicate
```

**Scenario C: Database transaction đang mở khi shutdown**

```
Shutdown signal → srv.Shutdown() chờ request hoàn tất
  Request A: POST /shorten đang trong transaction (pgx.BeginFunc)
  
  Nếu request A hoàn tất transaction trước khi pool.Close() → OK
  Nếu request A chưa kịp commit:
    - pool.Close() → đóng connection → transaction bị rollback
    - Rollback là tự động ở PostgreSQL → không mất dữ liệu
    - Client nhận HTTP 502 hoặc connection reset
```

**Scenario D: Gateway shutdown không timeout**

```
Gateway nhận SIGTERM
  srv.Shutdown(context.Background()) — KHÔNG TIMEOUT
  
  Request A kéo dài 30s (do upstream service chậm)
  Gateway chờ 30s...
  Request A hoàn tất → srv.Shutdown trả về
  main() kết thúc

  Nếu request A kéo dài vô hạn (bug, memory leak, deadlock):
    Gateway không bao giờ shutdown
    Cần kill -9 để force kill
    → KHÔNG graceful
```

### 5.10. Đề Xuất Cải Thiện

1. **Outbox Coordinator Graceful Shutdown:**
```go
func (c *OutboxCoordinator) Run(ctx context.Context) {
    // Dùng WaitGroup riêng
    var wg sync.WaitGroup
    // ...
    <-ctx.Done()
    close(jobs)
    wg.Wait() // Chờ workers publish xong
}

// Trong main():
coordinatorWG.Add(1)
go func() {
    defer coordinatorWG.Done()
    outboxCoordinator.Run(ctx)
}()

<-quit
cancel()
coordinatorWG.Wait() // Chờ coordinator dừng hẳn
log.Info("coordinator stopped")
srv.Shutdown(shutdownCtx)
```

2. **MarkPublished dùng context riêng:**
```go
func (c *OutboxCoordinator) publish(workerID int, record *OutboxRecord) {
    // Không nhận context — tự quản lý
    if err := c.publisher.Publish(record.EventType, record.Payload); err != nil {
        return
    }
    markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := c.store.MarkPublished(markCtx, record.ID); err != nil {
        c.log.Warn("outbox mark published failed", ...)
    }
}
```

3. **Gateway shutdown timeout:**
```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    log.Error("shutdown timeout", "error", err)
}
```

4. **Graceful period cho tất cả services:**
- Quy ước chung: 30s cho HTTP Shutdown, 30s cho background workers
- Monitoring: metrics về shutdown duration và failed graceful shutdowns
- Health check: trong quá trình shutdown, health endpoint trả về 503

### 5.11. Kết Luận

| Khía cạnh | Hiện tại | Cải thiện |
|---|---|---|
| Signal handling | ✅ SIGTERM + SIGINT | Buffer > 1? |
| HTTP server shutdown | ✅ Có timeout (URL) / ⚠️ Không timeout (Gateway) | Gateway cần timeout |
| Outbox coordinator | ⚠️ Không chờ workers hoàn tất | Cần WaitGroup |
| DB connections | ✅ defer pool.Close() | OK |
| Redis | ✅ defer redisClient.Close() | OK |
| RabbitMQ | ✅ defer rmqConn.Close() | OK |
| Context propagation | ⚠️ ctx sau cancel dùng cho publish/MarkPublished | Dùng context riêng |
| Idempotent shutdown | ✅ Multiple signals không gây lỗi | OK |

Graceful shutdown là một trong những khía cạnh dễ bị bỏ qua nhất trong thiết kế hệ thống. URL Shortener thực hiện graceful shutdown ở mức cơ bản, nhưng có thể cải thiện thêm để đảm bảo an toàn tuyệt đối cho outbox pattern và các background workers.

---

## Phụ Lục: Tổng Kết Các Pattern

| Pattern | File | Mục đích |
|---|---|---|
| Dummy bcrypt hash | `handler.go:12` | Chống timing attack |
| Cursor-based pagination | `store.go:59`, `service.go:268` | Phân trang hiệu quả |
| Outbox pattern | `outbox.go`, `outbox_store.go` | Đảm bảo event delivery |
| FOR UPDATE SKIP LOCKED | `outbox_store.go:54` | Ngăn duplicate processing |
| Circuit breaker | `circuitbreaker.go` | Chống cascading failure |
| Rate limiter (Redis) | `ratelimit.go` | Chống abuse |
| Graceful shutdown | `main.go` (các service) | Zero-downtime deployment |
| Transactional outbox | `service.go:97-136` | Dual write consistency |
| Worker pool | `outbox.go:32-38` | Parallel outbox processing |
| Fetch-limit+1 | `store.go:64`, `service.go:284-291` | Xác định has_more |
| Fail open (RL) | `ratelimit.go:40` | Graceful degradation |
| Half-open probe | `circuitbreaker.go:86-88` | Circuit recovery |
| Context timeout | `ratelimit.go:33`, `service.go:185` | Resource protection |
| Correlation ID | middleware | Request tracing |
| Prometheus metrics | Tất cả services | Monitoring |
| JWT middleware | auth middleware | Authentication |
| CORS middleware | gateway | Cross-origin support |

---

*Tài liệu này được tạo bởi agent khảo sát tự động, dựa trên phân tích mã nguồn của URL Shortener Microservices. Mọi đề xuất cải thiện đều mang tính tham khảo.*
