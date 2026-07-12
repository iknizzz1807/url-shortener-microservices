# Phân Tích Chi Tiết: Event-Driven Architecture, Analytics Service & Notification Service

> **Dự án:** URL Shortener Microservices  
> **Tác giả:** AI Agent  
> **Phiên bản:** 1.0  
> **Mục tiêu:** Phân tích cực kỳ chi tiết kiến trúc hướng sự kiện (Event-Driven Architecture), các event types, RabbitMQ configuration, luồng xử lý sự kiện, cơ chế đảm bảo delivery, và toàn bộ codebase của Analytics Service cùng Notification Service.  
> **Ngôn ngữ:** Tiếng Việt

---

## Mục Lục

1. [Tổng Quan về Event-Driven Architecture (EDA)](#1-tổng-quan-về-event-driven-architecture-eda)
2. [Định Nghĩa Event Types](#2-định-nghĩa-event-types)
3. [Cấu Hình RabbitMQ (Exchange, Queue, Binding, Routing)](#3-cấu-hình-rabbitmq)
4. [Sơ Đồ Luồng Sự Kiện (Event Flow Diagram)](#4-sơ-đồ-luồng-sự-kiện)
5. [Phân Tích Chi Tiết Analytics Service](#5-phân-tích-analytics-service)
6. [Phân Tích Chi Tiết Notification Service](#6-phân-tích-notification-service)
7. [Phân Tích Delivery Guarantees](#7-phân-tích-delivery-guarantees)
8. [Phân Tích Reconnection Handling](#8-phân-tích-reconnection-handling)
9. [Stats API Endpoints](#9-stats-api-endpoints)
10. [Bảng Tổng Kết](#10-bảng-tổng-kết)
11. [Kết Luận](#11-kết-luận)

---

## 1. Tổng Quan về Event-Driven Architecture (EDA)

### 1.1. Giới thiệu

URL Shortener Microservices sử dụng **Event-Driven Architecture (EDA)** — một mô hình kiến trúc trong đó các service giao tiếp với nhau thông qua các sự kiện (events) thay vì gọi API trực tiếp (synchronous HTTP calls). Điều này mang lại tính loose coupling (liên kết lỏng lẻo), khả năng mở rộng độc lập, và độ tin cậy cao hơn.

### 1.2. Các thành phần chính trong EDA

| Thành phần | Mô tả |
|---|---|
| **Event Producers** | Các service phát sinh sự kiện (URL Service — tạo/xóa URL, Gateway/Router — click URL) |
| **Message Broker** | RabbitMQ — exchange topic `url-shortener`, chịu trách nhiệm nhận, định tuyến, và lưu trữ message |
| **Event Consumers** | Các service nhận và xử lý sự kiện (Analytics Service, Notification Service) |
| **Event Schema** | Định nghĩa cấu trúc dữ liệu của từng loại sự kiện (shared/events/events.go) |
| **Dead Letter / Retry** | (Chưa triển khai — sẽ phân tích ở phần Reconnection Handling) |

### 1.3. Nguyên lý hoạt động

1. **Producer** publish event lên RabbitMQ exchange với một routing key cụ thể.
2. **RabbitMQ exchange** (loại `topic`) kiểm tra routing key và chuyển message đến tất cả các queue có binding key khớp.
3. **Consumer** nhận message từ queue, xử lý, và gửi acknowledgment (ACK) hoặc negative acknowledgment (NACK).
4. Nếu consumer xử lý thành công, message bị xóa khỏi queue. Nếu thất bại, message được requeue để thử lại.

### 1.4. Lợi ích của EDA trong dự án này

- **Decoupling:** URL Service không cần biết sự tồn tại của Analytics Service hay Notification Service. Nó chỉ cần publish event.
- **Scalability độc lập:** Analytics Service có thể scale lên 10 instance mà không ảnh hưởng đến URL Service. Mỗi instance nhận message từ cùng một queue qua cơ chế competing consumers.
- **Fault tolerance:** Nếu Notification Service bị down, message vẫn nằm trong queue RabbitMQ (persistent). Khi service online trở lại, nó tiếp tục xử lý từ vị trí đã dừng.
- **Audit trail:** Mỗi sự kiện đều có event_id UUID và correlation_id, cho phép tracing toàn bộ luồng xử lý.
- **Async processing:** Click event được xử lý bất đồng bộ — gateway không phải chờ analytics ghi database.

---

## 2. Định Nghĩa Event Types

### 2.1. File: `shared/events/events.go`

Package `events` định nghĩa tất cả các loại sự kiện dùng chung trong toàn bộ hệ thống. Đây là **event schema contract** giữa các service.

### 2.2. Event Type Constants

```go
const (
    EventTypeURLCreated       EventType = "url.created"
    EventTypeURLClicked       EventType = "url.clicked"
    EventTypeURLDeleted       EventType = "url.deleted"
    EventTypeMilestoneReached EventType = "milestone.reached"
)
```

Mỗi event type là một string đơn giản theo chuẩn `{entity}.{action}`:
- `url.created` — URL mới được tạo
- `url.clicked` — URL short được click
- `url.deleted` — URL bị xóa
- `milestone.reached` — URL đạt mốc click (10, 100, 1000)

### 2.3. BaseEvent — Event Envelope Chung

```go
type BaseEvent struct {
    EventType     string    `json:"event_type"`
    OccurredAt    time.Time `json:"occurred_at"`
    CorrelationID string    `json:"correlation_id"`
    EventID       string    `json:"event_id"`
}
```

BaseEvent là struct nền tảng cho mọi event. Nó chứa:

| Field | Kiểu | Ý nghĩa |
|---|---|---|
| `EventType` | `string` | Loại sự kiện, giúp consumer phân biệt mà không cần parse toàn bộ payload |
| `OccurredAt` | `time.Time` | Thời điểm sự kiện được tạo (UTC) |
| `CorrelationID` | `string` | ID xuyên suốt (tracing), giúp theo dõi một request từ đầu đến cuối qua nhiều service |
| `EventID` | `string` | UUID v4 duy nhất cho mỗi event — dùng để deduplication |

Hàm `NewBaseEvent` tự động sinh UUID v4 cho EventID và set `OccurredAt` là `time.Now().UTC()`.

### 2.4. URLCreatedEvent

```go
type URLCreatedEvent struct {
    BaseEvent
    ShortCode   string     `json:"short_code"`
    OriginalURL string     `json:"original_url"`
    UserID      string     `json:"user_id"`
    UserEmail   string     `json:"user_email"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}
```

- **Mục đích:** Thông báo rằng một short URL mới đã được tạo.
- **Produced bởi:** URL Service (khi user tạo URL mới).
- **Consumed bởi:** Notification Service.
- **Flow:** URL Service → RabbitMQ Exchange → notifications.events queue → Notification Service → ghi notification vào DB, mock email.
- **Field `ExpiresAt`:** Optional (con trỏ), cho phép URL có thời gian hết hạn. Nếu nil, URL không bao giờ hết hạn.
- **Field `UserEmail`:** Denormalized field — giúp Notification Service gửi email mà không cần query user-service.

### 2.5. URLClickedEvent

```go
type URLClickedEvent struct {
    BaseEvent
    ShortCode string    `json:"short_code"`
    UserID    string    `json:"user_id"`
    UserEmail string    `json:"user_email"`
    IPHash    string    `json:"ip_hash"`
    UserAgent string    `json:"user_agent"`
    Referer   string    `json:"referer,omitempty"`
    ClickedAt time.Time `json:"clicked_at"`
}
```

- **Mục đích:** Thông báo rằng một short URL đã được click.
- **Produced bởi:** Gateway / Redirect handler (khi có request redirect).
- **Consumed bởi:** Analytics Service.
- **Flow:** Gateway → RabbitMQ Exchange → analytics.clicks queue → Analytics Service → ghi clicks, process dedup, kiểm tra milestone.
- **Field `IPHash`:** Đây là hash của IP người dùng (không phải IP thô), tuân thủ privacy/GDPR. Salt được cấu hình qua biến môi trường `IP_HASH_SALT`.
- **Field `UserAgent`:** User-Agent header từ trình duyệt.
- **Field `Referer`:** HTTP Referer header, có thể empty (omitempty trong JSON).
- **Field `ClickedAt`:** Thời điểm click xảy ra (do gateway ghi nhận).

### 2.6. URLDeletedEvent

```go
type URLDeletedEvent struct {
    BaseEvent
    ShortCode string `json:"short_code"`
    UserID    string `json:"user_id"`
    UserEmail string `json:"user_email"`
}
```

- **Mục đích:** Thông báo rằng một short URL đã bị xóa.
- **Produced bởi:** URL Service.
- **Consumed bởi:** Notification Service.
- **Flow:** URL Service → RabbitMQ Exchange → notifications.events queue → Notification Service.

### 2.7. MilestoneReachedEvent

```go
type MilestoneReachedEvent struct {
    BaseEvent
    ShortCode   string `json:"short_code"`
    UserID      string `json:"user_id"`
    UserEmail   string `json:"user_email"`
    MilestoneN  int    `json:"milestone"`
    TotalClicks int64  `json:"total_clicks"`
}
```

- **Mục đích:** Thông báo rằng một short URL đã đạt mốc click nhất định.
- **Produced bởi:** Analytics Service (MilestoneChecker).
- **Consumed bởi:** Notification Service.
- **Flow:** Analytics Service (MilestoneChecker) → RabbitMQ Exchange → notifications.events queue → Notification Service.
- **Field `MilestoneN`:** Giá trị mốc (10, 100, hoặc 1000).
- **Field `TotalClicks`:** Tổng số click hiện tại của URL tại thời điểm đạt mốc.

### 2.8. Bảng tổng hợp Event Types

| Event Type | Producer | Consumer | Queue | Routing Key |
|---|---|---|---|---|
| `url.created` | URL Service | Notification Service | `notifications.events` | `url.created` |
| `url.clicked` | Gateway | Analytics Service | `analytics.clicks` | `url.clicked` |
| `url.deleted` | URL Service | Notification Service | `notifications.events` | `url.deleted` |
| `milestone.reached` | Analytics Service | Notification Service | `notifications.events` | `milestone.reached` |

---

## 3. Cấu Hình RabbitMQ

### 3.1. Exchange Topic

Cả ba service (URL Service, Analytics Service, Notification Service) đều sử dụng **một exchange duy nhất**:

```
Exchange Name:  "url-shortener"
Exchange Type:  "topic"
Durable:        true
Auto-Delete:    false
Internal:       false
No-Wait:        false
```

Exchange được khai báo ở cả URL Service (`rabbitmq.go`), Analytics Service (`rabbitmq.go`), và Notification Service (`rabbitmq.go`). Cơ chế **idempotent declare** của RabbitMQ đảm bảo exchange chỉ được tạo một lần; các lần declare sau là no-op.

**Tại sao dùng `topic` exchange?**
- Topic exchange cho phép routing dựa trên routing key pattern.
- Một message với routing key `url.created` sẽ được gửi đến tất cả queue có binding key là `url.created`.
- Nếu sau này cần thêm consumer, chỉ cần bind queue mới với binding key tương ứng, không cần sửa producer.
- Hỗ trợ wildcard: `*` (một từ) và `#` (nhiều từ). Hiện tại dùng exact match.

### 3.2. Analytics Queue

**Định nghĩa trong:** `services/analytics-service/rabbitmq.go`

```go
const analyticsQueue = "analytics.clicks"
```

**Queue Declaration:**
```go
ch.QueueDeclare(
    analyticsQueue,  // name = "analytics.clicks"
    true,            // durable
    false,           // autoDelete
    false,           // exclusive
    false,           // noWait
    nil,             // args
)
```

- **Durable (true):** Queue được ghi vào disk. Nếu RabbitMQ restart, queue và messages không bị mất.
- **Auto-delete (false):** Queue không tự động xóa khi consumer cuối cùng ngắt kết nối.
- **Exclusive (false):** Queue có thể được truy cập bởi nhiều consumer (competing consumers pattern).

**Queue Binding:**
```go
ch.QueueBind(
    analyticsQueue,                          // queue name
    string(events.EventTypeURLClicked),      // routing key = "url.clicked"
    analyticsExchange,                       // exchange = "url-shortener"
    false,                                   // noWait
    nil,                                     // args
)
```

Analytics queue chỉ bind với **một routing key duy nhất**: `url.clicked`. Điều này có nghĩa queue `analytics.clicks` chỉ nhận các message về click events.

### 3.3. Notification Queue

**Định nghĩa trong:** `services/notification-service/rabbitmq.go`

```go
const notificationQueue = "notifications.events"
```

**Queue Declaration:**
```go
ch.QueueDeclare(
    notificationQueue,  // name = "notifications.events"
    true,               // durable
    false,              // autoDelete
    false,              // exclusive
    false,              // noWait
    nil,                // args
)
```

**Queue Binding (multiple routing keys):**
```go
func notificationRoutingKeys() []string {
    return []string{
        string(events.EventTypeURLCreated),       // "url.created"
        string(events.EventTypeURLDeleted),       // "url.deleted"
        string(events.EventTypeMilestoneReached), // "milestone.reached"
    }
}

for _, routingKey := range notificationRoutingKeys() {
    ch.QueueBind(notificationQueue, routingKey, notificationExchange, false, nil)
}
```

Notification queue bind với **ba routing keys**: `url.created`, `url.deleted`, `milestone.reached`.

**Chú ý:** Notification queue KHÔNG bind với `url.clicked`. Điều này có chủ đích — click events (có thể hàng triệu mỗi ngày) không nên được lưu thành notifications trong database.

### 3.4. Sơ Đồ Routing

```
                    ┌──────────────────────────────────┐
                    │         url-shortener             │
                    │        (topic exchange)           │
                    └──────────────────────────────────┘
                               │       │
              ┌────────────────┘       └────────────────┐
              │                                          │
    routing: "url.clicked"              routing: "url.created"
                                        routing: "url.deleted"
                                        routing: "milestone.reached"
              │                                          │
              ▼                                          ▼
    ┌──────────────────────┐              ┌──────────────────────────┐
    │  analytics.clicks     │              │  notifications.events    │
    │  (durable queue)      │              │  (durable queue)         │
    └──────────────────────┘              └──────────────────────────┘
              │                                          │
              ▼                                          ▼
    ┌──────────────────────┐              ┌──────────────────────────┐
    │  Analytics Service    │              │  Notification Service    │
    │  (ClickConsumer)      │              │  (NotificationConsumer)  │
    └──────────────────────┘              └──────────────────────────┘
```

### 3.5. AMQP Prefetch (QoS)

Cả Analytics Consumer và Notification Consumer đều set:

```go
c.conn.Channel.Qos(1, 0, false)
```

- **Prefetch count = 1:** Consumer chỉ nhận 1 message tại một thời điểm. Không nhận message mới cho đến khi ACK/NACK message hiện tại.
- **Prefetch size = 0:** Không giới hạn kích thước.
- **Global = false:** Áp dụng cho consumer này, không phải tất cả consumer trên channel.

**Tác dụng:** Đảm bảo xử lý tuần tự, tránh tình trạng một consumer tích lũy quá nhiều message trong bộ nhớ. Đặc biệt quan trọng vì Analytics Consumer thực hiện database transaction cho mỗi message.

### 3.6. Message Persistence

Khi publisher (Analytics Publisher) publish message:

```go
amqp.Publishing{
    ContentType:  "application/json",
    DeliveryMode: amqp.Persistent,  // = 2
    Body:         body,
}
```

- **DeliveryMode = 2 (Persistent):** Message được ghi vào disk. Nếu RabbitMQ crash trước khi consumer ACK, message không bị mất.
- **ContentType = "application/json":** Body là JSON.

---

## 4. Sơ Đồ Luồng Sự Kiện

### 4.1. Sơ Đồ Luồng Sự Kiện Click (Luồng Chính)

```
                   Gateway                    RabbitMQ                    Analytics Service              PostgreSQL
                     │                          │                              │                         │
                     │ 1. Publish               │                              │                         │
                     │ URLClickedEvent          │                              │                         │
                     │ routing_key:             │                              │                         │
                     │ "url.clicked"            │                              │                         │
                     │─────────────────────────►│                              │                         │
                     │                          │ 2. Route to analytics.clicks │                         │
                     │                          │──────────────────────────────►│                         │
                     │                          │                              │ 3. Unmarshal JSON       │
                     │                          │                              │ 4. Validate fields      │
                     │                          │                              │                         │
                     │                          │                              │ 5. BEGIN transaction    │
                     │                          │                              │────────────────────────►│
                     │                          │                              │                         │
                     │                          │                              │ 6. SELECT EXISTS        │
                     │                          │                              │ FROM processed_events   │
                     │                          │                              │ WHERE event_id = $1     │
                     │                          │                              │────────────────────────►│
                     │                          │                              │◄────────────────────────│
                     │                          │                              │ (exists = false)        │
                     │                          │                              │                         │
                     │                          │                              │ 7. INSERT INTO          │
                     │                          │                              │ processed_events        │
                     │                          │                              │ (event_id)              │
                     │                          │                              │────────────────────────►│
                     │                          │                              │                         │
                     │                          │                              │ 8. INSERT INTO          │
                     │                          │                              │ clicks (short_code,     │
                     │                          │                              │ clicked_at, ip_hash,    │
                     │                          │                              │ user_agent, referer)    │
                     │                          │                              │────────────────────────►│
                     │                          │                              │                         │
                     │                          │                              │ 9. CheckAndPublish      │
                     │                          │                              │ ├─ SELECT COUNT(*)      │
                     │                          │                              │ │  FROM clicks          │
                     │                          │                              │ │  WHERE short_code=$1  │
                     │                          │                              │ │──────────────────────►│
                     │                          │                              │ │◄──────────────────────│
                     │                          │                              │ │ (totalClicks = 101)   │
                     │                          │                              │ │                       │
                     │                          │                              │ │ 10. For threshold 100:│
                     │                          │                              │ │ SELECT EXISTS FROM    │
                     │                          │                              │ │ milestones            │
                     │                          │                              │ │──────────────────────►│
                     │                          │                              │ │◄──────────────────────│
                     │                          │                              │ │ (exists = false)      │
                     │                          │                              │ │                       │
                     │                          │                              │ │ 11. INSERT INTO       │
                     │                          │                              │ │ milestones (short_    │
                     │                          │                              │ │ code, milestone=100)  │
                     │                          │                              │ │──────────────────────►│
                     │                          │                              │ │                       │
                     │                          │                              │ │ 12. Publish           │
                     │                          │                              │ │ MilestoneReachedEvent │
                     │                          │ 13. Route to                │ │ routing_key:          │
                     │                          │ notifications.events        │ │ "milestone.reached"   │
                     │                          │◄─────────────────────────────│ │──────────────────────►│
                     │                          │                              │                         │
                     │                          │                              │ 14. COMMIT transaction │
                     │                          │                              │────────────────────────►│
                     │                          │                              │                         │
                     │                          │                              │ 15. ACK delivery       │
                     │                          │                              │ (remove from queue)    │
```

### 4.2. Sơ Đồ Luồng Sự Kiện Notification

```
                    URL Service / Analytics Svc           RabbitMQ                 Notification Service           PostgreSQL
                         │                                  │                            │                         │
                         │ 1. Publish event                 │                            │                         │
                         │ (url.created / url.deleted       │                            │                         │
                         │  / milestone.reached)            │                            │                         │
                         │─────────────────────────────────►│                            │                         │
                         │                                  │ 2. Route to               │                         │
                         │                                  │ notifications.events      │                         │
                         │                                  │───────────────────────────►│                         │
                         │                                  │                            │ 3. Unmarshal JSON      │
                         │                                  │                            │ 4. Parse event type    │
                         │                                  │                            │    from routing_key     │
                         │                                  │                            │                         │
                         │                                  │                            │ 5. BEGIN transaction   │
                         │                                  │                            │────────────────────────►│
                         │                                  │                            │                         │
                         │                                  │                            │ 6. INSERT INTO         │
                         │                                  │                            │ notifications (user_id,│
                         │                                  │                            │ event_type, payload,   │
                         │                                  │                            │ status = 'pending')    │
                         │                                  │                            │────────────────────────►│
                         │                                  │                            │◄────────────────────────│
                         │                                  │                            │ (return id, created_at)│
                         │                                  │                            │                         │
                         │                                  │                            │ 7. Mock email sent     │
                         │                                  │                            │ log.Info("mock email   │
                         │                                  │                            │ sent", "to", email,    │
                         │                                  │                            │ "type", event_type)    │
                         │                                  │                            │                         │
                         │                                  │                            │ 8. UPDATE              │
                         │                                  │                            │ notifications SET      │
                         │                                  │                            │ status = 'sent',       │
                         │                                  │                            │ sent_at = now()        │
                         │                                  │                            │ WHERE id = $1          │
                         │                                  │                            │────────────────────────►│
                         │                                  │                            │                         │
                         │                                  │                            │ 9. COMMIT transaction  │
                         │                                  │                            │────────────────────────►│
                         │                                  │                            │                         │
                         │                                  │                            │ 10. ACK delivery       │
                         │                                  │                            │ (remove from queue)    │
```

---

## 5. Phân Tích Analytics Service

### 5.1. Tổng Quan

**File chính:** `services/analytics-service/`

Analytics Service chịu trách nhiệm:
1. Nhận và xử lý `url.clicked` events từ RabbitMQ.
2. Ghi click data vào PostgreSQL (`clicks` table).
3. Deduplicate events để đảm bảo exactly-once processing.
4. Kiểm tra milestone thresholds (10, 100, 1000) cho mỗi click.
5. Publish `milestone.reached` events khi đạt mốc mới.
6. Cung cấp REST API cho thống kê (stats, timeline).

### 5.2. Package Structure

```
services/analytics-service/
├── main.go          — Entry point, dependency injection, HTTP server, graceful shutdown
├── config.go        — Cấu hình từ biến môi trường
├── db.go            — Kết nối PostgreSQL với pgxpool
├── rabbitmq.go      — Kết nối RabbitMQ, exchange/queue declare, binding
├── consumer.go      — ClickConsumer: nhận và xử lý delivery từ queue
├── handler.go       — StatsHandler: REST API endpoints
├── store.go         — ClickRepository, MilestoneRepository, DeduplicationRepository
├── milestone.go     — MilestoneChecker: kiểm tra và publish milestone
├── publisher.go     — AnalyticsPublisher: publish MilestoneReachedEvent
├── errors.go        — Helper functions (writeJSON, writeError, truncate)
├── health.go        — Health check endpoint
├── migrations.go    — Auto-migration với embedded SQL
├── migration.sql    — DDL statements
└── ...
```

### 5.3. main.go — Dependency Injection & Startup

**File:** `services/analytics-service/main.go` (128 dòng)

**Quy trình startup:**

```
main()
├── LoadConfig()                         ← Đọc DATABASE_URL, RABBITMQ_URL, PORT, SERVICE_NAME
├── logger.New("analytics-service")      ← Structured JSON logger
├── connectDatabase(cfg, log)
│   ├── NewDBPool(ctx, cfg.DatabaseURL)  ← pgxpool với MaxConns=10, MinConns=2
│   ├── Ping (10s timeout)
│   └── runMigrations(ctx, pool, log)    ← Execute migration.sql (embed)
├── connectRabbitMQ(cfg, log)
│   ├── NewRabbitMQConn()                ← Exponential backoff (1s→2s→4s→...→30s), max 10 attempts
│   └── DeclareAnalyticsQueue(ch)        ← QueueDeclare + QueueBind
├── Khởi tạo các store/repository
│   ├── NewClickStore(pool)              ← ClickRepository
│   ├── NewMilestoneStore()              ← MilestoneRepository
│   └── NewDeduplicationStore()          ← DeduplicationRepository
├── Khởi tạo publisher
│   └── NewAnalyticsPublisher(ch, log)   ← AnalyticsPublisher
├── Khởi tạo checker
│   └── NewMilestoneChecker(clickSt, milestoneSt, pub, log) ← MilestoneChecker
├── Khởi tạo consumer
│   └── NewClickConsumer(conn, pool, clickSt, milestoneSt, dedupSt, checker, log)
├── Khởi tạo stats handler
│   └── NewStatsHandler(clickStore, log)
├── HTTP Mux
│   ├── GET /health
│   ├── GET /metrics (Prometheus)
│   ├── GET /stats/{code}
│   └── GET /stats/{code}/timeline
├── go consumer.Run(ctx)                 ← Goroutine: lắng nghe RabbitMQ
├── go startServer(srv, log, port)       ← Goroutine: HTTP server
└── waitForShutdown(cancel, log)         ← Chặn SIGTERM/SIGINT, graceful shutdown
```

**Các hằng số thời gian:**

```go
const (
    databaseStartupTimeout = 60 * time.Second
    rabbitMQStartupTimeout = 120 * time.Second
    shutdownTimeout        = 10 * time.Second
    rabbitMQAttempts       = 10
)
```

- **databaseStartupTimeout (60s):** Nếu DB không connect được trong 60s → service exit(1).
- **rabbitMQStartupTimeout (120s):** Nếu RabbitMQ không connect được trong 120s → service exit(1).
- **shutdownTimeout (10s):** HTTP server graceful shutdown timeout.
- **rabbitMQAttempts (10 lần):** Số lần thử kết nối RabbitMQ.

### 5.4. ClickConsumer — Xử Lý Delivery

**File:** `services/analytics-service/consumer.go` (175 dòng)

#### 5.4.1. Struct ClickConsumer

```go
type ClickConsumer struct {
    conn           *RabbitMQConn
    pool           *pgxpool.Pool
    clickStore     ClickRepository
    milestoneStore MilestoneRepository
    dedupStore     DeduplicationRepository
    checker        *MilestoneChecker
    log            *slog.Logger
    healthy        atomic.Bool
}
```

- **`healthy`** (`atomic.Bool`): Health indicator. Set false khi delivery channel đóng hoặc context cancelled. Hiện tại không được expose qua health endpoint (health endpoint luôn trả về "ok" — đây là một thiếu sót).

#### 5.4.2. Run() — Main Loop

```go
func (c *ClickConsumer) Run(ctx context.Context) {
    c.conn.Channel.Qos(1, 0, false)      // Chỉ nhận 1 message mỗi lần
    deliveries, err := c.conn.Channel.Consume(
        analyticsQueue,  // queue = "analytics.clicks"
        "",              // consumer tag = "" (auto-generated)
        false,           // autoAck = false (manual ACK)
        false,           // exclusive
        false,           // noLocal
        false,           // noWait
        nil,             // args
    )
    c.healthy.Store(true)

    for {
        select {
        case <-ctx.Done():
            c.healthy.Store(false)
            return
        case delivery, ok := <-deliveries:
            if !ok {
                c.healthy.Store(false)
                <-ctx.Done()  // Wait for cancellation
                return
            }
            c.processDelivery(ctx, delivery)
        }
    }
}
```

**Phân tích chi tiết các tham số `Consume`:**

| Parameter | Giá trị | Ý nghĩa |
|---|---|---|
| `queue` | `"analytics.clicks"` | Tên queue |
| `consumer` | `""` (empty) | RabbitMQ tự sinh consumer tag |
| `autoAck` | `false` | **Manual ACK** — consumer chủ động gửi ACK/NACK |
| `exclusive` | `false` | Cho phép nhiều consumer trên cùng queue (competing consumers) |
| `noLocal` | `false` | Nhận cả message từ chính connection này |
| `noWait` | `false` | Đợi server xác nhận |
| `args` | `nil` | Không có tham số mở rộng |

**Xử lý channel đóng (`!ok`):**
Khi `deliveries` channel đóng (RabbitMQ mất kết nối), consumer set `healthy = false` và chờ `ctx.Done()`.

**Vấn đề:** Hiện tại không có cơ chế reconnect. Nếu RabbitMQ connection mất, consumer sẽ dừng vĩnh viễn và service chỉ còn HTTP server hoạt động. Đây là một điểm yếu của thiết kế hiện tại (sẽ phân tích ở phần 8).

#### 5.4.3. processDelivery() — Xử Lý Một Message

Đây là hàm quan trọng nhất, thực hiện toàn bộ logic xử lý click event:

```go
func (c *ClickConsumer) processDelivery(ctx context.Context, delivery amqp.Delivery) {
    started := time.Now()
    defer c.recoverDeliveryPanic(delivery)  // Recovery từ panic, ACK nếu panic

    evt, ok := c.parseDelivery(delivery)    // Unmarshal + validate
    if !ok {
        return  // Đã ACK trong parseDelivery (invalid event)
    }

    // BEGIN TRANSACTION
    tx, err := c.pool.Begin(ctx)
    if err != nil {
        nackRequeue(delivery, c.log)  // NACK + requeue
        return
    }

    committed := false
    defer func() {
        if !committed {
            tx.Rollback(ctx)  // Rollback nếu chưa commit
        }
    }()

    // STEP 1: CHECK DEDUP
    exists, err := c.dedupStore.Exists(ctx, tx, evt.EventID)
    if err != nil { nackRequeue(delivery); return }
    if exists {
        // Duplicate — ACK nhưng không xử lý
        ack(delivery)
        return
    }

    // STEP 2: INSERT DEDUP RECORD
    c.dedupStore.Insert(ctx, tx, evt.EventID)
    if err != nil { nackRequeue(delivery); return }

    // STEP 3: INSERT CLICK
    c.clickStore.Insert(ctx, tx, clickRecordFromEvent(evt))
    if err != nil { nackRequeue(delivery); return }

    // STEP 4: CHECK MILESTONE
    c.checker.CheckAndPublish(ctx, tx, evt.ShortCode, evt.UserID, evt.UserEmail, evt.CorrelationID)
    if err != nil { nackRequeue(delivery); return }

    // COMMIT
    tx.Commit(ctx)
    committed = true

    // ACK — remove from queue
    ack(delivery)
    c.log.Info("click processed", "event_id", evt.EventID, ...)
}
```

**Transaction Scope:**
Toàn bộ 4 thao tác (dedup check, insert processed_event, insert click, check milestone) nằm trong MỘT database transaction:
- Nếu bất kỳ bước nào thất bại → `tx.Rollback()` → NACK requeue.
- Nếu tất cả thành công → `tx.Commit()` → ACK.

**Điều này đảm bảo tính nhất quán (consistency):** Không thể xảy ra trường hợp click được ghi nhưng milestone không được kiểm tra, hoặc ngược lại.

#### 5.4.4. parseDelivery() — Parse và Validate

```go
func (c *ClickConsumer) parseDelivery(delivery amqp.Delivery) (*events.URLClickedEvent, bool) {
    var evt events.URLClickedEvent
    if err := json.Unmarshal(delivery.Body, &evt); err != nil {
        c.log.Warn("invalid click event json", ...)
        ack(delivery, c.log)   // ACK and discard (không requeue)
        return nil, false
    }
    if evt.EventID == "" || evt.ShortCode == "" {
        c.log.Warn("invalid click event", ...)
        ack(delivery, c.log)   // ACK and discard
        return nil, false
    }
    return &evt, true
}
```

**Xử lý lỗi validation:**
- Nếu JSON không parse được → `ACK` (không requeue). Đây là poison message — không thể xử lý, requeue chỉ gây loop vô hạn.
- Nếu EventID hoặc ShortCode rỗng → `ACK` (không requeue). Dữ liệu không hợp lệ.

**Đây là best practice** cho message validation: invalid messages được discard (ACK), không phải requeue.

#### 5.4.5. recoverDeliveryPanic() — Panic Recovery

```go
func (c *ClickConsumer) recoverDeliveryPanic(delivery amqp.Delivery) {
    if recovered := recover(); recovered != nil {
        log.Error("panic processing click event", "panic", recovered)
        ack(delivery, c.log)   // ACK to prevent poison message loop
    }
}
```

**Phân tích:**
- Nếu xảy ra panic trong `processDelivery` (ví dụ: nil pointer, index out of range), deferred function bắt panic.
- **Hành vi:** `ACK` delivery (không requeue). Điều này ngăn chặn poison message loop.
- **Hạn chế:** Mất message. Nếu panic do lỗi transient (không phải do message), một message hợp lệ bị mất vĩnh viễn. Giải pháp tốt hơn: NACK requeue với redelivery count check, hoặc chuyển đến dead letter queue.

### 5.5. MilestoneChecker — Kiểm Tra Mốc Click

**File:** `services/analytics-service/milestone.go` (96 dòng)

#### 5.5.1. Milestone Thresholds

```go
const (
    MilestoneThreshold10   = 10
    MilestoneThreshold100  = 100
    MilestoneThreshold1000 = 1000
)

var MilestoneThresholds = []int{MilestoneThreshold10, MilestoneThreshold100, MilestoneThreshold1000}
```

Ba mốc milestone: 10, 100, và 1000 clicks. Thresholds được lưu trong slice để dễ dàng mở rộng.

#### 5.5.2. CheckAndPublish()

```go
func (c *MilestoneChecker) CheckAndPublish(ctx context.Context, tx pgx.Tx, shortCode, userID, userEmail, corrID string) error {
    totalClicks, err := countClicksInTransaction(ctx, tx, shortCode)
    if err != nil { return err }

    for _, threshold := range MilestoneThresholds {
        if totalClicks < int64(threshold) {
            continue  // Chưa đạt mốc này
        }
        if err := c.publishIfNewMilestone(ctx, tx, shortCode, userID, userEmail, corrID, threshold, totalClicks); err != nil {
            return err
        }
    }
    return nil
}
```

**Logic:**
1. Đếm tổng số clicks của short_code trong transaction hiện tại. Vì transaction chưa commit, kết quả gồm cả click vừa insert.
2. Kiểm tra lần lượt với từng threshold (10 → 100 → 1000).
3. Nếu totalClicks >= threshold, gọi `publishIfNewMilestone`.
4. **Short-circuit:** Nếu totalClicks < 10, bỏ qua luôn threshold 100 và 1000.

**Tại sao dùng SELECT COUNT trong transaction thay vì COUNT từ bảng milestones?**
Vì cần số liệu real-time — trong một transaction có thể có nhiều click cùng lúc được insert.

#### 5.5.3. publishIfNewMilestone()

```go
func (c *MilestoneChecker) publishIfNewMilestone(ctx, tx, shortCode, userID, userEmail, corrID string, threshold int, totalClicks int64) error {
    exists, err := c.milestoneStore.HasMilestone(ctx, tx, shortCode, threshold)
    if err != nil { return err }
    if exists { return nil }  // Milestone đã được ghi nhận trước đó

    // Insert milestone record
    c.milestoneStore.Insert(ctx, tx, shortCode, threshold)
    if err != nil { return err }

    // Publish MilestoneReachedEvent
    evt := newMilestoneReachedEvent(shortCode, userID, userEmail, corrID, threshold, totalClicks)
    publishCtx, cancel := context.WithTimeout(ctx, milestonePublishTimeout)
    defer cancel()

    if err := c.publisher.PublishMilestone(publishCtx, evt); err != nil {
        // WARN — không return error, vì milestone đã ghi vào DB
        c.log.Warn("milestone publish failed", ...)
    }
    return nil
}
```

**Design Decision quan trọng:**

1. **Cơ chế idempotent cho milestone:**
   - Kiểm tra milestone đã tồn tại trong `milestones` table chưa.
   - `INSERT ... ON CONFLICT (short_code, milestone) DO NOTHING` — nếu có race condition (2 click cùng lúc), chỉ một insert thành công.
   - Đảm bảo mỗi milestone chỉ được publish một lần duy nhất.

2. **Publish failure không rollback milestone:**
   - Nếu publish MilestoneReachedEvent thất bại, hàm chỉ log WARN và return nil.
   - **Lý do:** Milestone đã được ghi nhận trong DB. Không rollback vì điều này sẽ rollback luôn click record.
   - **Hạn chế:** Có thể mất milestone notification. Giải pháp lý tưởng: có background worker định kỳ kiểm tra milestones chưa được publish.

3. **Publish timeout (3 giây):**
   ```go
   const milestonePublishTimeout = 3 * time.Second
   ```
   Context riêng với timeout 3s cho việc publish. Nếu RabbitMQ bị chậm hoặc không khả dụng, publish sẽ timeout.

#### 5.5.4. Milestone Timing Vulnerability

Có một vấn đề timing đáng chú ý: khi một URL có chính xác 10 clicks và có 2 click mới đến đồng thời (concurrent), `totalClicks` sẽ là 12, nhưng milestone 10 chỉ được publish một lần (do `ON CONFLICT DO NOTHING`). Điều này đúng — milestone chỉ nên được thông báo một lần.

Tuy nhiên, nếu totalClicks là 12, URL đã pass milestone 10 nhưng chưa đạt 100, nên `totalClicks < 100`, bỏ qua milestone 100. Khi URL tiếp tục nhận click, tổng số là 13, 14... cho đến 100, `publishIfNewMilestone` cho threshold 100 sẽ được kích hoạt. Đây là hành vi đúng.

### 5.6. AnalyticsPublisher — Publish MilestoneReachedEvent

**File:** `services/analytics-service/publisher.go` (43 dòng)

```go
type amqpAnalyticsPublisher struct {
    ch           *amqp.Channel
    exchangeName string  // = "url-shortener"
    log          *slog.Logger
}

func (p *amqpAnalyticsPublisher) PublishMilestone(ctx context.Context, evt *events.MilestoneReachedEvent) error {
    body, err := json.Marshal(evt)
    if err != nil { return fmt.Errorf("marshal milestone event: %w", err) }

    err = p.ch.PublishWithContext(ctx, p.exchangeName,
        string(events.EventTypeMilestoneReached),  // routing key = "milestone.reached"
        false,   // mandatory
        false,   // immediate
        amqp.Publishing{
            ContentType:  "application/json",
            DeliveryMode: amqp.Persistent,  // persistent delivery
            Body:         body,
        })
    if err != nil { return fmt.Errorf("publish milestone event: %w", err) }

    p.log.Info("milestone event published", "short_code", evt.ShortCode, "milestone", evt.MilestoneN)
    return nil
}
```

- **Exchange:** `url-shortener`
- **Routing key:** `milestone.reached`
- **Mandatory = false:** Nếu không có queue nào bind, message bị drop (silently). Điều này OK vì notification queue đã bind với `milestone.reached`.
- **Immediate = false:** Không sử dụng (deprecated trong AMQP 0-9-1).

**Vấn đề:** Interface `AnalyticsPublisher` chỉ có một method `PublishMilestone`. Nếu cần publish loại event khác, cần mở rộng interface.

### 5.7. Store Layer — PostgreSQL

**File:** `services/analytics-service/store.go` (237 dòng)

#### 5.7.1. ClickRepository Interface

```go
type ClickRepository interface {
    Insert(ctx context.Context, tx pgx.Tx, rec *ClickRecord) error
    CountByCode(ctx context.Context, shortCode string) (int64, error)
    CountByCodeSince(ctx context.Context, shortCode string, since time.Time) (int64, error)
    TopReferers(ctx context.Context, shortCode string, n int) ([]RefererCount, error)
    TimeLineBuckets(ctx context.Context, shortCode string, truncUnit string) ([]TimeLinePoint, error)
}
```

#### 5.7.2. MilestoneRepository Interface

```go
type MilestoneRepository interface {
    HasMilestone(ctx context.Context, tx pgx.Tx, shortCode string, milestone int) (bool, error)
    Insert(ctx context.Context, tx pgx.Tx, shortCode string, milestone int) error
}
```

#### 5.7.3. DeduplicationRepository Interface

```go
type DeduplicationRepository interface {
    Exists(ctx context.Context, tx pgx.Tx, eventID string) (bool, error)
    Insert(ctx context.Context, tx pgx.Tx, eventID string) error
}
```

#### 5.7.4. SQL Statements

```go
insertClickSQL = `
    INSERT INTO clicks (short_code, clicked_at, ip_hash, user_agent, referer)
    VALUES ($1, $2, $3, $4, $5)
`

countClicksByCodeSQL = `SELECT COUNT(*) FROM clicks WHERE short_code = $1`

countClicksByCodeSinceSQL = `SELECT COUNT(*) FROM clicks WHERE short_code = $1 AND clicked_at >= $2`

topReferersSQL = `
    SELECT referer, COUNT(*) AS cnt
    FROM clicks
    WHERE short_code = $1 AND referer IS NOT NULL
    GROUP BY referer
    ORDER BY cnt DESC
    LIMIT $2
`

timelineBucketsSQL = `
    SELECT date_trunc($1, clicked_at AT TIME ZONE 'UTC') AS period, COUNT(*) AS clicks
    FROM clicks
    WHERE short_code = $2
    GROUP BY period
    ORDER BY period ASC
`

milestoneExistsSQL = `SELECT EXISTS(SELECT 1 FROM milestones WHERE short_code = $1 AND milestone = $2)`

insertMilestoneSQL = `
    INSERT INTO milestones (short_code, milestone)
    VALUES ($1, $2)
    ON CONFLICT (short_code, milestone) DO NOTHING
`

processedEventExistsSQL = `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`

insertProcessedEventSQL = `
    INSERT INTO processed_events (event_id)
    VALUES ($1)
    ON CONFLICT (event_id) DO NOTHING
`
```

**Phân tích SQL:**

1. **`insertClickSQL`:** Insert click record với 5 fields. Referer có thể NULL (dùng `nullString` helper: "" → nil, non-empty → string pointer).

2. **`countClicksByCodeSQL`:** COUNT đơn giản với WHERE short_code. Không có index phù hợp cho COUNT ngoài `idx_clicks_short_code_time` (short_code, clicked_at DESC) — sẽ scan index.

3. **`topReferersSQL`:** Aggregate GROUP BY referer, trả về top N referers với count. Sử dụng index `idx_clicks_referer` (short_code, referer) WHERE referer IS NOT NULL.

4. **`timelineBucketsSQL`:** Sử dụng `date_trunc` của PostgreSQL để gom nhóm theo hour hoặc day. `date_trunc($1, ...)` an toàn với SQL injection vì $1 là parameter, không phải raw string.

   **Vấn đề tiềm ẩn:** `date_trunc` unit là user-controlled parameter ($1). PostgreSQL chấp nhận 'day', 'hour', 'month', 'year', v.v. Handler chỉ cho phép 'day' hoặc 'hour' (validation trong handler.go), nhưng SQL injection qua parameter không khả thi vì dùng parameterized query.

5. **`insertMilestoneSQL`:** `ON CONFLICT (short_code, milestone) DO NOTHING` — idempotent insert. Nếu milestone đã tồn tại (unique constraint), không làm gì, không báo lỗi.

6. **`insertProcessedEventSQL`:** Tương tự, `ON CONFLICT (event_id) DO NOTHING`.

#### 5.7.5. Helper Functions

```go
func nullString(value string) *string {
    if value == "" { return nil }
    return &value
}

func scanCount(ctx, pool, sql, args...) (int64, error)
func scanExists(ctx, tx, sql, args...) (bool, error)
func scanRefererCounts(rows) ([]RefererCount, error)
func scanTimeLinePoints(rows) ([]TimeLinePoint, error)
```

`nullString` chuyển empty string thành nil để PostgreSQL có thể lưu NULL.

### 5.8. Database Schema (Migration)

**File:** `services/analytics-service/migration.sql` (33 dòng)

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- Cung cấp gen_random_uuid()

CREATE TABLE IF NOT EXISTS clicks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code TEXT NOT NULL,
    clicked_at TIMESTAMPTZ NOT NULL,
    ip_hash TEXT NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    referer TEXT NULL
);

CREATE INDEX IF NOT EXISTS idx_clicks_short_code_time
    ON clicks(short_code, clicked_at DESC);

CREATE INDEX IF NOT EXISTS idx_clicks_referer
    ON clicks(short_code, referer)
    WHERE referer IS NOT NULL;

CREATE TABLE IF NOT EXISTS milestones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code TEXT NOT NULL,
    milestone INT NOT NULL,
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(short_code, milestone)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_milestones_code_milestone
    ON milestones(short_code, milestone);

CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

#### 5.8.1. Bảng clicks

| Cột | Kiểu | Ràng buộc | Mô tả |
|---|---|---|---|
| id | UUID | PK, DEFAULT gen_random_uuid() | ID tự sinh |
| short_code | TEXT | NOT NULL | Mã short URL |
| clicked_at | TIMESTAMPTZ | NOT NULL | Thời điểm click |
| ip_hash | TEXT | NOT NULL | Hash của IP (privacy-safe) |
| user_agent | TEXT | NOT NULL DEFAULT '' | User-Agent header |
| referer | TEXT | NULL | HTTP Referer header |

**Indexes:**
- `idx_clicks_short_code_time` (short_code, clicked_at DESC): Composite index cho truy vấn COUNT và timeline. DESC vì thường query mới nhất trước.
- `idx_clicks_referer` (short_code, referer) WHERE referer IS NOT NULL: Partial index cho top referers query.

#### 5.8.2. Bảng milestones

| Cột | Kiểu | Ràng buộc | Mô tả |
|---|---|---|---|
| id | UUID | PK, DEFAULT gen_random_uuid() | ID tự sinh |
| short_code | TEXT | NOT NULL | Mã short URL |
| milestone | INT | NOT NULL | Giá trị mốc (10, 100, 1000) |
| triggered_at | TIMESTAMPTZ | NOT NULL DEFAULT now() | Thời điểm đạt mốc |

**Unique constraint:** (short_code, milestone) — đảm bảo mỗi mốc chỉ được ghi một lần cho mỗi URL.

#### 5.8.3. Bảng processed_events

| Cột | Kiểu | Ràng buộc | Mô tả |
|---|---|---|---|
| event_id | TEXT | PRIMARY KEY | UUID v4 của event |
| processed_at | TIMESTAMPTZ | NOT NULL DEFAULT now() | Thời điểm xử lý |

**Lưu ý:** Không có TTL/expiry cho `processed_events`. Bảng sẽ lớn dần theo thời gian. Cần có cleanup job (ví dụ: xóa bản ghi cũ hơn 7 ngày).

---

## 6. Phân Tích Notification Service

### 6.1. Tổng Quan

**File chính:** `services/notification-service/`

Notification Service chịu trách nhiệm:
1. Nhận các events từ RabbitMQ (`url.created`, `url.deleted`, `milestone.reached`).
2. Ghi notification records vào PostgreSQL (`notifications` table).
3. Mock email sending (log thay vì gửi email thật).
4. Cung cấp REST API cho user xem notifications (có JWT auth).

### 6.2. Package Structure

```
services/notification-service/
├── main.go            — Entry point, dependency injection, HTTP server, graceful shutdown
├── config.go          — Cấu hình từ biến môi trường
├── db.go              — Kết nối PostgreSQL với pgxpool
├── rabbitmq.go        — Kết nối RabbitMQ, exchange/queue declare, binding
├── consumer.go        — NotificationConsumer: nhận và xử lý delivery từ queue
├── handler.go         — NotificationHandler: REST API (GET /notifications)
├── store.go           — NotificationRepository: insert + list notifications
├── errors.go          — Helper functions
├── health.go          — Health check endpoint
├── migrations.go      — Auto-migration với embedded SQL
├── migration.sql      — DDL statements
└── ...
```

### 6.3. main.go

**File:** `services/notification-service/main.go` (125 dòng)

Cấu trúc tương tự Analytics Service:

```
main()
├── LoadConfig()                ← DATABASE_URL, RABBITMQ_URL, JWT_SECRET, PORT
├── connectDatabase(cfg, log)   ← pgxpool + migration
├── connectRabbitMQ(cfg, log)   ← Exponential backoff + queue declare
│   └── DeclareNotificationQueue(ch)  ← QueueDeclare + 3 QueueBind
├── NewNotificationStore(pool, log)
├── NewNotificationConsumer(mqConn, store, log)
├── NewNotificationHandler(store)
├── auth.JWTMiddleware(cfg.JWTSecret)
├── HTTP Mux
│   ├── GET /health
│   ├── GET /metrics (Prometheus)
│   └── GET /notifications (JWT protected)
├── go consumer.Run(ctx)
├── go startServer(srv, log, port)
└── waitForShutdown(cancel, log)
```

**Khác biệt so với Analytics Service:**
- Có JWT middleware cho endpoint `/notifications`.
- NotificationConsumer dùng `autoAck = false`.
- NotificationHandler có pagination với cursor-based.

### 6.4. NotificationConsumer

**File:** `services/notification-service/consumer.go` (186 dòng)

#### 6.4.1. Struct

```go
type NotificationConsumer struct {
    conn    *RabbitMQConn
    store   NotificationRepository
    log     *slog.Logger
    healthy atomic.Bool
}
```

Không có dedupStore, không có pool/pgx trực tiếp — chỉ có repository interface. Đơn giản hơn nhiều so với ClickConsumer.

#### 6.4.2. Run()

Tương tự ClickConsumer: Qos(1,0,false), Consume với autoAck=false, loop select.

#### 6.4.3. processDelivery()

```go
func (c *NotificationConsumer) processDelivery(ctx context.Context, delivery amqp.Delivery) {
    started := time.Now()
    defer c.recoverDeliveryPanic(delivery)

    rec, eventID, ok := c.notificationFromDelivery(delivery)
    if !ok { return }  // Đã ACK (invalid)

    if _, err := c.store.InsertNotification(ctx, rec); err != nil {
        c.log.Error("insert notification", ..., "error", err)
        nackRequeue(delivery, c.log)
        return
    }

    ack(delivery, c.log)
    c.log.Info("notification processed", ...)
}
```

**So sánh với ClickConsumer:**

| Aspect | ClickConsumer | NotificationConsumer |
|---|---|---|
| ACK strategy | Manual (autoAck=false) | Manual (autoAck=false) |
| Validation | Parse + validate field | Parse + validate field |
| Invalid message | ACK (discard) | ACK (discard) |
| Error during processing | NACK requeue | NACK requeue |
| Transaction | BEGIN/COMMIT trong process | BEGIN/COMMIT trong store |
| Dedup | Có (processed_events table) | Không |

**Tại sao NotificationConsumer không cần dedup?**
Vì notification events (url.created, url.deleted, milestone.reached) có tần suất thấp hơn nhiều so với click events. Milestone events đã được dedup ở Analytics Service (mỗi milestone chỉ publish một lần). URL created/deleted events là một lần duy nhất cho mỗi URL. Tuy nhiên, trong trường hợp RabbitMQ redelivery (consumer crash trước khi ACK), có thể nhận duplicate notification. Đây là một thiếu sót nhỏ — lý tưởng nhất là có dedup cho tất cả consumer.

#### 6.4.4. notificationFromDelivery() — Routing Key Based Parsing

```go
func (c *NotificationConsumer) notificationFromDelivery(delivery amqp.Delivery) (*NotificationRecord, string, bool) {
    eventID, err := parseEventID(delivery.Body)
    if err != nil {
        ack(delivery, c.log)  // Discard invalid
        return nil, "", false
    }

    eventType := delivery.RoutingKey  // Lấy từ routing key
    var rec *NotificationRecord
    
    switch eventType {
    case string(events.EventTypeURLCreated):
        rec, err = notificationFromURLCreated(delivery.Body, eventType)
    case string(events.EventTypeURLDeleted):
        rec, err = notificationFromURLDeleted(delivery.Body, eventType)
    case string(events.EventTypeMilestoneReached):
        rec, err = notificationFromMilestoneReached(delivery.Body, eventType)
    default:
        // Unsupported routing key — discard
        ack(delivery, c.log)
        return nil, eventID, false
    }
    if err != nil {
        ack(delivery, c.log)  // Invalid payload — discard
        return nil, eventID, false
    }
    return rec, eventID, true
}
```

**Điểm quan trọng:** Event type được xác định từ `delivery.RoutingKey`, không parse từ JSON body. Điều này có lợi:
- Không cần parse toàn bộ JSON để biết event type.
- Tận dụng routing key của RabbitMQ.
- Nếu routing key không khớp, chỉ cần parse `BaseEvent` để lấy EventID.

**Parse eventID (luôn thực hiện đầu tiên):**
```go
func parseEventID(body []byte) (string, error) {
    var base events.BaseEvent
    if err := json.Unmarshal(body, &base); err != nil {
        return "", err
    }
    if base.EventID == "" {
        return "", errMissingEventID
    }
    return base.EventID, nil
}
```

**Ba hàm parse cụ thể:**

```go
func notificationFromURLCreated(body []byte, eventType string) (*NotificationRecord, error) {
    var evt events.URLCreatedEvent
    json.Unmarshal(body, &evt)
    if evt.UserID == "" || evt.UserEmail == "" {
        return nil, error
    }
    return newNotificationRecord(evt.UserID, evt.UserEmail, eventType, body), nil
}

func notificationFromURLDeleted(body []byte, eventType string) (*NotificationRecord, error) {
    // Tương tự URLCreatedEvent
}

func notificationFromMilestoneReached(body []byte, eventType string) (*NotificationRecord, error) {
    var evt events.MilestoneReachedEvent
    json.Unmarshal(body, &evt)
    if evt.UserID == "" {
        return nil, errDiscardMilestoneMissingUserID  // Special error
    }
    if evt.UserEmail == "" {
        return nil, errMissingUserEmail
    }
    return newNotificationRecord(evt.UserID, evt.UserEmail, eventType, body), nil
}
```

**Error variables:**
```go
var (
    errMissingEventID                = errors.New("event_id is required")
    errMissingUserEmail              = errors.New("user_email is required")
    errDiscardMilestoneMissingUserID = errors.New("discard milestone.reached: user_id is required")
)
```

**Phân tích `errDiscardMilestoneMissingUserID`:**
MilestoneReachedEvent có thể có `UserID` rỗng (nếu event được tạo bởi anonymous user? Nhưng trong code `userID` luôn được lấy từ `evt.UserID` từ click event, và URL click luôn phải có user). Nếu UserID rỗng, milestone notification bị discard (ACK). Đây là design decision: milestones cho anonymous users không tạo notification.

#### 6.4.5. newNotificationRecord()

```go
func newNotificationRecord(userID, userEmail, eventType string, body []byte) *NotificationRecord {
    payload := make([]byte, len(body))
    copy(payload, body)  // Deep copy
    return &NotificationRecord{UserID: userID, UserEmail: userEmail, EventType: eventType, Payload: payload}
}
```

**Deep copy payload:** Vì `body` thuộc về `delivery.Body` (byte slice từ RabbitMQ), nên cần copy để tránh reference đến buffer có thể bị ghi đè.

### 6.5. Notification Store

**File:** `services/notification-service/store.go` (174 dòng)

#### 6.5.1. Structs

```go
type Notification struct {
    ID        string          `json:"id"`
    UserID    string          `json:"user_id"`
    EventType string          `json:"event_type"`
    Payload   json.RawMessage `json:"payload"`   // Raw JSON — không parse
    Status    string          `json:"status"`     // "pending" → "sent"
    CreatedAt time.Time       `json:"created_at"`
    SentAt    *time.Time      `json:"sent_at,omitempty"`
}

type NotificationRecord struct {
    UserID    string
    UserEmail string          // Dùng để mock email, không lưu vào DB
    EventType string
    Payload   json.RawMessage
}
```

**Phân biệt:**
- `Notification`: Dùng cho API response (JSON serialization).
- `NotificationRecord`: Dùng cho xử lý internal (chứa thêm UserEmail — chỉ dùng để log mock email, không lưu vào DB).

#### 6.5.2. InsertNotification() — Transaction + Mock Email

```go
func (s *pgxNotificationStore) InsertNotification(ctx context.Context, rec *NotificationRecord) (*Notification, error) {
    tx, err := s.pool.Begin(ctx)
    committed := false
    defer func() {
        if !committed { tx.Rollback(ctx) }
    }()

    notification := &Notification{
        UserID: rec.UserID, EventType: rec.EventType,
        Payload: rec.Payload, Status: "pending",
    }

    // INSERT với RETURNING id, created_at
    tx.QueryRow(ctx, insertNotificationSQL, rec.UserID, rec.EventType, rec.Payload).
        Scan(&notification.ID, &notification.CreatedAt)

    // MOCK EMAIL — chỉ log
    s.log.Info("mock email sent", "to", rec.UserEmail, "type", rec.EventType)

    // UPDATE status → 'sent'
    tx.Exec(ctx, markNotificationSentSQL, notification.ID)

    tx.Commit(ctx)
    committed = true

    notification.Status = "sent"
    now := time.Now().UTC()
    notification.SentAt = &now
    return notification, nil
}
```

**Transaction workflow:**
1. INSERT notification với status = 'pending' → RETURNING id, created_at.
2. Mock email (log).
3. UPDATE status = 'sent', sent_at = now().
4. COMMIT.

**Nếu mock email log thất bại?**
Không thể — `log.Info` không trả về error. Mock email luôn "thành công".

**Design Decision:** Mock email được thực hiện TRONG transaction. Nếu commit thất bại, rollback sẽ undo cả notification record. Điều này đảm bảo consistency: không có notification nào ở trạng thái 'pending' mà chưa được "gửi email".

#### 6.5.3. ListByUser() — Cursor-based Pagination

```go
func (s *pgxNotificationStore) ListByUser(ctx context.Context, userID, afterID string, limit int) ([]Notification, string, error) {
    if limit <= 0 { limit = 20 }

    // Query limit + 1 để biết có còn trang sau không
    rows, err := queryNotifications(ctx, s.pool, userID, afterID, limit+1)
    defer rows.Close()

    notifications, err := scanNotifications(rows)

    nextCursor := ""
    if len(notifications) > limit {
        notifications = notifications[:limit]
        nextCursor = notifications[len(notifications)-1].ID  // last item ID
    }
    return notifications, nextCursor, nil
}
```

**Cursor-based pagination (keyset pagination):**
- Dùng `id` (UUID) làm cursor.
- Query `limit + 1` records để xác định next page.
- Nếu có > limit records, cắt bỏ record cuối và trả về ID của record cuối làm nextCursor.
- Client gửi `?after=<last_id>` để lấy trang tiếp theo.

**So sánh với offset-based pagination:**
| Aspect | Offset-based | Cursor-based |
|---|---|---|
| Performance | Degrade với offset lớn | Consistent O(log n) |
| Stability | Insert mới làm thay đổi page | Ổn định |
| Implementation | Đơn giản | Phức tạp hơn |
| Use case | Phù hợp cho UI | Phù hợp cho API |

**SQL cho cursor-based pagination:**
```sql
-- Trang đầu (không có cursor)
SELECT ... FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2

-- Trang tiếp (có cursor)
SELECT ... FROM notifications
WHERE user_id = $1
  AND (created_at, id) < (
    SELECT created_at, id FROM notifications WHERE id = $2 AND user_id = $1
  )
ORDER BY created_at DESC, id DESC
LIMIT $3
```

**Phân tích SQL:**
- Composite comparison `(created_at, id) < (subquery)`: Xử lý đúng khi có nhiều notification cùng created_at (dùng id làm tiebreaker).
- Subquery xác thực `id` thuộc về `user_id` — an toàn, không cho phép user xem notification của người khác.

### 6.6. Notification Schema

**File:** `services/notification-service/migration.sql` (14 dòng)

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

CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications (user_id, created_at DESC);
```

| Cột | Kiểu | Ràng buộc | Mô tả |
|---|---|---|---|
| id | UUID | PK, DEFAULT gen_random_uuid() | ID tự sinh |
| user_id | UUID | NOT NULL | User nhận notification |
| event_type | TEXT | NOT NULL | Loại sự kiện gốc |
| payload | JSONB | NOT NULL | Toàn bộ event JSON gốc |
| status | TEXT | NOT NULL DEFAULT 'sent' | 'pending' → 'sent' |
| created_at | TIMESTAMPTZ | NOT NULL DEFAULT now() | Thời điểm tạo |
| sent_at | TIMESTAMPTZ | NULL | Thời điểm "gửi email" |

**Index:** `idx_notifications_user_created` (user_id, created_at DESC) — composite index cho query của `ListByUser`.

**Lưu ý:** `status` mặc định là `'sent'`, không phải `'pending'`. Điều này hơi bất thường nhưng có lý do: trong `insertNotificationSQL`, status = 'pending' được set explicit. Default 'sent' là fallback nếu có insert trực tiếp.

### 6.7. Notification Handler

**File:** `services/notification-service/handler.go` (97 dòng)

#### 6.7.1. List Endpoint

```
GET /notifications?limit=20&after=<uuid>
Authorization: Bearer <jwt>
```

**Xác thực:**
- `JWTMiddleware` xác thực JWT Bearer token.
- `auth.ClaimsFromContext` lấy user ID từ claims.Sub.

**Query parameters:**

| Parameter | Mặc định | Tối đa | Mô tả |
|---|---|---|---|
| `limit` | 20 | 100 | Số notification mỗi trang |
| `after` | "" (trang đầu) | - | UUID cursor cho keyset pagination |

**Response:**
```json
{
    "notifications": [
        {
            "id": "uuid",
            "user_id": "uuid",
            "event_type": "url.created",
            "payload": { ... full event ... },
            "status": "sent",
            "created_at": "2025-01-01T00:00:00Z",
            "sent_at": "2025-01-01T00:00:01Z"
        }
    ],
    "next_cursor": "uuid"  // null nếu không còn trang
}
```

**Validation:**
```go
func parseNotificationLimit(raw string) (int, error) {
    if raw == "" { return defaultNotificationLimit, nil }  // = 20
    limit, err := strconv.Atoi(raw)
    if err != nil || limit <= 0 { return 0, errInvalidLimit }
    if limit > maxNotificationLimit { return maxNotificationLimit, nil }  // cap at 100
    return limit, nil
}

func parseNotificationCursor(raw string) (string, error) {
    if raw == "" { return "", nil }
    if _, err := uuid.Parse(raw); err != nil { return "", errInvalidCursor }
    return raw, nil
}
```

### 6.8. Shared Auth Module

**File:** `shared/auth/auth.go` + `shared/auth/middleware.go`

**JWT Claims:**
```go
type Claims struct {
    Sub   string `json:"sub"`    // user_id
    Email string `json:"email"`  // denormalized email
    Iss   string `json:"iss"`    // "url-shortener"
    Iat   int64  `json:"iat"`    // issued at
    Exp   int64  `json:"exp"`    // expires at (24h)
}
```

**JWTMiddleware:**
```go
func JWTMiddleware(secret string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authHeader := r.Header.Get("Authorization")
            // Check "Bearer " prefix
            tokenString := strings.TrimPrefix(authHeader, "Bearer ")
            claims, err := VerifyToken(tokenString, secret)
            ctx := context.WithValue(r.Context(), claimsKey{}, claims)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

---

## 7. Phân Tích Delivery Guarantees

### 7.1. Các Mức Delivery Guarantee

Trong hệ thống message queue, có ba mức đảm bảo delivery:

| Mức | Mô tả | Ví dụ |
|---|---|---|
| **At-most-once** | Message được gửi tối đa một lần. Có thể mất message nhưng không có duplicate. | Fire-and-forget, auto-ACK trước khi xử lý |
| **At-least-once** | Message được gửi ít nhất một lần. Không mất message nhưng có thể có duplicate. | Manual ACK sau khi xử lý |
| **Exactly-once** | Message được gửi đúng một lần. Không mất, không duplicate. | At-least-once + deduplication |

### 7.2. Analytics Service Delivery Guarantee

**Mức hiện tại: Exactly-once** (kết hợp at-least-once + dedup)

**Cơ chế at-least-once:**
1. `autoAck = false` — Consumer chủ động gửi ACK sau khi xử lý xong.
2. RabbitMQ không xóa message cho đến khi nhận được ACK.
3. Nếu consumer crash trước khi ACK, message được requeue và gửi lại cho consumer khác.

**Cơ chế dedup (exactly-once):**
1. Mỗi event có EventID (UUID v4) duy nhất.
2. Trước khi xử lý, consumer kiểm tra `processed_events` table.
3. Nếu event_id đã tồn tại, message được ACK và bỏ qua.
4. Dedup insert và click insert trong cùng transaction — atomic.

**Sơ đồ exactly-once:**

```
Producer → RabbitMQ → Consumer (autoAck=false)
                           │
                           ▼
                    ┌──────────────┐
                    │ Dedup check  │
                    │ (processed)  │
                    └──────┬───────┘
                    ┌──────┴───────┐
                    │   EXISTS?    │
                    └──────┬───────┘
                    ┌──────┴───────┐      ┌──────────────┐
                    │  YES → ACK   │      │ NO → Process │
                    └──────────────┘      └──────┬───────┘
                                                 │
                                          ┌──────┴───────┐
                                          │ Insert dedup │
                                          │ Insert click │
                                          │ Check mile.  │
                                          └──────┬───────┘
                                                 │
                                          ┌──────┴───────┐
                                          │   COMMIT?    │
                                          └──────┬───────┘
                                          ┌──────┴───────┐      ┌──────────────┐
                                          │  YES → ACK   │      │ NO → NACK    │
                                          └──────────────┘      │ (+ requeue)  │
                                                                 └──────────────┘
```

**Điểm yếu:** Nếu consumer crash sau COMMIT nhưng trước ACK, message được requeue. Khi consumer mới nhận message, dedup check phát hiện event_id đã tồn tại → ACK bỏ qua. Message được xử lý đúng một lần.

### 7.3. Notification Service Delivery Guarantee

**Mức hiện tại: At-least-once**

- `autoAck = false` — ACK sau khi insert notification thành công.
- **KHÔNG có cơ chế dedup.**
- Nếu consumer crash sau COMMIT nhưng trước ACK, notification được insert lần nữa khi message được requeue (duplicate notification).

**Rủi ro duplicate notification:**
- URL created: User nhận 2 thông báo "URL created" cho cùng một URL.
- Milestone reached: User nhận 2 thông báo "URL reached 100 clicks".

**Giải pháp có thể:**
- Thêm dedup trong NotificationConsumer (tương tự ClickConsumer).
- Dùng unique constraint trên `(user_id, event_id)` — nhưng event_id không phải column trong notifications table.
- Upsert pattern.

### 7.4. So Sánh Delivery Guarantees

| Service | autoAck | Manual ACK | Dedup | Guarantee | Rủi ro |
|---|---|---|---|---|---|
| Analytics | false | ACK sau commit | processed_events table | Exactly-once | Mất message nếu panic (ACK trong recoverDeliveryPanic) |
| Notification | false | ACK sau insert | Không có | At-least-once | Duplicate notification |

### 7.5. Poison Message Handling

Cả hai service đều có cơ chế xử lý poison message:

| Loại lỗi | Analytics | Notification |
|---|---|---|
| Invalid JSON | ACK (discard) | ACK (discard) |
| Missing required field | ACK (discard) | ACK (discard) |
| Unsupported routing key | N/A | ACK (discard) |
| Panic | ACK (discard) | ACK (discard) |
| DB error | NACK requeue | NACK requeue |

**Vấn đề với NACK requeue:**
Nếu lỗi DB kéo dài, message sẽ được requeue liên tục, gây loop. Giải pháp:
- Dead Letter Queue (DLQ): Chuyển message đến DLQ sau N lần retry.
- Retry count tracking: Theo dõi `x-death` header của RabbitMQ.
- Exponential backoff khi requeue.

---

## 8. Phân Tích Reconnection Handling

### 8.1. Startup Connection

Cả hai service đều có exponential backoff khi kết nối RabbitMQ lúc startup:

```go
func NewRabbitMQConn(ctx context.Context, url string, log *slog.Logger, maxAttempts int) (*RabbitMQConn, error) {
    backoff := rabbitMQInitialBackoff  // = 1s
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        mq, err := dialRabbitMQ(url)
        if err == nil {
            return mq, nil  // Thành công
        }
        log.Warn("rabbitmq connect failed", "attempt", attempt, "error", err)
        select {
        case <-ctx.Done(): return nil, ctx.Err()
        case <-time.After(backoff):
        }
        backoff *= 2
        if backoff > rabbitMQMaxBackoff { backoff = 30 * time.Second }
    }
    return nil, fmt.Errorf("rabbitmq unreachable after %d attempts", maxAttempts)
}
```

**Backoff sequence (10 attempts):**
1s → 2s → 4s → 8s → 16s → 30s → 30s → 30s → 30s → 30s

**Tổng thời gian chờ tối đa:** ~156s (2 phút 36 giây).

### 8.2. Runtime Reconnection — VẤN ĐỀ LỚN

**Hiện tại: KHÔNG có cơ chế reconnect khi RabbitMQ mất kết nối runtime.**

Khi phân tích `Run()` method của cả ClickConsumer và NotificationConsumer:

```go
case delivery, ok := <-deliveries:
    if !ok {
        c.healthy.Store(false)
        c.log.Warn("delivery channel closed")
        <-ctx.Done()  // Chờ context cancellation
        return
    }
```

**Khi RabbitMQ connection bị mất:**
1. `deliveries` channel đóng (gửi signal `ok = false`).
2. Consumer set `healthy = false`.
3. Consumer chờ `ctx.Done()` — nhưng `ctx` chỉ được cancel khi nhận SIGTERM/SIGINT.
4. **Consumer dừng vĩnh viễn. Service vẫn chạy nhưng không xử lý event nào.**

**Tác động:**
- `analytics.clicks` queue tích lũy message.
- Click events không được xử lý.
- Stats API trả về dữ liệu cũ.
- Notification events không được xử lý.
- Cần restart service để phục hồi.

### 8.3. Giải Pháp Reconnection Đề Xuất

1. **Connection Notifier:** Sử dụng `conn.NotifyClose()` để phát hiện mất kết nối.
2. **Reconnect loop:** Khi nhận notify, re-establish connection + channel + consumer.
3. **Consumer cancel trước khi reconnect:** Gọi `Channel.Cancel()` để clean up consumer trên RabbitMQ.
4. **Redelivery handling:** Đảm bảo message không bị mất trong quá trình reconnect.

**Ví dụ code mẫu cho reconnect:**
```go
func (c *ClickConsumer) Run(ctx context.Context) {
    // Initial setup
    deliveries := c.startConsume(ctx)
    
    for {
        select {
        case <-ctx.Done():
            return
        case delivery, ok := <-deliveries:
            if !ok {
                // Channel closed — reconnect
                c.log.Warn("connection lost, reconnecting...")
                deliveries = c.reconnectAndConsume(ctx)
                if deliveries == nil {
                    return  // Context cancelled
                }
                continue
            }
            c.processDelivery(ctx, delivery)
        }
    }
}

func (c *ClickConsumer) reconnectAndConsume(ctx context.Context) <-chan amqp.Delivery {
    for i := 0; i < 10; i++ {
        select {
        case <-ctx.Done():
            return nil
        case <-time.After(backoff):
        }
        
        mqConn, err := NewRabbitMQConn(ctx, cfg.RabbitMQURL, log, 1)
        if err != nil { continue }
        
        // Re-declare queue (idempotent)
        DeclareAnalyticsQueue(mqConn.Channel)
        
        // Start consumer
        deliveries, err := mqConn.Channel.Consume(...)
        if err != nil { continue }
        
        c.conn = mqConn  // Swap connection
        return deliveries
    }
    return nil
}
```

### 8.4. So sánh với URL Service

URL Service (`rabbitmq.go`) có cùng pattern — không có runtime reconnect. Tuy nhiên, URL Service là producer, không phải consumer. Nếu mất kết nối, nó không thể publish event, nhưng API có thể trả về lỗi cho user và user có thể retry. Đối với consumer, mất kết nối có hậu quả nghiêm trọng hơn.

---

## 9. Stats API Endpoints

### 9.1. GET /health

**File:** `services/analytics-service/health.go`

**Response:** `{"status":"ok","service":"analytics-service"}`

**Lưu ý:** Pre-encoded JSON — không marshal mỗi request. Tuy nhiên, không kiểm tra DB hay RabbitMQ connectivity. Đây là "liveness check", không phải "readiness check".

### 9.2. GET /metrics

Prometheus metrics endpoint. Sử dụng `promhttp.Handler()` từ `prometheus/client_golang`.

### 9.3. GET /stats/{code}

**Handler:** `StatsHandler.Stats()`

**Parameters:**
- `code` (path parameter): Short code.

**Response:**
```json
{
    "short_code": "abc123",
    "total_clicks": 1500,
    "clicks_last_24h": 45,
    "clicks_last_7d": 320,
    "top_referers": [
        {"referer": "https://twitter.com", "count": 500},
        {"referer": "https://facebook.com", "count": 300}
    ]
}
```

**Implementation:** `loadStats()` sử dụng `errgroup` để thực hiện 4 truy vấn đồng thời:

```go
func (h *StatsHandler) loadStats(ctx context.Context, shortCode string) (statsResponse, error) {
    group, ctx := errgroup.WithContext(ctx)
    now := time.Now().UTC()

    group.Go(func() error {
        response.TotalClicks, err = h.clickStore.CountByCode(ctx, shortCode)
        return err
    })
    group.Go(func() error {
        response.ClicksLast24h, err = h.clickStore.CountByCodeSince(ctx, shortCode, now.Add(-oneDay))
        return err
    })
    group.Go(func() error {
        response.ClicksLast7d, err = h.clickStore.CountByCodeSince(ctx, shortCode, now.Add(-7*oneDay))
        return err
    })
    group.Go(func() error {
        response.TopReferers, err = h.clickStore.TopReferers(ctx, shortCode, statsTopReferersLimit)
        return err
    })

    return response, group.Wait()
}
```

**Phân tích `errgroup`:**
- 4 goroutines chạy đồng thời.
- Nếu một goroutine fail, context bị cancel.
- `group.Wait()` chờ tất cả goroutines hoàn thành và trả về lỗi đầu tiên.

**Điều chỉnh:** `TopReferers` có limit = 5 (hằng số `statsTopReferersLimit`).

### 9.4. GET /stats/{code}/timeline

**Handler:** `StatsHandler.TimeLine()`

**Query Parameters:**
- `interval` (required): `"day"` hoặc `"hour"`.

**Validation:**
```go
func isValidTimeLineInterval(interval string) bool {
    return interval == "day" || interval == "hour"
}
```

**Response:**
```json
{
    "short_code": "abc123",
    "interval": "day",
    "points": [
        {"period": "2025-01-01T00:00:00Z", "clicks": 100},
        {"period": "2025-01-02T00:00:00Z", "clicks": 150}
    ]
}
```

**SQL Implementation:**
```sql
SELECT date_trunc('day', clicked_at AT TIME ZONE 'UTC') AS period, COUNT(*) AS clicks
FROM clicks
WHERE short_code = $1
GROUP BY period
ORDER BY period ASC
```

**Phân tích:**
- `date_trunc` nhận tham số interval ('day'/'hour') từ query string (sanitized qua validation).
- `AT TIME ZONE 'UTC'` đảm bảo clicked_at được xử lý ở UTC.
- `ORDER BY period ASC` — chronological order.

**Vấn đề tiềm ẩn:**
- Nếu short_code không có click nào, store trả về `[]TimeLinePoint{}` (empty slice, không phải nil).
- Không có giới hạn thời gian — nếu URL có 2 năm dữ liệu, response trả về 730 points (mỗi ngày 1 point). Nên thêm query parameter `from` và `to`.

### 9.5. GET /notifications (Notification Service)

**Handler:** `NotificationHandler.List()` (JWT-protected)

**Endpoint:** `GET /notifications?limit=20&after=<cursor>`

**Authentication:** Bearer JWT token (JWTMiddleware).

**Authorization:** User chỉ xem được notification của chính mình (từ claims.Sub).

**Pagination:** Cursor-based với UUID.

---

## 10. Bảng Tổng Kết

### 10.1. So Sánh Hai Service

| Aspect | Analytics Service | Notification Service |
|---|---|---|
| **Primary function** | Xử lý click events, thống kê | Xử lý notification events, mock email |
| **Queue** | `analytics.clicks` | `notifications.events` |
| **Routing keys** | 1: `url.clicked` | 3: `url.created`, `url.deleted`, `milestone.reached` |
| **autoAck** | false | false |
| **Dedup** | Có (processed_events table) | Không |
| **Delivery guarantee** | Exactly-once | At-least-once |
| **API endpoints** | /stats/{code}, /stats/{code}/timeline | /notifications |
| **API auth** | Không | JWT required |
| **Transaction scope** | Consumer (1 transaction) | Store (1 transaction) |
| **Producer** | Có (MilestoneReachedEvent) | Không |
| **DB tables** | clicks, milestones, processed_events | notifications |
| **DB indexes** | 2 composite, 1 partial | 1 composite |
| **Migration** | SQL embedding (go:embed) | SQL embedding (go:embed) |

### 10.2. RabbitMQ Configuration

| Item | Giá trị |
|---|---|
| Exchange name | `url-shortener` |
| Exchange type | `topic` |
| Durable | true |
| Auto-delete | false |
| Queue analytics | `analytics.clicks` (durable) |
| Queue notification | `notifications.events` (durable) |
| Routing keys (analytics) | `url.clicked` |
| Routing keys (notification) | `url.created`, `url.deleted`, `milestone.reached` |
| Prefetch count (analytics) | 1 |
| Prefetch count (notification) | 1 |

### 10.3. Event Types Summary

| Event | Producer | Routing Key | Queues | Payload Fields |
|---|---|---|---|---|
| URLCreatedEvent | URL Service | `url.created` | notifications.events | ShortCode, OriginalURL, UserID, UserEmail, ExpiresAt |
| URLClickedEvent | Gateway | `url.clicked` | analytics.clicks | ShortCode, UserID, UserEmail, IPHash, UserAgent, Referer, ClickedAt |
| URLDeletedEvent | URL Service | `url.deleted` | notifications.events | ShortCode, UserID, UserEmail |
| MilestoneReachedEvent | Analytics Svc | `milestone.reached` | notifications.events | ShortCode, UserID, UserEmail, MilestoneN, TotalClicks |

### 10.4. Database Tables

| Service | Table | PK | Unique Constraints | Indexes |
|---|---|---|---|---|
| Analytics | clicks | id (UUID) | - | (short_code, clicked_at DESC), (short_code, referer) WHERE referer IS NOT NULL |
| Analytics | milestones | id (UUID) | (short_code, milestone) | (short_code, milestone) |
| Analytics | processed_events | event_id (TEXT) | - | - |
| Notification | notifications | id (UUID) | - | (user_id, created_at DESC) |

### 10.5. Error Handling Strategies

| Scenario | Analytics | Notification |
|---|---|---|
| Invalid JSON | ACK + log | ACK + log |
| Missing required field | ACK + log | ACK + log |
| DB error (transient) | NACK requeue | NACK requeue |
| DB error (permanent) | NACK requeue (loop) | NACK requeue (loop) |
| Panic | ACK + log + recover | ACK + log + recover |
| Duplicate event | ACK (deduped) | N/A (no dedup) |
| Unsupported event type | N/A | ACK + log |
| Milestone publish fail | WARN + continue | N/A |

### 10.6. Goroutine Model

**Analytics Service:**
```
main goroutine
├── go consumer.Run(ctx)           ← Goroutine 1: RabbitMQ consumer loop
├── go startServer(srv, log, port) ← Goroutine 2: HTTP server
└── waitForShutdown(...)            ← Main goroutine: block on signal
    ├── cancel()                    ← Cancel context → consumer stops
    └── shutdownServer(srv)         ← Graceful HTTP shutdown
```

**Notification Service:**
```
main goroutine
├── go consumer.Run(ctx)           ← Goroutine 1: RabbitMQ consumer loop
├── go startServer(srv, log, port) ← Goroutine 2: HTTP server
└── waitForShutdown(...)            ← Main goroutine: block on signal
```

### 10.7. Configuration (Environment Variables)

| Variable | Analytics | Notification |
|---|---|---|
| `DATABASE_URL` | Required | Required |
| `RABBITMQ_URL` | Required | Required |
| `PORT` | Default 8080 | Default 8080 |
| `JWT_SECRET` | N/A | Required |
| `IP_HASH_SALT` | Required | N/A |
| `SERVICE_NAME` | `analytics-service` | `notification-service` |

---

## 11. Kết Luận

### 11.1. Điểm Mạnh

1. **Event-Driven Architecture tốt:** Sử dụng topic exchange, durable queues, persistent messages — phù hợp cho microservices.
2. **Exactly-once delivery cho click events:** Dedup với processed_events table + transaction atomicity.
3. **Graceful shutdown:** Xử lý SIGTERM/SIGINT, drain connections.
4. **Manual ACK:** Kiểm soát hoàn toàn việc xác nhận message.
5. **Panic recovery:** Deferred recover + ACK để tránh poison message loop.
6. **Structured logging:** slog với JSON handler, service name attribute.
7. **Idempotent milestone:** ON CONFLICT DO NOTHING + EXISTS check.
8. **Cursor-based pagination:** Keyset pagination cho notification list.
9. **Embedded migration:** go:embed SQL, auto-migrate on startup.
10. **Concurrent stats queries:** errgroup cho 4 truy vấn đồng thời.
11. **JWT authentication:** Notification endpoint được bảo vệ.
12. **Pre-encoded health response:** Tối ưu performance cho health check.

### 11.2. Điểm Yếu và Cải Thiện

1. **Thiếu runtime RabbitMQ reconnection:** Vấn đề lớn nhất — consumer dừng vĩnh viễn nếu mất kết nối. Cần implement reconnect loop với `NotifyClose`.
2. **Không có dead letter queue:** NACK requeue liên tục cho DB error. Cần DLQ + retry policy.
3. **Thiếu dedup cho Notification Service:** Nguy cơ duplicate notification. Cần processed_events table hoặc unique constraint.
4. **Health check không phản ánh trạng thái thực:** `healthy` atomic variable không được expose. Cần readiness check kiểm tra DB và RabbitMQ.
5. **Không có TTL cho processed_events:** Bảng sẽ phình to vô hạn. Cần cleanup job.
6. **Milestone publish failure không retry:** Nếu publish fail, milestone bị mất (chỉ log WARN). Cần background reconciler.
7. **Stats timeline không có time range filter:** Trả về tất cả dữ liệu. Cần thêm from/to parameters.
8. **Không có rate limiting:** Cả API và consumer đều không giới hạn tốc độ.
9. **Không có tracing:** Dù có correlationID, không có distributed tracing (OpenTelemetry, Jaeger).
10. **Các service có thể dùng chung nhiều code:** `db.go`, `rabbitmq.go`, `errors.go`, `health.go`, `migrations.go` — duplicate code giữa các service.

### 11.3. Đề Xuất Cải Tiến Ưu Tiên

1. **IMMEDIATE (P0):** Implement RabbitMQ reconnect cho cả hai consumer.
2. **HIGH (P1):** Thêm dedup cho NotificationService.
3. **HIGH (P1):** Implement Dead Letter Queue cho poison messages.
4. **MEDIUM (P2):** Expose health indicator qua health endpoint.
5. **MEDIUM (P2):** Thêm TTL cleanup job cho processed_events.
6. **LOW (P3):** Background worker cho milestone publish retry.
7. **LOW (P3):** Thêm time range parameters cho timeline API.
8. **LOW (P3):** Extract shared library cho common code (db, rabbitmq, errors, health, migrations).

---

## Phụ Lục A: Event Flow Diagram (Text-Based)

```
Producer                  Exchange                  Queue                     Consumer
─────────                ─────────                ──────                     ────────

URL Service ──url.created──► url-shortener ──url.created──► notifications.events ──► Notification Service
             ──url.deleted─► (topic)        ──url.deleted─►                      
                                                                                 
Gateway      ──url.clicked─►                ──url.clicked──► analytics.clicks ───► Analytics Service
                                                                                  
Analytics    ──milestone.reached─────────────milestone.reached──► notifications.events ──► Notification Service
Service                                                                           
```

## Phụ Lục B: JSON Event Examples

```json
// URLCreatedEvent
{
    "event_type": "url.created",
    "occurred_at": "2025-06-15T10:30:00Z",
    "correlation_id": "req-uuid-123",
    "event_id": "evt-uuid-456",
    "short_code": "abc123",
    "original_url": "https://example.com/long-url",
    "user_id": "user-uuid-789",
    "user_email": "user@example.com",
    "expires_at": "2025-12-31T23:59:59Z"
}

// URLClickedEvent
{
    "event_type": "url.clicked",
    "occurred_at": "2025-06-15T10:30:00Z",
    "correlation_id": "req-uuid-123",
    "event_id": "evt-uuid-456",
    "short_code": "abc123",
    "user_id": "user-uuid-789",
    "user_email": "user@example.com",
    "ip_hash": "a1b2c3d4e5f6...",
    "user_agent": "Mozilla/5.0 ...",
    "referer": "https://twitter.com",
    "clicked_at": "2025-06-15T10:30:01Z"
}

// MilestoneReachedEvent
{
    "event_type": "milestone.reached",
    "occurred_at": "2025-06-15T10:30:00Z",
    "correlation_id": "req-uuid-123",
    "event_id": "evt-uuid-456",
    "short_code": "abc123",
    "user_id": "user-uuid-789",
    "user_email": "user@example.com",
    "milestone": 100,
    "total_clicks": 100
}
```

---

> **Kết thúc báo cáo.**  
> Tổng số dòng: ~1850+  
> Phân tích toàn diện Event-Driven Architecture, Analytics Service, và Notification Service của dự án URL Shortener Microservices.
