# Báo Cáo Phân Tích Kiến Trúc Hệ Thống URL Shortener Microservices

> **Tác giả:** AI Agent (phân tích tự động)
> **Ngày:** 2026-07-11
> **Phiên bản:** v1.0
> **Mục tiêu:** Phân tích cực kỳ chi tiết kiến trúc, mã nguồn, quyết định thiết kế, DDD bounded context, event storming, điểm mạnh/yếu và khuyến nghị.

---

## Mục Lục

1. [Tổng Quan Kiến Trúc Microservices](#1-tổng-quan-kiến-trúc-microservices)
2. [Domain-Driven Design & Bounded Context Analysis](#2-domain-driven-design--bounded-context-analysis)
3. [API Gateway - Phân Tích Chi Tiết Từng File](#3-api-gateway---phân-tích-chi-tiết-từng-file)
4. [Phân Tích Chi Tiết Từng Service](#4-phân-tích-chi-tiết-từng-service)
5. [Shared Packages](#5-shared-packages)
6. [Event-Driven Architecture & Outbox Pattern](#6-event-driven-architecture--outbox-pattern)
7. [Infrastructure & Deployment](#7-infrastructure--deployment)
8. [Monitoring & Observability](#8-monitoring--observability)
9. [Phân Tích Mã Nguồn Chi Tiết](#9-phân-tích-mã-nguồn-chi-tiết)
10. [Điểm Mạnh, Điểm Yếu & Khuyến Nghị](#10-điểm-mạnh-điểm-yếu--khuyến-nghị)
11. [Kết Luận](#11-kết-luận)

---

## 1. Tổng Quan Kiến Trúc Microservices

### 1.1 Danh sách 5 Services

Hệ thống URL Shortener được thiết kế theo kiến trúc microservices với **5 services chính** và **3 shared packages**, tất cả được tổ chức trong một Go workspace monorepo (`go.work`):

| # | Service | Vai trò | DB riêng | Port |
|---|---------|---------|----------|------|
| 1 | **Gateway** (`gateway/`) | API Gateway, reverse proxy, authentication, rate limiting, circuit breaker, metrics | Redis (rate limit) | 8080 |
| 2 | **User Service** (`services/user-service/`) | Đăng ký, đăng nhập, quản lý người dùng | PostgreSQL (`user_db`) | 8083 |
| 3 | **URL Service** (`services/url-service/`) | Tạo, đọc, xóa short URL, redirect, caching | PostgreSQL (`url_db`) + Redis (cache) | 8081 |
| 4 | **Analytics Service** (`services/analytics-service/`) | Ghi nhận click, thống kê, milestone | PostgreSQL (`analytics_db`) | 8082 |
| 5 | **Notification Service** (`services/notification-service/`) | Thông báo URL events cho người dùng | PostgreSQL (`notification_db`) | 8084 |

**Frontend** là một SPA (Single Page Application) dựng bằng Vite + React (tại `frontend/`), giao tiếp với backend qua Gateway.

### 1.2 Tech Stack Chi Tiết

#### Ngôn ngữ & Runtime
- **Go 1.23.0** - tất cả backend services và gateway
- **Go Workspace** (`go.work`) - quản lý monorepo với 8 module
- **TypeScript/React** (Vite) - frontend SPA với Docker multi-stage build

#### Framework & Thư Viện Chính

| Thư viện | Mục đích | Service sử dụng |
|-----------|----------|----------------|
| `net/http` (stdlib) | HTTP server & mux | Tất cả services |
| `net/http/httputil.ReverseProxy` | Reverse proxy | Gateway |
| `github.com/jackc/pgx/v5` | PostgreSQL driver | User, URL, Analytics, Notification |
| `github.com/redis/go-redis/v9` | Redis client | Gateway, URL Service |
| `github.com/rabbitmq/amqp091-go` | AMQP / RabbitMQ | URL, Analytics, Notification |
| `github.com/golang-jwt/jwt/v5` | JWT token | Gateway, User, Shared Auth |
| `github.com/prometheus/client_golang` | Prometheus metrics | Gateway + all services |
| `github.com/google/uuid` | UUID generation | URL Service |

#### Cơ Sở Dữ Liệu
- **PostgreSQL 16 Alpine** - 4 database riêng biệt (1 per service)
- **Redis 7 Alpine** - 2 mục đích: rate limiting (gateway), URL caching (url-service)
- **RabbitMQ 3.13** - message broker với topic exchange cho event-driven communication

#### Infrastructure
- **Nginx 1.27** - reverse proxy đầu vào, phục vụ static files và proxy API requests
- **Docker Compose** - local development với 18 containers
- **Kubernetes** - production deployment với Deployments, Services, ConfigMaps, Secrets

#### Monitoring Stack
- **Prometheus 2.53** - metrics collection và storage
- **Grafana 11.1** - dashboard visualization (circuit breaker, service overview)
- **Loki 2.9** + **Promtail** - log aggregation

### 1.3 Go Workspace Monorepo

File `go.work` định nghĩa workspace:

```go
go 1.23.0

use (
    ./gateway
    ./services/analytics-service
    ./services/notification-service
    ./services/url-service
    ./services/user-service
    ./shared/auth
    ./shared/events
    ./shared/logger
)
```

**8 modules** được quản lý trong cùng một workspace. Điều này cho phép:
- Phát triển local mà không cần `replace` directives phức tạp
- Shared packages (`shared/auth`, `shared/events`, `shared/logger`) được tham chiếu trực tiếp
- Mỗi service có `go.mod` riêng, cho phép build độc lập và deploy riêng rẽ
- Dễ dàng kiểm soát version giữa các module

### 1.4 Luồng Request Điển Hình

#### Luồng 1: User đăng ký tài khoản
```
Client → Nginx → Gateway → User Service → PostgreSQL
```

1. Client gửi `POST /api/auth/register` với JSON body `{email, password}`
2. Nginx bắt prefix `/api/` và proxy sang Gateway (`http://gateway_upstream`)
3. Gateway nhận request, `matchRoute` xác định `route.RequiresAuth = false`, bỏ qua JWT
4. Gateway không áp dụng rate limiting (không có `RateLimitKey`)
5. Gateway proxy request tới `user-service` (strip prefix `/api/auth` → `/register`)
6. User Service validate email/password, hash password bằng bcrypt, insert vào DB
7. Response JSON `{user_id, email}` được gửi ngược lại

#### Luồng 2: User login
```
Client → Nginx → Gateway → User Service → PostgreSQL → JWT
```

Tương tự luồng 1, nhưng User Service verify password và trả về JWT token.

#### Luồng 3: Create short URL (authenticated)
```
Client → Nginx → Gateway [JWT Auth → Rate Limit → Circuit Breaker] → URL Service [Auth → DB → Redis Cache → Outbox → RabbitMQ]
```

1. Client gửi `POST /api/shorten` với `Authorization: Bearer <token>` header
2. Nginx proxy sang Gateway
3. Gateway:
   - `matchRoute` → `RequiresAuth: true`, `RateLimitKey: "shorten"`
   - `jwtMiddleware`: verify JWT, inject claims vào context
   - `Handler.ServeHTTP`: check rate limit (10 requests/60s window)
   - Circuit breaker check cho url-service
   - Proxy request tới url-service (strip prefix `/api` → `/shorten`)
4. URL Service:
   - `JWTMiddleware` verify token (double verification)
   - `HandleShorten`: parse request body
   - `URLService.ShortenURL`:
     - Validate URL
     - Generate short code (base62, 7 ký tự)
     - BEGIN transaction: INSERT URL + INSERT outbox event
     - COMMIT (atomic - cả URL và event cùng thành công hoặc cùng thất bại)
   - `go func()`: Cache URL vào Redis
   - Response `{short_code, short_url, original_url, expires_at}`
5. `OutboxCoordinator` (background goroutine) poll outbox table, publish `url.created` event qua RabbitMQ
6. Analytics Service và Notification Service consume event từ RabbitMQ

#### Luồng 4: Redirect (anonymous)
```
Client → Nginx → Gateway [Rate Limit → Circuit Breaker] → URL Service [Cache → DB]
```

1. Client gửi `GET /r/abc1234`
2. Nginx bắt prefix `/r/` → proxy Gateway
3. Gateway: `RequiresAuth: false`, `RateLimitKey: "redirect"` (300 req/60s)
4. URL Service:
   - Check Redis cache trước (50ms timeout)
   - Cache HIT → trả về redirect ngay
   - Cache MISS → query PostgreSQL, cache kết quả (fire-and-forget goroutine)
5. `go writeAnalyticsEvent()`: Insert `url.clicked` event vào outbox (không blocking response)
6. HTTP 308 Permanent Redirect tới original URL

#### Luồng 5: Xem thống kê
```
Client → Nginx → Gateway → Analytics Service → PostgreSQL
```

`GET /api/stats/{code}` được proxy tới analytics-service, không cần auth (public stats).

#### Luồng 6: Xem notifications
```
Client → Nginx → Gateway [JWT] → Notification Service [JWT] → PostgreSQL
```

`GET /api/notifications` yêu cầu JWT authentication ở cả Gateway và Notification Service.

### 1.5 Sơ Đồ Kiến Trúc Tổng Thể

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Internet / Client                           │
└───────────────────────────┬─────────────────────────────────────────┘
                            │
                            ▼
                    ┌───────────────┐
                    │   Nginx 1.27  │  Port 80
                    │  Reverse Proxy│
                    └───────┬───────┘
                            │
              ┌─────────────┴─────────────┐
              │                           │
              ▼                           ▼
      ┌───────────────┐         ┌──────────────────┐
      │   Frontend    │         │ API Gateway      │
      │ Vite + React  │         │ Go 1.23          │
      │ Port 5173     │         │ Port 8080        │
      └───────────────┘         └────────┬─────────┘
                                         │
                         ┌───────────────┼───────────────┐
                         │               │               │
                         ▼               ▼               ▼
              ┌────────────────┐ ┌──────────────┐ ┌──────────────┐
              │  User Service  │ │  URL Service │ │  Notification│
              │  Go + pgx      │ │  Go + pgx    │ │  Go + pgx    │
              │  Port 8083     │ │  Port 8081   │ │  Port 8084   │
              └───────┬────────┘ └──────┬───────┘ └──────┬───────┘
                      │                 │                │
                      ▼                 ▼                ▼
              ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
              │  PostgreSQL  │  │  PostgreSQL  │  │  PostgreSQL  │
              │  user_db     │  │  url_db      │  │  notif_db    │
              │  Port 5434   │  │  Port 5432   │  │  Port 5435   │
              └──────────────┘  └──────┬───────┘  └──────────────┘
                                       │
                                       ▼
                              ┌────────────────┐
                              │  Analytics     │
                              │  Service       │
                              │  Go + pgx      │
                              │  Port 8082     │
                              └───────┬────────┘
                                      │
                                      ▼
                              ┌────────────────┐
                              │  PostgreSQL    │
                              │  analytics_db  │
                              │  Port 5433     │
                              └────────────────┘

         ┌──────────────────────────────────────────────────────┐
         │               Message Bus (RabbitMQ)                 │
         │  Topic Exchange: url-shortener                       │
         │  Routing keys: url.created, url.clicked,             │
         │                url.deleted, milestone.reached        │
         └──────────────────────────────────────────────────────┘

         ┌──────────────────────────────────────────────────────┐
         │               Redis Cache Layer                      │
         │  Gateway: rate limiting counters                     │
         │  URL Service: short code → original URL cache       │
         └──────────────────────────────────────────────────────┘
```

### 1.6 Docker Compose Topology

File `docker-compose.yml` định nghĩa **18 containers**:

| Container | Image | Port(s) | Depends On |
|-----------|-------|---------|------------|
| `url_db` | postgres:16-alpine | 5432 | - |
| `analytics_db` | postgres:16-alpine | 5433 | - |
| `user_db` | postgres:16-alpine | 5434 | - |
| `notification_db` | postgres:16-alpine | 5435 | - |
| `adminer` | adminer:latest | 8090 | - |
| `rabbitmq` | rabbitmq:3.13-management | 5672, 15672 | - |
| `redis` | redis:7-alpine | 6379 | - |
| `nginx` | nginx:1.27-alpine | 80 | gateway, frontend |
| `url-service` | Dockerfile | 8081 | url_db, redis, rabbitmq |
| `analytics-service` | Dockerfile | 8082 | analytics_db, rabbitmq |
| `user-service` | Dockerfile | 8083 | user_db |
| `notification-service` | Dockerfile | 8084 | notification_db, rabbitmq |
| `gateway` | Dockerfile | 8080 | url-service, analytics-service, user-service, notification-service |
| `frontend` | Dockerfile | 5173 | gateway |
| `prometheus` | prom/prometheus:v2.53.0 | 9090 | gateway |
| `grafana` | grafana/grafana:11.1.0 | 3000 | prometheus, loki |
| `loki` | grafana/loki:2.9.1 | 3100 | - |
| `promtail` | grafana/promtail:latest | - | loki |

**Volume mounts**: 10 persistent volumes cho databases, message broker, cache, và monitoring data.

**Health checks**: Tất cả services và infrastructure đều có health checks với `start_period`, `interval`, `timeout`, `retries` để đảm bảo dependency ordering đúng đắn.

### 1.7 Kubernetes Deployment (k8s/)

File `k8s/apps.yaml` định nghĩa:

| Resource | Replicas | Service Type | NodePort |
|----------|----------|--------------|----------|
| `url-service` Deployment + Service | 3 | ClusterIP | - |
| `analytics-service` Deployment + Service | 1 | ClusterIP | - |
| `user-service` Deployment + Service | 1 | ClusterIP | - |
| `notification-service` Deployment + Service | 1 | ClusterIP | - |
| `gateway` Deployment + Service | 2 | NodePort | 30080 |

**Điểm đáng chú ý trong K8s manifest:**
- **Secret Management**: `JWT_SECRET` được inject qua `secretKeyRef` (không hardcode)
- **ConfigMap**: Các URL kết nối (DB, Redis, RabbitMQ) được quản lý qua ConfigMap `app-config`
- **Readiness Probes**: Mỗi service có HTTP GET `/health` check
- **url-service replicas=3**: Service chính được scale để xử lý redirect traffic cao
- **gateway replicas=2**: Gateway được nhân đôi cho high availability
- **Namespace isolation**: Tất cả resources trong namespace `url-shortener`

### 1.8 Monitoring Stack

File `monitoring/prometheus.yml` cấu hình Prometheus scrape 5 targets với `scrape_interval: 5s`:
- `gateway:8080` (metrics từ `/metrics` endpoint)
- `url-service:8080`
- `analytics-service:8080`
- `user-service:8080`
- `notification-service:8080`

Mỗi service đều export Prometheus metrics qua `/metrics` endpoint.

Grafana được cấu hình với:
- **Datasource**: Prometheus, Loki
- **Dashboard tự động provisioning**: `circuit_breaker.json`, `services_overview.json`
- **Home dashboard**: Circuit Breaker overview
- **Admin credentials**: admin/admin (chỉ cho development)

---

## 2. Domain-Driven Design & Bounded Context Analysis

### 2.1 Xác định 4 Bounded Contexts

Dựa trên phân tích mã nguồn, hệ thống có **4 Bounded Contexts** rõ ràng:

#### Bounded Context 1: User & Authentication (Context Người Dùng)
- **Service**: `user-service`
- **Database**: `user_db` (PostgreSQL)
- **Aggregate Root**: `User`
- **Core Domain**: ✓ (Xác thực là chức năng cốt lõi)
- **Ubiquitous Language**: user, register, login, password, token, JWT, bearer

**Entities:**
- `User` (id, email, password_hash, created_at)

**Value Objects:**
- `Password` (hashed with bcrypt)
- `Email` (validated format)
- `JWT Token` (signed with HMAC-SHA256)

**Domain Events:**
- Không emit domain events trực tiếp (không có outbox trong user-service)

**Repositories:**
- `UserRepository` interface với implementation `pgxUserStore`
  - `Insert(ctx, email, passwordHash) (*User, error)`
  - `FindByEmail(ctx, email) (*User, error)`

**Services:**
- `PasswordHasher`: bcrypt hash + verify (cost = 12)
- `TokenIssuer`: JWT issue + verify (HS256, TTL configurable)

#### Bounded Context 2: URL Shortening (Context Rút Gọn URL)
- **Service**: `url-service`
- **Databases**: `url_db` (PostgreSQL) + Redis (cache)
- **Aggregate Root**: `ShortURL`
- **Core Domain**: ✓ (Chức năng kinh doanh chính)
- **Ubiquitous Language**: short code, original URL, redirect, base62, outbox, cache

**Entities:**
- `URLRecord` (id, short_code, original_url, user_id, user_email, created_at, expires_at, is_active)
- `OutboxRecord` (id, event_type, payload, created_at, locked_until, published_at)

**Value Objects:**
- `ShortCode` (7-char base62 string)
- `CachedURL` (Redis projection)
- `ShortenRequest` / `ShortenResponse` (HTTP DTOs)
- `RedirectInfo` (original_url, user_id, user_email, ip_hash)

**Domain Events (emitted via Outbox):**
- `URLCreatedEvent`
- `URLClickedEvent`
- `URLDeletedEvent`

**Domain Services:**
- `URLService` (business logic: shorten, redirect, list, deactivate)
- `ShortCodeGenerator` (crypto/rand + base62 encoding)
- `OutboxCoordinator` (poll + publish pattern)
- `URLValidator` (scheme, host validation)

**Repositories:**
- `URLStore` interface → `pgxURLStore`
  - `Insert(ctx, tx, record)`
  - `FindByCode(ctx, shortCode)`
  - `FindByUserID(ctx, userID, afterID, limit)`
  - `Deactivate(ctx, tx, shortCode, userID)`
- `OutboxStore` interface → `pgxOutboxStore`
  - `InsertEvent(ctx, tx, outbox)`
  - `FetchUnpublished(ctx, limit)`
  - `MarkPublished(ctx, id)`
- `Cache` interface → `redisCache`
  - `Get(ctx, code)`
  - `Set(ctx, code, cached, ttl)`
  - `Delete(ctx, code)`

#### Bounded Context 3: Analytics & Milestones (Context Thống Kê)
- **Service**: `analytics-service`
- **Database**: `analytics_db` (PostgreSQL)
- **Aggregate Root**: `Click`
- **Supporting Domain**: ✓ (Hỗ trợ chức năng chính)
- **Ubiquitous Language**: click, stats, milestone, dedup, timeline

**Entities:**
- `ClickRecord` (short_code, clicked_at, ip_hash, user_agent, referer)
- `DeduplicationRecord` (event_id)
- `MilestoneRecord` (short_code, milestone)

**Domain Events (consumed):**
- `URLClickedEvent` (từ URL Service qua RabbitMQ)

**Domain Events (emitted):**
- `MilestoneReachedEvent` (khi URL đạt 10, 100, 1000 clicks)

**Domain Services:**
- `MilestoneChecker`: check and publish milestone events
- `DeduplicationService`: đảm bảo idempotency khi xử lý click events
- `StatsHandler`: aggregate và trả về thống kê

**Repositories:**
- `ClickRepository`: insert và count clicks
- `MilestoneRepository`: check và insert milestones
- `DeduplicationRepository`: check và insert event IDs

#### Bounded Context 4: Notifications (Context Thông Báo)
- **Service**: `notification-service`
- **Database**: `notification_db` (PostgreSQL)
- **Aggregate Root**: `Notification`
- **Supporting Domain**: ✓
- **Ubiquitous Language**: notification, event, milestone, user notification

**Entities:**
- `NotificationRecord` (id, user_id, user_email, event_type, payload, created_at)

**Domain Events (consumed):**
- `URLCreatedEvent`
- `URLDeletedEvent`
- `MilestoneReachedEvent`

**Repositories:**
- `NotificationRepository`:
  - `InsertNotification(ctx, record)`
  - `ListByUserID(ctx, userID, limit)`

### 2.2 Context Mapping

Sử dụng các mẫu (patterns) trong Context Mapping:

```
┌─────────────────────┐         ┌──────────────────────┐
│  User & Auth        │◄──OHS──│  API Gateway          │
│  (user-service)     │         │  (gateway)            │
└─────────┬───────────┘         └──────────┬────────────┘
          │                                │
          │ OHS (Open Host Service)        │ OHS
          │ HTTP / REST                    │ HTTP / REST
          ▼                                ▼
┌─────────────────────┐         ┌──────────────────────┐
│  URL Shortening     │◄──OHS──│  API Gateway          │
│  (url-service)      │         └──────────────────────┘
└─────────┬───────────┘
          │
          │ Event-Driven (RabbitMQ)
          │ Published Events:
          │ • url.created
          │ • url.clicked
          │ • url.deleted
          ▼
┌─────────────────────┐         ┌──────────────────────┐
│  Analytics          │──OHS───▶│  API Gateway          │
│  (analytics-service)│         │  (stats endpoints)    │
└─────────┬───────────┘         └──────────────────────┘
          │
          │ Event-Driven (RabbitMQ)
          │ Published Events:
          │ • milestone.reached
          ▼
┌─────────────────────┐         ┌──────────────────────┐
│  Notifications      │──OHS───▶│  API Gateway          │
│  (notification-     │         │  (notifications       │
│   service)          │         │   endpoints)          │
└─────────────────────┘         └──────────────────────┘
```

**Patterns được sử dụng:**

1. **Open Host Service (OHS)** - Tất cả services expose HTTP API qua Gateway. Mỗi service publish một API protocol (REST) mà các consumer (Gateway) có thể dùng.

2. **Event-Driven / Published Language** - URL Service và Analytics Service publish domain events qua RabbitMQ. Các downstream services chỉ consume events qua topic exchange, không gọi trực tiếp API.

3. **Separate Ways** - Mỗi service có database riêng, không share DB trực tiếp. Giao tiếp chỉ qua HTTP API (synchronous) hoặc RabbitMQ (asynchronous).

4. **Anti-Corruption Layer (ACL)** - Gateway đóng vai trò ACL giữa external world và internal services:
   - JWT verification ở Gateway (không để unauthenticated requests vào internal network)
   - Path rewriting (strip prefix)
   - Rate limiting
   - Circuit breaker

### 2.3 Event Storming - Domain Events

#### Domain Events Catalog

| Event Type | Producer | Consumer(s) | Trigger | Payload |
|-----------|----------|-------------|---------|---------|
| `url.created` | URL Service | Analytics, Notification | User creates short URL | short_code, original_url, user_id, user_email, expires_at |
| `url.clicked` | URL Service | Analytics | User visits short URL | short_code, user_id, user_email, ip_hash, user_agent, referer, clicked_at |
| `url.deleted` | URL Service | Analytics (future), Notification | User deletes URL | short_code, user_id, user_email |
| `milestone.reached` | Analytics Service | Notification | URL reaches 10/100/1000 clicks | short_code, user_id, user_email, milestone, total_clicks |

#### Event Flow Diagram (Event Storming)

```
┌─────────────────────────────────────────────────────────────────┐
│                      Event Storming Map                         │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Command: Create Short URL                                      │
│  Actor: Authenticated User                                      │
│  Aggregate: ShortURL                                            │
│                                                                 │
│  [Register URL] → [Validate URL] → [Generate Code] → [Store]   │
│       │                                                │        │
│       │                                                ▼        │
│       │                                         ┌──────────┐   │
│       │                                         │ url.created│  │
│       │                                         │ (Outbox)  │  │
│       │                                         └─────┬─────┘  │
│       │                                               │        │
│       │                                               ▼        │
│       │                                         ┌──────────┐   │
│       │                                         │ Analytics │   │
│       │                                         │ (future)  │   │
│       │                                         └──────────┘   │
│       │                                               │        │
│       │                                               ▼        │
│       │                                         ┌──────────┐   │
│       │                                         │ Notify    │   │
│       │                                         │ User      │   │
│       └─────────────────────────────────────────┴──────────┘   │
│                                                                 │
│  Command: Click Short URL                                       │
│  Actor: Anonymous User                                          │
│  Aggregate: ShortURL                                            │
│                                                                 │
│  [Check Cache] → [Find URL] → [Check Active] → [308 Redirect]  │
│       │                                              │          │
│       │                                              │          │
│       │                                    ┌─────────▼──────┐  │
│       │                                    │ url.clicked    │  │
│       │                                    │ (Outbox)       │  │
│       │                                    └────────┬───────┘  │
│       │                                             │          │
│       │                                             ▼          │
│       │                                    ┌──────────────┐   │
│       │                                    │ Analytics:   │   │
│       │                                    │ Insert Click │   │
│       │                                    │ Dedup Check  │   │
│       │                                    │ Milestone?   │   │
│       │                                    └──────┬───────┘   │
│       │                                           │            │
│       │                              ┌────────────┴────────┐   │
│       │                              │ milestone.reached   │   │
│       │                              │ (if threshold met)  │   │
│       │                              └────────┬────────────┘   │
│       │                                       │                │
│       │                                       ▼                │
│       │                              ┌────────────────────┐   │
│       │                              │ Notification:      │   │
│       │                              │ Insert Milestone   │   │
│       │                              │ Notification       │   │
│       │                              └────────────────────┘   │
│                                                                 │
│  Command: Delete Short URL                                      │
│  Actor: Authenticated User (owner)                              │
│  Aggregate: ShortURL                                            │
│                                                                 │
│  [Deactivate URL] → [Invalidate Cache] → [url.deleted]          │
│                                           │                      │
│                                           ▼                      │
│                                  ┌────────────────────┐         │
│                                  │ Notification:      │         │
│                                  │ Insert Delete      │         │
│                                  │ Notification       │         │
│                                  └────────────────────┘         │
└─────────────────────────────────────────────────────────────────┘
```

### 2.4 Aggregate Roots

#### `User` Aggregate (User Context)
```
User {
    id: string (UUID)
    email: Email (value object)
    passwordHash: string (bcrypt)
    createdAt: timestamp
}
```
- **Invariants**: Email phải unique, password đã được hash
- **Repository**: `UserRepository`
- **Factory**: `NewHandler` với store + hasher + issuer

#### `ShortURL` Aggregate (URL Context)
```
ShortURL {
    id: string (UUID)
    shortCode: ShortCode (7-char base62)
    originalURL: URL
    userID: string
    userEmail: string
    createdAt: timestamp
    expiresAt: timestamp | nil
    isActive: boolean
}
```
- **Invariants**: shortCode phải unique, originalURL phải có scheme http/https
- **Business Rules**:
  - URL quá hạn không được redirect
  - URL bị deactivate không được redirect
  - Chỉ owner mới được deactivate URL
- **Repository**: `URLStore`
- **Caching**: `Cache` interface cho Redis projection

#### `Click` Aggregate (Analytics Context)
```
Click {
    shortCode: string
    clickedAt: timestamp
    ipHash: string (one-way hash)
    userAgent: string
    referer: string
}
```
- **Invariants**: Mỗi event chỉ được insert 1 lần (dedup bằng event_id)
- **Repository**: `ClickRepository`

#### `Notification` Aggregate (Notification Context)
```
Notification {
    id: string
    userID: string
    userEmail: string
    eventType: string
    payload: json
    createdAt: timestamp
}
```
- **Repository**: `NotificationRepository`

### 2.5 Ubiquitous Language

| Thuật ngữ | Định nghĩa | Context |
|-----------|-----------|---------|
| **Short Code** | Mã 7 ký tự base62 đại diện cho URL gốc | URL |
| **Short URL** | URL đầy đủ dạng `http://host/r/{short_code}` | URL |
| **Original URL** | URL đích dài cần rút gọn | URL |
| **Redirect** | Chuyển hướng HTTP 308 từ short URL → original URL | URL |
| **Click** | Một lần truy cập vào short URL | Analytics |
| **Milestone** | Ngưỡng click (10, 100, 1000) | Analytics |
| **Outbox** | Bảng database tạm thời lưu domain events chờ publish | URL |
| **Deduplication** | Kiểm tra event đã xử lý chưa để tránh trùng lặp | Analytics |
| **Hash IP** | Băm một chiều địa chỉ IP client để ẩn danh | URL, Analytics |
| **Rate Limit** | Giới hạn số request trong một khoảng thời gian | Gateway |
| **Circuit Breaker** | Ngắt kết nối tới upstream khi có quá nhiều lỗi | Gateway |

### 2.6 Strategic Design Decisions

1. **Tách biệt database hoàn toàn**: Mỗi service có PostgreSQL riêng, không share. Điều này đảm bảo loose coupling và cho phép mỗi service có schema độc lập.

2. **Event-Driven Communication**: Sử dụng RabbitMQ với Topic Exchange pattern cho asynchronous communication. URL Service publish events → Analytics và Notification consume. Điều này giúp URL Service không bị chậm bởi analytics processing.

3. **Transactional Outbox Pattern**: Đảm bảo consistency giữa database state và message publishing. URL và outbox event được insert trong cùng một database transaction.

4. **API Gateway làm ACL**: Gateway xử lý authentication, rate limiting, circuit breaker. Internal services không cần implement lại các cross-cutting concerns này.

5. **Double JWT Verification**: Cả Gateway và internal services đều verify JWT. Gateway kiểm tra để reject unauthorized requests sớm, internal services verify để đảm bảo an toàn nếu có request bypass Gateway.

6. **Cache-aside Pattern**: URL Service sử dụng Redis làm cache với TTL. Cache được populate khi URL được tạo hoặc khi có cache miss từ redirect flow.

---

## 3. API Gateway - Phân Tích Chi Tiết Từng File

### 3.1 `config.go` - Cấu hình hệ thống

**File:** `gateway/config.go` (70 dòng)

#### Cấu trúc `Config`

```go
type Config struct {
    URLServiceURL          string            // URL của url-service
    AnalyticsServiceURL    string            // URL của analytics-service  
    UserServiceURL         string            // URL của user-service
    NotificationServiceURL string            // URL của notification-service
    RedisURL               string            // Redis connection string
    JWTSecret              string            // Secret key cho JWT signing
    ShortenRateLimit       RateLimitConfig   // Rate limit cho POST /api/shorten
    RedirectRateLimit      RateLimitConfig   // Rate limit cho GET /r/{code}
    CircuitBreaker         CircuitBreakerConfig
    Port                   string            // HTTP listen port (default: "8080")
    ServiceName            string            // "gateway" - dùng cho logging
}
```

**Choices & Analysis:**
- **Environment variables**: Tất cả config đều từ env vars, không dùng file config. Phù hợp với 12-factor app và containerized deployment.
- **Required fields validation**: `URL_SERVICE_URL`, `ANALYTICS_SERVICE_URL`, `USER_SERVICE_URL`, `NOTIFICATION_SERVICE_URL`, `REDIS_URL`, `JWT_SECRET` được kiểm tra là required. Nếu thiếu, startup sẽ fail ngay với error message rõ ràng.
- **Default values**: Các config không critical có fallback:
  - `PORT` → `"8080"`
  - `SHORTEN_RATE_LIMIT` → 10 requests/60s
  - `REDIRECT_RATE_LIMIT` → 300 requests/60s
  - `CB_MAX_FAILURES` → 5
  - `CB_OPEN_TIMEOUT_SECS` → 30s
  - `CB_FAILURE_WINDOW_SECS` → 10s

#### Hàm `loadConfig()`

Đọc tất cả biến môi trường, parse số nguyên, kiểm tra required fields. Trả về lỗi nếu thiếu required field. Sử dụng `envOrDefault()` cho optional fields.

#### Hàm `envOrDefault(key, fallback string) string`

Helper function kiểm tra `os.LookupEnv`. Nếu biến môi trường tồn tại thì dùng, nếu không dùng fallback.

**Điểm yếu**: `JWT_SECRET` là plain string trong config. Nên dùng file-based secret hoặc secret manager trong production.

### 3.2 `main.go` - Startup Sequence

**File:** `gateway/main.go` (75 dòng)

#### Startup Sequence (dòng 16-75):

```
1. loadConfig()                          → Load environment variables
2. logger.New("gateway")                 → Tạo structured JSON logger
3. Upstream map initialization           → Map service names to URLs
4. NewProxy(upstreams)                   → Tạo reverse proxy
5. NewRateLimiter(redisURL)              → Kết nối Redis cho rate limiting
6. NewCircuitBreaker(...)                → Khởi tạo circuit breaker
   └─ WithStateChange(callback)          → Đăng ký callback cho state transitions
   └─ recordCBState("url-service", CLOSED) → Ghi nhận initial state
7. NewHandler(proxy, cfg, limiter, cb, log) → Tạo request handler
8. http.NewServeMux()                    → Tạo HTTP mux
9. Route registration:
   └─ GET /health                         → Health check
   └─ GET /metrics                        → Prometheus scrape endpoint
   └─ / (catch-all)                       → JWT middleware + Handler
10. Middleware chain wrapping:
    └─ correlationIDMiddleware           → Gán correlation ID
    └─ logger.RequestLogger              → Ghi log HTTP requests
    └─ corsMiddleware                    → CORS headers
11. http.Server creation                 → ":8080"
12. Goroutine: srv.ListenAndServe()      → Start HTTP server
13. Signal handler (SIGTERM, SIGINT)     → Graceful shutdown
14. srv.Shutdown()                       → Drain connections
```

#### Chi tiết từng bước:

**Dòng 16-21: Load Config**
```go
cfg, err := loadConfig()
if err != nil {
    fmt.Fprintf(os.Stderr, "config error: %v\n", err)
    os.Exit(1)
}
```
Sử dụng `fmt.Fprintf` và `os.Exit(1)` thay vì `log.Fatal` vì chưa có logger instance.

**Dòng 23: Logger Initialization**
```go
log := logger.New(cfg.ServiceName)
```
Tạo `slog.Logger` với JSON handler output ra stdout. Service name là "gateway".

**Dòng 25-30: Upstream Mapping**
```go
upstreams := map[string]string{
    "url-service":          cfg.URLServiceURL,
    "analytics-service":    cfg.AnalyticsServiceURL,
    "user-service":         cfg.UserServiceURL,
    "notification-service": cfg.NotificationServiceURL,
}
```
Map service logical names → URLs. Các key này được dùng trong `routingTable` để xác định upstream.

**Dòng 32: Proxy Creation**
```go
proxy := NewProxy(upstreams)
```

**Dòng 33-38: Rate Limiter**
```go
limiter, err := NewRateLimiter(cfg.RedisURL)
if err != nil {
    log.Error("rate limiter setup failed", "error", err)
    os.Exit(1)
}
defer limiter.Close()
```
Kết nối Redis cho rate limiting. Fatal error nếu Redis URL parse fails.

**Dòng 39-49: Circuit Breaker**
```go
cb := NewCircuitBreaker(
    cfg.CircuitBreaker.MaxFailures,
    time.Duration(cfg.CircuitBreaker.OpenTimeoutSecs)*time.Second,
    time.Duration(cfg.CircuitBreaker.FailureWindowSecs)*time.Second,
).WithStateChange(func(from, to State) {
    recordCBState("url-service", to)
    if to == StateOpen {
        recordCBTrip("url-service")
    }
})
recordCBState("url-service", StateClosed)
```
Circuit breaker được cấu hình với:
- `maxFailures=5` (mặc định)
- `openTimeout=30s` (mặc định)
- `failureWindow=10s` (mặc định)
- State change callback ghi metrics

**Vấn đề**: Circuit breaker chỉ bảo vệ `url-service`. Các services khác (analytics, user, notification) không được bảo vệ. Đây là một hạn chế có chủ ý hoặc thiếu sót.

**Dòng 50: Handler Creation**
```go
handler := NewHandler(proxy, cfg, limiter, cb, log)
```

**Dòng 52-55: Route Registration**
```go
mux := http.NewServeMux()
mux.HandleFunc("GET /health", NewHealthHandler(cfg.ServiceName))
mux.Handle("GET /metrics", promhttp.Handler())
mux.Handle("/", jwtMiddleware(cfg.JWTSecret, handler))
```
Sử dụng Go 1.22+ pattern routing.

**Dòng 57: Middleware Chain**
```go
app := corsMiddleware(logger.RequestLogger(log, correlationIDMiddleware(mux)))
```
Middleware chain từ trong ra ngoài:
1. `correlationIDMiddleware` - gán correlation ID
2. `logger.RequestLogger` - ghi log HTTP request
3. `corsMiddleware` - CORS headers

**Dòng 58: HTTP Server**
```go
srv := &http.Server{Addr: ":" + cfg.Port, Handler: app}
```

**Dòng 61-67: Goroutine Listen**
```go
go func() {
    log.Info("server listening", "port", cfg.Port)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Error("server error", "error", err)
        os.Exit(1)
    }
}()
```

**Dòng 69-74: Graceful Shutdown**
```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
<-quit

log.Info("shutting down gateway")
srv.Shutdown(context.Background())
```
- Channel buffer = 1 để không miss signal
- Bắt cả SIGTERM (container termination) và SIGINT (Ctrl+C)
- `srv.Shutdown(context.Background())` với context không timeout

**Vấn đề shutdown**: `srv.Shutdown` dùng `context.Background()` không có timeout. Nếu có connection treo, gateway sẽ không shutdown. Nên dùng context với timeout (ví dụ 30s).

### 3.3 `router.go` - Routing Table & Path Matching

**File:** `gateway/router.go` (40 dòng)

#### Cấu trúc `Route`

```go
type Route struct {
    Method       string   // HTTP method ("POST", "GET", "DELETE", "")
    PathPrefix   string   // Path prefix để match ("/api/shorten", "/r/", etc.)
    Upstream     string   // Target service name ("url-service", "user-service")
    StripPrefix  string   // Prefix cần strip khi proxy ("/api", "/r")
    RequiresAuth bool     // Có cần JWT authentication không
    RateLimitKey string   // Key để rate limit ("" = không rate limit)
}
```

#### Routing Table (9 routes)

| # | Method | Path | Upstream | Auth | Rate Limit | Strip |
|---|--------|------|----------|------|------------|-------|
| 1 | POST | `/api/auth/register` | user-service | ✗ | - | `/api/auth` → `/register` |
| 2 | POST | `/api/auth/login` | user-service | ✗ | - | `/api/auth` → `/login` |
| 3 | GET | `/api/me` | user-service | ✓ | - | `/api` → `/me` |
| 4 | POST | `/api/shorten` | url-service | ✓ | `shorten` | `/api` → `/shorten` |
| 5 | GET | `/api/urls` | url-service | ✓ | - | `/api` → `/urls` |
| 6 | DELETE | `/api/urls/` | url-service | ✓ | - | `/api` → `/urls/` |
| 7 | GET | `/r/` | url-service | ✗ | `redirect` | `/r` → `/` |
| 8 | GET | `/api/stats/` | analytics-service | ✗ | - | `/api` → `/stats/` |
| 9 | GET | `/api/notifications` | notification-service | ✓ | - | `/api` → `/notifications` |

#### Phân tích routing decisions:

1. **Auth endpoints không rate limit** - Đăng ký và đăng nhập không bị rate limit, đây có thể là vấn đề an ninh. Nên thêm rate limit cho login để chống brute force.
2. **Shorten có rate limit (10/60s)** - Hợp lý, ngăn spam tạo URL.
3. **Redirect có rate limit (300/60s)** - Rate limit cao hơn vì redirect là chức năng public chính.
4. **Stats public** - Ai cũng có thể xem thống kê nếu biết short code.
5. **Notifications yêu cầu auth** - Chỉ user đã login mới xem được notifications của mình.

#### Hàm `matchRoute(r *http.Request) *Route`

```go
func matchRoute(r *http.Request) *Route {
    for i := range routingTable {
        rt := &routingTable[i]
        if rt.Method != "" && rt.Method != r.Method {
            continue
        }
        if strings.HasPrefix(r.URL.Path, rt.PathPrefix) {
            return rt
        }
    }
    return nil
}
```

**Cơ chế matching:**
- Duyệt tuần tự routing table (first-match wins)
- Nếu `rt.Method` không empty, kiểm tra method match
- Kiểm tra path prefix với `strings.HasPrefix`
- Trả về route đầu tiên match

**Vấn đề**: First-match có thể gây nhầm lẫn. Nếu thêm route `GET /api/urls/deactivated`, nó sẽ match route 5 (GET /api/urls) thay vì route mới. Cần sắp xếp routes từ specific → general.

### 3.4 `proxy.go` - Reverse Proxy Implementation

**File:** `gateway/proxy.go` (106 dòng)

#### Hàm `doProxy` - Core Proxy Logic

**Reverse Proxy Details:**

1. **Path Rewriting**: `upstreamPath` được lấy từ context (đã được Handler set sau khi strip prefix). Nếu không có trong context, dùng path gốc.

2. **Director Function** (modifies outgoing request):
   - Parse `baseURL` (e.g., `http://url-service:8080`)
   - Set scheme và host cho upstream request
   - Set path từ `upstreamPath`
   - Forward headers: `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Real-IP`

3. **ErrorHandler**: Khi proxy không thể kết nối tới upstream, trả về 502 Bad Gateway.

4. **statusRecorder**: Wrapper `http.ResponseWriter` để capture status code mà upstream trả về.

**Điểm mạnh:**
- Sử dụng `httputil.ReverseProxy` chuẩn của Go stdlib
- Path rewriting linh hoạt qua context
- Forward đúng headers (X-Forwarded-For, X-Real-IP)

**Điểm yếu:**
- `baseURL` parse fallback: nếu `url.Parse` thất bại, dùng `baseURL` làm host với scheme `http`. Có thể dẫn đến lỗi nếu URL không hợp lệ.
- Không có connection pooling configuration
- Không có retry logic
- Không có timeout configuration riêng

### 3.5 `handler.go` - Request Handler

**File:** `gateway/handler.go` (116 dòng)

#### Cấu trúc `Handler`

```go
type Handler struct {
    proxy          *Proxy
    cfg            *Config
    rateLimiter    rateLimiter        // Interface
    circuitBreaker *CircuitBreaker
    log            *slog.Logger
}

type rateLimiter interface {
    Allow(ctx context.Context, key string, limit int, windowSecs int) (bool, int, error)
}
```

**Design pattern**: `rateLimiter` là interface, cho phép inject mock trong test (xem `fakeRateLimiter` trong `gateway_test.go`). Đây là dependency injection pattern.

#### Flow chi tiết của `ServeHTTP`:

1. **Route matching**: `matchRoute(r)` trả về route đầu tiên match
2. **404 handling**: Nếu không match route nào → JSON `{"error": "not found"}` với status 404
3. **Rate limiting**:
   - Nếu route có `RateLimitKey`, gọi `checkRateLimit`
   - **Fail-open**: Nếu rate limiter gặp lỗi (Redis down), chỉ log warning và cho phép request đi tiếp. Design decision: ưu tiên availability hơn consistency.
   - **Rate limit exceeded**: Trả về 429 Too Many Requests với `Retry-After` header
4. **Path rewriting**: Strip prefix từ path gốc, lưu path đã rewrite vào context
5. **Circuit breaker** (chỉ cho url-service):
   - Bọc proxy call trong `circuitBreaker.Do()`
   - Nếu upstream trả về 5xx, coi là failure
   - Nếu circuit Open → 503 Service Unavailable
   - Ghi metrics: `cb_rejected_total`, `requests_total{status_class="circuit_open"}`
6. **Regular proxy**: Cho các services không có circuit breaker
7. **Metrics recording**: `requests_total` (by service + status_class) và `request_duration_seconds`

#### Hàm `checkRateLimit`

```go
func (h *Handler) checkRateLimit(r *http.Request, key string) (bool, int, error) {
    if h.rateLimiter == nil {
        return true, 0, nil    // No rate limiter → allow all
    }
    cfg := h.cfg.RedirectRateLimit
    if key == "shorten" {
        cfg = h.cfg.ShortenRateLimit
    }
    return h.rateLimiter.Allow(r.Context(), rateLimitKey(key, clientIP(r)), cfg.Limit, cfg.WindowSecs)
}
```

- Nếu `rateLimiter == nil` (trong test), bỏ qua rate limiting
- Chọn config dựa trên key ("shorten" vs "redirect")
- Key format: `"{route_key}:{client_ip}"` (e.g., `"shorten:192.168.1.1"`)

### 3.6 `middleware.go` - Correlation ID & CORS

**File:** `gateway/middleware.go` (48 dòng)

#### Correlation ID Middleware

```go
const correlationIDHeader = "X-Correlation-ID"

func correlationIDMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        correlationID := r.Header.Get(correlationIDHeader)
        if correlationID == "" {
            correlationID = newCorrelationID()
            r.Header.Set(correlationIDHeader, correlationID)
        }
        w.Header().Set(correlationIDHeader, correlationID)
        ctx := logger.ContextWithCorrelationID(r.Context(), correlationID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

**Mục đích:**
- Trace request qua các services
- Nếu client gửi Correlation ID, giữ nguyên
- Nếu không, generate mới (16 bytes crypto/rand → hex → 32 ký tự)
- Inject vào context và response header

#### CORS Middleware

```go
func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH, HEAD")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Correlation-ID")
        w.Header().Set("Access-Control-Expose-Headers", "X-Correlation-ID, Retry-After")

        if r.Method == http.MethodOptions {
            w.WriteHeader(http.StatusNoContent)
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

**Phân tích:**
- `Access-Control-Allow-Origin: *` - Cho phép tất cả origins. Không phù hợp cho production nếu cần bảo mật.
- OPTIONS preflight trả về 204 No Content

**Điểm yếu**: `Access-Control-Allow-Origin: *` quá permissive. Nên cấu hình CORS dựa trên config.

### 3.7 `jwtmiddleware.go` - JWT Authentication

**File:** `gateway/jwtmiddleware.go` (34 dòng)

```go
func jwtMiddleware(secret string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        route := matchRoute(r)
        if route == nil || !route.RequiresAuth {
            next.ServeHTTP(w, r)
            return
        }

        authHeader := r.Header.Get("Authorization")
        if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
            writeError(w, http.StatusUnauthorized, "unauthorized")
            return
        }

        claims, err := auth.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "), secret)
        if err != nil {
            writeError(w, http.StatusUnauthorized, "unauthorized")
            return
        }

        ctx := context.WithValue(r.Context(), auth.TestClaimsKey{}, claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

**Logic:**
1. Route check: Nếu không yêu cầu auth → bỏ qua
2. Authorization header check: Phải có `Bearer <token>`
3. Token verification: `auth.VerifyToken` verify signature, extract claims
4. Context injection: Lưu Claims vào context
5. Next handler

### 3.8 `circuitbreaker.go` - Circuit Breaker State Machine

**File:** `gateway/circuitbreaker.go` (173 dòng)

#### State Machine Diagram

```
        ┌──────────┐
        │  CLOSED  │ ◄────────────────────────────┐
        └────┬─────┘                              │
             │                                    │
      failures >= maxFailures                     │
      (trong failureWindow)                       │
             │                                    │
             ▼                                    │
        ┌──────────┐        openTimeout expired   │
        │   OPEN   │ ────────────────────────────►│
        └────┬─────┘                              │
             │                                    │
    (next request arrives)                        │
             │                                    │
             ▼                                    │
        ┌───────────┐         success             │
        │ HALF_OPEN │ ────────────────────────────┤
        └─────┬─────┘                             │
              │                                   │
         failure                                  │
              │                                   │
              └──────► OPEN (reset timer) ────────┘
```

#### Hàm `Do` - Core Logic

**Phase 1: Pre-execution check**
- `StateOpen` + trong openTimeout → reject với `ErrCircuitOpen`
- `StateOpen` + hết timeout → `StateHalfOpen`, cho phép 1 probe
- `StateHalfOpen` + đang có probe → reject
- `StateHalfOpen` + không có probe → request này là probe
- `StateClosed` → allow

**Phase 2: Context cancellation check**
Kiểm tra context cancellation trước khi gọi upstream.

**Phase 3: Execute upstream call**

**Phase 4: On Failure**
- Half-Open + failure → back to OPEN
- Closed + failure → increment counter, nếu >= maxFailures → OPEN
- Reset failure window nếu đã hết window

**Phase 5: On Success**
- Half-Open + success → back to CLOSED, reset counters
- Closed + success → reset counters

#### Điểm mạnh:
- Lock-free trong hầu hết operations
- Context-aware
- Half-Open probe mechanism
- State change callback cho metrics

#### Điểm yếu:
- **No automatic half-open recovery**: Chỉ chuyển Half-Open khi có request mới
- **Single service**: Chỉ config cho url-service
- **No error type differentiation**: Tất cả errors đều tính là failures
- **No sliding window**: Fixed window với `windowStart`

### 3.9 `ratelimit.go` - Rate Limiter

**File:** `gateway/ratelimit.go` (90 dòng)

#### Fixed Window Algorithm

```go
func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, windowSecs int) (bool, int, error) {
    ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
    defer cancel()

    fullKey := "rl:" + key

    count, err := rl.client.Incr(ctx, fullKey).Result()
    if err != nil {
        return true, 0, err             // Fail-open
    }

    if count == 1 {
        if err := rl.client.Expire(ctx, fullKey, time.Duration(windowSecs)*time.Second).Err(); err != nil {
            return true, 0, err         // Fail-open
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
}
```

**Algorithm:**
1. Context timeout: 100ms cho Redis operations
2. Key prefix: `"rl:"` để namespace
3. INCR: Atomic increment
4. First request (count == 1): Set TTL = window
5. Rate limit check: count > limit → exceeded
6. Retry-After: TTL còn lại

**Fail-open behavior**: Mọi Redis error đều allow request đi qua.

**Điểm yếu của Fixed Window:**
- **Boundary problem**: User có thể vượt limit ở biên giữa 2 windows
- **Redis single point of failure**: Nếu Redis down, rate limiting disabled
- **IP spoofing**: X-Forwarded-For có thể bị giả mạo

### 3.10 `metrics.go` - Prometheus Metrics

**File:** `gateway/metrics.go` (60 dòng)

#### Prometheus Metrics Catalog

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `gateway_circuit_breaker_state` | Gauge | `service` | CB state (0=CLOSED, 1=HALF_OPEN, 2=OPEN) |
| `gateway_circuit_breaker_trips_total` | Counter | `service` | Số lần CB chuyển sang OPEN |
| `gateway_circuit_breaker_rejected_total` | Counter | `service` | Số request bị reject vì OPEN |
| `gateway_requests_total` | Counter | `service`, `status_class` | Request count by service + status class |
| `gateway_request_duration_seconds` | Histogram | `service` | Latency histogram (default buckets) |

### 3.11 `health.go` - Health Check

**File:** `gateway/health.go` (24 dòng)

```go
func NewHealthHandler(serviceName string) http.HandlerFunc {
    resp := HealthResponse{Status: "ok", Service: serviceName}
    body, _ := json.Marshal(resp)  // Pre-encoded
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write(body)
    }
}
```

**Optimization**: Response body được pre-encode tại init time.

**Design decision**: Health check **không** kiểm tra kết nối database vì "too expensive".

### 3.12 `gateway_test.go` - Unit Tests

**File:** `gateway/gateway_test.go` (241 dòng)

#### Test Coverage

| Test | Mục đích |
|------|----------|
| `TestCircuitBreakerTransitions` | Verify CB state machine transitions |
| `TestRateLimitRejectsAndFailOpen` | Rate limit reject (429) + fail-open khi Redis down |
| `TestRouterAndPathRewrite` | Path rewriting: `/r/abc1234` → `/abc1234` |
| `TestJWTMiddlewareProtectsPrivateRoutes` | JWT required cho protected routes |
| `TestJWTMiddlewareSkipsPublicRoutes` | No JWT required cho public routes |
| `TestCorsMiddleware` | OPTIONS preflight → 204, regular GET → pass through |

**Test Patterns:**
- **FakeRateLimiter**: Mock implementation của `rateLimiter` interface
- **httptest.NewServer**: Fake upstream server
- **httptest.NewRecorder**: Capture HTTP response

### 3.13 `go.mod` - Dependencies

```go
module github.com/ikniz/url-shortener/gateway

go 1.23.0

require (
    github.com/ikniz/url-shortener/shared/logger v0.0.0
    github.com/ikniz/url-shortener/shared/auth v0.0.0
    github.com/redis/go-redis/v9 v9.6.0
    github.com/prometheus/client_golang v1.23.2
    github.com/golang-jwt/jwt/v5 v5.3.1
)

replace (
    github.com/ikniz/url-shortener/shared/auth => ../shared/auth
    github.com/ikniz/url-shortener/shared/logger => ../shared/logger
)
```

---

## 4. Phân Tích Chi Tiết Từng Service

### 4.1 User Service

**Vị trí:** `services/user-service/`

**File structure:**
```
user-service/
├── main.go           # Entry point, DI setup, HTTP server
├── handler.go        # HTTP handlers: Register, Login, Me
├── store.go          # UserRepository implementation (pgx)
├── token.go          # JWT token issuer
├── password.go       # bcrypt hashing
├── validate.go       # Email/password validation
├── errors.go         # Error definitions
├── config.go         # Config loading
├── db.go             # Database pool creation
├── health.go         # Health endpoint
├── user_test.go      # Tests
├── Dockerfile        # Container image
└── go.mod            # Module definition
```

**Core Business Logic:**

**Register Flow:**
1. Validate `Content-Type: application/json`
2. Max body size: 1MB (`http.MaxBytesReader`)
3. Parse JSON body: `{email, password}`
4. Validate email format
5. Validate password length (≥ 8 ký tự)
6. Hash password với bcrypt (cost=12)
7. Insert user vào DB
8. Nếu email duplicate → 409 Conflict
9. Return `{user_id, email}` → 201 Created

**Login Flow:**
1. Validate Content-Type, max body
2. Parse JSON body: `{email, password}`
3. Tìm user bằng email
4. **Timing-safe comparison**: Nếu user không tồn tại, vẫn gọi `hasher.Verify` với dummy hash để tránh timing attack
5. Verify password với bcrypt hash
6. Issue JWT token (HS256, TTL = 24h default)
7. Return `{token, expires_at}` → 200 OK

**Me Flow:**
1. JWT middleware verify token
2. Extract claims từ context
3. Return `{user_id, email}` → 200 OK

**Security:**
- **Timing attack protection**: Dùng dummy hash khi user không tồn tại
- **Constant-time comparison**: bcrypt.CompareHashAndPassword
- **Input validation**: Email format, password length
- **SQL injection protection**: pgx parameterized queries

### 4.2 URL Service

**Vị trí:** `services/url-service/`

**File structure:**
```
url-service/
├── main.go           # Entry point, DI, HTTP server, OutboxCoordinator
├── handler.go        # HTTP handlers: Shorten, Redirect, GetUrls, Deactivate
├── service.go        # URLService business logic
├── store.go          # URLStore implementation (pgx)
├── outbox.go         # OutboxCoordinator (poll + publish)
├── outbox_store.go   # OutboxStore implementation (pgx)
├── cache.go          # Redis cache implementation
├── redis.go          # Redis client creation (non-fatal ping)
├── rabbitmq.go       # RabbitMQ connection + exchange declaration
├── publisher.go      # AMQP publisher
├── codegen.go        # ShortCodeGenerator (crypto/rand + base62)
├── base62.go         # Base62 encode/decode
├── validate.go       # URL validation
├── errors.go         # Error definitions + HTTP helpers
├── config.go         # Config loading
├── db.go             # Database pool creation
├── utils.go          # Utility functions
├── url_test.go       # Unit tests
├── Dockerfile        # Container image
└── go.mod            # Module definition
```

**Key Architecture Decisions:**

1. **Three-layer architecture**: Handler → Service → Store
2. **Transactional Outbox Pattern**: URL và outbox event trong cùng transaction
3. **Cache-aside Pattern**: Check cache → query DB → populate cache
4. **Short Code Generation**: crypto/rand 8 bytes → big.Int → base62, 7 characters
5. **Shorten Transaction**: BEGIN → INSERT URL → INSERT outbox → COMMIT
6. **Redirect Flow**: Cache check (50ms) → DB query → populate cache → 308 redirect

### 4.3 Analytics Service

**Vị trí:** `services/analytics-service/`

**Key Features:**
- RabbitMQ consumer cho `url.clicked` events
- Deduplication bằng event_id
- Milestone detection (10, 100, 1000 clicks)
- Stats endpoints: total clicks, timeline
- Transaction atomic: dedup + click insert + milestone check

### 4.4 Notification Service

**Vị trí:** `services/notification-service/`

**Key Features:**
- Multi-event consumer (url.created, url.deleted, milestone.reached)
- Payload preservation
- Manual Ack + NACK with Requeue
- Panic recovery

---

## 5. Shared Packages

### 5.1 `shared/auth` - Authentication Package

**Files:**
- `auth.go` (123 dòng): Token verification, Claims struct, TokenIssuer interface
- `middleware.go` (57 dòng): HTTP middleware, context helpers

**Key Components:**
- `Claims` struct with Sub, Email, Iss, Iat, Exp fields
- `TokenIssuer` interface: Issue + Verify
- `VerifyToken`: Parse JWT, check signing method, verify signature, extract claims, validate issuer
- `JWTMiddleware`: HTTP middleware pattern
- `ClaimsFromContext`: Extract claims from context

### 5.2 `shared/events` - Domain Events Package

**Event Types:**
- `url.created` (URLCreatedEvent)
- `url.clicked` (URLClickedEvent)
- `url.deleted` (URLDeletedEvent)
- `milestone.reached` (MilestoneReachedEvent)

**BaseEvent:** event_type, occurred_at, correlation_id, event_id (UUID v4)

### 5.3 `shared/logger` - Structured Logging

**Features:**
- JSON format output via `slog`
- Service name as default field
- Correlation ID support
- RequestLogger middleware: method, path, status, duration_ms
- Level based on status: ≥500 → Error, ≥400 → Warn, else → Info

---

## 6. Event-Driven Architecture & Outbox Pattern

### 6.1 Transactional Outbox Pattern

URL Service sử dụng **Transactional Outbox Pattern** để đảm bảo reliable event publishing:

```
  1. Request: POST /shorten
  2. BEGIN TRANSACTION
     ├── INSERT INTO urls (...)
     └── INSERT INTO outbox (event_type, payload)
  3. COMMIT
  4. Response to client
  5. OutboxCoordinator (background goroutine, polls every 2s):
     ├── SELECT FROM outbox WHERE published_at IS NULL
     │   FOR UPDATE SKIP LOCKED LIMIT 50
     ├── Mark locked_until = now + 30s
     ├── Publish to RabbitMQ (topic exchange)
     └── UPDATE outbox SET published_at = now()
  6. RabbitMQ delivers to queues
```

**Why Outbox Pattern?**
- **Atomicity**: URL và event trong cùng transaction
- **Reliability**: Nếu RabbitMQ down, event vẫn an toàn trong database
- **Ordering**: Events poll theo created_at ASC
- **At-least-once delivery**: Consumer phải handle deduplication

### 6.2 RabbitMQ Topic Exchange

```
Exchange: url-shortener (topic, durable)

Routing Keys:
├── url.created       → notification queue
├── url.clicked       → analytics queue
├── url.deleted       → notification queue
└── milestone.reached → notification queue
```

### 6.3 Deduplication & Idempotency

Analytics Service implement deduplication để đảm bảo mỗi event chỉ xử lý một lần:
1. Check dedup table: event_id exists?
2. Duplicate → ACK + discard
3. New → INSERT dedup + INSERT click + milestone check trong 1 transaction

---

## 7. Infrastructure & Deployment

### 7.1 Nginx Reverse Proxy

```
upstream gateway_upstream { server gateway:8080; }
upstream frontend_upstream { server frontend:5173; }

location /api/ → gateway_upstream
location /r/   → gateway_upstream
location /health → gateway_upstream
location /     → frontend_upstream (with WebSocket support)
```

### 7.2 Docker Compose

- 18 containers
- 9 health checks
- 10 persistent volumes
- Bridge network `url-shortener`

### 7.3 Kubernetes Manifests

| Service | Replicas | Type |
|---------|----------|------|
| url-service | 3 | ClusterIP |
| analytics-service | 1 | ClusterIP |
| user-service | 1 | ClusterIP |
| notification-service | 1 | ClusterIP |
| gateway | 2 | NodePort :30080 |

---

## 8. Monitoring & Observability

### 8.1 Prometheus Metrics

- Scrape interval: 5s
- 5 targets (gateway + 4 services)
- Gateway metrics: CB state, CB trips, CB rejected, requests total, request duration

### 8.2 Grafana Dashboards

- Auto-provisioned datasources: Prometheus, Loki
- Dashboards: `circuit_breaker.json`, `services_overview.json`
- Home dashboard: Circuit Breaker overview

### 8.3 Loki + Promtail Log Aggregation

- Loki 2.9.1 + Promtail
- Docker container log collection

---

## 9. Phân Tích Mã Nguồn Chi Tiết

### 9.1 Gateway main.go - Phân Tích Từng Dòng

**Dòng 1-14: Package + Imports**
```go
package main

import (
    "context"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/ikniz/url-shortener/shared/logger"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)
```

**Dòng 16-21: Config Load**
- Sử dụng `os.Stderr` cho error output
- Exit code 1 chỉ lỗi startup

**Dòng 23-30: Logger + Upstreams**
- `service` field = "gateway"
- Map keys khớp với Route.Upstream

**Dòng 32-38: Proxy + Rate Limiter**
- Thread-safe proxy
- Fatal nếu Redis URL parse fails
- `defer limiter.Close()` đảm bảo cleanup

**Dòng 39-49: Circuit Breaker**
- Chain method call: `NewCircuitBreaker(...).WithStateChange(...)`
- Initial state = CLOSED

**Dòng 52-55: Route Registration**
- Go 1.22+ pattern routing
- `/` catch-all route (health/metrics match trước)
- jwtMiddleware wraps handler

**Dòng 57: Middleware Chain**
- Execution: corsMiddleware → RequestLogger → correlationIDMiddleware → mux

**Dòng 61-74: Server + Graceful Shutdown**
- Goroutine listen
- Signal handling: SIGTERM, SIGINT
- `srv.Shutdown(context.Background())` - không có timeout

### 9.2 Circuit Breaker State Machine - Chi tiết

**State:**
- `StateClosed (0)`: Hoạt động bình thường
- `StateOpen (1)`: Ngắt kết nối
- `StateHalfOpen (2)`: Thử nghiệm

**Locking strategy:**
- Lock phải được acquire cho mọi state mutation
- `notifyStateChange` gọi AFTER unlock (tránh deadlock)

**Half-Open probe guard:**
- `halfOpenProbe bool`: Chỉ 1 request được thử ở Half-Open state
- Các request khác bị reject với ErrCircuitOpen

**Failure window:**
- Fixed window bắt đầu từ `windowStart`
- Reset failures nếu `time.Since(windowStart) > failureWindow`
- Nếu `failures >= maxFailures` → OPEN

### 9.3 Rate Limiter - Fixed Window Algorithm

**Key format:** `rl:{route_key}:{client_ip}`

**Algorithm steps:**
1. Context timeout 100ms
2. INCR atomic counter
3. First request: EXPIRE set TTL
4. Count > limit → rate limited
5. Retry-After = TTL remaining

**Fail-open: Mọi Redis error → allow request**

**IP extraction: X-Forwarded-For → X-Real-IP → RemoteAddr**

### 9.4 Outbox Coordinator - Worker Pool Pattern

```
Main Goroutine:
  └── poll() every 2s → FetchUnpublished → send to jobs channel

3 Worker Goroutines:
  └── receive job → Publish to RabbitMQ → MarkPublished
```

**Constants:**
- Batch size: 50
- Workers: 3
- Poll interval: 2s
- Lock timeout: 30s

### 9.5 Base62 Codec

- 7 characters
- 62^7 ≈ 3.52 trillion unique codes
- Uses `math/big` for arbitrary precision arithmetic
- Modulo 62 at each position
- Decode: result = result * 62 + index

---

## 10. Điểm Mạnh, Điểm Yếu & Khuyến Nghị

### 10.1 Điểm Mạnh (Strengths)

#### Kiến Trúc
1. **Microservices đúng chuẩn**: 5 services với database riêng, giao tiếp qua HTTP và message broker
2. **API Gateway pattern**: Tập trung cross-cutting concerns (auth, rate limit, CORS, metrics)
3. **Event-Driven Architecture**: Async communication qua RabbitMQ
4. **Transactional Outbox Pattern**: Đảm bảo consistency
5. **DDD alignment**: Bounded contexts rõ ràng, aggregate roots, ubiquitous language
6. **Circuit Breaker**: Bảo vệ system khỏi cascading failures
7. **Fail-open design**: Rate limiter lỗi không block traffic

#### Code Quality
1. **Go stdlib first**: Sử dụng `net/http`, `httputil.ReverseProxy`
2. **Interface-based design**: Dễ test và mock
3. **Dependency Injection**: Qua constructor
4. **Graceful Shutdown**: Signal handling
5. **Context propagation**: Correlation ID xuyên suốt
6. **Thread safety**: sync.Mutex
7. **Comprehensive tests**: CB, rate limit, CORS, JWT, routing

#### Infrastructure
1. **Docker Compose + K8s**: Local + production
2. **Health checks**: Proper dependencies
3. **Prometheus + Grafana**: Auto-provisioned
4. **Loki + Promtail**: Log aggregation
5. **CI/CD**: GitHub Actions
6. **Go Workspace**: Monorepo management

#### Security
1. **JWT authentication**: HMAC-SHA256
2. **Double verification**: Gateway + internal services
3. **bcrypt hashing**: Cost=12
4. **Timing attack protection**: Dummy hash
5. **IP hashing**: One-way hash
6. **Input validation**: URL scheme/host

### 10.2 Điểm Yếu (Weaknesses)

#### Kiến Trúc
1. **Single circuit breaker**: Chỉ bảo vệ url-service, các service khác không có CB
2. **Fixed window rate limit**: Boundary problem có thể bypass
3. **Circuit breaker không có tự động recovery timer**: Chỉ chuyển Half-Open khi có request mới
4. **No service discovery**: URLs hardcoded qua env vars
5. **No retry logic trong proxy**

#### Code Quality
1. **TestClaimsKey dùng trong production**: Technical debt
2. **formatInt implementation**: Kém hiệu quả hơn strconv.Itoa
3. **Shutdown không timeout**: Có thể treo vĩnh viễn
4. **No HTTP timeouts cho Gateway server**: Default zero (vô hạn)
5. **error handling**: Nhiều chỗ ignore error từ json.Encode

#### Security
1. **CORS Allow-Origin: ***: Quá permissive
2. **X-Forwarded-For spoofing**: Có thể bypass rate limit
3. **JWT secret trong env var**: Nên dùng secret manager
4. **No CSRF protection**
5. **No request validation ở Gateway**: Proxy transparent

#### Infrastructure
1. **No circuit breaker cho tất cả services**
2. **No rate limit cho auth endpoints**: Dễ bị brute force
3. **Prometheus scrape interval 5s**: Quá aggressive
4. **Grafana admin/admin**: Default credentials

#### Monitoring
1. **No distributed tracing** (Jaeger/Zipkin)
2. **No structured error tracking** (Sentry)
3. **No SLI/SLO definitions**
4. **No alerting rules** (Alertmanager)

### 10.3 Khuyến Nghị Cải Thiện (Recommendations)

#### Critical (Cần làm ngay)
1. **Thêm circuit breaker cho tất cả upstream services**
2. **Thêm rate limit cho auth endpoints** (login/register)
3. **Fix **``sign`**`sử dụng trong production**: Dùng unexported key hoặc JWTMiddleware từ shared/auth
4. **Thêm graceful shutdown timeout**: 30s timeout cho srv.Shutdown
5. **Cấu hình HTTP timeouts cho Gateway server**

#### High Priority
1. **Sliding window rate limit**: Thay thế fixed window algorithm
2. **CORS dynamic origin**: Cấu hình qua env var thay vì wildcard
3. **Circuit breaker auto recovery timer**: Tự động chuyển Half-Open sau timeout
4. **Thêm retry logic trong proxy**: Với exponential backoff
5. **Distributed tracing**: Jaeger integration
6. **Structured error tracking**: Sentry integration

#### Medium Priority
1. **Health check depth**: DB connectivity check (optional, configurable)
2. **Rate limit by user ID**: Ngoài IP-based
3. **Circuit breaker metrics cho tất cả services**: Không chỉ url-service
4. **Request validation schema**: OpenAPI/Swagger validation
5. **Caching headers**: Cache-Control cho redirect responses
6. **K8s Ingress Controller**: Thay vì NodePort trực tiếp

#### Low Priority
1. **gRPC cho inter-service communication**: HTTP/2 + protobuf
2. **Service Mesh**: Istio/Linkerd cho observability
3. **Database connection pooling tuning**: Max connections, idle timeout
4. **Chaos Engineering**: Chaos Monkey testing
5. **Blue/Green deployment**: Zero-downtime deployments
6. **Canary releases**: Traffic splitting

---

## 11. Kết Luận

Hệ thống URL Shortener Microservices là một implementation xuất sắc của kiến trúc microservices với Go. Các điểm nổi bật:

1. **Kiến trúc vững chắc**: API Gateway + Event-Driven + Outbox Pattern
2. **DDD alignment**: 4 bounded contexts rõ ràng với aggregate roots và ubiquitous language
3. **Production-ready**: Health checks, graceful shutdown, metrics, monitoring
4. **Scalable**: Multi-replica K8s deployment, async event processing
5. **Resilient**: Circuit breaker, fail-open rate limiter, deduplication
6. **Well-tested**: Unit tests cho các component chính
7. **Container-native**: Docker Compose + K8s + CI/CD

Các điểm yếu chủ yếu tập trung ở:
- Security hardening (CORS, rate limit cho auth)
- Completeness (circuit breaker cho tất cả services)
- Observability (distributed tracing)
- Production hardening (timeouts, error handling)

Với các khuyến nghị trên, hệ thống có thể được cải thiện đáng kể về độ tin cậy, bảo mật và khả năng quan sát.

**Kết luận chung**: Đây là một hệ thống microservices được thiết kế tốt, tuân thủ các best practices của Go và cloud-native architecture, phù hợp cho production deployment với một số cải tiến nhỏ.

---

*Báo cáo được tạo tự động bởi AI Agent vào ngày 2026-07-11 dựa trên phân tích mã nguồn đầy đủ.*

---

## Phụ Lục A: Phân Tích Chi Tiết Các Quyết Định Kiến Trúc

### A.1 Tại sao dùng ReverseProxy từ stdlib thay vì thư viện third-party?

**Quyết định**: Sử dụng `net/http/httputil.ReverseProxy` của Go stdlib.

**Lý do:**
1. Không cần thêm dependency
2. `ReverseProxy` đã hỗ trợ đầy đủ: Director function, ErrorHandler, ModifyResponse, buffer pooling
3. Performance tốt (zero allocation hot path trong Go 1.23)
4. Dễ maintain (không phải theo dõi updates từ bên ngoài)

**Trade-off**: Mất đi các features như:
- Circuit breaker tích hợp (phải tự implement)
- Load balancing (phải tự thêm backend)
- Retry logic (phải tự wrap)
- Dynamic service discovery

### A.2 Tại sao dùng RabbitMQ thay vì Kafka?

**Quyết định**: Sử dụng RabbitMQ 3.13 với topic exchange.

**Lý do:**
1. **Use case phù hợp**: Event-driven với throughput trung bình (không phải event streaming)
2. **Routing linh hoạt**: Topic exchange cho phép routing keys pattern matching
3. **Complexity thấp**: So với Kafka, RabbitMQ dễ deploy và operation hơn
4. **AMQP chuẩn**: Hỗ trợ bởi nhiều thư viện
5. **Management UI**: RabbitMQ có built-in management

**Khi nào nên dùng Kafka?**
- Nếu click events cần lưu trữ lâu dài (log compaction)
- Nếu throughput > 100K messages/second
- Nếu cần replay events
- Nếu cần exactly-once semantics

### A.3 Tại sao dùng PostgreSQL cho tất cả services?

**Quyết định**: 4 PostgreSQL databases (user_db, url_db, analytics_db, notification_db).

**Lý do:**
1. **Polyglot persistence không cần thiết**: Tất cả data đều là structured relational
2. **Giảm operational complexity**: Chỉ cần biết 1 database technology
3. **pgx performance**: pgx là Go PostgreSQL driver nhanh nhất
4. **JSONB support**: Cho phép lưu semi-structured data (event payloads)
5. **Transactional guarantees**: ACID cho outbox pattern

### A.4 Tại sao fail-open cho rate limiter?

**Quyết định**: Khi Redis không available, rate limiter cho phép tất cả requests đi qua.

**Lý do:**
1. **Availability > Consistency**: Ưu tiên system hoạt động thay vì block traffic
2. **Redis là single point of failure**: Không có replica cho rate limit data
3. **Rate limit là optional protection**: Không critical cho system correctness
4. **Attack window nhỏ**: Rate limit chỉ ngăn spam, không phải security boundary

**Trade-off**: Khi Redis down, attacker có thể spam API không giới hạn.

### A.5 Tại sao double JWT verification?

**Quyết định**: Cả Gateway và internal services đều verify JWT.

**Lý do:**
1. **Defense in depth**: Nếu Gateway bị compromised hoặc có direct access tới internal services
2. **Internal services có thể được gọi trực tiếp**: Trong K8s, services có thể gọi nhau qua service names
3. **Future-proof**: Internal services độc lập, không phụ thuộc Gateway
4. **Testing**: Dễ test internal services riêng lẻ

### A.6 Tại sao path rewriting?

**Quyết định**: Gateway strip prefix trước khi proxy (e.g., `/api/shorten` → `/shorten`).

**Lý do:**
1. **Internal services không biết về external routing**: URL Service chỉ biết `/shorten`, không biết `/api/shorten`
2. **Flexibility**: Có thể thay đổi external API paths mà không ảnh hưởng internal
3. **Nginx đã xử lý `/api/` prefix**: Gateway nhận request với prefix intact
4. **REST convention**: Internal endpoints clean, không có `/api` prefix

---

## Phụ Lục B: Phân Tích Security

### B.1 Authentication Flow

```
Client                    Gateway                   User Service
  │                         │                          │
  │  POST /api/auth/login   │                          │
  │  {email, password}      │                          │
  │────────────────────────►│                          │
  │                         │  POST /login             │
  │                         │  {email, password}       │
  │                         │─────────────────────────►│
  │                         │                          │
  │                         │                          ├── FindByEmail
  │                         │                          ├── Verify password
  │                         │                          ├── Issue JWT
  │                         │                          │
  │                         │  200 {token, expires_at} │
  │                         │◄─────────────────────────│
  │  200 {token, expires_at}│                          │
  │◄────────────────────────│                          │
```

### B.2 Authorized Request Flow

```
Client                    Gateway                   URL Service
  │                         │                          │
  │  POST /api/shorten      │                          │
  │  Authorization: Bearer  │                          │
  │  {url: "..."}           │                          │
  │────────────────────────►│                          │
  │                         │  jwtMiddleware:          │
  │                         │  ├── matchRoute (auth)   │
  │                         │  ├── VerifyToken         │
  │                         │  └── inject claims       │
  │                         │                          │
  │                         │  handler.ServeHTTP:      │
  │                         │  ├── rate limit check    │
  │                         │  ├── circuit breaker     │
  │                         │  └── proxy → /shorten    │
  │                         │─────────────────────────►│
  │                         │                          │
  │                         │                          │  JWTMiddleware:
  │                         │                          │  ├── VerifyToken
  │                         │                          │  └── inject claims
  │                         │                          │
  │                         │                          │  HandleShorten:
  │                         │                          │  ├── validate URL
  │                         │                          │  ├── generate code
  │                         │                          │  ├── BEGIN TX
  │                         │                          │  │  ├── INSERT URL
  │                         │                          │  │  └── INSERT outbox
  │                         │                          │  ├── COMMIT
  │                         │                          │  └── cache Redis
  │                         │                          │
  │                         │  201 {short_code, ...}   │
  │                         │◄─────────────────────────│
  │  201 {short_code, ...}  │                          │
  │◄────────────────────────│                          │
```

### B.3 Security Threats & Mitigations

| Threat | Severity | Mitigation |
|--------|----------|------------|
| JWT token theft | High | Short TTL (24h), HTTPS required in production |
| Brute force login | Medium | bcrypt cost=12 (slow hash), dummy hash timing protection |
| SQL injection | High | pgx parameterized queries (no raw SQL拼接) |
| XSS | Medium | Frontend sanitization, JSON response (not HTML) |
| CSRF | Medium | JWT token in Authorization header (not cookie) |
| Rate limit bypass | Low | IP-based rate limiting, fail-open design |
| Service-to-service MITM | Medium | Internal network isolation via Docker/K8s network policies |

---

## Phụ Lục C: Performance Analysis

### C.1 Short Code Collision Probability

- 62^7 = 3,521,614,606,208 unique codes (~3.5 trillion)
- Birthday paradox: Sau 1M URLs, collision probability ≈ n²/(2*N) = 10^12/(7*10^12) ≈ 1.4 × 10^-7
- Retry 3 lần: Probability of all 3 failing ≈ (1.4 × 10^-7)^3 ≈ 2.7 × 10^-21 (negligible)
- **Kết luận**: 7 ký tự base62 đủ an toàn cho hàng tỷ URLs

### C.2 Redis Cache Performance

- Cache check timeout: 50ms (nếu Redis chậm, fallback sang DB)
- Cache TTL: URL expiry time hoặc 1h (nếu không có expiry)
- Cache hit ratio dự kiến: >90% cho read-heavy workload (redirect)
- Cache invalidation: Khi URL bị deactivate hoặc expired

### C.3 Circuit Breaker Impact

- CB state transitions không ảnh hưởng performance (chỉ lock ngắn)
- Half-Open probe: 1 request timeout 30s → 30s degraded performance nếu probe thất bại
- Metrics recording: Prometheus counter increment là O(1) operation

---

## Phụ Lục D: Topic Exchange Configuration Detail

### D.1 RabbitMQ Exchange

```go
const (
    exchangeName = "url-shortener"
    exchangeType = "topic"
)

func declareExchange(ch *amqp.Channel) error {
    return ch.ExchangeDeclare(
        exchangeName, // name
        exchangeType, // kind: "topic"
        true,         // durable
        false,        // autoDelete
        false,        // internal
        false,        // noWait
        nil,          // args
    )
}
```

### D.2 Queue Declarations

**Analytics Queue:**
```go
const analyticsQueue = "analytics.queue"

func DeclareAnalyticsQueue(ch *amqp.Channel) error {
    _, err := ch.QueueDeclare(
        analyticsQueue,
        true,  // durable
        false, // autoDelete
        false, // exclusive
        false, // noWait
        nil,   // args
    )
    if err != nil {
        return err
    }
    return ch.QueueBind(
        analyticsQueue,
        "url.clicked",   // routing key
        exchangeName,    // exchange
        false,
        nil,
    )
}
```

**Notification Queue:**
```go
const notificationQueue = "notification.queue"

func DeclareNotificationQueue(ch *amqp.Channel) error {
    _, err := ch.QueueDeclare(
        notificationQueue,
        true,  // durable
        false, // autoDelete
        false, // exclusive
        false, // noWait
        nil,   // args
    )
    if err != nil {
        return err
    }
    // Bind to multiple routing keys
    for _, key := range []string{"url.created", "url.deleted", "milestone.reached"} {
        if err := ch.QueueBind(notificationQueue, key, exchangeName, false, nil); err != nil {
            return err
        }
    }
    return nil
}
```

---

## Phụ Lục E: Error Codes Catalog

### E.1 Gateway Errors

| HTTP Status | Code | Condition |
|-------------|------|-----------|
| 400 | `not found` | No matching route |
| 401 | `unauthorized` | Missing/invalid JWT |
| 429 | `rate limit exceeded` | Too many requests |
| 502 | `upstream not found` | Invalid upstream name |
| 502 | `bad gateway` | Upstream connection error |
| 503 | `url-service unavailable` | Circuit breaker OPEN |

### E.2 URL Service Errors

| HTTP Status | Error | Description |
|-------------|-------|-------------|
| 400 | `invalid request body` | JSON parse error |
| 400 | `invalid URL` | URL validation failed |
| 400 | `missing short code` | Empty path value |
| 401 | `user not authenticated` | Missing/invalid claims |
| 403 | `forbidden` | Not URL owner |
| 404 | `url not found` | Short code not in DB |
| 409 | `short code already exists` | Collision (after 3 retries) |
| 410 | `url has expired` | URL past expiry date |
| 410 | `url has been deactivated` | URL soft-deleted |
| 500 | `database error` | Internal DB error |

### E.3 User Service Errors

| HTTP Status | Error | Description |
|-------------|-------|-------------|
| 400 | `invalid request body` | JSON parse error |
| 400 | `invalid email format` | Email validation |
| 400 | `password must be at least 8 characters` | Password validation |
| 401 | `invalid credentials` | Wrong email/password |
| 409 | `email already registered` | Duplicate email |
| 415 | `content-type must be application/json` | Wrong Content-Type |
| 500 | `internal server error` | Generic error |

### E.4 Analytics Service Errors

| HTTP Status | Error | Description |
|-------------|-------|-------------|
| 404 | `not found` | No stats for code |
| 500 | `internal server error` | Generic error |

### E.5 Notification Service Errors

| HTTP Status | Error | Description |
|-------------|-------|-------------|
| 401 | `unauthorized` | Invalid JWT |
| 404 | `not found` | No notifications |
| 500 | `internal server error` | Generic error |

---

## Phụ Lục F: File-by-File Analysis Summary

### F.1 Gateway Files

| File | Lines | Functions | Types | Purpose |
|------|-------|-----------|-------|---------|
| `main.go` | 75 | `main` | - | Entry point, DI, graceful shutdown |
| `config.go` | 70 | `loadConfig`, `envOrDefault` | `Config`, `CircuitBreakerConfig` | Configuration |
| `router.go` | 40 | `matchRoute` | `Route` | Routing table |
| `proxy.go` | 106 | `NewProxy`, `ServeHTTP`, `ServeHTTPStatus`, `doProxy`, `SetUpstreamPath` | `Proxy`, `upstreamPathKey`, `statusRecorder` | Reverse proxy |
| `handler.go` | 116 | `NewHandler`, `ServeHTTP`, `checkRateLimit`, `recordUpstreamMetrics`, `formatInt` | `Handler`, `rateLimiter` | Request handler |
| `middleware.go` | 48 | `correlationIDMiddleware`, `newCorrelationID`, `corsMiddleware` | - | Middleware |
| `jwtmiddleware.go` | 34 | `jwtMiddleware` | - | JWT authentication |
| `circuitbreaker.go` | 173 | `NewCircuitBreaker`, `WithStateChange`, `notifyStateChange`, `Do`, `State` | `State`, `CircuitBreaker` | Circuit breaker |
| `ratelimit.go` | 90 | `NewRateLimiter`, `Allow`, `Close`, `rateLimitKey`, `clientIP`, `parseInt` | `RateLimiter`, `RateLimitConfig` | Rate limiter |
| `metrics.go` | 60 | `recordCBState`, `recordCBTrip`, `recordCBRejected` | - | Prometheus metrics |
| `errors.go` | 20 | `writeError`, `writeJSON` | - | Error helpers |
| `health.go` | 24 | `NewHealthHandler` | `HealthResponse` | Health check |
| `gateway_test.go` | 241 | `TestCircuitBreakerTransitions`, `TestRateLimitRejectsAndFailOpen`, etc. | `fakeRateLimiter` | Tests |

### F.2 URL Service Files

| File | Lines | Purpose |
|------|-------|---------|
| `main.go` | 119 | Entry point, DI, OutboxCoordinator |
| `handler.go` | 209 | HTTP handlers |
| `service.go` | 349 | Business logic |
| `store.go` | 110 | URL data access |
| `outbox.go` | 97 | Outbox poller |
| `outbox_store.go` | 91 | Outbox data access |
| `cache.go` | 60 | Redis cache |
| `redis.go` | 41 | Redis client |
| `rabbitmq.go` | 101 | RabbitMQ connection |
| `publisher.go` | 38 | AMQP publisher |
| `codegen.go` | 40 | Short code generator |
| `base62.go` | 69 | Base62 codec |
| `validate.go` | 24 | URL validation |
| `errors.go` | 30 | Error definitions |
| `config.go` | - | Config loading |
| `db.go` | - | Database pool |

### F.3 Shared Package Files

| File | Lines | Functions | Purpose |
|------|-------|-----------|---------|
| `shared/auth/auth.go` | 123 | `VerifyToken`, `Claims.IsExpired`, `Claims.ExpiresAt` | JWT verification |
| `shared/auth/middleware.go` | 57 | `JWTMiddleware`, `ClaimsFromContext` | HTTP middleware |
| `shared/events/events.go` | 75 | `NewBaseEvent` | Domain events |
| `shared/logger/logger.go` | 80 | `New`, `WithCorrelationID`, `ContextWithCorrelationID`, `CorrelationIDFromContext`, `RequestLogger` | Structured logging |

---

## Phụ Lục G: Go Version-Specific Features Used

### G.1 Go 1.22 Pattern Routing

```go
mux.HandleFunc("GET /health", handler)
mux.HandleFunc("POST /shorten", handler)
mux.HandleFunc("GET /{code}", handler)
mux.Handle("DELETE /urls/{code}", authMw(handler))
```

Benefits:
- Method-based routing (GET, POST, DELETE)
- Path parameters (`r.PathValue("code")`)
- No third-party router needed

### G.2 Go 1.21 slog

```go
import "log/slog"

log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
log.Info("server listening", "port", port)
log.Error("failed", "error", err)
```

Benefits:
- Structured logging (key-value pairs)
- JSON output format
- Level-based logging (Info, Warn, Error)
- Zero-allocation hot path

### G.3 Go Workspace (go.work)

```go
go 1.23.0

use (
    ./gateway
    ./services/url-service
    ./shared/logger
)
```

Benefits:
- Monorepo management
- Local replace directives không cần thiết
- Build cache sharing
- Cross-module refactoring

---

## Phụ Lục H: Container Specifications

### H.1 Gateway Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o gateway .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/gateway /gateway
EXPOSE 8080
CMD ["/gateway"]
```

Multi-stage build:
- **Stage 1 (builder)**: Go 1.23-alpine, download dependencies, build binary
- **Stage 2 (runtime)**: Alpine 3.19, chỉ copy binary + ca-certificates
- **Final image size**: ~15MB (so với ~1GB cho builder stage)

### H.2 URL Service Dockerfile

Tương tự Gateway, nhưng build context là root (do có shared packages).

### H.3 Frontend Dockerfile

```dockerfile
FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 5173
CMD ["nginx", "-g", "daemon off;"]
```

---

*Báo cáo hoàn chỉnh được tạo tự động bởi AI Agent. Tổng số dòng: 2200+*

*Hết báo cáo.*
