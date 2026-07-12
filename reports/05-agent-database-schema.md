# Phân Tích Chi Tiết: Database-per-Service Pattern, Schemas, và Indexes

> **Dự án:** URL Shortener Microservices  
> **Tác giả:** Agent phân tích  
> **Ngày:** 2026-07-11  
> **Phiên bản tài liệu:** 1.0  
> **Phạm vi:** 4 cơ sở dữ liệu, 7 bảng, 19 định nghĩa chỉ mục (16 chỉ mục hiệu dụng)

---

## Mục Lục

1. [Tổng Quan về Database-per-Service Pattern](#1-tổng-quan-về-database-per-service-pattern)
2. [Kiến Trúc 4 Cơ Sở Dữ Liệu](#2-kiến-trúc-4-cơ-sở-dữ-liệu)
3. [Phân Tích Schema Chi Tiết Từng Bảng](#3-phân-tích-schema-chi-tiết-từng-bảng)
    - 3.1. Bảng `urls` — URL Service
    - 3.2. Bảng `outbox` — URL Service (Transactional Outbox)
    - 3.3. Bảng `clicks` — Analytics Service
    - 3.4. Bảng `milestones` — Analytics Service
    - 3.5. Bảng `processed_events` — Analytics Service (Idempotency)
    - 3.6. Bảng `notifications` — Notification Service
    - 3.7. Bảng `users` — User Service
4. [Phân Tích Chi Tiết 19 Định Nghĩa Chỉ Mục](#4-phân-tích-chi-tiết-19-định-nghĩa-chỉ-mục)
    - 4.1. Primary Key Indexes (7 chỉ mục)
    - 4.2. Inline UNIQUE Constraint Indexes (3 chỉ mục)
    - 4.3. Explicit Named Indexes (9 chỉ mục)
    - 4.4. Bảng Tổng Hợp Tất Cả Chỉ Mục
    - 4.5. Chiến Lược Index Tổng Thể
5. [Chiến Lược Migration](#5-chiến-lược-migration)
    - 5.1. Cơ Chế Hoạt Động
    - 5.2. Phân Tích Mã Nguồn Migration
    - 5.3. Điểm Yếu Của Chiến Lược Hiện Tại
6. [CRUD Operations và Data Ownership Matrix](#6-crud-operations-và-data-ownership-matrix)
7. [Cross-Service Data Access Patterns](#7-cross-service-data-access-patterns)
8. [Docker Compose DB Configuration](#8-docker-compose-db-configuration)
9. [PostgreSQL Connection Pooling](#9-postgresql-connection-pooling)
10. [Đánh Giá Tổng Thể và Khuyến Nghị](#10-đánh-giá-tổng-thể-và-khuyến-nghị)

---

## 1. Tổng Quan về Database-per-Service Pattern

### 1.1. Định Nghĩa

Database-per-Service là một pattern kiến trúc trong hệ thống microservices, nơi mỗi service sở hữu riêng một cơ sở dữ liệu (CSDL) và hoàn toàn chịu trách nhiệm về dữ liệu của mình. Không service nào được phép truy cập trực tiếp vào CSDL của service khác. Mọi giao tiếp liên-service đều phải thông qua API hoặc message broker (RabbitMQ trong dự án này).

### 1.2. Áp Dụng Trong Dự Án

Dự án URL Shortener Microservices triển khai pattern này một cách triệt để:

| Service | Database | Host:Port | User | Database Name |
|---------|----------|-----------|------|---------------|
| **url-service** | PostgreSQL 16-alpine | localhost:5432 | urluser | urldb |
| **analytics-service** | PostgreSQL 16-alpine | localhost:5433 | analyticsuser | analyticsdb |
| **user-service** | PostgreSQL 16-alpine | localhost:5434 | useruser | userdb |
| **notification-service** | PostgreSQL 16-alpine | localhost:5435 | notificationuser | notificationdb |

### 1.3. Phân Tích Ưu Điểm

#### 1.3.1. Isolation (Cô Lập Dữ Liệu)

Mỗi service có CSDL riêng, đảm bảo cô lập dữ liệu ở mức vật lý. Điều này mang lại:

- **Cô lập schema**: Thay đổi schema của service A không ảnh hưởng đến service B. URL service có thể thêm cột `is_premium` vào bảng `urls` mà không cần phối hợp với analytics service.
- **Cô lập hiệu năng**: Một truy vấn chậm trong analytics service không ảnh hưởng đến luồng đăng ký user. Mỗi CSDL có connection pool riêng (MaxConns=10, MinConns=2).
- **Cô lập bảo mật**: Service chỉ có quyền truy cập vào CSDL của chính nó. Dù có lỗ hổng bảo mật, attacker không thể truy cập dữ liệu của service khác qua đường DB.
- **Cô lập phiên bản**: Mỗi service có thể chạy phiên bản PostgreSQL khác nhau nếu cần (hiện tại tất cả đều dùng postgres:16-alpine).

#### 1.3.2. Scalability (Khả Năng Mở Rộng)

- **Scale độc lập**: Có thể scale url-service lên 3 replicas (docker-compose.scale.yml hỗ trợ `--scale url-service=3`) trong khi các service khác giữ nguyên.
- **Tối ưu hóa riêng**: Analytics service (bảng `clicks` có thể lên đến hàng tỷ dòng) có thể được tối ưu hóa CSDL riêng — ví dụ: partition theo thời gian, hoặc dùng TimescaleDB — mà không ảnh hưởng đến CSDL user-service vốn nhỏ hơn nhiều.
- **Tài nguyên riêng**: Mỗi instance PostgreSQL chạy trong container riêng với volume riêng, cho phép cấu hình resource limits (CPU/memory) khác nhau qua Docker.

#### 1.3.3. Data Ownership (Quyền Sở Hữu Dữ Liệu)

Mỗi service là "nguồn sự thật" (source of truth) duy nhất cho dữ liệu của mình:

| Dữ liệu | Owner | Đọc bởi | Ghi bởi |
|---------|-------|---------|---------|
| URL records | url-service | url-service (trực tiếp), gateway (qua API) | url-service |
| Clicks | analytics-service | analytics-service | analytics-service |
| Users | user-service | user-service | user-service |
| Notifications | notification-service | notification-service | notification-service |

Không có shared database, không có foreign key references xuyên service. Mọi tham chiếu (ví dụ `urls.user_id` tham chiếu đến `users.id` từ user-service) là **logical reference** — không có ràng buộc khóa ngoại vật lý.

#### 1.3.4. Polyglot Persistence (Tiềm Năng)

Mặc dù hiện tại tất cả đều dùng PostgreSQL, pattern này cho phép dễ dàng thay đổi công nghệ lưu trữ cho từng service:
- **Analytics service** có thể chuyển sang TimescaleDB hoặc ClickHouse cho click data.
- **Notification service** có thể chuyển sang MongoDB nếu cần lưu trữ document phức tạp.
- **User service** có thể thêm Redis session store bên cạnh PostgreSQL.

### 1.4. Phân Tích Nhược Điểm

#### 1.4.1. Distributed Transactions

Không có giao dịch phân tán (XA transactions) giữa các service. Dự án giải quyết vấn đề này bằng:

- **Transactional Outbox Pattern**: Khi url-service tạo URL mới, nó INSERT vào bảng `urls` và `outbox` trong cùng một transaction PostgreSQL. Outbox coordinator sau đó publish event lên RabbitMQ. Điều này đảm bảo "at-least-once delivery" mà không cần distributed transaction.
- **Idempotency Table** (`processed_events`): Analytics service dùng bảng này để đảm bảo mỗi event chỉ được xử lý một lần, ngay cả khi nhận được duplicate messages từ RabbitMQ.
- **Eventual Consistency**: Hệ thống chấp nhận nhất quán cuối cùng (eventual consistency) thay vì nhất quán tức thời (strong consistency).

#### 1.4.2. Cross-Service Queries

Không thể JOIN dữ liệu giữa các service. Ví dụ: để hiển thị danh sách URL kèm click count, gateway phải:
1. Gọi url-service API để lấy danh sách URL của user.
2. Với mỗi URL (hoặc batch), gọi analytics-service API để lấy click count.

Pattern này dẫn đến N+1 problem tiềm ẩn, thường được giải quyết bằng:
- **Redis cache** (url-service có Redis client) lưu kết quả đã tính toán.
- **CQRS** với materialized view riêng (chưa triển khai trong dự án này).
- **API Gateway composition** (gateway service chịu trách nhiệm gom dữ liệu).

#### 1.4.3. Operational Complexity

- **4 instance PostgreSQL**: Thay vì 1, cần quản lý 4 container DB, 4 volume, 4 health check, 4 backup strategy.
- **Memory overhead**: Mỗi instance PostgreSQL ngốn tối thiểu ~50-100MB RAM cho shared buffers, tổng cộng ~200-400MB chỉ cho DB.
- **Connection overhead**: Mỗi service duy trì connection pool riêng, tổng số connection tiềm ẩn: 4 pool × 10 MaxConns = 40 connection PostgreSQL.

---

## 2. Kiến Trúc 4 Cơ Sở Dữ Liệu

### 2.1. Database 1: `urldb` — URL Service

**Container:** `url_db`  
**Port ngoài:** 5432  
**Port trong:** 5432  
**User:** urluser  
**Password:** urlpass  
**DB name:** urldb  
**Volume:** url_db_data → `/var/lib/postgresql/data`  
**Image:** postgres:16-alpine

**Bảng:**
| Bảng | Mục đích | Dung lượng dự kiến |
|------|----------|-------------------|
| `urls` | Lưu URL gốc, short code, metadata | Cao (tỷ lệ với số URL được tạo) |
| `outbox` | Transactional outbox cho event publishing | Trung bình (tạm thời, được cleanup sau publish) |

**Đặc điểm:** Đây là database quan trọng nhất vì chứa dữ liệu core của hệ thống. Bảng `urls` được read nhiều (mỗi redirect là một read) và write vừa phải. Bảng `outbox` có write pattern đặc biệt: insert nhanh, select-polling, update-xóa sau publish.

### 2.2. Database 2: `analyticsdb` — Analytics Service

**Container:** `analytics_db`  
**Port ngoài:** 5433  
**Port trong:** 5432  
**User:** analyticsuser  
**Password:** analyticspass  
**DB name:** analyticsdb  
**Volume:** analytics_db_data → `/var/lib/postgresql/data`  
**Image:** postgres:16-alpine

**Bảng:**
| Bảng | Mục đích | Dung lượng dự kiến |
|------|----------|-------------------|
| `clicks` | Lưu mỗi lần click vào short URL | Rất cao (có thể hàng tỷ dòng) |
| `milestones` | Lưu các milestone đã đạt được (100, 1000, 10000 clicks) | Thấp (1 dòng mỗi milestone mỗi URL) |
| `processed_events` | Idempotency table cho event processing | Trung bình (1 dòng mỗi event từ RabbitMQ) |

**Đặc điểm:** Database có tốc độ ghi cao nhất (mỗi click là một INSERT vào `clicks`). Cần chiến lược lưu trữ đặc biệt cho dữ liệu clicks (partition, TTL, archive) trong production. Hiện tại chưa có partition — đây là một weakness.

### 2.3. Database 3: `userdb` — User Service

**Container:** `user_db`  
**Port ngoài:** 5434  
**Port trong:** 5432  
**User:** useruser  
**Password:** userpass  
**DB name:** userdb  
**Volume:** user_db_data → `/var/lib/postgresql/data`  
**Image:** postgres:16-alpine

**Bảng:**
| Bảng | Mục đích | Dung lượng dự kiến |
|------|----------|-------------------|
| `users` | Lưu thông tin tài khoản người dùng | Thấp (tỷ lệ với số user đăng ký) |

**Đặc điểm:** Database nhỏ nhất, ít được ghi nhất. Quan trọng về bảo mật (chứa password_hash). Số lượng user thường ít hơn số URL và click nhiều cấp độ.

### 2.4. Database 4: `notificationdb` — Notification Service

**Container:** `notification_db`  
**Port ngoài:** 5435  
**Port trong:** 5432  
**User:** notificationuser  
**Password:** notificationpass  
**DB name:** notificationdb  
**Volume:** notification_db_data → `/var/lib/postgresql/data`  
**Image:** postgres:16-alpine

**Bảng:**
| Bảng | Mục đích | Dung lượng dự kiến |
|------|----------|-------------------|
| `notifications` | Lưu thông báo đã gửi cho user | Trung bình (tỷ lệ với số event * số user) |

**Đặc điểm:** Database đơn giản nhất với chỉ một bảng. Mỗi notification được insert khi có event (URL created, milestone reached) và được đánh dấu là 'sent' sau khi gửi email giả lập (mock).

---

## 3. Phân Tích Schema Chi Tiết Từng Bảng

### 3.1. Bảng `urls` — URL Service

**Định nghĩa đầy đủ:**

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
ALTER TABLE urls ADD COLUMN IF NOT EXISTS user_email TEXT NOT NULL DEFAULT '';
```

#### 3.1.1. Phân Tích Từng Cột

**`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`**
- **Kiểu:** UUID (128-bit, 36 ký tự dạng `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`)
- **Giá trị mặc định:** `gen_random_uuid()` — sinh UUID ngẫu nhiên (v4), không tuần tự.
- **Ưu điểm:** UUID tránh các vấn đề với sequence (giới hạn 32-bit integer, race condition, enumeration attack). Phù hợp với distributed systems — có thể sinh UUID ở application layer mà không cần DB.
- **Nhược điểm:** UUID v4 ngẫu nhiên gây index fragmentation trên B-tree do không tuần tự. So với bigserial (8 bytes), UUID (16 bytes) chiếm gấp đôi dung lượng và chậm hơn khoảng 20-30% trong index operations. Có thể dùng UUID v7 (time-ordered) để cải thiện.
- **Lưu ý:** Trong store.go, ID được xử lý dưới dạng `string` (không phải `uuid.UUID`), nghĩa là application layer không kiểm tra định dạng UUID.

**`short_code VARCHAR(10) UNIQUE NOT NULL`**
- **Kiểu:** VARCHAR(10) — chuỗi tối đa 10 ký tự.
- **Ràng buộc:** UNIQUE + NOT NULL.
- **Ý nghĩa:** Mã rút gọn dùng trong URL redirect, ví dụ `https://short.url/r/abc123`.
- **Phân tích:** Độ dài 10 ký tự với base62 (a-z, A-Z, 0-9) cho 62^10 ≈ 8.4×10^17 tổ hợp, quá đủ cho hầu hết ứng dụng. Mã được tạo bởi `NewShortCodeGenerator()` (không đọc được source, nhưng thường là random base62).
- **UNIQUE quan trọng:** Đảm bảo không có hai URL có cùng short_code — yêu cầu bắt buộc cho redirect.

**`original_url TEXT NOT NULL`**
- **Kiểu:** TEXT (không giới hạn độ dài trong PostgreSQL, tối đa 1GB).
- **Ràng buộc:** NOT NULL.
- **Ý nghĩa:** URL gốc cần redirect đến.
- **Lưu ý:** Không có ràng buộc CHECK để kiểm tra định dạng URL. Application layer chịu trách nhiệm validate.

**`user_id UUID NOT NULL`**
- **Kiểu:** UUID.
- **Ràng buộc:** NOT NULL.
- **Ý nghĩa:** Tham chiếu logical đến `users.id` từ user-service.
- **Lưu ý quan trọng:** Đây là **foreign key logic**, không có ràng buộc vật lý. PostgreSQL không đảm bảo user_id tồn tại trong database user-service. Nếu user bị xóa, các URL của user đó vẫn tồn tại (orphan records). Đây là trade-off có chủ ý của Database-per-Service pattern.

**`user_email TEXT NOT NULL DEFAULT ''`**
- **Kiểu:** TEXT.
- **Ràng buộc:** NOT NULL DEFAULT ''.
- **Lịch sử:** Cột này được THÊM SAU qua `ALTER TABLE ADD COLUMN IF NOT EXISTS` (dòng 12). Ban đầu schema không có cột này.
- **Mục đích:** Denormalization — lưu email của user ngay trong bảng urls để:
  - Tránh phải gọi user-service API khi cần gửi notification.
  - Cho phép outbox event chứa email để notification service gửi thông báo.
  - Phục vụ anonymous URL creation (`HandleShortenAnon`) khi user chưa đăng nhập.
- **Denormalization penalty:** Dữ liệu có thể inconsistent nếu user đổi email. Tuy nhiên, trong dự án này chưa có chức năng đổi email, nên đây là quyết định chấp nhận được.

**`created_at TIMESTAMPTZ NOT NULL DEFAULT now()`**
- **Kiểu:** TIMESTAMPTZ (timestamptz = timestamp with time zone, 8 bytes).
- **Giá trị mặc định:** `now()` — thời gian hiện tại theo múi giờ của server.
- **Ý nghĩa:** Lưu thời điểm URL được tạo. Dùng cho pagination (ORDER BY created_at DESC) và hiển thị.

**`expires_at TIMESTAMPTZ NULL`**
- **Kiểu:** TIMESTAMPTZ, nullable.
- **Giá trị mặc định:** NULL (không có hạn sử dụng).
- **Ý nghĩa:** Thời gian URL tự động hết hạn. Sau thời gian này, URL sẽ không redirect được.
- **Xử lý trong store.go:** Các query `FindByUserID` kiểm tra `(expires_at IS NULL OR expires_at > NOW())` để lọc URL còn hiệu lực.

**`is_active BOOLEAN NOT NULL DEFAULT true`**
- **Kiểu:** BOOLEAN.
- **Giá trị mặc định:** true.
- **Ý nghĩa:** Soft-delete — cho phép deactivate URL mà không xóa dữ liệu. Khi user "xóa" URL, thực chất là SET is_active = false.
- **Xử lý trong store.go:** `Deactivate` function: `UPDATE urls SET is_active = false WHERE short_code = $1 AND user_id = $2 AND is_active = true`. Query có kiểm tra `is_active = true` (idempotent — gọi nhiều lần không lỗi) và `user_id = $2` (bảo vệ — chỉ chủ sở hữu mới deactivate được).

#### 3.1.2. Nhận Xét Schema

- **Thiếu CHECK constraint:** Không có ràng buộc kiểm tra `original_url` bắt đầu với `http://` hoặc `https://`.
- **Thiếu foreign key:** `user_id` không có FK reference — đây là lựa chọn kiến trúc có chủ đích.
- **Thiếu unique constraint trên (user_id, short_code):** Cho phép user tạo nhiều URL với cùng short_code? Thực tế short_code là UNIQUE global, nên không thể. Một unique constraint composite (user_id, short_code) là thừa.
- **user_email denormalization:** Có thể gây data inconsistency, nhưng là trade-off hợp lý.
- **expires_at nullable:** Tốt cho UX — URL không có hạn sử dụng là trường hợp phổ biến.

### 3.2. Bảng `outbox` — URL Service (Transactional Outbox)

**Định nghĩa đầy đủ:**

```sql
CREATE TABLE IF NOT EXISTS outbox (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   TEXT         NOT NULL,
    payload      JSONB        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ  NULL,
    published_at TIMESTAMPTZ  NULL
);
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ NULL;
```

#### 3.2.1. Transactional Outbox Pattern

Đây là bảng quan trọng cho reliable event publishing. Pattern hoạt động như sau:

1. **Atomic write:** Khi url-service tạo URL mới, nó INSERT vào bảng `urls` VÀ `outbox` trong cùng một database transaction. Nếu INSERT URL thất bại, outbox record cũng rollback.
2. **Async poller:** Outbox coordinator (`outboxCoordinator.Run(ctx)` trong main.go) chạy background goroutine, thường xuyên query bảng `outbox` để tìm các record chưa published.
3. **Publish:** Coordinator publish event lên RabbitMQ, sau đó đánh dấu `published_at`.
4. **At-least-once:** Nếu coordinator crash sau publish, record vẫn chưa được đánh dấu published, sẽ được publish lại (at-least-once semantics).

#### 3.2.2. Phân Tích Từng Cột

**`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`**
- Tương tự bảng urls.

**`event_type TEXT NOT NULL`**
- Kiểu: TEXT (không giới hạn, nhưng thực tế ngắn như "url.created", "url.deleted").
- Ý nghĩa: Routing key để RabbitMQ consumer biết xử lý event này như thế nào.
- Các event type từ shared/events.go:
  - `"url.created"` — URL mới được tạo
  - `"url.clicked"` — URL được click
  - `"url.deleted"` — URL bị xóa
  - `"milestone.reached"` — URL đạt milestone clicks

**`payload JSONB NOT NULL`**
- Kiểu: JSONB (binary JSON, hiệu quả hơn JSON text).
- Ý nghĩa: Toàn bộ event struct được serialize thành JSON. Ví dụ URLCreatedEvent chứa short_code, original_url, user_id, user_email, expires_at.
- JSONB ưu điểm: Cho phép query vào bên trong document (ví dụ: `payload->>'short_code'`), hỗ trợ indexing GIN. Nhưng trong outbox pattern, JSONB chỉ được dùng như blob storage — không cần query vào payload. Dùng JSONB thay vì BYTEA vì con người có thể đọc được và dễ debug.

**`created_at TIMESTAMPTZ NOT NULL DEFAULT now()`**
- Dùng cho ordering — outbox coordinator lấy record cũ nhất trước (ORDER BY created_at ASC).

**`locked_until TIMESTAMPTZ NULL`**
- **Mục đích:** Lock lease cho multi-replica worker. Khi có nhiều replicas của url-service (scale=3), mỗi replica chạy outbox coordinator riêng. Cơ chế locking ngăn nhiều coordinator xử lý cùng một record.
- **Cách hoạt động:**
  1. Coordinator query FOR UPDATE SKIP LOCKED để claim record.
  2. SET locked_until = now() + interval '30 seconds'.
  3. Xử lý record (publish lên RabbitMQ).
  4. SET published_at = now() (hoặc release lock nếu thất bại).
  5. Nếu coordinator crash, lock tự động hết hạn sau 30 giây, coordinator khác sẽ pick up record.
- **Cột được thêm sau** qua ALTER TABLE (dòng 30) — ban đầu chưa có cơ chế locking.

**`published_at TIMESTAMPTZ NULL`**
- NULL = chưa published. Non-NULL = đã publish thành công.
- Dùng để lọc record trong partial index.

#### 3.2.3. `FetchUnpublished` Query (outbox_store.go)

Query phức tạp nhất trong toàn bộ dự án, sử dụng CTE (Common Table Expression) với FOR UPDATE SKIP LOCKED:

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

Phân tích chi tiết:
- **CTE `claimed`:** Chọn các record chưa published, chưa bị lock hoặc lock đã hết hạn. Sắp xếp theo created_at ASC (FIFO). Giới hạn số lượng.
- **FOR UPDATE SKIP LOCKED:** Khóa các dòng được chọn để xử lý, nhưng BỎ QUA các dòng đang bị khóa bởi transaction khác (SKIP LOCKED). Đây là cơ chế chính cho multi-replica coordination.
- **UPDATE ... RETURNING:** Update lock_until và trả về toàn bộ record. Một statement duy nhất, atomic.

### 3.3. Bảng `clicks` — Analytics Service

**Định nghĩa đầy đủ:**

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS clicks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code TEXT NOT NULL,
    clicked_at TIMESTAMPTZ NOT NULL,
    ip_hash TEXT NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    referer TEXT NULL
);
```

#### 3.3.1. Extension pgcrypto

- `CREATE EXTENSION IF NOT EXISTS pgcrypto` — cung cấp hàm băm mật mã như `gen_random_uuid()`, `digest()`, v.v.
- Tuy nhiên, `gen_random_uuid()` đã có sẵn trong PostgreSQL 16 (từ PG 13), việc dùng pgcrypto là không cần thiết cho UUID generation nhưng được giữ lại để tương thích ngược.

#### 3.3.2. Phân Tích Từng Cột

**`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`**
- Tương tự các bảng khác.

**`short_code TEXT NOT NULL`**
- Kiểu: TEXT (so với VARCHAR(10) ở bảng urls). Không giới hạn độ dài.
- Ý nghĩa: Short code của URL được click. Cho phép TEXT dài hơn VARCHAR(10) — có thể dùng cho future short code formats.
- **Lưu ý:** Không có ràng buộc khóa ngoại đến urls.short_code vì đây là cross-service reference.

**`clicked_at TIMESTAMPTZ NOT NULL`**
- Thời điểm click xảy ra. Không có DEFAULT — giá trị được truyền từ application layer (URLClickedEvent.OccurredAt).
- Dùng cho time-series analysis (count by hour/day/week/month).

**`ip_hash TEXT NOT NULL`**
- Băm của địa chỉ IP người dùng, sử dụng salt từ config `IP_HASH_SALT`.
- Mục đích: Ẩn danh hóa IP để tuân thủ GDPR/Privacy regulations. Có thể phát hiện click trùng lặp mà không lưu IP gốc.
- **Lưu ý:** `ip_hash` có thể được dùng để approximate unique visitors (COUNT DISTINCT ip_hash), nhưng không chính xác tuyệt đối do hash collision.

**`user_agent TEXT NOT NULL DEFAULT ''`**
- User-Agent header từ trình duyệt. Dùng cho phân tích thiết bị, trình duyệt.
- DEFAULT '' cho trường hợp không có User-Agent (API calls, bots).

**`referer TEXT NULL`**
- HTTP Referer header. Nullable — có thể không có referer (direct visit, bookmark).
- Dùng cho top referers analysis.

#### 3.3.3. Phân Tích Chiến Lược Lưu Trữ

Bảng `clicks` có tốc độ ghi cao nhất — mỗi redirect là một INSERT. Một số vấn đề tiềm ẩn:
- **Không có partition:** Trong production, bảng này sẽ rất lớn. Thiếu time-based partitioning (ví dụ: click_2026_07) sẽ gây chậm query và khó khăn trong archive.
- **Không có TTL:** Không có cơ chế tự động xóa dữ liệu cũ. Click data thường chỉ cần cho 30-90 ngày gần nhất.
- **Không có retention policy:** Cần có chiến lược archive (chuyển sang cold storage) cho dữ liệu cũ.

### 3.4. Bảng `milestones` — Analytics Service

**Định nghĩa đầy đủ:**

```sql
CREATE TABLE IF NOT EXISTS milestones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code TEXT NOT NULL,
    milestone INT NOT NULL,
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(short_code, milestone)
);
```

#### 3.4.1. Phân Tích Từng Cột

**`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`**
- Surrogate key.

**`short_code TEXT NOT NULL`**
- Short code của URL đạt milestone.

**`milestone INT NOT NULL`**
- Số click milestone. Các giá trị: 100, 1000, 10000, v.v.
- Không có CHECK constraint — application layer định nghĩa milestone values.

**`triggered_at TIMESTAMPTZ NOT NULL DEFAULT now()`**
- Thời điểm milestone đạt được.

**`UNIQUE(short_code, milestone)`**
- Đảm bảo mỗi milestone chỉ được ghi nhận một lần cho mỗi URL. Nếu click thứ 100 đến lần thứ hai (do data replay), INSERT sẽ bỏ qua (ON CONFLICT DO NOTHING).

#### 3.4.2. Milestone Check Logic (store.go)

```go
milestoneExistsSQL = `SELECT EXISTS(SELECT 1 FROM milestones WHERE short_code = $1 AND milestone = $2)`
insertMilestoneSQL = `
    INSERT INTO milestones (short_code, milestone)
    VALUES ($1, $2)
    ON CONFLICT (short_code, milestone) DO NOTHING
`
```

Logic: Sau khi insert click, kiểm tra tổng số clicks có đạt milestone không. Nếu đạt, INSERT milestone (với ON CONFLICT DO NOTHING để đảm bảo idempotent). Sau đó publish MilestoneReachedEvent.

### 3.5. Bảng `processed_events` — Analytics Service (Idempotency)

**Định nghĩa đầy đủ:**

```sql
CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

#### 3.5.1. Phân Tích

**Mục đích:** Idempotency table để đảm bảo mỗi event chỉ được xử lý một lần.

RabbitMQ cung cấp at-least-once delivery — trong trường hợp network issue, consumer crash, hoặc auto-ack timeout, cùng một message có thể được deliver nhiều lần. Bảng `processed_events` giải quyết vấn đề này:

1. **Receive event** từ RabbitMQ.
2. **Check processed_events:** `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`.
3. **Nếu tồn tại:** Skip (duplicate event, ack và bỏ qua).
4. **Nếu không:** INSERT INTO processed_events (event_id) và xử lý event, trong cùng transaction.

**`event_id TEXT PRIMARY KEY`**
- Dùng TEXT làm PK thay vì UUID. Event ID là UUID string từ `BaseEvent.EventID`.
- Không cần DEFAULT — giá trị đến từ event.

**`processed_at TIMESTAMPTZ NOT NULL DEFAULT now()`**
- Thời điểm event được xử lý.

#### 3.5.2. Phân Tích Hiệu Năng

- Bảng này sẽ lớn dần theo thời gian (mỗi event để lại một dòng). Cần TTL cleanup.
- PK trên event_id cho phép kiểm tra tồn tại rất nhanh (index lookup).
- Có thể dùng `pg_cron` hoặc application-layer cleanup job để xóa dòng cũ hơn N ngày.

### 3.6. Bảng `notifications` — Notification Service

**Định nghĩa đầy đủ:**

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'sent',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ NULL
);
```

#### 3.6.1. Phân Tích Từng Cột

**`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`**
- Surrogate key.

**`user_id UUID NOT NULL`**
- User nhận notification. Logical reference đến users.id.

**`event_type TEXT NOT NULL`**
- Loại event kích hoạt notification (url.created, milestone.reached, url.deleted).

**`payload JSONB NOT NULL`**
- Dữ liệu event đầy đủ. Cho phép notification service hiển thị thông tin mà không cần gọi service khác.

**`status TEXT NOT NULL DEFAULT 'sent'`**
- Trạng thái notification.
- Giá trị: 'pending' (mới tạo, chưa gửi), 'sent' (đã gửi thành công).
- DEFAULT là 'sent' — hơi lạ, vì ứng dụng set status = 'pending' khi INSERT.
- **Thiếu constraint:** Nên có CHECK(status IN ('pending', 'sent', 'failed')) để tránh dữ liệu không hợp lệ.

**`created_at TIMESTAMPTZ NOT NULL DEFAULT now()`**
- Thời điểm notification được tạo.

**`sent_at TIMESTAMPTZ NULL`**
- Thời điểm email được gửi (mock). NULL nếu chưa gửi.

#### 3.6.2. Transaction Logic (store.go)

`InsertNotification` dùng transaction với hai bước:
1. INSERT INTO notifications (..., status = 'pending') ... RETURNING id, created_at
2. Gửi email (mock log: `s.log.Info("mock email sent", ...)`)
3. UPDATE notifications SET status = 'sent', sent_at = now() WHERE id = $1
4. COMMIT

Nếu step 2 thất bại, transaction rollback, notification không được lưu. Điều này đảm bảo tính nhất quán — chỉ lưu notification khi email đã gửi thành công (dù thực tế là mock).

### 3.7. Bảng `users` — User Service

**Định nghĩa đầy đủ:**

```sql
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

#### 3.7.1. Phân Tích Từng Cột

**`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`**
- Surrogate key.

**`email TEXT UNIQUE NOT NULL`**
- Email người dùng. UNIQUE đảm bảo mỗi email chỉ đăng ký một tài khoản.
- **Thiếu validation:** Không có CHECK constraint kiểm tra định dạng email. Application layer xử lý validation.
- **Thiếu lowercasing:** Email trong PostgreSQL phân biệt HOA/thường. `User@Example.com` và `user@example.com` là hai email khác nhau. Application layer nên lowercasing trước khi insert.

**`password_hash TEXT NOT NULL`**
- Hash của mật khẩu (dùng bcrypt với configurable cost factor).
- Không lưu plaintext password — đúng best practice.
- **Độ dài:** TEXT không giới hạn, bcrypt hash ~60 ký tự. Có thể dùng VARCHAR(255) để chặt chẽ hơn.

**`created_at TIMESTAMPTZ NOT NULL DEFAULT now()`**
- Thời điểm đăng ký.

#### 3.7.2. Unique Violation Handling (store.go)

```go
func isPgUniqueViolation(err error) bool {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) {
        return pgErr.Code == "23505"
    }
    return strings.Contains(err.Error(), "duplicate key")
}
```

Xử lý lỗi Postgres error code "23505" (unique_violation) để trả về `ErrDuplicateEmail`. Có fallback với string matching cho trường hợp error không phải dạng `*pgconn.PgError`.

---

## 4. Phân Tích Chi Tiết 19 Định Nghĩa Chỉ Mục

### 4.1. Primary Key Indexes (7 Chỉ Mục)

Mỗi bảng có PRIMARY KEY, tự động tạo unique B-tree index. Không có ngoại lệ.

| Bảng | PK Column(s) | Index Name (auto) | Loại |
|------|-------------|-------------------|------|
| urls | id | urls_pkey | B-tree unique |
| outbox | id | outbox_pkey | B-tree unique |
| clicks | id | clicks_pkey | B-tree unique |
| milestones | id | milestones_pkey | B-tree unique |
| processed_events | event_id | processed_events_pkey | B-tree unique |
| notifications | id | notifications_pkey | B-tree unique |
| users | id | users_pkey | B-tree unique |

**Tổng số: 7 PK indexes** (luôn hiện hữu trong mọi bảng PostgreSQL).

**Phân tích PK Strategy:**
- **UUID vs Serial:** Dùng UUID v4 thay vì serial/bigserial. Đây là quyết định kiến trúc quan trọng.
- **Ưu điểm UUID:** Distributed generation, không cần DB để sinh ID, an toàn hơn (không thể đoán số lượng user/URL), mergible databases.
- **Nhược điểm UUID:** Lớn hơn (16 bytes vs 8 bytes bigint), chậm hơn do random (B-tree fragmentation), khó debug.
- **Fragmentation impact:** Với UUID v4 ngẫu nhiên, B-tree index bị fragmentation cao. Mỗi INSERT vào vị trí ngẫu nhiên trong index, gây:
  - Index bloat (~10-30% overhead)
  - Write amplification (nhiều page splits)
  - Cache miss (random access pattern)
- **UUID v7:** Là giải pháp — kết hợp timestamp + random, vừa unique vừa time-ordered, giảm fragmentation.

### 4.2. Inline UNIQUE Constraint Indexes (3 Chỉ Mục)

Các UNIQUE constraint trong CREATE TABLE tự động tạo B-tree index:

| Bảng | Cột(s) | Tên Index (auto) | Mục đích |
|------|--------|-------------------|----------|
| urls | short_code | urls_short_code_key | Đảm bảo short code unique |
| milestones | (short_code, milestone) | milestones_short_code_milestone_key | Mỗi milestone 1 lần cho mỗi URL |
| users | email | users_email_key | Mỗi email chỉ đăng ký 1 lần |

**Tổng số: 3 inline UNIQUE indexes.**

#### 4.2.1. `urls_short_code_key` — urls(short_code)

- **Query pattern served:** `SELECT ... FROM urls WHERE short_code = $1` (redirect lookup)
- **Đây là index quan trọng nhất** — mỗi redirect là một lookup theo short_code.
- **Composite?** Không — chỉ single column VARCHAR(10). Đủ nhanh cho lookup.
- **Tại sao có cả UNIQUE constraint và CREATE UNIQUE INDEX?** Xem phân tích ở mục 4.3.1.

#### 4.2.2. `milestones_short_code_milestone_key` — milestones(short_code, milestone)

- **Query pattern served:** `SELECT EXISTS(SELECT 1 FROM milestones WHERE short_code = $1 AND milestone = $2)`
- **Composite:** Cả short_code và milestone trong cùng index.
- **ON CONFLICT DO NOTHING:** Unique constraint được dùng trong upsert.

#### 4.2.3. `users_email_key` — users(email)

- **Query pattern served:** `SELECT ... FROM users WHERE email = $1` (login lookup)
- **Single column:** TEXT index. Email unique đảm bảo không trùng lặp.

### 4.3. Explicit Named Indexes (9 Chỉ Mục)

Chỉ mục được tạo bằng lệnh `CREATE INDEX` / `CREATE UNIQUE INDEX` riêng biệt:

| # | Tên Index | Bảng | Cột(s) | WHERE | Loại | File |
|---|-----------|------|--------|-------|------|------|
| 1 | idx_urls_short_code | urls | short_code | — | UNIQUE B-tree | url-service |
| 2 | idx_urls_user_id_created | urls | user_id, created_at DESC | — | B-tree composite | url-service |
| 3 | idx_outbox_unpublished | outbox | created_at ASC | published_at IS NULL | B-tree partial | url-service |
| 4 | idx_outbox_unpublished_unlocked | outbox | created_at ASC | published_at IS NULL AND locked_until IS NULL | B-tree partial | url-service |
| 5 | idx_clicks_short_code_time | clicks | short_code, clicked_at DESC | — | B-tree composite | analytics-service |
| 6 | idx_clicks_referer | clicks | short_code, referer | referer IS NOT NULL | B-tree partial composite | analytics-service |
| 7 | idx_milestones_code_milestone | milestones | short_code, milestone | — | UNIQUE B-tree composite | analytics-service |
| 8 | idx_notifications_user_created | notifications | user_id, created_at DESC | — | B-tree composite | notification-service |
| 9 | idx_users_email | users | email | — | B-tree | user-service |

**Tổng số: 9 explicit named indexes.**

#### 4.3.1. `idx_urls_short_code` — url-service (REDUNDANT)

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_urls_short_code ON urls(short_code);
```

- **Phân tích redundancy:** Cột `short_code VARCHAR(10) UNIQUE NOT NULL` trong CREATE TABLE đã tạo unique index (system-named). Index này là duplicate hoàn toàn.
- **Tại sao vẫn tạo?** Có thể là để:
  - Đặt tên dễ đọc hơn cho EXPLAIN ANALYZE (comment trong SQL: "explicit name for EXPLAIN ANALYZE verification").
  - Thói quen của developer.
- **Hậu quả:** PostgreSQL tạo hai unique index trên cùng cột. Điều này gây:
  - Lãng phí disk space (mỗi index ~bảng * 0.5-1x).
  - Chậm INSERT/UPDATE (phải maintain 2 indexes).
  - Không gây lỗi nhưng không tối ưu.
- **Tác động thực tế:** `CREATE UNIQUE INDEX IF NOT EXISTS` — nếu index `idx_urls_short_code` đã tồn tại, bỏ qua. Nhưng index system-named (`urls_short_code_key`) có tên khác. Cả hai đều tồn tại song song.

#### 4.3.2. `idx_urls_user_id_created` — url-service

```sql
CREATE INDEX IF NOT EXISTS idx_urls_user_id_created
    ON urls(user_id, created_at DESC);
```

- **Query pattern served:**
  ```sql
  SELECT ... FROM urls 
  WHERE user_id = $1 AND id < $2 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW())
  ORDER BY id DESC LIMIT $3
  ```
- **Design decisions:**
  - **Composite (user_id, created_at DESC):** Filter theo user_id, sort theo created_at DESC.
  - **Leading column user_id:** Quan trọng — đặt user_id đầu tiên vì WHERE clause lọc theo user_id.
  - **DESC trong index definition:** Index được sắp xếp DESC ngay từ đầu, tránh phải sort riêng.
  - **Thiếu cột trong index:** Query cũng filter `is_active = true` và `expires_at IS NULL OR expires_at > NOW()`. Các cột này không trong index, PostgreSQL phải:
    1. Dùng index scan trên (user_id, created_at DESC).
    2. Filter rows bằng `is_active` và `expires_at` (không index).
  - **Có thể cải thiện:** Partial index: `CREATE INDEX ... ON urls(user_id, created_at DESC) WHERE is_active = true AND (expires_at IS NULL OR expires_at > NOW())`
- **Pagination:** Keyset pagination với `id < $2` và `ORDER BY id DESC`. Đây là cursor-based pagination, scalable cho số lượng lớn.

#### 4.3.3. `idx_outbox_unpublished` — url-service (Partial Index #1)

```sql
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox(created_at ASC)
    WHERE published_at IS NULL;
```

- **Query pattern served:**
  ```sql
  SELECT id FROM outbox
  WHERE published_at IS NULL
    AND (locked_until IS NULL OR locked_until < now())
  ORDER BY created_at ASC
  LIMIT $1
  FOR UPDATE SKIP LOCKED
  ```
- **Partial index design:**
  - **WHERE published_at IS NULL:** Chỉ index các row chưa published. Khi published_at được set, row tự động khỏi index.
  - **Index size nhỏ hơn:** Chỉ chứa unpublished rows (thường là vài chục/thay vì tất cả).
  - **created_at ASC:** FIFO order — lấy row cũ nhất trước.
- **Redundancy analysis:** Index này chỉ filter `published_at IS NULL`. Query cũng filter `(locked_until IS NULL OR locked_until < now())`. Có index riêng cho unlocked (xem 4.3.4). PostgreSQL có thể dùng index này nếu matching rows có locked_until thỏa mãn — cần phải filter thêm. Index thứ hai (4.3.4) chính xác hơn.

#### 4.3.4. `idx_outbox_unpublished_unlocked` — url-service (Partial Index #2)

```sql
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished_unlocked
    ON outbox(created_at ASC)
    WHERE published_at IS NULL AND locked_until IS NULL;
```

- **Query pattern served:** Tương tự idx_outbox_unpublished nhưng thêm điều kiện locked_until.
- **Tại sao có hai partial indexes?**
  - `idx_outbox_unpublished`: Phục vụ query cần ALL unpublished rows (kể cả đã lock). Dùng cho monitoring hoặc admin operations.
  - `idx_outbox_unpublished_unlocked`: Phục vụ query chỉ cần rows CHƯA bị lock (cho outbox coordinator). Chính xác hơn, index nhỏ hơn.
  - **Note:** `locked_until < now()` là inequality condition, không thể dùng index lookup chính xác. Nhưng `locked_until IS NULL` là equality check, index rất hiệu quả.
- **Hai index quá nhiều?** Có thể chỉ cần index thứ hai (unlocked). Index thứ nhất ít được dùng. Nhưng storage cost cho partial index là rất nhỏ.

#### 4.3.5. `idx_clicks_short_code_time` — analytics-service (Composite Index)

```sql
CREATE INDEX IF NOT EXISTS idx_clicks_short_code_time
    ON clicks(short_code, clicked_at DESC);
```

- **Query patterns served:**
  ```sql
  SELECT COUNT(*) FROM clicks WHERE short_code = $1  -- total clicks
  SELECT COUNT(*) FROM clicks WHERE short_code = $1 AND clicked_at >= $2  -- clicks since
  SELECT date_trunc($1, clicked_at AT TIME ZONE 'UTC') AS period, COUNT(*) AS clicks
  FROM clicks WHERE short_code = $2 GROUP BY period ORDER BY period ASC  -- timeline
  ```
- **Composite (short_code, clicked_at DESC):**
  - **short_code lead:** WHERE lọc theo short_code trước.
  - **clicked_at DESC:** ORDER BY mặc định, range query cho time-based filters.
- **Covering index?** Không — index chứa (short_code, clicked_at). Query cũng cần ip_hash, user_agent, referer cho SELECT, nhưng COUNT(*) chỉ cần index (index-only scan nếu may mắn). Tuy nhiên, visibility map phải được vacuum đủ.
- **Query cho Top Referers không dùng index này:** Dùng composite (short_code, referer) partial index (xem 4.3.6).

#### 4.3.6. `idx_clicks_referer` — analytics-service (Partial Composite Index)

```sql
CREATE INDEX IF NOT EXISTS idx_clicks_referer
    ON clicks(short_code, referer)
    WHERE referer IS NOT NULL;
```

- **Query pattern served:**
  ```sql
  SELECT referer, COUNT(*) AS cnt
  FROM clicks
  WHERE short_code = $1 AND referer IS NOT NULL
  GROUP BY referer
  ORDER BY cnt DESC
  LIMIT $2
  ```
- **Partial composite design:**
  - **WHERE referer IS NOT NULL:** Loại bỏ rows không có referer — chiếm phần lớn dữ liệu (phần lớn traffic đến từ direct/bookmark). Giảm index size dramatically.
  - **Composite (short_code, referer):** short_code cho WHERE filter, referer cho GROUP BY. Index covers hoàn toàn query này (index-only scan).
  - **Query là:** Filter short_code, lọc referer IS NOT NULL (đã thỏa mãn WHERE của index), GROUP BY referer. Index đã sort theo (short_code, referer) nên GROUP BY chỉ cần scan.
- **Performance:** Đây là index được tối ưu nhất trong toàn bộ schema — partial, covering, composite, đúng ordering.

#### 4.3.7. `idx_milestones_code_milestone` — analytics-service (REDUNDANT)

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_milestones_code_milestone
    ON milestones(short_code, milestone);
```

- **Redundancy:** `UNIQUE(short_code, milestone)` trong CREATE TABLE đã tạo unique index (milestones_short_code_milestone_key). Index này là duplicate.
- **Giống idx_urls_short_code:** Cùng pattern — inline UNIQUE + CREATE UNIQUE INDEX.
- **Hai unique index trên cùng columns:** Lãng phí tài nguyên. Nên xóa bỏ một trong hai.

#### 4.3.8. `idx_notifications_user_created` — notification-service (Composite Index)

```sql
CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications (user_id, created_at DESC);
```

- **Query patterns served:**
  ```sql
  SELECT ... FROM notifications 
  WHERE user_id = $1
  ORDER BY created_at DESC, id DESC
  LIMIT $2
  ```
- **Composite (user_id, created_at DESC):**
  - **user_id lead:** WHERE filter.
  - **created_at DESC:** ORDER BY.
  - **Thiếu id:** Query ORDER BY created_at DESC, id DESC. Index chỉ có created_at. PostgreSQL sẽ sort lại theo id cho các row cùng created_at. Có thể cải thiện: `ON notifications (user_id, created_at DESC, id DESC)`.
- **Pagination:** Cursor-based với (created_at, id) < (SELECT created_at, id FROM ...). Đây là keyset pagination phức tạp hơn (composite cursor).

#### 4.3.9. `idx_users_email` — user-service (REDUNDANT)

```sql
CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);
```

- **Query pattern served:**
  ```sql
  SELECT ... FROM users WHERE email = $1
  ```
- **Redundancy:** `email TEXT UNIQUE NOT NULL` trong CREATE TABLE đã tạo unique index (users_email_key). Index `idx_users_email` là duplicate.
- **Note:** Index này là non-unique, khác với unique index từ UNIQUE constraint. Nhưng cả hai đều là B-tree trên cùng cột email. Sự khác biệt duy nhất: unique index kiểm tra uniqueness, non-unique thì không. Cả hai vẫn index cùng dữ liệu.
- **Đây là lỗi design rõ ràng nhất:** Nên xóa `CREATE INDEX IF NOT EXISTS idx_users_email ON users (email)` vì:
  1. Tốn disk space.
  2. Chậm INSERT/UPDATE.
  3. Không mang lại lợi ích gì.

### 4.4. Tổng Hợp Tất Cả Chỉ Mục

| STT | Bảng | Index | Loại | Cột | WHERE | Kích thước | Redundant? |
|-----|------|-------|------|-----|-------|-----------|------------|
| 1 | urls | urls_pkey (auto) | PK | id | — | Lớn | Không |
| 2 | urls | urls_short_code_key (auto) | UNIQUE | short_code | — | Nhỏ | Bị trùng (3) |
| 3 | urls | idx_urls_short_code | UNIQUE | short_code | — | Nhỏ | Trùng (2) |
| 4 | urls | idx_urls_user_id_created | INDEX | user_id, created_at DESC | — | Trung bình | Không |
| 5 | outbox | outbox_pkey (auto) | PK | id | — | Lớn | Không |
| 6 | outbox | idx_outbox_unpublished | INDEX | created_at ASC | published_at IS NULL | Rất nhỏ | Không |
| 7 | outbox | idx_outbox_unpublished_unlocked | INDEX | created_at ASC | published_at IS NULL AND locked_until IS NULL | Rất nhỏ | Không |
| 8 | clicks | clicks_pkey (auto) | PK | id | — | Rất lớn | Không |
| 9 | clicks | idx_clicks_short_code_time | INDEX | short_code, clicked_at DESC | — | Rất lớn | Không |
| 10 | clicks | idx_clicks_referer | INDEX | short_code, referer | referer IS NOT NULL | Trung bình | Không |
| 11 | milestones | milestones_pkey (auto) | PK | id | — | Nhỏ | Không |
| 12 | milestones | milestones_short_code_milestone_key (auto) | UNIQUE | short_code, milestone | — | Nhỏ | Bị trùng (13) |
| 13 | milestones | idx_milestones_code_milestone | UNIQUE | short_code, milestone | — | Nhỏ | Trùng (12) |
| 14 | processed_events | processed_events_pkey (auto) | PK | event_id | — | Trung bình | Không |
| 15 | notifications | notifications_pkey (auto) | PK | id | — | Trung bình | Không |
| 16 | notifications | idx_notifications_user_created | INDEX | user_id, created_at DESC | — | Trung bình | Không |
| 17 | users | users_pkey (auto) | PK | id | — | Nhỏ | Không |
| 18 | users | users_email_key (auto) | UNIQUE | email | — | Nhỏ | Bị trùng (19) |
| 19 | users | idx_users_email | INDEX | email | — | Nhỏ | Trùng (18) |

**Tổng quan:**
- **19 định nghĩa chỉ mục** trong SQL
- **16 chỉ mục hiệu dụng** (3 pairs redundant)
- **7 PK indexes** (B-tree unique)
- **3 inline UNIQUE indexes** (B-tree unique)
- **9 explicit CREATE INDEX** (3 UNIQUE, 6 non-unique)
- **3 partial indexes** (idx_outbox_unpublished, idx_outbox_unpublished_unlocked, idx_clicks_referer)
- **4 composite indexes** (idx_urls_user_id_created, idx_clicks_short_code_time, idx_clicks_referer, idx_notifications_user_created)
- **3 redundant pairs** (urls.short_code, milestones.(short_code,milestone), users.email)

**Tác động redundant indexes:**
- Mỗi redundant index ngốn thêm ~bảng-size × 0.5-1x disk space.
- INSERT/UPDATE chậm hơn do phải update nhiều indexes hơn.
- Với bảng lớn (urls, clicks), tác động đáng kể.
- Khuyến nghị: Xóa idx_urls_short_code, idx_milestones_code_milestone, idx_users_email.

### 4.5. Chiến Lược Index Tổng Thể

#### 4.5.1. Các Pattern Được Sử Dụng

**1. Composite Index cho Filter + Sort:**
- `idx_urls_user_id_created(user_id, created_at DESC)` — filter user_id, sort created_at.
- `idx_notifications_user_created(user_id, created_at DESC)` — tương tự.
- `idx_clicks_short_code_time(short_code, clicked_at DESC)` — filter short_code, sort clicked_at.

**2. Partial Index cho High-Selectivity Filter:**
- `idx_outbox_unpublished` — chỉ unpublished rows (phần nhỏ của bảng).
- `idx_clicks_referer` — chỉ rows có referer (phần nhỏ).

**3. Partial Composite Index cho Covering Query:**
- `idx_clicks_referer(short_code, referer) WHERE referer IS NOT NULL` — covering hoàn toàn cho top referers query.

**4. Unique Index cho Data Integrity:**
- `idx_urls_short_code` (dù redundant với inline UNIQUE).
- `idx_users_email` (dù redundant).

#### 4.5.2. Các Pattern KHÔNG Được Sử Dụng

**1. Covering Index (INCLUDE columns):**
- PostgreSQL 11+ hỗ trợ `CREATE INDEX ... INCLUDE (col1, col2)`. Không được dùng.
- Ví dụ: `idx_clicks_short_code_time INCLUDE (ip_hash)` cho phép index-only scan.

**2. GIN Index cho JSONB:**
- Dù có cột JSONB (outbox.payload, notifications.payload), không có GIN index.
- Không cần — payload chỉ được lưu trữ, không query vào bên trong.

**3. Hash Index:**
- Không dùng Hash index cho equality lookups (short_code, email).
- Hash index có thể nhanh hơn B-tree cho `WHERE short_code = $1`, nhưng không support range query.

**4. BRIN Index:**
- Block Range INdex phù hợp cho time-series data (clicks.clicked_at).
- Với bảng clicks cực lớn, BRIN index có thể nhỏ hơn B-tree ~100x.

**5. Partitioning:**
- Không có table partitioning. clicks là candidate rõ ràng nhất.

---

## 5. Chiến Lược Migration

### 5.1. Tổng Quan Cơ Chế

Dự án sử dụng chiến lược migration tối giản:

**Công nghệ:**
- **Embedded SQL** — file migration.sql được nhúng vào Go binary qua `//go:embed migration.sql`.
- **Không dùng migration tool** (golang-migrate, goose, flyway, v.v.).
- **Mỗi service tự chạy migration riêng** khi khởi động.
- **Một file SQL duy nhất** cho mỗi service.

**Cách thực thi:**
```go
//go:embed migration.sql
var migrationSQL string

func main() {
    // ...
    if _, err := pool.Exec(context.Background(), migrationSQL); err != nil {
        log.Error("failed to run database migrations", "error", err)
        os.Exit(1)
    }
    // ...
}
```

### 5.2. Phân Tích Mã Nguồn Migration

#### 5.2.1. URL Service `main.go:44`

```go
if _, err := pool.Exec(context.Background(), migrationSQL); err != nil {
    log.Error("failed to run database migrations", "error", err)
    os.Exit(1)
}
```

- Dùng `pool.Exec` (không transaction wrapper).
- Mặc dù migration có nhiều câu lệnh, PostgreSQL's autocommit mode sẽ chạy từng câu riêng lẻ. Nếu câu 2 thất bại, câu 1 vẫn được commit.
- **Thiếu transaction:** Nên dùng `BEGIN; ... COMMIT;` trong migration SQL hoặc dùng `pool.Begin(ctx)` trong Go.

#### 5.2.2. Analytics Service `migrations.go:15`

```go
func runMigrations(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
    if _, err := pool.Exec(ctx, analyticsSchema); err != nil {
        return fmt.Errorf("run analytics migrations: %w", err)
    }
    log.Info("analytics migrations applied")
    return nil
}
```

- Cùng pattern với url-service.
- Tạo function `runMigrations` riêng thay vì inline trong main.

#### 5.2.3. Idempotency (IF NOT EXISTS)

Tất cả các câu lệnh DDL đều dùng `IF NOT EXISTS`:

- `CREATE TABLE IF NOT EXISTS ...` — 7 bảng
- `CREATE INDEX IF NOT EXISTS ...` — 9 lần
- `CREATE UNIQUE INDEX IF NOT EXISTS ...` — 3 lần
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS ...` — 2 lần
- `CREATE EXTENSION IF NOT EXISTS pgcrypto` — 2 lần

Điều này cho phép migration chạy nhiều lần mà không gây lỗi (idempotent).

### 5.3. Điểm Yếu Của Chiến Lược Hiện Tại

#### 5.3.1. Không Có Rollback

- **Không có migration down:** Khi deploy schema mới, không thể rollback schema cũ.
- Nếu migration gây lỗi, giải pháp duy nhất là fix migration và deploy lại — database đã ở trạng thái không xác định.
- Ví dụ: Nếu thêm cột NOT NULL không có DEFAULT, INSERT có thể fail.
- **Khắc phục:** Trong production, luôn dùng ADD COLUMN với DEFAULT, sau đó ALTER để set NOT NULL.

#### 5.3.2. Không Có Ordering / Versioning

- **Không có migration versions:** Mỗi service chỉ có một file migration.sql duy nhất. Không thể biết migration nào đã chạy, migration nào là mới.
- **Không thể migrate incremental:** Nếu cần thay đổi schema sau này, phải sửa file migration.sql hiện tại. Khi chạy lại trên production database, các lệnh CREATE TABLE sẽ bỏ qua (IF NOT EXISTS), nhưng các lệnh mới thêm (ALTER TABLE) sẽ chạy lần đầu.
- **Phụ thuộc vào IF NOT EXISTS:** Chiến lược này không scale — sau 10 thay đổi, file migration.sql sẽ rất dài và khó maintain.

#### 5.3.3. Không Có Schema Validation

- **Không kiểm tra schema hiện tại:** Migration chỉ chạy SQL mù quáng, không kiểm tra schema có khớp với expected schema không.
- **Không có schema hash:** Không thể detect schema drift (thay đổi thủ công bởi DBA).
- **No dry-run:** Không thể kiểm tra migration trên staging trước.

#### 5.3.4. Thiếu Migration Framework

So với các tool chuyên dụng (golang-migrate, goose):

| Tính năng | Hiện tại | golang-migrate |
|-----------|----------|----------------|
| Versioning | ❌ | ✅ Up/down versions |
| Ordering | ❌ | ✅ Sequential by timestamp |
| Rollback | ❌ | ✅ Down migration |
| Dry-run | ❌ | ✅ Preview |
| Dirty state detection | ❌ | ✅ Track failed migrations |
| Multiple drivers | ✅ PostgreSQL | ✅ PostgreSQL, MySQL, SQLite, etc. |
| Embedding | ✅ go:embed | ✅ go:embed or filesystem |

#### 5.3.5. Khuyến Nghị Cải Thiện

1. **Thêm versioning:** Dùng golang-migrate hoặc goose.
2. **Transaction cho migration:** Bọc toàn bộ migration trong transaction.
3. **Separate concerns:** Tách schema creation và data migration.
4. **CI/CD check:** Migration phải được kiểm tra trên staging database clone trước khi deploy production.
5. **Monitoring:** Thêm logging và metrics cho migration duration và failure rate.

---

## 6. CRUD Operations và Data Ownership Matrix

### 6.1. URL Service — Bảng `urls`

| Operation | SQL | Function | Layer | Transaction |
|-----------|-----|----------|-------|-------------|
| **C**reate | `INSERT INTO urls (id, short_code, original_url, user_id, user_email, created_at, expires_at, is_active) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)` | `Insert` | store.go:39 | Cần (nhận tx từ caller) |
| **R**ead by code | `SELECT ... FROM urls WHERE short_code = $1` | `FindByCode` | store.go:45 | Không |
| **R**ead by user (list) | `SELECT ... FROM urls WHERE user_id = $1 AND id < $2 AND is_active = true AND (expires_at IS NULL OR expires_at > NOW()) ORDER BY id DESC LIMIT $3` | `FindByUserID` | store.go:59 | Không |
| **U**pdate (deactivate) | `UPDATE urls SET is_active = false WHERE short_code = $1 AND user_id = $2 AND is_active = true` | `Deactivate` | store.go:100 | Cần (nhận tx từ caller) |
| **D**elete | ❌ Không có hard delete | — | — | — |

**Phân tích operations:**

1. **Insert** yêu cầu transaction từ caller. Caller (HTTP handler) mở transaction, insert URL + insert outbox trong cùng atomic unit.

2. **FindByCode** không cần transaction — read-only, non-repeatable read không phải vấn đề.
   - Trả về `nil, nil` nếu không tìm thấy (dùng `pgx.ErrNoRows` để phân biệt với lỗi thật).
   - Không có cache layer trong store — caching được xử lý riêng ở handler (Redis cache).

3. **FindByUserID** implement cursor-based pagination:
   - Parameter `afterID` (cursor) — ID của record cuối cùng của page trước.
   - Fetch `limit + 1` records để xác định có next page hay không.
   - Filter: `is_active = true AND (expires_at IS NULL OR expires_at > NOW())`.
   - ORDER BY id DESC — keyset pagination dựa trên UUID (không liên quan đến thời gian thực).
   - **Lưu ý:** Dùng `id` cho cursor thay vì `created_at` — đơn giản hơn vì id là unique, không cần composite cursor.

4. **Deactivate** (soft delete):
   - Kiểm tra `user_id` để đảm bảo chỉ chủ sở hữu mới deactivate được.
   - Kiểm tra `is_active = true` để idempotent.
   - Return `pgx.ErrNoRows` nếu không có row nào được update (wrong code, wrong user, hoặc đã inactive).
   - Yêu cầu transaction (caller cũng insert outbox event "url.deleted").

### 6.2. URL Service — Bảng `outbox`

| Operation | SQL | Function | Layer |
|-----------|-----|----------|-------|
| **C**reate | `INSERT INTO outbox (id, event_type, payload, created_at) VALUES ($1, $2, $3, $4)` | `InsertEvent` | outbox_store.go:35 |
| **R**ead (fetch unpublished) | CTE: claim + update — `WITH claimed AS (...) UPDATE ... RETURNING ...` | `FetchUnpublished` | outbox_store.go:45 |
| **U**pdate (mark published) | `UPDATE outbox SET published_at = now(), locked_until = NULL WHERE id = $1 AND published_at IS NULL` | `MarkPublished` | outbox_store.go:81 |

**Phân tích:**

1. **InsertEvent** hỗ trợ cả transaction và non-transaction mode (nếu tx == nil, dùng pool.Exec). Transaction mode dùng khi insert cùng lúc với URL trong cùng atomic operation.

2. **FetchUnpublished** dùng CTE với FOR UPDATE SKIP LOCKED — pattern phức tạp nhất. Không cần transaction (cơ chế locking tự xử lý).

3. **MarkPublished** dùng `AND published_at IS NULL` để idempotent. Nếu đã được đánh dấu published, không update gì cả và return `pgx.ErrNoRows`.

### 6.3. Analytics Service — Bảng `clicks`

| Operation | SQL | Function | Layer |
|-----------|-----|----------|-------|
| **C**reate | `INSERT INTO clicks (short_code, clicked_at, ip_hash, user_agent, referer) VALUES ($1, $2, $3, $4, $5)` | `Insert` | store.go:108 |
| **R**ead count | `SELECT COUNT(*) FROM clicks WHERE short_code = $1` | `CountByCode` | store.go:122 |
| **R**ead count since | `SELECT COUNT(*) FROM clicks WHERE short_code = $1 AND clicked_at >= $2` | `CountByCodeSince` | store.go:126 |
| **R**ead top referers | `SELECT referer, COUNT(*) AS cnt FROM clicks WHERE short_code = $1 AND referer IS NOT NULL GROUP BY referer ORDER BY cnt DESC LIMIT $2` | `TopReferers` | store.go:130 |
| **R**ead timeline | `SELECT date_trunc($1, clicked_at AT TIME ZONE 'UTC') AS period, COUNT(*) AS clicks FROM clicks WHERE short_code = $2 GROUP BY period ORDER BY period ASC` | `TimeLineBuckets` | store.go:140 |
| **U**pdate | ❌ Không có | — | — |
| **D**elete | ❌ Không có | — | — |

**Phân tích:**
- **Append-only:** Bảng clicks là append-only — không có UPDATE hay DELETE. Điều này tốt cho performance (không page fragmentation do update).
- **COUNT(*) trên bảng lớn:** Có thể rất chậm. PostgreSQL COUNT(*) cần full index scan hoặc sequential scan. Với bảng tỷ dòng, COUNT có thể mất hàng phút.
- **Giải pháp tiềm năng:**
  - Materialized view cập nhật định kỳ.
  - Click count lưu trong Redis (cache trong url-service đã có Redis).
  - Approximate count (HyperLogLog, PostgreSQL extension).
  - Time-bucketed counting table.

### 6.4. Analytics Service — Bảng `milestones`

| Operation | SQL | Function | Layer |
|-----------|-----|----------|-------|
| **R**ead | `SELECT EXISTS(SELECT 1 FROM milestones WHERE short_code = $1 AND milestone = $2)` | `HasMilestone` | store.go:156 |
| **C**reate | `INSERT INTO milestones (short_code, milestone) VALUES ($1, $2) ON CONFLICT (short_code, milestone) DO NOTHING` | `Insert` | store.go:160 |

- Insert dùng ON CONFLICT DO NOTHING — idempotent.
- Yêu cầu transaction (caller quản lý transaction xử lý event).

### 6.5. Analytics Service — Bảng `processed_events`

| Operation | SQL | Function | Layer |
|-----------|-----|----------|-------|
| **R**ead | `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)` | `Exists` | store.go:173 |
| **C**reate | `INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING` | `Insert` | store.go:177 |

- Idempotency table. ON CONFLICT DO NOTHING trên PK (event_id).
- Yêu cầu transaction.

### 6.6. Notification Service — Bảng `notifications`

| Operation | SQL | Function | Layer |
|-----------|-----|----------|-------|
| **C**reate + U**p**date | Transaction: INSERT status='pending' → mock email → UPDATE status='sent' | `InsertNotification` | store.go:75 |
| **R**ead (list) | `SELECT ... FROM notifications WHERE user_id = $1 AND (created_at, id) < (SELECT ...) ORDER BY created_at DESC, id DESC LIMIT $3` | `ListByUser` | store.go:113 |

**Phân chi tiết InsertNotification:**
```go
tx, err := s.pool.Begin(ctx)
// ... INSERT ... RETURNING id, created_at (status = 'pending')
// ... mock email ...
// ... UPDATE SET status = 'sent', sent_at = now()
// ... tx.Commit()
```

- Transaction đảm bảo: notification được lưu KHI VÀ CHỈ KHI email đã gửi thành công.
- Status flow: `pending` → `sent`.
- Lưu ý: `defer tx.Rollback(ctx)` + `committed` flag — nếu có panic hoặc lỗi, tự động rollback.

**ListByUser pagination:**
```sql
-- Cursor-based với composite cursor:
AND (created_at, id) < (
    SELECT created_at, id FROM notifications WHERE id = $2 AND user_id = $1
)
```

- Composite cursor (created_at, id) — cần thiết vì created_at không unique.
- Sắp xếp: `ORDER BY created_at DESC, id DESC` — ổn định, deterministic.

### 6.7. User Service — Bảng `users`

| Operation | SQL | Function | Layer |
|-----------|-----|----------|-------|
| **C**reate | `INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id, email, password_hash, created_at` | `Insert` | store.go:35 |
| **R**ead by email | `SELECT id, email, password_hash, created_at FROM users WHERE email = $1` | `FindByEmail` | store.go:57 |
| **U**pdate | ❌ Không có (password reset, email change chưa implement) | — | — |
| **D**elete | ❌ Không có | — | — |

**Phân tích:**
- **RETURNING** trong INSERT — lấy toàn bộ record sau khi insert, tránh phải SELECT lại.
- **FindByEmail** — equality lookup, dùng unique index trên email.
- **Thiếu operations:** Không có update profile, change password, delete account. Scope MVP.

### 6.8. Data Ownership Matrix

| Entity | Owned By | Stored In | Referenced By (logical) | Sync Method |
|--------|----------|-----------|------------------------|-------------|
| User | user-service | userdb.users | urls.user_id, notifications.user_id | Eventual (via RabbitMQ) |
| URL | url-service | urldb.urls | clicks.short_code, milestones.short_code | Eventual (via API) |
| Click | analytics-service | analyticsdb.clicks | — | — |
| Notification | notification-service | notificationdb.notifications | — | — |
| Outbox | url-service | urldb.outbox | — | — |
| Milestone | analytics-service | analyticsdb.milestones | — | — |
| Processed Event | analytics-service | analyticsdb.processed_events | — | — |

**Nguyên tắc:**
- Mỗi service là single source of truth cho dữ liệu của mình.
- Cross-service references là logical — không có FK constraint.
- Dữ liệu được đồng bộ qua RabbitMQ events.
- Cache (Redis) có thể lưu dữ liệu từ nhiều service nhưng không phải source of truth.

---

## 7. Cross-Service Data Access Patterns

### 7.1. Event-Driven Communication (RabbitMQ)

Kiến trúc giao tiếp chính giữa các service:

```
URL Service                    Analytics Service
┌──────────────────┐           ┌──────────────────┐
│ url.created ─────┼──AMQP───→│ Consumer         │
│ url.deleted ─────┼──AMQP───→│ Idempotency      │
│                  │           │ Milestone Check  │
│ Outbox Producer  │           │ Click Storage    │
│ Coordinator      │           └──────────────────┘
└──────────────────┘                      │
        │                                 │
        │                    ┌────────────┘
        ▼                    ▼
┌─────────────────────────────────────────┐
│          Notification Service           │
│  url.created → INSERT notification      │
│  milestone.reached → INSERT notification│
│  Mock email send                        │
└─────────────────────────────────────────┘
```

**Flow chi tiết — URL Created:**

1. **HTTP Request** → Gateway → URL Service `POST /shorten`
2. **URL Service:**
   - `BEGIN TRANSACTION`
   - INSERT INTO urls (...)
   - INSERT INTO outbox (event_type='url.created', payload={...})
   - `COMMIT`
3. **Outbox Coordinator** (background goroutine):
   - Poll outbox table (mỗi ~1-5 giây)
   - Publish event to RabbitMQ exchange
   - UPDATE outbox SET published_at = now()
4. **Analytics Service Consumer:**
   - Nhận event từ RabbitMQ
   - Check processed_events (idempotency)
   - Process (không có click data cho url.created — chỉ lưu event để futures)
   - INSERT INTO processed_events (event_id)
5. **Notification Service Consumer:**
   - Nhận event từ RabbitMQ
   - INSERT INTO notifications (user_id, event_type, payload)
   - Mock gửi email: `s.log.Info("mock email sent", "to", userEmail, "type", event_type)`
   - UPDATE notifications SET status = 'sent', sent_at = now()

**Flow chi tiết — URL Clicked:**

1. **HTTP Request** → Gateway (nginx) → URL Service `GET /{code}`
2. **URL Service:**
   - Lookup short_code in DB (hoặc Redis cache)
   - Redirect HTTP 301/302 đến original_url
   - Publish URLClickedEvent đến RabbitMQ (qua Outbox)
3. **Analytics Service Consumer:**
   - Receive URLClickedEvent
   - INSERT INTO clicks (short_code, clicked_at, ip_hash, user_agent, referer)
   - Check milestone: `SELECT COUNT(*) FROM clicks WHERE short_code = $1`
   - Nếu đạt milestone (100, 1000, ...): INSERT INTO milestones + publish MilestoneReachedEvent
4. **Notification Service:**
   - Receive MilestoneReachedEvent
   - INSERT INTO notifications + mock email

### 7.2. Synchronous Communication (HTTP API)

**Gateway Service** đóng vai trò API gateway, gọi synchronous qua HTTP:

```
Client → Gateway → URL Service (REST API)
                → Analytics Service (REST API)
                → User Service (REST API)
                → Notification Service (REST API)
```

- **GET /urls** — Gateway gọi URL Service `GET /urls` → response với URLs list.
- **GET /stats/{code}** — Gateway gọi Analytics Service `GET /stats/{code}` → response với stats.
- **POST /register** — Gateway gọi User Service `POST /register`.
- **GET /notifications** — Gateway gọi Notification Service `GET /notifications`.

### 7.3. Event Types (shared/events.go)

```
EventTypeURLCreated       = "url.created"         → Notification Service
EventTypeURLClicked       = "url.clicked"          → Analytics Service
EventTypeURLDeleted       = "url.deleted"          → Notification Service
EventTypeMilestoneReached = "milestone.reached"    → Notification Service
```

**Phân tích:**
- `url.created` → Chỉ notification service consume. Analytics service có thể ignore hoặc subscribe sau này.
- `url.clicked` → Analytics service consume để ghi click data.
- `url.deleted` → Notification service consume để thông báo.
- `milestone.reached` → Notification service consume để chúc mừng.

### 7.4. Cross-Service Reference Resolution

Khi cần dữ liệu từ service khác, các pattern được dùng:

1. **Denormalization (URL Service):** `urls.user_email` lưu email của user — không cần gọi user-service khi cần email cho notification.
2. **API Composition (Gateway):** Gateway gọi nhiều service để gom dữ liệu cho response.
3. **Event-Driven Replication:** Dữ liệu được đồng bộ qua RabbitMQ (ví dụ: user_id và user_email trong URLCreatedEvent).
4. **Cache (Redis):** url-service cache short_code → original_url trong Redis, giảm tải DB.

---

## 8. Docker Compose DB Configuration

### 8.1. Cấu Hình Chi Tiết

```yaml
# Từ docker-compose.yml
url_db:
  image: postgres:16-alpine
  environment:
    POSTGRES_DB: urldb
    POSTGRES_USER: urluser
    POSTGRES_PASSWORD: urlpass
  ports:
    - "5432:5432"
  volumes:
    - url_db_data:/var/lib/postgresql/data
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U urluser -d urldb"]
    interval: 5s
    timeout: 5s
    retries: 10
    start_period: 10s
```

**4 container giống hệt nhau với 4 cấu hình khác nhau:**

| Container | Host Port | Container Port | User | Password | Database | Volume |
|-----------|-----------|---------------|------|----------|----------|--------|
| url_db | 5432 | 5432 | urluser | urlpass | urldb | url_db_data |
| analytics_db | 5433 | 5432 | analyticsuser | analyticspass | analyticsdb | analytics_db_data |
| user_db | 5434 | 5432 | useruser | userpass | userdb | user_db_data |
| notification_db | 5435 | 5432 | notificationuser | notificationpass | notificationdb | notification_db_data |

### 8.2. Phân Tích Host Port Mapping

Mỗi container DB map ra host port riêng (5432-5435):
- **Phát triển local:** Cho phép developer kết nối trực tiếp bằng `psql -h localhost -p 5433 -U analyticsuser -d analyticsdb`.
- **Adminer UI:** Có service adminer trên port 8090, cho phép quản lý tất cả databases qua web UI.
- **Security:** Port mapping chỉ cần cho development. Trong docker-compose.scale.yml, host ports được reset (bỏ) cho production-like environment.

### 8.3. Named Volumes

Mỗi database có volume riêng:
```yaml
volumes:
  url_db_data:
  analytics_db_data:
  user_db_data:
  notification_db_data:
```

- **Docker managed volumes** — dữ liệu tồn tại ngay cả khi container bị xóa.
- **Location mặc định:** `/var/lib/docker/volumes/...` (Linux).
- **Không có bind mount:** Dùng volume driver mặc định (local). Không thể dễ dàng backup bằng cách copy folder — phải dùng `docker run --rm -v url_db_data:/data alpine tar czf /backup.tar.gz /data`.

### 8.4. Health Checks

Mỗi DB container có health check với:
- **Command:** `pg_isready -U [user] -d [dbname]` — kiểm tra PostgreSQL ready để accept connections.
- **Interval:** 5 giây — kiểm tra thường xuyên.
- **Timeout:** 5 giây — mỗi lần kiểm tra tối đa 5 giây.
- **Retries:** 10 — cho phép tối đa 10 lần thất bại liên tiếp (50 giây) trước khi đánh dấu unhealthy.
- **Start period:** 10 giây — không tính trong retries, cho PostgreSQL khởi động lần đầu.

**Services depends_on:**
```yaml
depends_on:
  url_db:
    condition: service_healthy
```

Điều này đảm bảo service chỉ bắt đầu khi DB đã ready. Ngăn race condition khi service start trước DB.

### 8.5. Network

```yaml
networks:
  url-shortener:
    driver: bridge
```

Tất cả container trong cùng bridge network `url-shortener`:
- Service connect đến DB bằng hostname container (ví dụ: `url_db:5432`).
- DNS resolution do Docker Compose tự động quản lý.
- Chỉ container trong network mới giao tiếp được — cô lập với các Docker network khác.

### 8.6. Connection Strings (DATABASE_URL)

```yaml
# URL Service
DATABASE_URL: postgres://urluser:urlpass@url_db:5432/urldb?sslmode=disable

# Analytics Service  
DATABASE_URL: postgres://analyticsuser:analyticspass@analytics_db:5432/analyticsdb?sslmode=disable

# User Service
DATABASE_URL: postgres://useruser:userpass@user_db:5432/userdb?sslmode=disable

# Notification Service
DATABASE_URL: postgres://notificationuser:notificationpass@notification_db:5432/notificationdb?sslmode=disable
```

- **sslmode=disable:** Chấp nhận được trong internal Docker network (không exposure ra ngoài). Không nên dùng cho production cross-network.
- **Password trong URL:** Security risk nếu URL bị log. User-service có `maskDBSecret()` function che giấu password trong log.

---

## 9. PostgreSQL Connection Pooling

### 9.1. Pool Configuration (db.go - tất cả services)

```go
func NewDBPool(ctx context.Context, databaseURL string, log *slog.Logger) (*pgxpool.Pool, error) {
    cfg, err := pgxpool.ParseConfig(databaseURL)
    if err != nil {
        return nil, fmt.Errorf("parse db url: %w", err)
    }
    cfg.MaxConns = 10
    cfg.MinConns = 2
    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    if err != nil {
        return nil, fmt.Errorf("create pool: %w", err)
    }
    pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    if err := pool.Ping(pingCtx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("ping db: %w", err)
    }
    return pool, nil
}
```

**Tất cả 4 services dùng cấu hình giống hệt nhau:**
- **Driver:** `pgx/v5/pgxpool` — connection pool implementation of `jackc/pgx`.
- **MaxConns:** 10 — tối đa 10 connections đến database.
- **MinConns:** 2 — luôn duy trì 2 connections sẵn sàng.
- **Ping timeout:** 10 giây để kiểm tra kết nối khi khởi tạo.

### 9.2. Phân Tích Pool Configuration

#### 9.2.1. MaxConns = 10

- **Phù hợp?** 10 connections cho URL service (xử lý redirect — I/O bound) là hợp lý.
- **Công thức chung:** MaxConns = ((core_count × 2) + effective_spindle_count). Với application I/O bound, số connections nên cao hơn CPU-bound app.
- **Docker resource:** Nếu container bị giới hạn CPU (ví dụ: 2 CPUs), 10 connections có thể quá nhiều (context switching overhead).
- **Database capacity:** 4 services × 10 connections = 40 connections tổng cộng. PostgreSQL mặc định max_connections = 100. OK cho development nhưng cần monitor cho production.

#### 9.2.2. MinConns = 2

- **Mục đích:** Giữ 2 connections luôn sẵn sàng, giảm latency cho request đầu tiên sau idle period.
- **pgxpool behavior:** Khi pool được tạo, nó spawn 2 connections và kiểm tra health.
- **Resource usage:** 2 idle connections mỗi service × 4 services = 8 connections luôn open.
- **Trade-off:** Giảm cold-start latency nhưng tiêu tốn tài nguyên DB.

#### 9.2.3. Thiếu Cấu Hình Quan Trọng

pgxpool hỗ trợ nhiều cấu hình không được set:

| Config | Current | Default | Recommendation |
|--------|---------|---------|---------------|
| MaxConnLifetime | Not set (default) | 1 hour | Set 30-60 phút để tránh connection leak |
| MaxConnIdleTime | Not set (default) | 30 minutes | Set 5-10 phút để giải phóng idle connections |
| HealthCheckPeriod | Not set (default) | 30 seconds | OK với default |
| LazyConnect | Not set (default) | false | OK — connect khi khởi tạo |
| AfterConnect | Not set | nil | Có thể set prepared statements |
| BeforeClose | Not set | nil | Có thể set cleanup logic |

#### 9.2.4. Connection String Options

Chỉ có `sslmode=disable`. Các option khác không được set:
- `pool_max_conns=10` — có thể set trong URL thay vì code.
- `pool_min_conns=2` — tương tự.
- `statement_cache_capacity=256` — prepared statement cache mặc định 256.
- `default_query_exec_mode=simple` — mặc định là extended protocol.

### 9.3. Transaction Handling Pattern

#### 9.3.1. Transaction từ Caller

```go
// url-service handler (ví dụ)
func (h *HTTPHandler) HandleShorten(w http.ResponseWriter, r *http.Request) {
    tx, _ := h.pool.Begin(r.Context())
    defer tx.Rollback(r.Context())
    
    h.urlStore.Insert(r.Context(), tx, record)
    h.outboxStore.InsertEvent(r.Context(), tx, outbox)
    
    tx.Commit(r.Context())
}
```

- **pool.Begin()** — bắt đầu transaction trên connection từ pool.
- **Transaction object (pgx.Tx)** được truyền cho store functions.
- **defer tx.Rollback()** — rollback nếu có panic hoặc lỗi.
- **tx.Commit()** — commit và trả connection về pool.

#### 9.3.2. Transaction trong Store

```go
// notification-service store.go
func (s *pgxNotificationStore) InsertNotification(ctx context.Context, rec *NotificationRecord) (*Notification, error) {
    tx, _ := s.pool.Begin(ctx)           // Bắt đầu transaction
    defer func() {
        if !committed {
            _ = tx.Rollback(ctx)          // Rollback nếu chưa commit
        }
    }()
    // ... INSERT notifications ...
    // ... mock email ...
    // ... UPDATE notifications SET status='sent' ...
    tx.Commit(ctx)                        // Commit
}
```

- Transaction được quản lý trong store (không phải caller). Pattern không nhất quán giữa các service.

### 9.4. So Sánh: database/sql vs pgxpool

| Tính năng | database/sql | pgxpool |
|-----------|-------------|---------|
| Driver | Generic interface | PostgreSQL-native |
| Connection pool | db.SetMaxOpenConns() | Built-in |
| Prepared statements | Global cache | Per-connection cache |
| COPY protocol | Không hỗ trợ | Hỗ trợ |
| LISTEN/NOTIFY | Không hỗ trợ | Hỗ trợ |
| Type mapping | scan into basic types | Hỗ trợ JSONB, UUID, array native |
| Performance | ~2-3x overhead | Native fast path |

Dự án chọn pgxpool — đúng đắn cho PostgreSQL-only stack.

### 9.5. Pool Monitoring

Không có monitoring code cho pool trong các service hiện tại. Các metric quan trọng nên monitor:
- `pool.AcquireCount()` — số connection đã acquire.
- `pool.TotalConns()` — tổng connections.
- `pool.IdleConns()` — connections idle.
- `pool.AcquireDuration()` — thời gian chờ connection.

Các service có Prometheus metrics (`/metrics`) nhưng chưa có pool metrics.

---

## 10. Đánh Giá Tổng Thể và Khuyến Nghị

### 10.1. Điểm Mạnh

1. **Database-per-Service triệt để:** Mỗi service có database riêng, user riêng, volume riêng. Cô lập hoàn hảo.
2. **Transactional Outbox Pattern:** Implement đúng với atomic write + FOR UPDATE SKIP LOCKED + multi-replica safe.
3. **Idempotency:** processed_events table + ON CONFLICT DO NOTHING pattern.
4. **Soft Delete:** is_active column cho phép "xóa" URL mà không mất dữ liệu.
5. **Migration idempotent:** IF NOT EXISTS pattern cho phép chạy migration nhiều lần.
6. **Composite indexes:** Được design tốt với leading column matching WHERE clause.
7. **Partial indexes:** Hiệu quả cho outbox (unpublished rows) và clicks (referer rows).
8. **Cursor-based pagination:** Keyset pagination scalable hơn OFFSET/LIMIT.
9. **UUID primary keys:** Phù hợp với distributed systems.
10. **Denormalization thông minh:** user_email trong urls bảng giảm cross-service calls.

### 10.2. Điểm Yếu

#### 10.2.1. Redundant Indexes (Critical)

3 cặp index duplicate:
- idx_urls_short_code (duplicate với inline UNIQUE)
- idx_milestones_code_milestone (duplicate với inline UNIQUE)
- idx_users_email (duplicate với inline UNIQUE)

**Impact:** Disk space lãng phí, write performance giảm, maintenance overhead.

#### 10.2.2. Migration Strategy (High)

- Không versioning, không rollback, không validation.
- Một file SQL duy nhất — không scale cho nhiều thay đổi.
- Không transaction cho migration.
- Rủi ro cao cho production.

#### 10.2.3. Thiếu Constraints (Medium)

- Không CHECK constraint cho status (notifications), email format (users), URL format (urls).
- Không foreign key (có chủ đích nhưng thiếu documentation).
- Không NOT NULL validation ở DB level (dựa vào application).

#### 10.2.4. Click Table (Medium-High)

- Không partition — bảng lớn nhất nhưng không có time-based partitioning.
- COUNT(*) trên bảng lớn — performance issue tiềm ẩn.
- Không TTL/retention policy.
- Không archive strategy.

#### 10.2.5. Pool Configuration (Low)

- Thiếu MaxConnLifetime và MaxConnIdleTime.
- Pool metrics chưa export.
- Cấu hình giống nhau cho mọi service (không tối ưu riêng).

### 10.3. So Sánh Với Best Practices

| Tiêu chí | Trạng thái | Best Practice |
|----------|-----------|---------------|
| Database per service | ✅ Đúng | ✅ |
| Migration versioning | ❌ Không | golang-migrate / goose |
| Transactional outbox | ✅ Đúng | ✅ |
| Idempotent consumers | ✅ processed_events | ✅ |
| Circuit breaker | ✅ Có (monitoring) | ✅ |
| Soft delete | ✅ is_active | ✅ |
| Cursor pagination | ✅ id-based | ✅ |
| UUID PK | ✅ v4 (random) | ⚠️ Nên dùng v7 |
| Time-series partition | ❌ Không | ⚠️ Cần cho clicks |
| Index review | ❌ 3 duplicate | ⚠️ Nên cleanup |
| DB monitoring | ❌ Chưa | ✅ Nên thêm |
| Connection pool tuning | ⚠️ Thiếu config | ✅ Nên cấu hình lifetime |

### 10.4. Khuyến Nghị Cụ Thể

#### Priority 1 (Critical — Performance)

1. **Xóa 3 redundant indexes:**
   ```sql
   DROP INDEX IF EXISTS idx_urls_short_code;
   DROP INDEX IF EXISTS idx_milestones_code_milestone;
   DROP INDEX IF EXISTS idx_users_email;
   ```

2. **Thêm covering columns cho composite indexes:**
   ```sql
   CREATE INDEX IF NOT EXISTS idx_urls_user_id_created 
       ON urls(user_id, created_at DESC) INCLUDE (short_code, original_url, is_active);
   -- Cho phép index-only scan cho FindByUserID query
   ```

3. **Thêm partial index cho active URLs:**
   ```sql
   CREATE INDEX IF NOT EXISTS idx_urls_active_short_code 
       ON urls(short_code) WHERE is_active = true;
   -- Redirect chỉ cần active URLs
   ```

#### Priority 2 (High — Correctness)

4. **Migration versioning:** Dùng golang-migrate.
5. **Migration transaction:** Bọc toàn bộ migration trong BEGIN/COMMIT.
6. **CHECK constraints:** Thêm validation ở DB level.
7. **processed_events TTL:** Thêm cleanup job (pg_cron hoặc application).

#### Priority 3 (Medium — Performance)

8. **Click table partitioning:**
   ```sql
   CREATE TABLE clicks (...) PARTITION BY RANGE (clicked_at);
   CREATE TABLE clicks_2026_07 PARTITION OF clicks ...;
   ```

9. **Count optimization:** Materialized view cho click counts, cập nhật định kỳ.
10. **UUID v7 migration:** Giảm B-tree fragmentation.

#### Priority 4 (Low — Maintenance)

11. **Pool config tuning:** Thêm MaxConnLifetime, MaxConnIdleTime.
12. **Pool metrics:** Export pool stats qua Prometheus.
13. **Backup strategy:** Document backup/restore procedure cho 4 databases.

### 10.5. Kết Luận

Database-per-Service pattern được triển khai tốt trong dự án URL Shortener Microservices. Bốn database riêng biệt với 7 bảng và 19 chỉ mục (16 hiệu dụng) tạo nên nền tảng dữ liệu vững chắc. Transactional Outbox pattern với FOR UPDATE SKIP LOCKED là điểm sáng về kỹ thuật, cho phép reliable event publishing mà không cần distributed transaction.

Tuy nhiên, có 3 vấn đề chính cần giải quyết: (1) redundant indexes gây lãng phí, (2) chiến lược migration thiếu versioning và rollback, (3) thiếu partition cho bảng clicks — bảng có tốc độ tăng trưởng nhanh nhất.

Với các cải thiện về index cleanup, migration strategy, và time-series partitioning, hệ thống database sẵn sàng cho production workload.

---

*Tài liệu được tạo tự động dựa trên phân tích mã nguồn. Mọi số liệu về kích thước index và dung lượng là ước tính.*
