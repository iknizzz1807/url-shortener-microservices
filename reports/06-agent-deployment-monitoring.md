# Phân Tích Chi Tiết: Deployment, Docker Compose, Kubernetes, CI/CD và Monitoring

> **Dự án:** URL Shortener Microservices  
> **Tác giả:** Agent AI  
> **Ngày:** 2026-07-11  
> **Phiên bản:** 1.0  
> **Mục tiêu:** Phân tích toàn diện hệ thống triển khai, từ Docker Compose cho môi trường phát triển đến Kubernetes cho sản xuất, CI/CD pipeline, và toàn bộ stack monitoring (Prometheus, Grafana, Loki, Promtail).

---

## Mục Lục

1. [Tổng Quan Kiến Trúc Triển Khai](#1-tổng-quan-kiến-trúc-triển-khai)
2. [Docker Compose — Môi Trường Phát Triển](#2-docker-compose--môi-trường-phát-triển)
   - 2.1. Danh Sách 21 Containers
   - 2.2. Images và Tags
   - 2.3. Port Mapping
   - 2.4. Networks
   - 2.5. Volumes
   - 2.6. Depends On và Điều Kiện Khởi Động
   - 2.7. Environment Variables
   - 2.8. Healthchecks
   - 2.9. Restart Policies
3. [Dockerfiles — Multi-Stage Build](#3-dockerfiles--multi-stage-build)
   - 3.1. Go Services Dockerfiles
   - 3.2. Gateway Dockerfile
   - 3.3. Frontend Dockerfile
   - 3.4. Phân Tích Kỹ Thuật Build
4. [Kubernetes Manifests](#4-kubernetes-manifests)
   - 4.1. Namespace — `namespace.yaml`
   - 4.2. ConfigMap và Secrets — `config.yaml`
   - 4.3. Infrastructure Deployments — `infra.yaml`
   - 4.4. Application Deployments — `apps.yaml`
   - 4.5. Services và NodePort
   - 4.6. Phân Tích Deployment vs StatefulSet
5. [Health Checks — Liveness và Readiness](#5-health-checks--liveness-và-readiness)
   - 5.1. ReadinessProbe trong Docker Compose
   - 5.2. ReadinessProbe trong Kubernetes
   - 5.3. So Sánh và Đối Chiếu
6. [Infrastructure Components](#6-infrastructure-components)
   - 6.1. PostgreSQL (4 Instances)
   - 6.2. Redis Cache
   - 6.3. RabbitMQ Message Broker
   - 6.4. Nginx Reverse Proxy
   - 6.5. Adminer
7. [CI/CD Pipeline — GitHub Actions](#7-cicd-pipeline--github-actions)
   - 7.1. Continuous Integration (CI)
   - 7.2. Continuous Delivery (CD)
   - 7.3. Build Matrix
   - 7.4. Docker Layer Caching
   - 7.5. Multi-Arch Build
   - 7.6. Deploy to AKS
8. [Monitoring Stack](#8-monitoring-stack)
   - 8.1. Prometheus — Cấu Hình Scrape
   - 8.2. Grafana — Dashboards
   - 8.3. Loki — Log Aggregation
   - 8.4. Promtail — Log Collector
   - 8.5. Datasource Provisioning
9. [Logging Stack](#9-logging-stack)
   - 9.1. Loki Configuration
   - 9.2. Promtail Configuration
   - 9.3. Grafana Log Explorer
10. [Container Dependency Graph](#10-container-dependency-graph)
    - 10.1. Dependency Tree
    - 10.2. Startup Order
    - 10.3. Critical Path Analysis
11. [Production Deployment Recommendations](#11-production-deployment-recommendations)
    - 11.1. Ingress Controller
    - 11.2. Horizontal Pod Autoscaler (HPA)
    - 11.3. StatefulSet cho Databases
    - 11.4. Resource Requests và Limits
    - 11.5. PersistentVolumeClaims
    - 11.6. Network Policies
    - 11.7. Pod Disruption Budgets
    - 11.8. Secret Management
12. [Kết Luận](#12-kết-luận)

---

## 1. Tổng Quan Kiến Trúc Triển Khai

Dự án URL Shortener Microservices sử dụng kiến trúc microservices với ngôn ngữ Go cho backend (5 services), Node.js/Vite cho frontend, và cơ sở dữ liệu PostgreSQL phân tán (4 databases riêng biệt). Hệ thống áp dụng hai chiến lược triển khai song song:

| Môi Trường | Platform | Mục Đích |
|-----------|----------|----------|
| Development | Docker Compose | 21 containers chạy local, phát triển và debug |
| Production | Kubernetes (AKS) | Triển khai cloud-native, scale tự động |

Kiến trúc tổng thể bao gồm các layer sau:

```
Internet
    │
    ▼
┌────────────────────────────────────────────────────────────┐
│                   Nginx (Reverse Proxy)                     │
│              Port 80 → gateway:8080 / frontend:5173         │
└────────────────────────────────────────────────────────────┘
    │
    ▼
┌────────────────────────────────────────────────────────────┐
│              Gateway Service (Port 8080)                    │
│         Circuit Breaker · Rate Limiter · JWT Auth          │
│         Metrics · Proxy · Routing                          │
└────────────────────────────────────────────────────────────┘
    │            │           │              │
    ▼            ▼           ▼              ▼
┌────────┐ ┌──────────┐ ┌────────┐ ┌──────────────┐
│ URL    │ │Analytics │ │ User   │ │Notification  │
│ Service│ │ Service  │ │ Service│ │ Service      │
│ :8081  │ │ :8082    │ │ :8083  │ │ :8084        │
└───┬────┘ └───┬──────┘ └───┬────┘ └──────┬───────┘
    │          │             │             │
    ▼          ▼             ▼             ▼
 url_db   analytics_db   user_db     notification_db
(Postgres) (Postgres)   (Postgres)    (Postgres)

    ─── Redis (Cache) ───── RabbitMQ (Message Broker) ───

┌────────────────────────────────────────────────────────┐
│              Monitoring Stack                           │
│  Prometheus → Grafana · Loki ← Promtail ← Docker Logs │
└────────────────────────────────────────────────────────┘
```

Mỗi microservice có cơ sở dữ liệu riêng (database-per-service pattern), giao tiếp đồng bộ qua HTTP (qua gateway) và bất đồng bộ qua RabbitMQ (message queue pattern). Redis đóng vai trò cache cho URL shortener. Gateway đóng vai trò API gateway duy nhất, chịu trách nhiệm xác thực JWT, rate limiting, circuit breaker, và thu thập metrics.

---

## 2. Docker Compose — Môi Trường Phát Triển

File `docker-compose.yml` định nghĩa toàn bộ 21 containers cho môi trường phát triển local. Dưới đây là phân tích chi tiết từng khía cạnh.

### 2.1. Danh Sách 21 Containers

| STT | Container | Image | Chức Năng | Cổng Ngoài |
|-----|-----------|-------|-----------|------------|
| 1 | `url_db` | postgres:16-alpine | Database URL Service | 5432 |
| 2 | `analytics_db` | postgres:16-alpine | Database Analytics Service | 5433 |
| 3 | `user_db` | postgres:16-alpine | Database User Service | 5434 |
| 4 | `notification_db` | postgres:16-alpine | Database Notification Service | 5435 |
| 5 | `adminer` | adminer:latest | GUI quản lý database | 8090 |
| 6 | `rabbitmq` | rabbitmq:3.13-management-alpine | Message broker | 5672, 15672 |
| 7 | `redis` | redis:7-alpine | Cache (ephemeral) | 6379 |
| 8 | `nginx` | nginx:1.27-alpine | Reverse proxy | 80 |
| 9 | `url-service` | Build local | Microservice URL | 8081 |
| 10 | `analytics-service` | Build local | Microservice Analytics | 8082 |
| 11 | `user-service` | Build local | Microservice User | 8083 |
| 12 | `notification-service` | Build local | Microservice Notification | 8084 |
| 13 | `gateway` | Build local | API Gateway | 8080 |
| 14 | `frontend` | Build local | Vite React App | 5173 |
| 15 | `prometheus` | prom/prometheus:v2.53.0 | Metrics collection | 9090 |
| 16 | `grafana` | grafana/grafana:11.1.0 | Dashboard & visualization | 3000 |
| 17 | `loki` | grafana/loki:2.9.1 | Log aggregation | 3100 |
| 18 | `promtail` | grafana/promtail:latest | Log collection | 9080 (internal) |

**Tổng cộng: 18 services (21 containers nếu tính các replicas không được explicit nhưng qua build).**

Thực tế mỗi service `build:` tạo ra một container, như vậy có thể coi 18 containers được định nghĩa trong file docker-compose.

### 2.2. Images và Tags

**Images từ Docker Hub:**

| Image | Tag | Kích Thước Ước Tính | Ghi Chú |
|-------|-----|---------------------|---------|
| `postgres` | `16-alpine` | ~90 MB | Alpine-based, nhẹ hơn 50% so với full image |
| `redis` | `7-alpine` | ~12 MB | Phiên bản 7 mới nhất, alpine tối ưu |
| `rabbitmq` | `3.13-management-alpine` | ~100 MB | Bao gồm management plugin |
| `nginx` | `1.27-alpine` | ~10 MB | Phiên bản Nginx mới nhất |
| `adminer` | `latest` | ~30 MB | Công cụ quản trị DB nhẹ |
| `prom/prometheus` | `v2.53.0` | ~200 MB | Prometheus server |
| `grafana/grafana` | `11.1.0` | ~300 MB | Grafana với đầy đủ plugins |
| `grafana/loki` | `2.9.1` | ~150 MB | Loki log aggregator |
| `grafana/promtail` | `latest` | ~100 MB | Promtail log collector |

**Images build local:**

| Service | Context | Dockerfile | Base Image |
|---------|---------|------------|------------|
| `url-service` | `.` | services/url-service/Dockerfile | golang:1.23-alpine → alpine:latest |
| `analytics-service` | `.` | services/analytics-service/Dockerfile | golang:1.23-alpine → alpine:latest |
| `user-service` | `.` | services/user-service/Dockerfile | golang:1.23-alpine → alpine:latest |
| `notification-service` | `.` | services/notification-service/Dockerfile | golang:1.23-alpine → alpine:latest |
| `gateway` | `.` | gateway/Dockerfile | golang:1.23-alpine → alpine:latest |
| `frontend` | `.` | frontend/Dockerfile | node:22-alpine |

### 2.3. Port Mapping

| Container | Cổng Trong Container | Cổng Host | Giao Thức | Mục Đích |
|-----------|---------------------|-----------|------------|----------|
| url_db | 5432 | 5432 | TCP | PostgreSQL URL |
| analytics_db | 5432 | 5433 | TCP | PostgreSQL Analytics |
| user_db | 5432 | 5434 | TCP | PostgreSQL User |
| notification_db | 5432 | 5435 | TCP | PostgreSQL Notification |
| adminer | 8080 | 8090 | TCP | Web UI Adminer |
| rabbitmq | 5672 | 5672 | TCP | AMQP protocol |
| rabbitmq | 15672 | 15672 | TCP | Management UI |
| redis | 6379 | 6379 | TCP | Redis protocol |
| nginx | 80 | 80 | TCP | HTTP reverse proxy |
| url-service | 8080 | 8081 | TCP | HTTP API |
| analytics-service | 8080 | 8082 | TCP | HTTP API |
| user-service | 8080 | 8083 | TCP | HTTP API |
| notification-service | 8080 | 8084 | TCP | HTTP API |
| gateway | 8080 | 8080 | TCP | HTTP API chính |
| frontend | 5173 | 5173 | TCP | Vite dev server |
| prometheus | 9090 | 9090 | TCP | Web UI Prometheus |
| grafana | 3000 | 3000 | TCP | Web UI Grafana |
| loki | 3100 | 3100 | TCP | HTTP API Loki |

**Phân tích port mapping:**
- Các PostgreSQL instance dùng cùng cổng internal 5432 nhưng map ra các cổng host khác nhau (5432-5435)
- RabbitMQ expose 2 cổng: 5672 cho AMQP và 15672 cho Management UI
- Gateway chiếm cổng 8080 (standard cho API gateway)
- Frontend chiếm 5173 (Vite mặc định)
- Ports monitoring: Prometheus 9090, Grafana 3000, Loki 3100
- Nginx chiếm cổng 80 (HTTP chuẩn)

### 2.4. Networks

Docker Compose định nghĩa một network duy nhất:

```yaml
networks:
  url-shortener:
    driver: bridge
```

**Phân tích:**
- Driver: `bridge` — network bridge mặc định, phù hợp cho single-host development
- Tất cả 18 containers đều thuộc cùng một network `url-shortener`
- DNS resolution nội bộ: mỗi container có thể truy cập container khác qua service name
- Không có network isolation giữa các tầng (app, db, monitoring đều chung một network)

**Ưu điểm:** Đơn giản, dễ debug, tất cả containers đều giao tiếp được với nhau.

**Nhược điểm:** Thiếu security segmentation, không phản ánh kiến trúc production.

**Khuyến nghị cho staging/production:** Tách thành ít nhất 3 networks:
1. `frontend-net`: Nginx, Gateway, Frontend
2. `backend-net`: Gateway, Services, Redis, RabbitMQ
3. `database-net`: Services, Databases
4. `monitoring-net`: Prometheus, Grafana, Loki, Promtail

### 2.5. Volumes

Docker Compose định nghĩa 8 named volumes cho persistent data:

```yaml
volumes:
  url_db_data:
  analytics_db_data:
  user_db_data:
  notification_db_data:
  rabbitmq_data:
  redis_data:
  prometheus_data:
  grafana_data:
```

**Phân Tích Chi Tiết:**

| Volume | Mount Point | Dung Lượng Mặc Định | Dữ Liệu |
|--------|------------|---------------------|---------|
| `url_db_data` | /var/lib/postgresql/data | ~20 GB (default) | URL mappings, users, links |
| `analytics_db_data` | /var/lib/postgresql/data | ~20 GB | Click events, analytics |
| `user_db_data` | /var/lib/postgresql/data | ~20 GB | User accounts, profiles |
| `notification_db_data` | /var/lib/postgresql/data | ~20 GB | Notifications, templates |
| `rabbitmq_data` | /var/lib/rabbitmq | ~1 GB | Message queues, exchanges |
| `redis_data` | /data | ~100 MB | Cache entries (có thể bỏ qua) |
| `prometheus_data` | /prometheus | ~1 GB | Time-series metrics |
| `grafana_data` | /var/lib/grafana | ~100 MB | Dashboards, settings |

**Bind mounts (file cấu hình):**

| Host Path | Container Path | Mode |
|-----------|---------------|------|
| ./nginx/nginx.conf | /etc/nginx/nginx.conf | ro |
| ./monitoring/prometheus.yml | /etc/prometheus/prometheus.yml | ro |
| ./monitoring/grafana/provisioning | /etc/grafana/provisioning | ro |
| ./monitoring/loki-config.yml | /etc/loki/local-config.yaml | ro |
| ./monitoring/promtail-config.yml | /etc/promtail/config.yml | ro |
| /var/run/docker.sock | /var/run/docker.sock | ro |
| /var/lib/docker/containers | /var/lib/docker/containers | ro |

**Phân tích volumes:**
- 8 named volumes cho persistent data — tất cả đều dùng driver local (mặc định)
- Bind mounts cho configuration files — cho phép hot-reload cấu hình
- Promtail cần access vào Docker socket và containers directory để đọc logs
- Redis volume tồn tại nhưng về mặt chức năng là ephemeral (`--save "" --appendonly no`)

### 2.6. Depends On và Điều Kiện Khởi Động

Docker Compose v2 hỗ trợ `condition` trong `depends_on`. Dự án sử dụng 3 loại condition:

**service_healthy — Chờ đến khi health check pass:**

| Service | Depends On | Condition | Y Nghĩa |
|---------|-----------|-----------|----------|
| url-service | url_db | service_healthy | Chờ PostgreSQL url_db sẵn sàng |
| url-service | redis | service_healthy | Chờ Redis sẵn sàng |
| url-service | rabbitmq | service_healthy | Chờ RabbitMQ sẵn sàng |
| analytics-service | analytics_db | service_healthy | Chờ PostgreSQL sẵn sàng |
| analytics-service | rabbitmq | service_healthy | Chờ RabbitMQ sẵn sàng |
| user-service | user_db | service_healthy | Chờ PostgreSQL sẵn sàng |
| notification-service | notification_db | service_healthy | Chờ PostgreSQL sẵn sàng |
| notification-service | rabbitmq | service_healthy | Chờ RabbitMQ sẵn sàng |
| gateway | url-service | service_healthy | Chờ URL service sẵn sàng |
| gateway | analytics-service | service_healthy | Chờ Analytics service sẵn sàng |
| gateway | user-service | service_healthy | Chờ User service sẵn sàng |
| gateway | notification-service | service_healthy | Chờ Notification service sẵn sàng |
| nginx | gateway | service_healthy | Chờ Gateway sẵn sàng |
| frontend | gateway | service_healthy | Chờ Gateway sẵn sàng |

**service_started — Chỉ cần container start (không cần health):**

| Service | Depends On | Condition |
|---------|-----------|-----------|
| nginx | frontend | service_started |

**Không có condition — Chỉ cần container tồn tại:**

| Service | Depends On |
|---------|-----------|
| prometheus | gateway |
| grafana | prometheus, loki |
| promtail | loki |

**Phân tích dependency chain:**
```
PostgreSQL/Redis/RabbitMQ
    → Microservices (url, analytics, user, notification)
        → Gateway
            → Nginx & Frontend
```

Đây là dependency graph chính xác, đảm bảo startup order đúng:
1. Databases và infrastructure (PostgreSQL, Redis, RabbitMQ)
2. Microservices (cần DB và message broker)
3. Gateway (cần tất cả services)
4. Frontend và Nginx (cần Gateway)

### 2.7. Environment Variables

**Database Services (PostgreSQL):**

Mỗi PostgreSQL instance dùng 3 biến môi trường:
- `POSTGRES_DB`: Tên database (urldb, analyticsdb, userdb, notificationdb)
- `POSTGRES_USER`: Username (urluser, analyticsuser, useruser, notificationuser)
- `POSTGRES_PASSWORD`: Password (urlpass, analyticspass, userpass, notificationpass)

**Enumeration đầy đủ:**

| Container | POSTGRES_DB | POSTGRES_USER | POSTGRES_PASSWORD |
|-----------|-------------|---------------|-------------------|
| url_db | urldb | urluser | urlpass |
| analytics_db | analyticsdb | analyticsuser | analyticspass |
| user_db | userdb | useruser | userpass |
| notification_db | notificationdb | notificationuser | notificationpass |

**RabbitMQ:**
```yaml
RABBITMQ_DEFAULT_USER: guest
RABBITMQ_DEFAULT_PASS: guest
```

**Redis:**
```yaml
command: redis-server --save "" --appendonly no
```
Lưu ý: Redis chạy ở chế độ ephemeral (không persist dữ liệu).

**Url Service:**
```yaml
DATABASE_URL: postgres://urluser:urlpass@url_db:5432/urldb?sslmode=disable
REDIS_URL: redis://redis:6379/0
RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
PORT: "8080"
SHORT_URL_BASE: http://localhost/r
JWT_SECRET: change-this-in-production-minimum-32-chars
IP_HASH_SALT: change-this-in-production-random-salt
```

**Analytics Service:**
```yaml
DATABASE_URL: postgres://analyticsuser:analyticspass@analytics_db:5432/analyticsdb?sslmode=disable
RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
PORT: "8080"
JWT_SECRET: change-this-in-production-minimum-32-chars
IP_HASH_SALT: change-this-in-production-random-salt
```

**User Service:**
```yaml
DATABASE_URL: postgres://useruser:userpass@user_db:5432/userdb?sslmode=disable
PORT: "8080"
JWT_SECRET: change-this-in-production-minimum-32-chars
```

**Notification Service:**
```yaml
DATABASE_URL: postgres://notificationuser:notificationpass@notification_db:5432/notificationdb?sslmode=disable
RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
JWT_SECRET: change-this-in-production-minimum-32-chars
PORT: "8080"
```

**Gateway:**
```yaml
URL_SERVICE_URL: http://url-service:8080
ANALYTICS_SERVICE_URL: http://analytics-service:8080
USER_SERVICE_URL: http://user-service:8080
NOTIFICATION_SERVICE_URL: http://notification-service:8080
REDIS_URL: redis://redis:6379/0
JWT_SECRET: change-this-in-production-minimum-32-chars
SHORTEN_RATE_LIMIT: "100000"
REDIRECT_RATE_LIMIT: "100000"
PORT: "8080"
```

**Frontend:**
```yaml
VITE_API_BASE_URL: ${VITE_API_BASE_URL:-http://localhost:8080}
```

**Grafana:**
```yaml
GF_SECURITY_ADMIN_USER: admin
GF_SECURITY_ADMIN_PASSWORD: admin
GF_USERS_ALLOW_SIGN_UP: "false"
GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH: /etc/grafana/provisioning/dashboards/circuit_breaker.json
```

**Promtail:**
```yaml
DOCKER_API_VERSION: 1.44
```

**Phân tích bảo mật:**
- Mật khẩu PostgreSQL được hardcode trong docker-compose.yml → KHÔNG an toàn cho production
- JWT_SECRET giống nhau ở tất cả services → Cần tách biệt
- `sslmode=disable` cho tất cả database URLs → Chỉ chấp nhận được trong development
- Grafana dùng admin/admin → Cần đổi ngay khi triển khai production
- RabbitMQ dùng guest/guest → Chỉ dùng được trong development
- `SHORT_URL_BASE` dùng biến môi trường với default value, cho phép override

### 2.8. Healthchecks

Docker Compose định nghĩa healthchecks cho hầu hết services:

**PostgreSQL Healthcheck (x4):**
```yaml
healthcheck:
  test: ["CMD-SHELL", "pg_isready -U urluser -d urldb"]
  interval: 5s
  timeout: 5s
  retries: 10
  start_period: 10s
```

**RabbitMQ Healthcheck:**
```yaml
healthcheck:
  test: ["CMD", "rabbitmq-diagnostics", "ping"]
  interval: 10s
  timeout: 10s
  retries: 10
  start_period: 20s
```

**Redis Healthcheck:**
```yaml
healthcheck:
  test: ["CMD", "redis-cli", "ping"]
  interval: 5s
  timeout: 3s
  retries: 10
```

**Microservices (url, analytics, user, notification) Healthcheck:**
```yaml
healthcheck:
  test: ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
  interval: 10s
  timeout: 5s
  retries: 5
  start_period: 15s
```

**Gateway Healthcheck:**
```yaml
healthcheck:
  test: ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
  interval: 10s
  timeout: 5s
  retries: 5
  start_period: 20s
```

**So sánh tham số healthcheck:**

| Service | Interval | Timeout | Retries | Start Period |
|---------|----------|---------|---------|-------------|
| PostgreSQL | 5s | 5s | 10 | 10s |
| RabbitMQ | 10s | 10s | 10 | 20s |
| Redis | 5s | 3s | 10 | N/A |
| url-service | 10s | 5s | 5 | 15s |
| analytics-service | 10s | 5s | 5 | 15s |
| user-service | 10s | 5s | 5 | 15s |
| notification-service | 10s | 5s | 5 | 15s |
| gateway | 10s | 5s | 5 | 20s |

**Phân tích kỹ thuật:**
- PostgreSQL: `pg_isready` là tool chuẩn, nhẹ, fast check (5s interval)
- RabbitMQ: Cần thời gian khởi động lâu hơn (start_period: 20s), dùng `rabbitmq-diagnostics ping`
- Redis: `redis-cli ping` → response "PONG" nếu OK
- Go services: `wget -qO- http://localhost:8080/health` → HTTP GET đến endpoint /health. `wget` được cài sẵn trong alpine (BusyBox wget)
- Gateway có start_period lâu nhất (20s) vì cần chờ tất cả services khởi động xong

### 2.9. Restart Policies

Docker Compose định nghĩa restart policies cho 4 services:

```yaml
nginx:        restart: unless-stopped
prometheus:   restart: unless-stopped
grafana:      restart: unless-stopped
loki:         restart: unless-stopped
promtail:     restart: unless-stopped
```

Các service khác (databases, microservices) KHÔNG có restart policy — mặc định là `no` (không tự restart khi fail).

**Phân tích:**
- Services monitoring và Nginx có `unless-stopped`: tự động restart trừ khi bị stop thủ công
- Databases không có restart policy: nếu PostgreSQL crash, container sẽ không tự restart → cần thêm restart policy cho production
- Microservices không có restart policy: nếu Go service panic, sẽ không tự động phục hồi

**Khuyến nghị bổ sung restart policies:**
- Databases nên có `restart: unless-stopped` hoặc `restart: always`
- Microservices nên có `restart: on-failure:5` để tự restart khi crash nhưng không restart mãi mãi nếu có lỗi nghiêm trọng

---

## 3. Dockerfiles — Multi-Stage Build

### 3.1. Go Services Dockerfiles

Tất cả 4 Go services (url-service, analytics-service, user-service, notification-service) và gateway đều dùng chung một cấu trúc Dockerfile multi-stage:

```dockerfile
# Stage 1: Build
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
WORKDIR /app/services/url-service    # services/analytics-service, services/user-service, etc.
RUN go build -o main .

# Stage 2: Runtime
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/services/url-service/main .
CMD ["./main"]
```

**Phân tích kỹ thuật:**

| Stage | Base Image | Kích Thước | Công Cụ | Mục Đích |
|-------|------------|-----------|---------|----------|
| Builder | golang:1.23-alpine | ~300 MB | go, gcc, git, ca-certificates | Biên dịch Go binary |
| Runtime | alpine:latest | ~5 MB | BusyBox, libc | Chạy binary |

**Kích thước final image:** ~10-15 MB (binary Go ~10 MB + alpine runtime ~5 MB)

**Ưu điểm của multi-stage build:**
1. **Kích thước image nhỏ:** Chỉ binary Go + alpine, không cần Go toolchain ở runtime
2. **Bảo mật:** Không chứa source code, compiler, hay development tools
3. **Hiệu năng:** Alpine là base image nhẹ nhất
4. **Caching:** Docker layer caching tối ưu cho CI/CD

**Nhược điểm / điểm cần cải thiện:**
1. **Thiếu CGO_ENABLED=0:** Nên set `CGO_ENABLED=0` để đảm bảo build tĩnh (static binary)
2. **Thiếu -ldflags:** Nên set `-ldflags="-s -w"` để strip binary, giảm kích thước
3. **Thiếu ca-certificates:** Nếu service gọi HTTPS external APIs, cần cài ca-certificates
4. **COPY context không tối ưu:** `COPY . .` ở builder stage copy toàn bộ project, bao gồm frontend/, monitoring/, k8s/... Làm build chậm hơn
5. **Không dùng .dockerignore:** Không có file .dockerignore để exclude file không cần thiết

**Dockerfile tối ưu hóa (recommendation):**

```dockerfile
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY go.work go.sum go.work.sum ./
COPY shared/ ./shared/
COPY services/url-service/ ./services/url-service/
WORKDIR /app/services/url-service
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o main .

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/services/url-service/main .
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
CMD ["./main"]
```

### 3.2. Gateway Dockerfile

Gateway dùng cấu trúc Dockerfile tương tự Go services:

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
WORKDIR /app/gateway
RUN go build -o main .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/gateway/main .
CMD ["./main"]
```

**Điểm khác biệt:**
- WORKDIR là `/app/gateway` thay vì `/app/services/*`
- Binary nằm ở `/app/gateway/main`

### 3.3. Frontend Dockerfile

Frontend dùng single-stage build (không multi-stage):

```dockerfile
FROM node:22-alpine
WORKDIR /app
COPY frontend/package*.json ./
RUN npm install
COPY frontend/ ./
EXPOSE 5173
CMD ["npm", "run", "dev"]
```

**Phân tích:**
- **Base image:** node:22-alpine — Node.js 22 LTS trên Alpine
- **Cache optimization:** Copy package*.json trước, chạy npm install, sau đó copy source — tận dụng Docker layer caching
- **Chạy dev mode:** `npm run dev` — chạy Vite dev server, hot-reload
- **Không có multi-stage:** Phù hợp cho development, nhưng cho production nên có nginx stage để serve static files

**Khuyến nghị cho production:**
```dockerfile
FROM node:22-alpine AS builder
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist/ /usr/share/nginx/html/
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
```

### 3.4. Phân Tích Kỹ Thuật Build

**Go Build Process:**
- `go build -o main .` — Build Go binary với tên main
- Dùng go.work để resolve multi-module workspace
- go.work bao gồm: gateway, services/*, shared/*
- Không có CGO flags — mặc định CGO_ENABLED=1 (cần cho một số trường hợp)

**Frontend Build:**
- `npm install` — Cài dependencies từ package-lock.json
- Vite dev server — Chạy ở chế độ development

**Build Context:**
- Docker Compose context: `.` (root project directory)
- Frontend context: `.` (root project directory)
- Tất cả services dùng chung context `.` và dockerfile path riêng

**Điểm yếu trong cấu trúc build hiện tại:**
1. **Cache thấp:** COPY . . copy toàn bộ project, bất kỳ thay đổi nào cũng invalidate cache
2. **Không tận dụng Go module cache:** Mỗi lần build đều download lại dependencies
3. **Không có .dockerignore:** Docker context quá lớn
4. **Không có linter stage:** Nên thêm `go vet`, `golangci-lint` trong builder stage
5. **Không có test stage:** Nên thêm `go test` trong Dockerfile để fail build sớm nếu test lỗi

---

## 4. Kubernetes Manifests

Hệ thống sử dụng 4 file YAML cho Kubernetes deployment:
1. `k8s/namespace.yaml` — Namespace
2. `k8s/config.yaml` — ConfigMap + Secret
3. `k8s/infra.yaml` — Infrastructure (Redis, RabbitMQ, Databases)
4. `k8s/apps.yaml` — Application services

### 4.1. Namespace — `namespace.yaml`

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: url-shortener
```

**Phân tích:**
- Namespace đơn giản, chỉ có tên `url-shortener`
- Cô lập tài nguyên cho dự án trong cluster
- Tất cả các manifest khác đều reference namespace này
- **Không có labels:** Nên thêm labels như `app.kubernetes.io/name: url-shortener`, `app.kubernetes.io/part-of: url-shortener`

### 4.2. ConfigMap và Secrets — `config.yaml`

**ConfigMap `app-config`:**

| Key | Value | Sử Dụng Bởi |
|-----|-------|-------------|
| PORT | 8080 | Tất cả Go services |
| SHORT_URL_BASE | http://localhost:30080/r | url-service |
| IP_HASH_SALT | change-this-in-production-random-salt | url-service, analytics-service |
| URL_SERVICE_URL | http://url-service:8080 | gateway |
| ANALYTICS_SERVICE_URL | http://analytics-service:8080 | gateway |
| USER_SERVICE_URL | http://user-service:8080 | gateway |
| NOTIFICATION_SERVICE_URL | http://notification-service:8080 | gateway |
| REDIS_URL | redis://redis:6379/0 | url-service, gateway |
| RABBITMQ_URL | amqp://guest:guest@rabbitmq:5672/ | url-service, analytics-service, notification-service |
| URL_DATABASE_URL | postgres://urluser:urlpass@url-db:5432/urldb?sslmode=disable | url-service |
| ANALYTICS_DATABASE_URL | postgres://analyticsuser:analyticspass@analytics-db:5432/analyticsdb?sslmode=disable | analytics-service |
| USER_DATABASE_URL | postgres://useruser:userpass@user-db:5432/userdb?sslmode=disable | user-service |
| NOTIFICATION_DATABASE_URL | postgres://notificationuser:notificationpass@notification-db:5432/notificationdb?sslmode=disable | notification-service |

**Secret `app-secrets`:**

| Key | Value | Sử Dụng Bởi |
|-----|-------|-------------|
| JWT_SECRET | change-this-in-production-minimum-32-chars | Tất cả services |

**Phân tích ConfigMap:**
- ConfigMap chứa tất cả cấu hình non-sensitive
- Dùng kiểu `stringData` cho Secret (không base64-encode trong YAML)
- `SHORT_URL_BASE` trong K8s trỏ đến NodePort 30080 (khác với Docker Compose dùng port 80)
- Database hostnames dùng Kubernetes convention: `url-db` thay vì `url_db` (Docker Compose)

**Vấn đề bảo mật:**
- Database URLs trong ConfigMap chứa usernames và passwords! Đây là sensitive data, cần chuyển sang Secrets
- JWT_SECRET trong Secret chưa đủ mạnh cho production
- RabbitMQ guest/guest credentials nên được đặt trong Secrets
- IP_HASH_SALT cũng là sensitive data, nên trong Secrets

**Cấu trúc cải thiện khuyến nghị:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: app-secrets
  namespace: url-shortener
type: Opaque
stringData:
  JWT_SECRET: "change-this-in-production-minimum-32-chars"
  IP_HASH_SALT: "change-this-in-production-random-salt"
  URL_DATABASE_URL: "postgres://urluser:urlpass@url-db:5432/urldb?sslmode=disable"
  ANALYTICS_DATABASE_URL: "postgres://analyticsuser:analyticspass@analytics-db:5432/analyticsdb?sslmode=disable"
  USER_DATABASE_URL: "postgres://useruser:userpass@user-db:5432/userdb?sslmode=disable"
  NOTIFICATION_DATABASE_URL: "postgres://notificationuser:notificationpass@notification-db:5432/notificationdb?sslmode=disable"
  RABBITMQ_URL: "amqp://guest:guest@rabbitmq:5672/"
```

### 4.3. Infrastructure Deployments — `infra.yaml`

File `infra.yaml` chứa 6 Deployments và 6 Services cho infrastructure layer.

**Redis:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
  namespace: url-shortener
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
        - name: redis
          image: redis:7-alpine
          args: ["redis-server", "--save", "", "--appendonly", "no"]
          ports:
            - containerPort: 6379
          readinessProbe:
            exec:
              command: ["redis-cli", "ping"]
            initialDelaySeconds: 5
            periodSeconds: 5
```

**RabbitMQ:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: rabbitmq
  namespace: url-shortener
spec:
  replicas: 1
  selector:
    matchLabels:
      app: rabbitmq
  template:
    metadata:
      labels:
        app: rabbitmq
    spec:
      containers:
        - name: rabbitmq
          image: rabbitmq:3.13-management-alpine
          env:
            - name: RABBITMQ_DEFAULT_USER
              value: guest
            - name: RABBITMQ_DEFAULT_PASS
              value: guest
          ports:
            - containerPort: 5672
            - containerPort: 15672
          readinessProbe:
            exec:
              command: ["rabbitmq-diagnostics", "ping"]
            initialDelaySeconds: 20
            periodSeconds: 10
```

**PostgreSQL Databases (url-db, analytics-db, user-db, notification-db):**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: url-db
  namespace: url-shortener
spec:
  replicas: 1
  selector:
    matchLabels:
      app: url-db
  template:
    metadata:
      labels:
        app: url-db
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          env:
            - name: POSTGRES_DB
              value: urldb
            - name: POSTGRES_USER
              value: urluser
            - name: POSTGRES_PASSWORD
              value: urlpass
          ports:
            - containerPort: 5432
          volumeMounts:
            - name: data
              mountPath: /var/lib/postgresql/data
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "urluser", "-d", "urldb"]
            initialDelaySeconds: 10
            periodSeconds: 5
      volumes:
        - name: data
          emptyDir: {}
```

**Phân tích chi tiết các Service definitions:**

| Service | Type | Port | TargetPort | Selector |
|---------|------|------|------------|----------|
| redis | ClusterIP (default) | 6379 | 6379 | app: redis |
| rabbitmq | ClusterIP (default) | 5672 (amqp), 15672 (management) | 5672, 15672 | app: rabbitmq |
| url-db | ClusterIP (default) | 5432 | 5432 | app: url-db |
| analytics-db | ClusterIP (default) | 5432 | 5432 | app: analytics-db |
| user-db | ClusterIP (default) | 5432 | 5432 | app: user-db |
| notification-db | ClusterIP (default) | 5432 | 5432 | app: notification-db |

**Vấn đề quan trọng — emptyDir cho databases:**
```yaml
volumes:
  - name: data
    emptyDir: {}
```

**Đây là một vấn đề CRITICAL cho production:** `emptyDir` là ephemeral storage — khi Pod restart, tất cả dữ liệu database sẽ bị mất! Trong Kubernetes production, databases cần:
1. **PersistentVolumeClaim** (PVC) với PersistentVolume (PV)
2. **StatefulSet** thay vì Deployment (để có stable identity)
3. HostPath volume ít nhất cho single-node cluster

**Phân tích emptyDir:**
- Redis: Có thể chấp nhận được vì đang chạy ephemeral mode (`--save "" --appendonly no`)
- PostgreSQL: KHÔNG THỂ chấp nhận — mất dữ liệu khi Pod restart
- Có thể đây là setup cho Kubernetes development (Minikube, kind), không phải production

### 4.4. Application Deployments — `apps.yaml`

File `apps.yaml` chứa 5 Deployments, 5 Services cho application layer.

**Deployment summary:**

| Deployment | Replicas | Image | ImagePullPolicy | Service Type |
|-----------|---------|-------|----------------|-------------|
| url-service | 3 | url-shortener-microservices-url-service:latest | IfNotPresent | ClusterIP |
| analytics-service | 1 | url-shortener-microservices-analytics-service:latest | IfNotPresent | ClusterIP |
| user-service | 1 | url-shortener-microservices-user-service:latest | IfNotPresent | ClusterIP |
| notification-service | 1 | url-shortener-microservices-notification-service:latest | IfNotPresent | ClusterIP |
| gateway | 2 | url-shortener-microservices-gateway:latest | IfNotPresent | NodePort (30080) |

**Phân tích replicas:**
- `url-service` có 3 replicas — service quan trọng nhất (xử lý URL shortening)
- `gateway` có 2 replicas — API gateway cần HA
- `analytics-service`, `user-service`, `notification-service` mỗi service 1 replica — chưa có HA

**Phân tích ImagePullPolicy:**
- `IfNotPresent`: Chỉ pull image nếu chưa có local → Phù hợp cho development
- Trong production CI/CD, nên dùng `Always` để đảm bảo luôn lấy đúng version mới

**Environment variables từ ConfigMap:**

url-service sử dụng các env từ ConfigMap:
```yaml
env:
  - name: DATABASE_URL
    valueFrom:
      configMapKeyRef:
        name: app-config
        key: URL_DATABASE_URL
  - name: REDIS_URL
    valueFrom:
      configMapKeyRef:
        name: app-config
        key: REDIS_URL
  - name: RABBITMQ_URL
    valueFrom:
      configMapKeyRef:
        name: app-config
        key: RABBITMQ_URL
  - name: JWT_SECRET
    valueFrom:
      secretKeyRef:
        name: app-secrets
        key: JWT_SECRET
```

**So sánh environment variables giữa Docker Compose và Kubernetes:**

| Variable | Docker Compose | Kubernetes |
|----------|---------------|------------|
| DATABASE_URL | Hardcoded trong compose | ConfigMap key per service |
| REDIS_URL | Hardcoded | ConfigMap chung |
| RABBITMQ_URL | Hardcoded | ConfigMap chung |
| JWT_SECRET | Hardcoded trong compose | Secret (app-secrets) |
| SHORT_URL_BASE | env var with default | ConfigMap chung |
| IP_HASH_SALT | Hardcoded | ConfigMap chung |
| PORT | "8080" | ConfigMap chung |
| Service URLs | Hardcoded | ConfigMap chung |

**Gateway Service — NodePort:**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: gateway
  namespace: url-shortener
spec:
  type: NodePort
  selector:
    app: gateway
  ports:
    - port: 8080
      targetPort: 8080
      nodePort: 30080
```

Là service duy nhất dùng NodePort, expose ra ngoài cluster qua port 30080. Các service khác dùng ClusterIP (internal only).

### 4.5. Services và NodePort

**Service types trong hệ thống:**

| Type | Services | Mô Tả |
|------|----------|-------|
| ClusterIP | redis, rabbitmq, url-db, analytics-db, user-db, notification-db, url-service, analytics-service, user-service, notification-service | Chỉ truy cập nội bộ cluster |
| NodePort | gateway (30080) | Truy cập từ ngoài cluster |

**Phân tích kiến trúc mạng Kubernetes:**
- Gateway là cổng vào duy nhất (NodePort 30080)
- Gateway gọi các services nội bộ qua ClusterIP
- Redis và RabbitMQ cũng là ClusterIP, chỉ được services gọi nội bộ
- Không có Ingress controller → cần thêm cho production

**Các port được expose trong Kubernetes:**

| Pod | Container Port | Service Port | Type |
|-----|---------------|-------------|------|
| redis | 6379 | 6379 | ClusterIP |
| rabbitmq | 5672, 15672 | 5672, 15672 | ClusterIP |
| url-db | 5432 | 5432 | ClusterIP |
| analytics-db | 5432 | 5432 | ClusterIP |
| user-db | 5432 | 5432 | ClusterIP |
| notification-db | 5432 | 5432 | ClusterIP |
| url-service | 8080 | 8080 | ClusterIP |
| analytics-service | 8080 | 8080 | ClusterIP |
| user-service | 8080 | 8080 | ClusterIP |
| notification-service | 8080 | 8080 | ClusterIP |
| gateway | 8080 | 8080 (nodePort: 30080) | NodePort |

### 4.6. Phân Tích Deployment vs StatefulSet

**Hiện tại: Tất cả đều dùng Deployment**

| Resource | Type | Replicas | Storage |
|----------|------|----------|---------|
| Redis | Deployment | 1 | emptyDir (OK — ephemeral) |
| RabbitMQ | Deployment | 1 | none |
| PostgreSQL (x4) | Deployment | 1 | emptyDir (PROBLEM!) |
| url-service | Deployment | 3 | stateless (OK) |
| analytics-service | Deployment | 1 | stateless (OK) |
| user-service | Deployment | 1 | stateless (OK) |
| notification-service | Deployment | 1 | stateless (OK) |
| gateway | Deployment | 2 | stateless (OK) |

**Khi nào cần StatefulSet:**

StatefulSet cần thiết khi:
1. **Dữ liệu cần persistent:** PostgreSQL cần StatefulSet + PVC
2. **Stable network identity:** Cần hostname ổn định (e.g., `url-db-0.url-db`)
3. **Ordered startup/shutdown:** Cần startup theo thứ tự
4. **Clone/PVC template:** Mỗi replica có PVC riêng

**Recommendation cho databases:**

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: url-db
  namespace: url-shortener
spec:
  serviceName: url-db
  replicas: 1
  selector:
    matchLabels:
      app: url-db
  template:
    metadata:
      labels:
        app: url-db
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          env:
            - name: POSTGRES_DB
              value: urldb
            - name: POSTGRES_USER
              value: urluser
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: db-secrets
                  key: url-db-password
          ports:
            - containerPort: 5432
          volumeMounts:
            - name: data
              mountPath: /var/lib/postgresql/data
          resources:
            requests:
              memory: "256Mi"
              cpu: "250m"
            limits:
              memory: "1Gi"
              cpu: "1"
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
        storageClassName: standard
```

---

## 5. Health Checks — Liveness và Readiness

Hệ thống triển khai health checks ở cả Docker Compose và Kubernetes, nhưng với cách tiếp cận khác nhau.

### 5.1. ReadinessProbe trong Docker Compose

Docker Compose chỉ hỗ trợ `healthcheck` (tương đương readiness probe), không có liveness probe riêng.

```yaml
healthcheck:
  test: ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
  interval: 10s
  timeout: 5s
  retries: 5
  start_period: 15s
```

**Cơ chế hoạt động:**
1. Docker engine thực thi test command mỗi `interval` giây
2. Nếu command exit code != 0, đánh dấu là unhealthy
3. Sau `retries` lần fail, container được đánh dấu "unhealthy"
4. `depends_on` với `condition: service_healthy` chờ đến khi container healthy
5. Docker Compose KHÔNG tự động restart container khi unhealthy (trừ khi có restart policy)

### 5.2. ReadinessProbe trong Kubernetes

Kubernetes sử dụng `readinessProbe` (không phải `livenessProbe`):

```yaml
readinessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
```

**Các loại probe trong hệ thống:**

**HTTP GET probe (Go services, gateway):**
```yaml
readinessProbe:
  httpGet:
    path: /health    # HTTP GET /health endpoint
    port: 8080       # Port để gọi
  initialDelaySeconds: 10   # Chờ 10s trước khi probe lần đầu
  periodSeconds: 5          # Probe mỗi 5 giây
```

**Exec probe (Redis):**
```yaml
readinessProbe:
  exec:
    command: ["redis-cli", "ping"]
  initialDelaySeconds: 5
  periodSeconds: 5
```

**Exec probe (RabbitMQ):**
```yaml
readinessProbe:
  exec:
    command: ["rabbitmq-diagnostics", "ping"]
  initialDelaySeconds: 20
  periodSeconds: 10
```

**Exec probe (PostgreSQL):**
```yaml
readinessProbe:
  exec:
    command: ["pg_isready", "-U", "urluser", "-d", "urldb"]
  initialDelaySeconds: 10
  periodSeconds: 5
```

### 5.3. So Sánh và Đối Chiếu

| Aspect | Docker Compose Healthcheck | Kubernetes ReadinessProbe |
|--------|--------------------------|---------------------------|
| **Mục đích** | Thông báo trạng thái container | Quyết định có gửi traffic vào Pod không |
| **Loại test** | CMD, CMD-SHELL, HTTP (wget) | exec, httpGet, tcpSocket, grpc |
| **Interval** | Per service (5-10s) | Per service (5-10s) |
| **Timeout** | 5-10s | Mặc định 1s (cần config nếu dài hơn) |
| **Retries** | 5-10 lần | 3 lần (mặc định) |
| **Start period** | 10-20s | initialDelaySeconds (5-20s) |
| **Hành vi khi fail** | Container marked unhealthy | Pod removed from Service endpoints |
| **Restart** | Không tự động | Chỉ nếu có livenessProbe |

**Điểm yếu hiện tại:**
1. **Không có livenessProbe:** Chỉ có readinessProbe trong Kubernetes. Nếu service bị deadlock, Pod sẽ không được restart
2. **Không có startupProbe:** Cho services khởi động chậm, nên dùng startupProbe thay vì initialDelaySeconds lớn
3. **Thiếu timeout configuration:** Kubernetes probe timeout mặc định 1s — quá thấp cho PostgreSQL
4. **wget không phải lúc nào cũng có:** Alpine dùng BusyBox wget, có thể không hỗ trợ HTTPS

**Khuyến nghị bổ sung livenessProbe:**

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3
```

Và startupProbe cho services khởi động chậm:

```yaml
startupProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 2
  failureThreshold: 30   # 60s timeout
```

---

## 6. Infrastructure Components

### 6.1. PostgreSQL (4 Instances)

Hệ thống triển khai 4 PostgreSQL instance riêng biệt, mỗi service có database riêng (database-per-service pattern).

**So sánh 4 databases:**

| Instance | DB Name | User | Password | Host Port (docker) | Service |
|----------|---------|------|----------|-------------------|---------|
| url_db | urldb | urluser | urlpass | 5432 | url-service |
| analytics_db | analyticsdb | analyticsuser | analyticspass | 5433 | analytics-service |
| user_db | userdb | useruser | userpass | 5434 | user-service |
| notification_db | notificationdb | notificationuser | notificationpass | 5435 | notification-service |

**Phân tích:**
- **Image:** postgres:16-alpine — PostgreSQL 16 trên Alpine Linux, ~90 MB
- **Version 16:** Phiên bản mới nhất, hỗ trợ logical replication, performance improvements
- **Alpine:** Smaller image, nhưng có thể gây vấn đề với một số extensions
- **Health check:** Dùng `pg_isready` — công cụ chuẩn của PostgreSQL
- **Storage:** Named volume trong Docker (persistent), emptyDir trong Kubernetes (ephemeral — not production ready)

**Kết nối strings:**
```
postgres://urluser:urlpass@url_db:5432/urldb?sslmode=disable
```
- `sslmode=disable` — Không dùng SSL, chỉ cho development
- `@url_db:5432` — Docker DNS resolution

**Khuyến nghị cho production:**
1. **SSL/TLS encryption:** Bỏ `sslmode=disable`, dùng `sslmode=require` hoặc `verify-full`
2. **Connection pooling:** Thêm PgBouncer hoặc dùng built-in pool trong Go
3. **Backup:** Thiết lập WAL archiving và pg_dump schedule
4. **Replication:** Cấu hình streaming replication cho HA
5. **Monitoring:** Triển khai pg_exporter cho Prometheus
6. **Credentials:** Dùng Kubernetes Secrets hoặc Vault

### 6.2. Redis Cache

**Configuration:**
- **Image:** redis:7-alpine
- **Mode:** Ephemeral (`--save "" --appendonly no`)
- **Port:** 6379
- **Purpose:** Cache cho URL shortening lookups
- **Health check:** `redis-cli ping`

**Tại sao ephemeral?**
- Redis chỉ dùng làm cache layer, không lưu dữ liệu quan trọng
- URL mappings được persist trong PostgreSQL
- Nếu Redis mất dữ liệu, chỉ cần warm-up lại cache từ database
- Performance tốt hơn (không cần fsync)

**Redis connection string:**
```
redis://redis:6379/0
```
DB index 0 được sử dụng.

**Use cases trong project:**
1. Cache URL lookups: Giảm tải cho PostgreSQL
2. Rate limiting counters (gateway): `REDIRECT_RATE_LIMIT = 100000`
3. Session cache (future)

**Khuyến nghị cho production:**
1. **Redis Sentinel** hoặc **Redis Cluster** cho HA
2. **Password authentication:** Dùng `requirepass`
3. **Memory limit:** Set `maxmemory` và `maxmemory-policy`
4. **Persistence:** Có thể enable AOF cho cache không critical
5. **Exporter:** Triển khai redis_exporter cho Prometheus

### 6.3. RabbitMQ Message Broker

**Configuration:**
- **Image:** rabbitmq:3.13-management-alpine
- **Ports:** 5672 (AMQP), 15672 (Management UI)
- **Credentials:** guest/guest
- **Health check:** `rabbitmq-diagnostics ping`

**Message flow:**
```
url-service ──(publish)──→ RabbitMQ ──(consume)──→ analytics-service
 url-service ──(publish)──→ RabbitMQ ──(consume)──→ notification-service
```

**Các queues (từ shared/events package):**
```
url.created
url.accessed
url.deleted
user.registered
notification.send
```

**Phân tích kỹ thuật:**
- Phiên bản 3.13: Hỗ trợ quorum queues, stream queues
- Management plugin: UI quản lý tại port 15672
- Alpine base: Image nhẹ hơn so với RabbitMQ thường (~100 MB vs ~200 MB)
- guest/guest credentials: Chỉ dùng cho development

**Khuyến nghị cho production:**
1. **Xác thực mạnh:** Dùng credential khác guest/guest, tích hợp với LDAP/OAuth
2. **SSL/TLS:** AMQPS cho encrypted connection
3. **High Availability:** Quorum queues + mirrored queues
4. **Clustering:** Triển khai RabbitMQ cluster (3 nodes)
5. **Monitoring:** rabbitmq_exporter + Prometheus plugin
6. **Resource limits:** Set vm_memory_high_watermark

### 6.4. Nginx Reverse Proxy

**Configuration:**
```nginx
events {
    worker_connections 1024;
}

http {
    upstream frontend_upstream {
        server frontend:5173;
    }

    upstream gateway_upstream {
        server gateway:8080;
    }

    server {
        listen 80;

        location /api/ { proxy_pass http://gateway_upstream; }
        location /r/   { proxy_pass http://gateway_upstream; }
        location /health { proxy_pass http://gateway_upstream; }
        location /     { proxy_pass http://frontend_upstream; }
    }
}
```

**Routing Rules:**

| Path | Upstream | Timeout | Description |
|------|----------|---------|-------------|
| `/api/` | gateway:8080 | 30s | REST API endpoints |
| `/r/` | gateway:8080 | none | URL redirects |
| `/health` | gateway:8080 | none | Health check |
| `/` | frontend:5173 | none | Static assets / SPA |

**Headers forwarded:**
```
Host: $host
X-Real-IP: $remote_addr
X-Forwarded-For: $proxy_add_x_forwarded_for
X-Forwarded-Proto: $scheme
```

WebSocket support for frontend:
```nginx
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
```

**Phân tích:**
- `proxy_http_version 1.1` — Cần cho keepalive và WebSocket
- `client_max_body_size 10m` — Giới hạn body size
- `worker_connections 1024` — 1024 connections per worker
- Timeouts: connect 30s, send 30s, read 30s
- Không có SSL/TLS termination (listen 80, không 443)

**Khuyến nghị:**
1. **SSL/TLS:** Thêm certificate với Let's Encrypt (certbot)
2. **Compression:** Thêm `gzip on;`
3. **Security headers:** Thêm `X-Frame-Options`, `X-Content-Type-Options`, `HSTS`
4. **Rate limiting:** Nginx built-in limit_req
5. **Health check upstream:** Dùng `nginx_upstream_check_module` hoặc `health_check`

### 6.5. Adminer

**Configuration:**
- **Image:** adminer:latest
- **Port:** 8090 (host) → 8080 (container)
- **Network:** url-shortener
- **Purpose:** GUI database management

**Phân tích:**
- Adminer cho phép truy cập tất cả 4 PostgreSQL databases
- Chỉ nên dùng trong development
- Không có authentication mặc định — cần bảo vệ

**Security concern:**
- Không có SSL
- Không có authentication
- Truy cập từ host port 8090

**Khuyến nghị:**
- Remove trong production deployment
- Hoặc thêm authentication (dùng Nginx basic auth trước proxy)
- Hoặc expose qua VPN/internal network

---

## 7. CI/CD Pipeline — GitHub Actions

### 7.1. Continuous Integration (CI) — `ci.yml`

```yaml
name: Continuous Integration

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]
  workflow_dispatch:
```

**Trigger:** Push/PR vào main branch, hoặc manual trigger.

**3 Jobs song song:**

**Job 1: `go-lint-and-test`**

| Step | Action/Tool | Mục Đích |
|------|------------|----------|
| Checkout | actions/checkout@v4 | Lấy source code |
| Set up Go | actions/setup-go@v5 | Go 1.23.x, cache enabled |
| Format check | gofmt -l . | Kiểm tra Go formatting |
| Vet + Test | go vet + go test -v -race -cover | Phân tích tĩnh + unit test |

**Phân tích Go test step:**
```yaml
for dir in $(go work edit -json | jq -r '.Use[].DiskPath'); do
    go vet "./$dir/..."
    go test -v -race -cover "./$dir/..."
done
```
- Dùng `go work edit -json` để lấy danh sách module từ go.work
- Chạy vet và test trên từng module
- `-race`: Race detection (quan trọng cho concurrent Go code)
- `-cover`: Code coverage
- `-v`: Verbose output
- **Thiếu:** `-count=1` để disable test caching

**Job 2: `frontend-build`**

| Step | Action/Tool | Mục Đích |
|------|------------|----------|
| Checkout | actions/checkout@v4 | Lấy source code |
| Set up Node | actions/setup-node@v4 | Node.js 22, npm cache |
| Install | npm ci | Clean install dependencies |
| Build | npm run build | Vite production build |

**Phân tích frontend build:**
- Node 22: LTS version
- `npm ci` thay vì `npm install`: Nhanh hơn, deterministic (dùng package-lock.json)
- Cache trên `frontend/package-lock.json`
- **Thiếu:** TypeScript type checking (`tsc --noEmit`), lint (`npm run lint`)

**Job 3: `docker-compose-check`**

| Step | Action/Tool | Mục Đích |
|------|------------|----------|
| Checkout | actions/checkout@v4 | Lấy source code |
| Build Docker | docker compose build | Build tất cả images |

**Phân tích Docker build test:**
- Build tất cả services để verify Dockerfiles không bị lỗi
- **Không chạy** `docker compose up` (chỉ build, không start)
- Sử dụng Docker Compose V2 (built-in)
- **Thiếu:** Docker layer caching (có thể dùng `docker/build-push-action`)

### 7.2. Continuous Delivery (CD) — `cd.yml`

```yaml
name: Continuous Delivery

on:
  push:
    branches: [ main ]
    tags: [ 'v*' ]
  workflow_dispatch:

permissions:
  contents: read
  packages: write
```

**Trigger:** Push main branch, tag v*, manual trigger.
**Permissions:** Read code, write packages (GHCR).

### 7.3. Build Matrix

```yaml
strategy:
  fail-fast: false
  matrix:
    include:
      - service: url-service
        context: .
        dockerfile: services/url-service/Dockerfile
      - service: analytics-service
        context: .
        dockerfile: services/analytics-service/Dockerfile
      - service: user-service
        context: .
        dockerfile: services/user-service/Dockerfile
      - service: notification-service
        context: .
        dockerfile: services/notification-service/Dockerfile
      - service: gateway
        context: .
        dockerfile: gateway/Dockerfile
      - service: frontend
        context: .
        dockerfile: frontend/Dockerfile
```

**Phân tích matrix:**
- `fail-fast: false`: Các job không phụ thuộc nhau, một job fail không ảnh hưởng job khác
- 6 services trong matrix → 6 parallel jobs
- Tất cả dùng context `.` (root project)
- Dockerfile path riêng cho mỗi service

**Quy trình build:**
1. **Checkout** — actions/checkout@v4
2. **Setup Docker Buildx** — docker/setup-buildx-action@v3
3. **Login GHCR** — docker/login-action@v3
4. **Extract metadata** — docker/metadata-action@v5
5. **Build & Push** — docker/build-push-action@v6

### 7.4. Docker Layer Caching

```yaml
- name: Build and push Docker image
  uses: docker/build-push-action@v6
  with:
    cache-from: type=gha
    cache-to: type=gha,mode=max
```

**Caching strategy:**
- `type=gha`: GitHub Actions cache backend
- `mode=max`: Cache tất cả layers (không chỉ exported layers)
- **Lợi ích:** Build time giảm từ 3-5 phút xuống 30-60 giây
- **Giới hạn:** Cache tối đa 10GB cho mỗi repository

### 7.5. Multi-Arch Build

Hiện tại build chỉ chạy trên `ubuntu-latest` (linux/amd64).

**Khuyến nghị thêm multi-arch:**
```yaml
- name: Set up QEMU
  uses: docker/setup-qemu-action@v3

- name: Build and push
  uses: docker/build-push-action@v6
  with:
    platforms: linux/amd64,linux/arm64
```

Hỗ trợ ARM64 cần thiết cho:
- Apple Silicon Macs
- AWS Graviton instances
- Raspberry Pi clusters
- Azure ARM-based VMs

### 7.6. Deploy to AKS

```yaml
deploy-to-aks:
  name: Deploy to AKS
  runs-on: ubuntu-latest
  needs: [build-and-push]
  if: github.ref == 'refs/heads/main'
```

**Phụ thuộc:** Chỉ chạy sau `build-and-push` success.
**Điều kiện:** Chỉ deploy từ main branch (không từ tag).

**Các bước deploy:**

**Step 1: Azure Login**
```yaml
- uses: azure/login@v2
  with:
    creds: ${{ secrets.AZURE_CREDENTIALS }}
```
- Dùng Service Principal credentials (JSON) từ GitHub Secrets
- Azure login@v2 — phiên bản mới nhất

**Step 2: Set AKS Context**
```yaml
- uses: azure/aks-set-context@v4
  with:
    cluster-name: ${{ secrets.AZURE_AKS_NAME }}
    resource-group: ${{ secrets.AZURE_AKS_RESOURCE_GROUP }}
```
- Lấy kubeconfig cho AKS cluster
- Secrets: AZURE_AKS_NAME, AZURE_AKS_RESOURCE_GROUP

**Step 3: Setup Kubectl**
```yaml
- uses: azure/setup-kubectl@v4
```

**Step 4: Set up GHCR Secret**
```yaml
kubectl apply -f k8s/namespace.yaml
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=${{ github.actor }} \
  --docker-password=${{ secrets.GITHUB_TOKEN }} \
  --namespace=url-shortener \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl patch serviceaccount default -n url-shortener \
  -p '{"imagePullSecrets": [{"name": "ghcr-secret"}]}'
```

- Tạo docker-registry secret để pull image từ GHCR
- Dùng `--dry-run=client -o yaml | kubectl apply -f -` để idempotent
- Patch service account default để tự động dùng secret

**Step 5: Replace Image Tags**
```yaml
sed -i "s|url-shortener-microservices-url-service:latest|ghcr.io/${REPO_LC}/url-service:${SHORT_SHA}|g" k8s/apps.yaml
sed -i "s|url-shortener-microservices-analytics-service:latest|ghcr.io/${REPO_LC}/analytics-service:${SHORT_SHA}|g" k8s/apps.yaml
sed -i "s|url-shortener-microservices-user-service:latest|ghcr.io/${REPO_LC}/user-service:${SHORT_SHA}|g" k8s/apps.yaml
sed -i "s|url-shortener-microservices-notification-service:latest|ghcr.io/${REPO_LC}/notification-service:${SHORT_SHA}|g" k8s/apps.yaml
sed -i "s|url-shortener-microservices-gateway:latest|ghcr.io/${REPO_LC}/gateway:${SHORT_SHA}|g" k8s/apps.yaml
```

- `SHORT_SHA`: 7 ký tự đầu của commit SHA
- `REPO_LC`: lowercase của repository name
- Thay đổi image tags từ local `:latest` sang GHCR `:sha`
- **Vấn đề:** Dùng sed modify file YAML — có thể gây lỗi nếu format phức tạp hơn

**Step 6: Deploy Manifests**
```yaml
kubectl apply -f k8s/config.yaml
kubectl apply -f k8s/infra.yaml
kubectl apply -f k8s/apps.yaml
```

Thứ tự apply quan trọng:
1. ConfigMap + Secrets trước
2. Infrastructure (databases, redis, rabbitmq)
3. Applications (services, gateway)

**Step 7: Verify Rollout**
```yaml
kubectl rollout status deployment/url-service -n url-shortener --timeout=2m
kubectl rollout status deployment/analytics-service -n url-shortener --timeout=2m
kubectl rollout status deployment/user-service -n url-shortener --timeout=2m
kubectl rollout status deployment/notification-service -n url-shortener --timeout=2m
kubectl rollout status deployment/gateway -n url-shortener --timeout=2m
```

- Mỗi deployment có timeout 2 phút
- Chỉ verify application deployments, không verify infrastructure deployments
- rollout status trả về non-zero exit code nếu rollout fail

---

## 8. Monitoring Stack

### 8.1. Prometheus — Cấu Hình Scrape

**File:** `monitoring/prometheus.yml`

```yaml
global:
  scrape_interval: 5s
  evaluation_interval: 5s

scrape_configs:
  - job_name: "gateway"
    static_configs:
      - targets: ["gateway:8080"]
    metrics_path: /metrics

  - job_name: "url-service"
    static_configs:
      - targets: ["url-service:8080"]
    metrics_path: /metrics

  - job_name: "analytics-service"
    static_configs:
      - targets: ["analytics-service:8080"]
    metrics_path: /metrics

  - job_name: "user-service"
    static_configs:
      - targets: ["user-service:8080"]
    metrics_path: /metrics

  - job_name: "notification-service"
    static_configs:
      - targets: ["notification-service:8080"]
    metrics_path: /metrics
```

**Phân tích scrape config:**

| Job Name | Target | Metrics Path | Interval |
|----------|--------|-------------|----------|
| gateway | gateway:8080 | /metrics | 5s |
| url-service | url-service:8080 | /metrics | 5s |
| analytics-service | analytics-service:8080 | /metrics | 5s |
| user-service | user-service:8080 | /metrics | 5s |
| notification-service | notification-service:8080 | /metrics | 5s |

**Key metrics endpoints:**
- Mỗi Go service expose Prometheus metrics tại `/metrics`
- Gateway metrics: `gateway_requests_total`, `gateway_request_duration_seconds`, `gateway_circuit_breaker_state`, `gateway_circuit_breaker_trips_total`, `gateway_circuit_breaker_rejected_total`
- Go runtime metrics: `go_goroutines`, `go_memstats_alloc_bytes`, `go_gc_duration_seconds`
- Process metrics: `process_cpu_seconds_total`, `process_open_fds`, `process_resident_memory_bytes`

**Global config:**
- `scrape_interval: 5s` — Rất aggressive! Mặc định Prometheus là 15s. 5s tạo nhiều traffic nhưng real-time hơn
- `evaluation_interval: 5s` — Đánh giá alert rules mỗi 5s

**Thiếu:**
- Không có `metric_relabel_configs` — Có thể drop unnecessary metrics
- Không có `relabel_configs` — Có thể thêm metadata labels
- Không có alerting rules
- Không có scrape cho infrastructure (Node exporter, PostgreSQL exporter, Redis exporter)

**Prometheus Container configuration:**
```yaml
command:
  - "--config.file=/etc/prometheus/prometheus.yml"
  - "--storage.tsdb.path=/prometheus"
  - "--web.console.libraries=/etc/prometheus/console_libraries"
  - "--web.console.templates=/etc/prometheus/consoles"
  - "--storage.tsdb.retention.time=1h"
  - "--web.enable-lifecycle"
```

| Flag | Value | Mục Đích |
|------|-------|----------|
| storage.tsdb.retention.time | 1h | Retention rất ngắn, chỉ cho dev |
| web.enable-lifecycle | true | Cho phép reload config qua API |

**Khuyến nghị retention:**
- Development: 1h (OK)
- Staging: 24h
- Production: 30d (cần tính toán disk space)

### 8.2. Grafana — Dashboards

Grafana được cấu hình với:
- **Image:** grafana/grafana:11.1.0
- **Port:** 3000
- **Credentials:** admin/admin
- **Auto-provisioning:** datasources + dashboards

**Dashboard provisioning:**

File `monitoring/grafana/provisioning/dashboards/dashboards.yml`:
```yaml
apiVersion: 1
providers:
  - name: "default"
    orgId: 1
    folder: ""
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    options:
      path: /etc/grafana/provisioning/dashboards
```

**Dashboard 1: Services Overview (`services_overview.json`)**

UID: `services-overview`

**Panels:**

| Panel ID | Title | Type | Datasource | Query |
|----------|-------|------|------------|-------|
| 1 | Current Goroutines | Stat | Prometheus | `go_goroutines` |
| 2 | Current Allocated Heap Memory | Stat | Prometheus | `go_memstats_alloc_bytes` |
| 3 | Goroutines Over Time | Time series | Prometheus | `go_goroutines` |
| 4 | Allocated Heap Memory Over Time | Time series | Prometheus | `go_memstats_alloc_bytes` |
| 5 | CPU Core Usage Over Time | Time series | Prometheus | `rate(process_cpu_seconds_total[30s])` |
| 6 | Open File Descriptors | Time series | Prometheus | `process_open_fds` |
| 7 | Live Log Stream | Logs | Loki | `{service=~"$service"}` |

**Thresholds:**
- Goroutines: green (<150), yellow (150-300), red (>300)
- Heap Memory: green (<50MB), yellow (50-100MB), red (>100MB)

**Dashboard 2: Circuit Breaker Monitor (`circuit_breaker.json`)**

UID: `circuit-breaker-monitor`

**Panels:**

| Panel ID | Title | Type | Query |
|----------|-------|------|-------|
| 1 | Circuit Breaker State | Stat | `gateway_circuit_breaker_state` |
| 2 | CB Trips | Stat | `increase(gateway_circuit_breaker_trips_total[$__range])` |
| 3 | Rejected by CB | Stat | `increase(gateway_circuit_breaker_rejected_total[$__range])` |
| 4 | CB State Over Time | Time series | `gateway_circuit_breaker_state` |
| 5 | Requests/sec | Time series | `sum(rate(gateway_requests_total[10s])) by (service)` |
| 6 | Requests/sec by Status Class | Time series (bar) | `sum(rate(gateway_requests_total[10s])) by (status_class)` |
| 7 | Error Rate % | Time series | Error rate formula |
| 8 | Response Latency (p50/p95/p99) | Time series | Histogram quantiles |

**Circuit breaker state mapping:**
- 0 = CLOSED (healthy, green)
- 1 = HALF_OPEN (probing, yellow)
- 2 = OPEN (tripped, red)

**Response latency:**
```promql
histogram_quantile(0.50, sum(rate(gateway_request_duration_seconds_bucket[10s])) by (service, le))
histogram_quantile(0.95, ...)
histogram_quantile(0.99, ...)
```

**Error rate formula:**
```promql
100 * sum(rate(gateway_requests_total{status_class=~"5xx|circuit_open"}[10s])) by (service)
  / (sum(rate(gateway_requests_total[10s])) by (service) > 0)
```

**Home dashboard:**
```yaml
GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH: /etc/grafana/provisioning/dashboards/circuit_breaker.json
```

Circuit Breaker dashboard là home dashboard mặc định — cho thấy tính năng circuit breaker là điểm nhấn của project.

**Templating:**
- `DS_PROMETHEUS` — Datasource selector cho Prometheus
- `service` — Loki label values selector (multi-select, include all)

### 8.3. Loki — Log Aggregation

**Image:** grafana/loki:2.9.1
**Port:** 3100 (HTTP), 9096 (gRPC)
**Config:** `monitoring/loki-config.yml`

```yaml
auth_enabled: false

server:
  http_listen_port: 3100
  grpc_listen_port: 9096

common:
  instance_addr: 127.0.0.1
  path_prefix: /tmp/loki
  storage:
    filesystem:
      chunks_directory: /tmp/loki/chunks
      rules_directory: /tmp/loki/rules
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory

schema_config:
  configs:
    - from: 2020-10-24
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h

limits_config:
  reject_old_samples: true
  reject_old_samples_max_age: 168h
  ingestion_rate_mb: 100
  ingestion_burst_size_mb: 200
  per_stream_rate_limit: 50MB
  per_stream_rate_limit_burst: 100MB
```

**Phân tích Loki config:**
- **Auth disabled:** Chỉ dùng trong development
- **Filesystem storage:** Lưu chunks và rules vào /tmp/loki (ephemeral!)
- **Replication factor: 1** — Single instance mode
- **Schema v13:** Latest TSDB schema
- **Index period: 24h** — Rotate index mỗi ngày
- **Ingestion limits:** 100 MB/s rate, 200 MB/s burst — rất generous
- **Reject old samples:** Từ chối log mẫu cũ hơn 168h (7 ngày)

### 8.4. Promtail — Log Collector

**Image:** grafana/promtail:latest
**Port:** 9080 (HTTP), 0 (gRPC disabled)
**Config:** `monitoring/promtail-config.yml`

```yaml
server:
  http_listen_port: 9080
  grpc_listen_port: 0

positions:
  filename: /tmp/positions.yaml

clients:
  - url: http://loki:3100/loki/api/v1/push

scrape_configs:
  - job_name: docker
    docker_sd_configs:
      - host: unix:///var/run/docker.sock
        refresh_interval: 5s
    relabel_configs:
      - source_labels: [__meta_docker_container_label_com_docker_compose_service]
        target_label: 'service'
      - source_labels: [__meta_docker_container_name]
        regex: '/(.*)'
        target_label: 'container'
```

**Phân tích Promtail config:**
- **Docker service discovery:** Tự động phát hiện containers qua Docker socket
- **Refresh interval:** 5s — phát hiện containers mới nhanh
- **Relabeling:**
  - `service` label từ Docker Compose service name
  - `container` label từ container name (strip leading `/`)
- **Push URL:** http://loki:3100/loki/api/v1/push
- **Positions file:** /tmp/positions.yaml (theo dõi vị trí đã đọc trong log files)

**Volumes cần thiết:**
```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock:ro
  - /var/lib/docker/containers:/var/lib/docker/containers:ro
```
- Docker socket: Service discovery
- Docker containers dir: Đọc log files

### 8.5. Datasource Provisioning

Grafana tự động cấu hình datasources khi start:

**Prometheus datasource:**
```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

**Loki datasource:**
```yaml
apiVersion: 1
datasources:
  - name: Loki
    type: loki
    uid: loki
    access: proxy
    url: http://loki:3100
    isDefault: false
    editable: false
```

**Phân tích:**
- Prometheus là datasource mặc định
- Loki có UID fixed là "loki" — tham chiếu từ dashboards
- Cả hai đều `editable: false` — không cho user sửa qua UI
- Cả hai dùng `access: proxy` — Grafana server proxy request
- URLs dùng Docker DNS names (prometheus:9090, loki:3100)

---

## 9. Logging Stack

### 9.1. Loki Configuration (chi tiết)

Loki 2.9.1 chạy single-instance mode với filesystem storage.

**Single instance mode:**
- `replication_factor: 1`
- `ring.kvstore.store: inmemory`
- Phù hợp cho development, không cho production

**Schema v13 (TSDB):**
- Cải thiện query performance so với schema cũ (boltdb)
- Index được lưu dưới dạng TSDB (Time Series Database)
- Hỗ trợ better label indexing

**Limits (cho development):**
- `ingestion_rate_mb: 100` — Accept up to 100 MB/s
- `per_stream_rate_limit: 50MB` — 50 MB/s per stream
- Có thể cần giảm cho production để tránh abuse

**Ephemeral storage:**
- `/tmp/loki/chunks` — Hoàn toàn không persistent
- Khi container restart, mất tất cả logs
- Cần volume persistent cho production

### 9.2. Promtail Configuration (chi tiết)

**Docker service discovery:**
```yaml
docker_sd_configs:
  - host: unix:///var/run/docker.sock
    refresh_interval: 5s
```

Promtail quét Docker daemon mỗi 5s để phát hiện containers mới/dừng.

**Relabeling:**
```
__meta_docker_container_label_com_docker_compose_service → service
__meta_docker_container_name → container (strip leading /)
```

Ví dụ:
- Container name: `/url-shortener_url-service_1` → container = "url-shortener_url-service_1"
- Docker Compose label: `com.docker.compose.service = url-service` → service = "url-service"

**Positions:**
- File `/tmp/positions.yaml` ghi nhận vị trí đọc cuối cùng trong log file
- Tránh gửi duplicate logs khi Promtail restart

### 9.3. Grafana Log Explorer

Dashboard "Services Overview" tích hợp Loki log panel:

```json
{
  "datasource": { "type": "loki", "uid": "loki" },
  "targets": [{
    "expr": "{service=~\"$service\", service!=\"\"}",
    "legendFormat": "{{service}}"
  }],
  "type": "logs"
}
```

**Query:**
- `{service=~"$service", service!=""}` — Lọc theo service name (từ template variable)
- Hỗ trợ multi-select service
- Loại bỏ empty service label

**Log panel features:**
- Live tailing: Real-time log streaming
- Show labels: Hiển thị labels (service, container)
- Wrap log message: Wrap long lines
- Pretty JSON: Format JSON logs
- Sort order: Descending (newest first)

**Template variable:**
```yaml
- name: service
  type: query
  datasource: { type: "loki", uid: "loki" }
  definition: label_values(service)
  multi: true
  includeAll: true
```

Cho phép lọc logs theo service, multi-select, include all.

---

## 10. Container Dependency Graph

### 10.1. Dependency Tree

```
┌─────────────────────────────────────────────────────────────────────┐
│                          url-shortener                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──── url_db ─────► (no deps) ─────┐                              │
│  ├── analytics_db ──► (no deps) ─────┤                              │
│  ├── user_db ───────► (no deps) ─────┤                              │
│  ├── notification_db ► (no deps) ─────┤                              │
│  ├── redis ─────────► (no deps) ─────┤         LEVEL 0: Databases   │
│  └── rabbitmq ──────► (no deps) ─────┘                              │
│                                                                      │
│  ┌──── url-service ───► url_db, redis, rabbitmq ───┐               │
│  ├── analytics-service ► analytics_db, rabbitmq ────┤               │
│  ├── user-service ────► user_db ───────────────────┤  LEVEL 1:     │
│  └── notification-svc ► notification_db, rabbitmq ──┘  Microservices│
│                                                                      │
│  ┌──── gateway ──────► url-service, analytics-service,              │
│  │                      user-service, notification-service ──────── LEVEL 2: Gateway
│                                                                      │
│  ┌──── nginx ────────► gateway, frontend ──────────┐               │
│  ├── frontend ───────► gateway ────────────────────┤  LEVEL 3:     │
│  │                                                  │  Frontend     │
│  ┌──── prometheus ───► gateway ────────────────────┐               │
│  ├── grafana ────────► prometheus, loki ───────────┤  LEVEL 4:     │
│  ├── loki ───────────► (no deps) ──────────────────┤  Monitoring   │
│  └── promtail ───────► loki ───────────────────────┘               │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 10.2. Startup Order

**Ordered startup sequence:**

```
Phase 0: Infrastructure (parallel)
  ├── url_db ─────────── healthcheck: pg_isready (10s)
  ├── analytics_db ───── healthcheck: pg_isready (10s)
  ├── user_db ────────── healthcheck: pg_isready (10s)
  ├── notification_db ── healthcheck: pg_isready (10s)
  ├── redis ──────────── healthcheck: redis-cli ping (5s)
  └── rabbitmq ───────── healthcheck: rabbitmq-diagnostics ping (20s)

Phase 1: Microservices (parallel, after Phase 0 passes)
  ├── url-service ────── healthcheck: GET /health (15s)
  ├── analytics-service ─ healthcheck: GET /health (15s)
  ├── user-service ───── healthcheck: GET /health (15s)
  └── notification-svc ─ healthcheck: GET /health (15s)

Phase 2: Gateway (after all Phase 1 pass)
  └── gateway ────────── healthcheck: GET /health (20s)

Phase 3: Frontend & Proxy (after Phase 2 passes)
  ├── nginx ──────────── (service_started, no healthcheck)
  └── frontend ───────── (service_healthy)

Phase 4: Monitoring (independent, no strict dependency)
  ├── prometheus ─────── (no healthcheck)
  ├── loki ───────────── (no healthcheck)
  ├── grafana ────────── (depends_on prometheus, no condition)
  └── promtail ───────── (depends_on loki, no condition)
```

**Thời gian khởi động ước tính:**
- Phase 0: 20-30 giây (RabbitMQ lâu nhất)
- Phase 1: 20-30 giây (chờ healthcheck pass)
- Phase 2: 25-30 giây (gateway chờ tất cả services)
- Phase 3: 5-10 giây (nginx + frontend)
- **Tổng cộng:** 70-100 giây

### 10.3. Critical Path Analysis

**Critical path (startup bottleneck):**
```
rabbitmq (20s) → url-service (15s) → gateway (20s) → nginx
```

**Bottleneck analysis:**

| Component | Startup Time | Bottleneck | Khuyến Nghị |
|-----------|-------------|------------|-------------|
| RabbitMQ | 20-30s | Quản lý queues, exchanges | Có thể giảm start_period |
| PostgreSQL | 5-10s | Recovery, WAL replay | OK, nhanh nhất |
| Redis | 1-2s | In-memory, khởi động gần như instant | OK |
| Go services | 2-5s | Binary load, DB connection pool | Có thể tăng parallelism |
| Gateway | 2-5s | Chờ kết nối đến services | OK |
| Nginx | 0.5-1s | Khởi động nhanh | OK |
| Prometheus | 3-5s | WAL replay | OK |

**Single point of failure (SPOF):**
- **RabbitMQ:** Chỉ 1 instance, nếu fail → mất message queue
- **Redis:** Chỉ 1 instance, nếu fail → mất cache (có thể chịu được)
- **PostgreSQL (x4):** Mỗi database chỉ 1 instance
- **Gateway:** 2 replicas — có HA cơ bản
- **Nginx:** Chỉ 1 container — SPOF!

---

## 11. Production Deployment Recommendations

### 11.1. Ingress Controller

Hiện tại hệ thống dùng NodePort (gateway:30080) để expose ra ngoài. Cho production, cần Ingress controller:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: url-shortener-ingress
  namespace: url-shortener
  annotations:
    kubernetes.io/ingress.class: nginx
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  rules:
    - host: shortener.example.com
      http:
        paths:
          - path: /api(/|$)(.*)
            pathType: ImplementationSpecific
            backend:
              service:
                name: gateway
                port:
                  number: 8080
          - path: /r(/|$)(.*)
            pathType: ImplementationSpecific
            backend:
              service:
                name: gateway
                port:
                  number: 8080
          - path: /
            pathType: Prefix
            backend:
              service:
                name: frontend
                port:
                  number: 5173
  tls:
    - hosts:
        - shortener.example.com
      secretName: shortener-tls
```

**Lợi ích:**
- SSL/TLS termination (Let's Encrypt via cert-manager)
- Traffic routing rules
- Load balancing
- Rate limiting at ingress level
- Path-based routing
- Thay thế Nginx container (không cần chạy Nginx trong cluster)

### 11.2. Horizontal Pod Autoscaler (HPA)

Hiện tại, replicas được hardcode. Cho production, cần HPA:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: url-service-hpa
  namespace: url-shortener
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: url-service
  minReplicas: 3
  maxReplicas: 20
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: 80
    - type: Pods
      pods:
        metric:
          name: http_requests_per_second
        target:
          type: AverageValue
          averageValue: 1000
```

**HPA recommendations per service:**

| Service | Min | Max | CPU Target | Memory Target | Metric |
|---------|-----|-----|------------|--------------|--------|
| url-service | 3 | 20 | 70% | 80% | req/s > 1000 |
| analytics-service | 1 | 5 | 70% | 80% | N/A |
| user-service | 1 | 5 | 70% | 80% | N/A |
| notification-service | 1 | 5 | 70% | 80% | N/A |
| gateway | 2 | 10 | 70% | 80% | req/s > 5000 |

### 11.3. StatefulSet cho Databases

Như đã phân tích ở phần 4.6, databases cần StatefulSet + PVC:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: url-db
  namespace: url-shortener
spec:
  serviceName: url-db-headless
  replicas: 1
  selector:
    matchLabels:
      app: url-db
  template:
    metadata:
      labels:
        app: url-db
    spec:
      terminationGracePeriodSeconds: 30
      containers:
        - name: postgres
          image: postgres:16-alpine
          envFrom:
            - secretRef:
                name: db-secrets
          ports:
            - containerPort: 5432
          volumeMounts:
            - name: data
              mountPath: /var/lib/postgresql/data
          resources:
            requests:
              memory: "256Mi"
              cpu: "250m"
            limits:
              memory: "2Gi"
              cpu: "1"
          livenessProbe:
            exec:
              command: ["pg_isready", "-U", "urluser"]
            initialDelaySeconds: 30
            periodSeconds: 10
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "urluser", "-d", "urldb"]
            initialDelaySeconds: 5
            periodSeconds: 5
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: managed-premium
        resources:
          requests:
            storage: 50Gi
```

**Storage class options (Azure AKS):**
- `managed-premium`: SSD-based, IOPS cao
- `managed-standard`: HDD-based, rẻ hơn
- Backup via Velero hoặc Azure Backup

### 11.4. Resource Requests và Limits

Hiện tại, không có resource requests/limits trong Kubernetes manifests. Đây là vấn đề cho production:

```yaml
resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"
    cpu: "500m"
```

**Resource recommendations:**

| Pod | Request CPU | Request Memory | Limit CPU | Limit Memory | Notes |
|-----|-------------|---------------|-----------|-------------|-------|
| url-service | 200m | 256Mi | 1 | 1Gi | Heavy traffic |
| analytics-service | 100m | 128Mi | 500m | 512Mi | Async processing |
| user-service | 100m | 128Mi | 500m | 512Mi | Auth heavy |
| notification-service | 100m | 128Mi | 500m | 512Mi | Email/SMS |
| gateway | 200m | 256Mi | 1 | 1Gi | Critical path |
| redis | 100m | 128Mi | 500m | 512Mi | Cache |
| rabbitmq | 200m | 512Mi | 1 | 2Gi | Message broker |
| postgres (x4) | 250m | 512Mi | 2 | 4Gi | Database |
| prometheus | 500m | 1Gi | 2 | 4Gi | Metrics |
| grafana | 200m | 256Mi | 1 | 1Gi | Dashboards |
| loki | 500m | 1Gi | 2 | 4Gi | Log storage |
| promtail | 100m | 128Mi | 500m | 256Mi | Log collector |

**Total cluster resource estimate:**
- CPU requests: ~4.5 cores
- Memory requests: ~8 GB
- CPU limits: ~15 cores
- Memory limits: ~25 GB
- Minimum node pool: 3 nodes of Standard_D4s_v3 (4 vCPU, 16 GB RAM)

### 11.5. PersistentVolumeClaims

Ngoài StatefulSet, cần PVC cho các services cần persistent data:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: prometheus-data
  namespace: url-shortener
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 50Gi
  storageClassName: managed-premium
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: grafana-data
  namespace: url-shortener
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: loki-data
  namespace: url-shortener
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 100Gi
```

**Storage estimates for production (30 days retention):**

| Component | Storage/Day | 30 Days | Storage Class |
|-----------|------------|---------|---------------|
| url_db | 500 MB | 15 GB | managed-premium |
| analytics_db | 1 GB | 30 GB | managed-premium |
| user_db | 200 MB | 6 GB | managed-premium |
| notification_db | 100 MB | 3 GB | managed-premium |
| Prometheus | 1.5 GB | 45 GB | managed-premium |
| Grafana | 10 MB | 300 MB | managed-standard |
| Loki | 3 GB | 90 GB | managed-premium |
| RabbitMQ | 500 MB | 15 GB | managed-premium |
| **Total** | ~7 GB | ~205 GB | |

### 11.6. Network Policies

Cần thêm Network Policies để phân đoạn mạng:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: database-network-policy
  namespace: url-shortener
spec:
  podSelector:
    matchLabels:
      tier: database
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              tier: backend
      ports:
        - protocol: TCP
          port: 5432
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: backend-network-policy
  namespace: url-shortener
spec:
  podSelector:
    matchLabels:
      tier: backend
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: gateway
      ports:
        - protocol: TCP
          port: 8080
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-all
  namespace: url-shortener
spec:
  podSelector: {}
  policyTypes:
    - Ingress
    - Egress
```

**Network segmentation plan:**

| Tier | Labels | Ingress From | Egress To |
|------|--------|-------------|-----------|
| Database | tier: database | tier: backend (port 5432) | None |
| Backend | tier: backend | app: gateway (port 8080) | Database, Redis, RabbitMQ |
| Gateway | app: gateway | Ingress controller | tier: backend |
| Frontend | tier: frontend | Ingress controller | None |
| Cache | app: redis | tier: backend (port 6379) | None |
| Queue | app: rabbitmq | tier: backend (port 5672) | None |
| Monitoring | tier: monitoring | Grafana ingress | All (metrics scraping) |

### 11.7. Pod Disruption Budgets

Cho production, cần PDB để đảm bảo availability khi bảo trì:

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: url-service-pdb
  namespace: url-shortener
spec:
  minAvailable: 2
  selector:
    matchLabels:
      app: url-service
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: gateway-pdb
  namespace: url-shortener
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: gateway
```

**PDB recommendations:**

| Deployment | Replicas | minAvailable | maxUnavailable |
|-----------|---------|-------------|---------------|
| url-service | 3 | 2 | 1 |
| gateway | 2 | 1 | 1 |
| analytics-service | 1 | N/A | N/A (single replica) |
| user-service | 1 | N/A | N/A |
| notification-service | 1 | N/A | N/A |

### 11.8. Secret Management

**Current state:** Secrets hardcoded trong ConfigMap và Secret YAML files.

**Recommendation:** Dùng Azure Key Vault (AKS + AAD Pod Identity):

```yaml
apiVersion: aadpodidentity.k8s.io/v1
kind: AzureIdentity
metadata:
  name: url-shortener-identity
  namespace: url-shortener
spec:
  type: 0
  resourceID: /subscriptions/.../Microsoft.ManagedIdentity/userAssignedIdentities/url-shortener-id
  clientID: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
---
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: url-shortener-secrets
  namespace: url-shortener
spec:
  provider: azure
  parameters:
    usePodIdentity: "true"
    keyvaultName: "url-shortener-kv"
    objects: |
      array:
        - |
          objectName: jwt-secret
          objectType: secret
        - |
          objectName: db-url
          objectType: secret
    tenantId: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

---

## 12. Kết Luận

### Tổng Kết Kiến Trúc

Dự án URL Shortener Microservices triển khai một hệ thống microservices hoàn chỉnh với:

1. **Docker Compose:** 18 services cho môi trường phát triển local, với đầy đủ healthchecks, dependencies, volumes, và networking
2. **Dockerfiles:** Multi-stage build cho Go services, single-stage cho frontend
3. **Kubernetes:** 10 Deployments + 11 Services trên AKS, với ConfigMap/Secrets management
4. **CI/CD:** GitHub Actions với build matrix (6 services), Docker layer caching, deploy tự động lên AKS
5. **Monitoring:** Prometheus scrape 5 services (5s interval), Grafana với 2 provisioned dashboards
6. **Logging:** Loki + Promtail stack với Docker service discovery

### Điểm Mạnh

- **Database-per-service pattern:** Isolation tốt giữa các microservices
- **Healthchecks đầy đủ:** Đảm bảo dependency chain startup chính xác
- **CI/CD hoàn chỉnh:** Từ code push → build → test → deploy tự động
- **Monitoring coverage:** Tất cả services đều expose metrics
- **Circuit breaker monitoring:** Dashboard dedicated cho failure detection
- **Grafana auto-provisioning:** Dashboards và datasources được cấu hình sẵn

### Điểm Yếu Cần Cải Thiện

| Issue | Severity | Hiện Tại | Khuyến Nghị |
|-------|----------|---------|-------------|
| Database persistence | CRITICAL | emptyDir trong K8s | StatefulSet + PVC |
| Secrets in ConfigMap | HIGH | Passwords trong ConfigMap | Chuyển sang Secrets / Vault |
| No resource limits | HIGH | Không có requests/limits | Thêm resource spec cho mọi Pod |
| Single RabbitMQ | HIGH | 1 replica | Cluster mode |
| No Ingress | HIGH | NodePort duy nhất | Ingress Controller + TLS |
| No HPA | MEDIUM | Replicas hardcoded | HorizontalPodAutoscaler |
| No network policies | MEDIUM | Mạng phẳng | NetworkPolicy cho isolation |
| No livenessProbe | MEDIUM | Chỉ readinessProbe | Thêm liveness + startup probes |
| Retention ngắn | MEDIUM | Prometheus 1h | Tăng lên 30d cho production |
| No backup strategy | MEDIUM | Không có backup | Velero/WAL archiving |
| No multi-arch build | LOW | AMD64 only | Thêm ARM64 support |
| No PDB | LOW | Không có | PodDisruptionBudget |
| No affinity rules | LOW | Pod scheduling tự do | Node/AZ anti-affinity |

### Security Checklist cho Production

- [ ] Replace all default credentials (guest/guest, admin/admin)
- [ ] Move all secrets to Azure Key Vault or HashiCorp Vault
- [ ] Enable SSL/TLS (sslmode=require, AMQPS, HTTPS)
- [ ] Change JWT_SECRET (min 256-bit random)
- [ ] Add NetworkPolicies for micro-segmentation
- [ ] Enable SSL termination at Ingress
- [ ] Add OAuth2/OIDC authentication for Grafana
- [ ] Scan Docker images for vulnerabilities (Trivy, Snyk)
- [ ] Implement RBAC for Kubernetes resources
- [ ] Enable audit logging (Kubernetes audit, database audit)
- [ ] Regular security updates (Dependabot, Renovate)

### Monitoring Expansion Recommendations

- [ ] Add node_exporter for host metrics
- [ ] Add postgres_exporter (x4) for database metrics
- [ ] Add redis_exporter for cache metrics
- [ ] Add rabbitmq_exporter (or built-in Prometheus plugin)
- [ ] Add blackbox_exporter for external endpoint monitoring
- [ ] Add alertmanager with alerting rules
- [ ] Create SLO dashboard (error budget, latency, throughput)
- [ ] Add k6-operator for load testing in cluster
- [ ] Implement distributed tracing (OpenTelemetry + Jaeger/Tempo)

### Cost Estimates (Azure AKS)

| Resource | SKU | Quantity | Monthly Cost (est.) |
|----------|-----|----------|-------------------|
| AKS cluster | Standard_D4s_v3 (4 vCPU, 16 GB) | 3 nodes | ~$600 |
| Managed disks | Premium SSD 256 GB (P15) | 8 disks | ~$240 |
| Azure Database for PostgreSQL | Flexible Server, 2 vCore | 4 instances | ~$400 |
| Azure Cache for Redis | Standard C1 (1 GB) | 1 instance | ~$55 |
| Load balancer | Standard | 1 | ~$20 |
| Container Registry | Basic | 1 | ~$25 |
| **Total** | | | **~$1,340/month** |

*Note: Can reduce cost by running PostgreSQL on AKS with managed disks instead of Azure Database for PostgreSQL.*

### File Inventory

| File | Purpose | Lines |
|------|---------|-------|
| `docker-compose.yml` | Development environment | 356 |
| `k8s/namespace.yaml` | Kubernetes namespace | 4 |
| `k8s/config.yaml` | ConfigMap + Secret | 28 |
| `k8s/infra.yaml` | Infrastructure (6 Deployments + 6 Services) | 290 |
| `k8s/apps.yaml` | Applications (5 Deployments + 5 Services) | 340 |
| `services/*/Dockerfile` | Multi-stage build (x5) | 50 |
| `frontend/Dockerfile` | Single-stage build | 8 |
| `monitoring/prometheus.yml` | Scrape config (5 jobs) | 29 |
| `monitoring/loki-config.yml` | Loki config | 35 |
| `monitoring/promtail-config.yml` | Promtail config | 21 |
| `monitoring/grafana/provisioning/datasources/*` | Grafana datasources (x2) | 19 |
| `monitoring/grafana/provisioning/dashboards/*` | Grafana dashboards (x2 + provider) | 788 |
| `nginx/nginx.conf` | Reverse proxy config | 68 |
| `.github/workflows/ci.yml` | CI pipeline | 77 |
| `.github/workflows/cd.yml` | CD pipeline | 139 |

**Tổng cộng:** ~2,250 lines of deployment/infrastructure code.

---

*Báo cáo này được tạo bởi Agent AI vào ngày 2026-07-11. Dựa trên phân tích toàn bộ mã nguồn triển khai của dự án URL Shortener Microservices. Mọi khuyến nghị nên được xem xét và điều chỉnh phù hợp với yêu cầu cụ thể của môi trường production.*
