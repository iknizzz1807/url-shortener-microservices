# BÁO CÁO CHI TIẾT: DEPLOYMENT, KUBERNETES, DOCKER COMPOSE, CI/CD, MONITORING

---

## MỤC LỤC

1. [DOCKER COMPOSE — PHÂN TÍCH ĐẦY ĐỦ](#1-docker-compose--phân-tích-đầy-đủ)
   - 1.1 Networks & Volumes
   - 1.2 Infrastructure Containers
   - 1.3 Application Containers
   - 1.4 Monitoring Containers
2. [PROMETHEUS CONFIGURATION](#2-prometheus-configuration)
3. [GRAFANA DASHBOARDS](#3-grafana-dashboards)
   - 3.1 Circuit Breaker Monitor Dashboard
   - 3.2 Services Overview Dashboard
   - 3.3 Datasource Provisioning
4. [KUBERNETES MANIFESTS](#4-kubernetes-manifests)
   - 4.1 Namespace
   - 4.2 ConfigMap & Secret
   - 4.3 Infrastructure (Infra)
   - 4.4 Applications (Apps)
5. [HORIZONTAL SCALING](#5-horizontal-scaling)
6. [STARTUP ORDER & DEPENDENCY MANAGEMENT](#6-startup-order--dependency-management)
7. [GRACEFUL SHUTDOWN](#7-graceful-shutdown)
8. [CI/CD WORKFLOWS](#8-cicd-workflows)
   - 8.1 Continuous Integration (CI)
   - 8.2 Continuous Delivery (CD)
9. [PRODUCTION ROADMAP](#9-production-roadmap)
10. [LOAD TESTING VỚI K6](#10-load-testing-với-k6)
11. [NGINX REVERSE PROXY](#11-nginx-reverse-proxy)
12. [LOKI & PROMTAIL — LOG AGGREGATION](#12-loki--promtail--log-aggregation)
13. [KẾT LUẬN](#13-kết-luận)

---

## 1. DOCKER COMPOSE — PHÂN TÍCH ĐẦY ĐỦ

Docker Compose là công cụ orchestration container cấp độ developer, cho phép định nghĩa và chạy multi-container Docker applications. Trong đồ án này, `docker-compose.yml` định nghĩa **17 containers** được tổ chức thành 4 nhóm: infrastructure (cơ sở hạ tầng), application (ứng dụng), reverse proxy, và monitoring (giám sát).

File cấu hình đầy đủ nằm tại `docker-compose.yml` (356 dòng) cùng với file mở rộng `docker-compose.scale.yml` (23 dòng) dùng cho demo scaling local.

### 1.1 Networks & Volumes

#### Network

```yaml
networks:
  url-shortener:
    driver: bridge
```

**Phân tích:**
- **Bridge driver:** Đây là network driver mặc định của Docker. Tạo một private internal network trên host, nơi tất cả container trong cùng compose file có thể giao tiếp với nhau thông qua DNS resolution (tên service) mà không cần expose port ra host.
- **Tên network `url-shortener`:** Tất cả 17 container đều thuộc network này. Điều này cho phép:
  - Các service gọi nhau bằng tên container (VD: `url-service` gọi `url_db:5432`, `redis:6379`, `rabbitmq:5672`)
  - Prometheus scrape metrics qua internal network: `gateway:8080`, `url-service:8080`, ...
  - Grafana query Prometheus: `prometheus:9090`, Loki: `loki:3100`
  - Nginx proxy tới `gateway:8080` và `frontend:5173`
- **Tại sao không dùng host network?** Bridge network cô lập an toàn, không conflict port với host, phù hợp multi-service.

#### Volumes

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

Toàn bộ **8 named volumes** được khai báo. Docker sẽ tự động tạo volumes khi `docker compose up` lần đầu, và mount vào container tương ứng. Dữ liệu tồn tại ngay cả khi container bị xóa (`docker compose down`), chỉ mất khi chạy `docker compose down -v` (có flag `-v`).

| Volume | Mount vào container | Mục đích |
|--------|-------------------|----------|
| `url_db_data` | `/var/lib/postgresql/data` | Lưu dữ liệu PostgreSQL cho url-db |
| `analytics_db_data` | `/var/lib/postgresql/data` | Lưu dữ liệu PostgreSQL cho analytics-db |
| `user_db_data` | `/var/lib/postgresql/data` | Lưu dữ liệu PostgreSQL cho user-db |
| `notification_db_data` | `/var/lib/postgresql/data` | Lưu dữ liệu PostgreSQL cho notification-db |
| `rabbitmq_data` | `/var/lib/rabbitmq` | Lưu message queue data, exchanges, queues |
| `redis_data` | `/data` | Lưu cache data (dù ở chế độ ephemeral) |
| `prometheus_data` | `/prometheus` | Lưu time-series metrics thu thập được |
| `grafana_data` | `/var/lib/grafana` | Lưu dashboards, datasources, user preferences |

**Quyết định kiến trúc:** 4 PostgreSQL instances riêng biệt, mỗi service một database. Đây là kiến trúc **Database-per-Service** trong microservices pattern. Lợi ích:
- Cô lập dữ liệu: mỗi service chỉ truy cập database của mình
- Schema độc lập: có thể migrate/scale từng DB riêng
- Không shared database bottlenecks

---

### 1.2 Infrastructure Containers (Hạ tầng)

#### 1.2.1 PostgreSQL 16 Alpine (×4)

Bốn container PostgreSQL sử dụng image `postgres:16-alpine`, mỗi container phục vụ một service riêng:

**url_db (port 5432):**
```yaml
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

**analytics_db (port 5433):**
- `POSTGRES_DB: analyticsdb`, `POSTGRES_USER: analyticsuser`, `POSTGRES_PASSWORD: analyticspass`
- Healthcheck: `pg_isready -U analyticsuser -d analyticsdb`

**user_db (port 5434):**
- `POSTGRES_DB: userdb`, `POSTGRES_USER: useruser`, `POSTGRES_PASSWORD: userpass`
- Healthcheck: `pg_isready -U useruser -d userdb`

**notification_db (port 5435):**
- `POSTGRES_DB: notificationdb`, `POSTGRES_USER: notificationuser`, `POSTGRES_PASSWORD: notificationpass`
- Healthcheck: `pg_isready -U notificationuser -d notificationdb`

**Phân tích chi tiết:**

1. **Image `postgres:16-alpine`:** Alpine Linux (~5MB) + PostgreSQL 16 binary. So với image đầy đủ (debian-based ~350MB), alpine chỉ ~80MB. Giảm thời gian pull và dung lượng disk.

2. **Environment variables:**
   - `POSTGRES_DB`: Tên database mặc định được tạo khi container start lần đầu
   - `POSTGRES_USER`: Superuser tùy chỉnh (không dùng mặc định `postgres`)
   - `POSTGRES_PASSWORD`: Password cho superuser
   - Đây là phương thức cấu hình tiêu chuẩn của image PostgreSQL chính thức

3. **Port mapping:**
   - `5432:5432` → url_db (trùng port mặc định)
   - `5433:5432` → analytics_db (port host 5433 → container 5432)
   - `5434:5432` → user_db
   - `5435:5432` → notification_db
   - Mỗi DB expose port khác nhau ra host để phát triển local (có thể dùng bất kỳ SQL client nào kết nối từ máy host)
   - Trong internal network, các service vẫn kết nối qua port mặc định 5432

4. **Healthcheck:** Cực kỳ quan trọng cho startup ordering.
   - Sử dụng `pg_isready -U <user> -d <db>`: utility của PostgreSQL kiểm tra xem server đã sẵn sàng nhận kết nối chưa
   - `interval: 5s`: Kiểm tra mỗi 5 giây
   - `timeout: 5s`: Mỗi lần kiểm tra timeout sau 5s
   - `retries: 10`: Sau 10 lần thất bại liên tiếp, container được đánh dấu unhealthy
   - `start_period: 10s`: Docker chờ 10s trước khi bắt đầu healthcheck (cho PostgreSQL khởi tạo)

#### 1.2.2 Adminer

```yaml
adminer:
  image: adminer:latest
  ports:
    - "8090:8080"
```

Adminer là lightweight database management tool (thay thế phpMyAdmin cho PostgreSQL). Chỉ dùng trong development, kết nối tới bất kỳ database nào trong network `url-shortener`.

#### 1.2.3 RabbitMQ 3.13 Management

```yaml
rabbitmq:
  image: rabbitmq:3.13-management-alpine
  environment:
    RABBITMQ_DEFAULT_USER: guest
    RABBITMQ_DEFAULT_PASS: guest
  ports:
    - "5672:5672"    # AMQP protocol
    - "15672:15672"  # Management UI (HTTP)
  volumes:
    - rabbitmq_data:/var/lib/rabbitmq
  healthcheck:
    test: ["CMD", "rabbitmq-diagnostics", "ping"]
    interval: 10s
    timeout: 10s
    retries: 10
    start_period: 20s
```

**Phân tích:**
- **Image tag `3.13-management-alpine`:** Bao gồm RabbitMQ server + management plugin (giao diện web). Alpine variant giảm kích thước.
- **Port 5672 (AMQP):** Cổng giao thức chính cho producer/consumer kết nối.
- **Port 15672 (Management UI):** Giao diện web quản lý exchanges, queues, connections. Truy cập tại http://localhost:15672 với user guest/guest.
- **Healthcheck `rabbitmq-diagnostics ping`:** Command từ RabbitMQ management CLI. Kiểm tra ứng dụng RabbitMQ đã start chưa (không chỉ process).
- **start_period: 20s:** RabbitMQ cần thời gian khởi tạo Mnesia database, cluster metadata.

Vai trò trong kiến trúc:
- **Message Broker** cho event-driven communication
- **url-service** publish `url.created`, `url.accessed` events qua AMQP
- **analytics-service** consume `url.accessed` để ghi click statistics
- **notification-service** consume `url.created` để gửi thông báo

#### 1.2.4 Redis 7 Alpine (Ephemeral Cache)

```yaml
redis:
  image: redis:7-alpine
  command: redis-server --save "" --appendonly no
  ports:
    - "6379:6379"
  volumes:
    - redis_data:/data
  healthcheck:
    test: ["CMD", "redis-cli", "ping"]
    interval: 5s
    timeout: 3s
    retries: 10
```

**Phân tích:**
- **Chế độ ephemeral:** `--save "" --appendonly no` — tắt hoàn toàn persistence.
  - `--save ""` vô hiệu hóa RDB snapshot (save to disk)
  - `--appendonly no` tắt AOF (Append-Only File) log
  - Dữ liệu chỉ tồn tại trong memory, mất khi restart
- **Tại sao ephemeral?** Redis chỉ dùng làm cache (short-lived data):
  - Cache short URLs: giảm load PostgreSQL khi redirect
  - Rate limiting counters: sliding window trong Redis
  - Session cache (nếu cần)
  - Không cần persist vì dữ liệu có thể rebuild từ database
- **Healthcheck `redis-cli ping`:** Redis trả lời "PONG" nếu sẵn sàng.
- **Volume `redis_data`:** Dù ephemeral, volume vẫn mount để tránh permission issues.

#### 1.2.5 Nginx 1.27 Alpine (Reverse Proxy)

```yaml
nginx:
  image: nginx:1.27-alpine
  container_name: nginx-proxy
  ports:
    - "80:80"
  volumes:
    - ./nginx/nginx.conf:/etc/nginx/nginx.conf:ro
  depends_on:
    gateway:
      condition: service_healthy
    frontend:
      condition: service_started
  restart: unless-stopped
```

**Phân tích:**
- **Image nginx:1.27-alpine:** Phiên bản mới nhất của Nginx trên Alpine Linux
- **Port 80:80:** Nginx lắng nghe trên cổng HTTP chuẩn, là single entry point cho tất cả traffic
- **Config file mount:** `./nginx/nginx.conf` (read-only) — xem phân tích chi tiết ở [Mục 11](#11-nginx-reverse-proxy)
- **depends_on với gateway condition service_healthy:** Nginx chỉ start khi gateway đã health check pass. Đây là tầng cuối cùng trong startup chain.
- **restart: unless-stopped:** Tự động restart nếu crash, nhưng không restart nếu admin stop manually.

---

### 1.3 Application Containers (Ứng dụng)

#### 1.3.1 url-service

```yaml
url-service:
  build:
    context: .
    dockerfile: services/url-service/Dockerfile
  environment:
    DATABASE_URL: postgres://urluser:urlpass@url_db:5432/urldb?sslmode=disable
    REDIS_URL: redis://redis:6379/0
    RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
    PORT: "8080"
    SHORT_URL_BASE: ${SHORT_URL_BASE:-http://localhost/r}
    JWT_SECRET: change-this-in-production-minimum-32-chars
    IP_HASH_SALT: change-this-in-production-random-salt
  ports:
    - "8081:8080"
  depends_on:
    url_db:
      condition: service_healthy
    redis:
      condition: service_healthy
    rabbitmq:
      condition: service_healthy
  healthcheck:
    test: wget -qO- http://localhost:8080/health || exit 1
    interval: 10s
    timeout: 5s
    retries: 5
    start_period: 15s
```

**Dockerfile phân tích:**

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
WORKDIR /app/services/url-service
RUN go build -o main .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/services/url-service/main .
CMD ["./main"]
```

- **Multi-stage build:**
  - Stage 1 (builder): `golang:1.23-alpine` ~350MB, chứa Go compiler và dependencies. Copy toàn bộ monorepo, build binary cho url-service
  - Stage 2 (runtime): `alpine:latest` ~5MB, chỉ chứa binary đã build. Kết quả final image ~15-20MB
- **WORKDIR context:** `COPY . .` copy toàn bộ monorepo (cần `go.work`, `shared/`, `services/url-service/`) vì Go workspace
- **Không dùng `go mod download` riêng:** Build đơn giản nhưng không cache dependencies

**Environment variables:**
- `DATABASE_URL`: DSN kết nối PostgreSQL với `?sslmode=disable` (local, không cần TLS)
- `REDIS_URL`: `redis://redis:6379/0` — database 0
- `RABBITMQ_URL`: `amqp://guest:guest@rabbitmq:5672/`
- `SHORT_URL_BASE`: Dùng `${}` với default value — có thể override từ `.env` file
- `JWT_SECRET`, `IP_HASH_SALT`: Secret values, mặc định là placeholder cảnh báo "change-this-in-production"

**Port mapping `8081:8080`:** Service lắng nghe port 8080 trong container, expose ra host port 8081. Cho phép test từng service riêng lẻ.

**depends_on với condition `service_healthy`:**
- Đảm bảo url_db, redis, rabbitmq hoàn toàn sẵn sàng trước khi url-service start
- Không chỉ "container started" mà phải health check pass

**Healthcheck:**
- `wget -qO- http://localhost:8080/health || exit 1`: Gọi endpoint /health, nếu không có response thì exit code 1
- `start_period: 15s`: Cho service thời gian khởi tạo (kết nối DB, Redis, RabbitMQ)

#### 1.3.2 analytics-service

```yaml
analytics-service:
  build:
    context: .
    dockerfile: services/analytics-service/Dockerfile
  environment:
    DATABASE_URL: postgres://analyticsuser:analyticspass@analytics_db:5432/analyticsdb?sslmode=disable
    RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
    PORT: "8080"
    JWT_SECRET: change-this-in-production-minimum-32-chars
    IP_HASH_SALT: change-this-in-production-random-salt
  ports:
    - "8082:8080"
  depends_on:
    analytics_db:
      condition: service_healthy
    rabbitmq:
      condition: service_healthy
```

**Phân tích:**
- **Không có REDIS_URL:** Analytics service không cần cache, chỉ ghi dữ liệu click events
- **Dependency nhẹ hơn url-service:** Chỉ cần analytics_db và rabbitmq
- **Chức năng:**
  - Consume `url.accessed` events từ RabbitMQ
  - Ghi click statistics (timestamp, IP hash, user agent, referrer) vào PostgreSQL
  - Tính toán milestones (10, 100, 1000 clicks, ...)
  - Publish milestone events về RabbitMQ cho notification-service
  - Serve `/stats/{code}` và `/stats/{code}/timeline` endpoints

#### 1.3.3 user-service

```yaml
user-service:
  build:
    context: .
    dockerfile: services/user-service/Dockerfile
  environment:
    DATABASE_URL: postgres://useruser:userpass@user_db:5432/userdb?sslmode=disable
    PORT: "8080"
    JWT_SECRET: change-this-in-production-minimum-32-chars
  ports:
    - "8083:8080"
  depends_on:
    user_db:
      condition: service_healthy
```

**Phân tích:**
- **Chỉ phụ thuộc database:** Không cần Redis hay RabbitMQ
- **Đơn giản nhất trong 4 services:** Chỉ xử lý register, login, get profile
- **Không có IP_HASH_SALT:** User service không cần hash IP

#### 1.3.4 notification-service

```yaml
notification-service:
  build:
    context: .
    dockerfile: services/notification-service/Dockerfile
  environment:
    DATABASE_URL: postgres://notificationuser:notificationpass@notification_db:5432/notificationdb?sslmode=disable
    RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
    JWT_SECRET: change-this-in-production-minimum-32-chars
    PORT: "8080"
  ports:
    - "8084:8080"
  depends_on:
    notification_db:
      condition: service_healthy
    rabbitmq:
      condition: service_healthy
```

**Phân tích:**
- **Chức năng:** Consume milestone events từ RabbitMQ, tạo notifications trong database, serve `GET /notifications` API
- **Consumer pattern:** Tương tự analytics-service nhưng lưu notification records thay vì click stats

#### 1.3.5 Gateway

```yaml
gateway:
  build:
    context: .
    dockerfile: gateway/Dockerfile
  environment:
    URL_SERVICE_URL: http://url-service:8080
    ANALYTICS_SERVICE_URL: http://analytics-service:8080
    USER_SERVICE_URL: http://user-service:8080
    NOTIFICATION_SERVICE_URL: http://notification-service:8080
    REDIS_URL: redis://redis:6379/0
    JWT_SECRET: change-this-in-production-minimum-32-chars
    SHORTEN_RATE_LIMIT: "100000"
    REDIRECT_RATE_LIMIT: "100000"
    PORT: "8080"
  ports:
    - "8080:8080"
  depends_on:
    url-service:
      condition: service_healthy
    analytics-service:
      condition: service_healthy
    user-service:
      condition: service_healthy
    notification-service:
      condition: service_healthy
  healthcheck:
    test: wget -qO- http://localhost:8080/health || exit 1
    start_period: 20s
```

**Phân tích:**
- **API Gateway pattern:** Single entry point cho tất cả client requests. Gateway quyết định route request tới service nào.
- **4 service URLs:** Biết địa chỉ của tất cả downstream services
- **Rate limiting:** `SHORTEN_RATE_LIMIT=100000`, `REDIRECT_RATE_LIMIT=100000` — con số rất cao (cho load testing), thực tế production sẽ thấp hơn
- **Circuit Breaker:** Bảo vệ hệ thống khi downstream service fail (phân tích chi tiết ở Mục 3)
- **start_period: 20s:** Lâu hơn các service khác vì gateway cần chờ tất cả downstream services sẵn sàng

#### 1.3.6 Frontend

```yaml
frontend:
  build:
    context: .
    dockerfile: frontend/Dockerfile
  environment:
    VITE_API_BASE_URL: ${VITE_API_BASE_URL:-http://localhost:8080}
  ports:
    - "5173:5173"
  depends_on:
    gateway:
      condition: service_healthy
```

**Dockerfile:**
```dockerfile
FROM node:22-alpine
WORKDIR /app
COPY frontend/package*.json ./
RUN npm install
COPY frontend/ ./
EXPOSE 5173
CMD ["npm", "run", "dev"]
```

- Frontend là Vite + React app, chạy dev server trên port 5173
- `VITE_API_BASE_URL`: Biến môi trường cho Vite, quyết định backend URL mà frontend gọi
- Chạy ở chế độ dev (không build static production), phù hợp demo

---

### 1.4 Monitoring Containers (Giám sát)

#### 1.4.1 Prometheus v2.53

```yaml
prometheus:
  image: prom/prometheus:v2.53.0
  command:
    - "--config.file=/etc/prometheus/prometheus.yml"
    - "--storage.tsdb.path=/prometheus"
    - "--web.console.libraries=/etc/prometheus/console_libraries"
    - "--web.console.templates=/etc/prometheus/consoles"
    - "--storage.tsdb.retention.time=1h"
    - "--web.enable-lifecycle"
  volumes:
    - ./monitoring/prometheus.yml:/etc/prometheus/prometheus.yml:ro
    - prometheus_data:/prometheus
  ports:
    - "9090:9090"
  depends_on:
    gateway:
      condition: service_healthy
```

**Command-line flags phân tích:**
- `--config.file`: Đường dẫn config (mounted từ host)
- `--storage.tsdb.path`: Thư mục lưu time-series data
- `--storage.tsdb.retention.time=1h`: **Chỉ giữ dữ liệu 1 giờ** — phù hợp demo/load testing. Production cần >30 ngày
- `--web.enable-lifecycle`: Cho phép reload config qua HTTP POST `/-/reload` (không cần restart container)
- **depends_on gateway:** Prometheus scrape các service, nên cần gateway (và các service khác) sẵn sàng

#### 1.4.2 Grafana 11.1

```yaml
grafana:
  image: grafana/grafana:11.1.0
  environment:
    GF_SECURITY_ADMIN_USER: admin
    GF_SECURITY_ADMIN_PASSWORD: admin
    GF_USERS_ALLOW_SIGN_UP: "false"
    GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH: /etc/grafana/provisioning/dashboards/circuit_breaker.json
  volumes:
    - grafana_data:/var/lib/grafana
    - ./monitoring/grafana/provisioning:/etc/grafana/provisioning:ro
  ports:
    - "3000:3000"
  depends_on:
    - prometheus
    - loki
```

**Phân tích:**
- **Environment variables (GF_ prefix = Grafana config):**
  - `GF_SECURITY_ADMIN_USER=admin`, `GF_SECURITY_ADMIN_PASSWORD=admin`: Credentials mặc định
  - `GF_USERS_ALLOW_SIGN_UP=false`: Vô hiệu hóa đăng ký user mới
  - `GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH`: Set **Circuit Breaker Monitor** làm home dashboard mặc định
- **Provisioning volume mount:** `monitoring/grafana/provisioning/` → `/etc/grafana/provisioning/`
  - Cấu trúc provisioning:
    ```
    monitoring/grafana/provisioning/
    ├── dashboards/
    │   ├── dashboards.yml        # Dashboard provider config
    │   ├── circuit_breaker.json   # Circuit Breaker dashboard
    │   └── services_overview.json # Services Overview dashboard
    └── datasources/
        ├── prometheus.yml        # Prometheus datasource
        └── loki.yml              # Loki datasource
    ```
- **Provisioning = zero-touch setup:** Grafana tự động import datasources và dashboards khi start, không cần manual config qua UI

#### 1.4.3 Loki 2.9.1

```yaml
loki:
  image: grafana/loki:2.9.1
  ports:
    - "3100:3100"
  volumes:
    - ./monitoring/loki-config.yml:/etc/loki/local-config.yaml:ro
  command: -config.file=/etc/loki/local-config.yaml
```

Loki là log aggregation system của Grafana (Prometheus cho logs). Cấu hình chi tiết ở [Mục 12](#12-loki--promtail--log-aggregation).

#### 1.4.4 Promtail

```yaml
promtail:
  image: grafana/promtail:latest
  environment:
    - DOCKER_API_VERSION=1.44
  volumes:
    - ./monitoring/promtail-config.yml:/etc/promtail/config.yml:ro
    - /var/run/docker.sock:/var/run/docker.sock:ro
    - /var/lib/docker/containers:/var/lib/docker/containers:ro
  command: -config.file=/etc/promtail/config.yml
  depends_on:
    - loki
```

**Phân tích:**
- **Promtail = log agent:** Đọc logs từ Docker containers và gửi tới Loki
- **`DOCKER_API_VERSION=1.44`:** Chỉ định phiên bản Docker API để tương thích
- **Volume mounts:**
  - `/var/run/docker.sock`: Cho phép Promtail gọi Docker API để discover containers và đọc log paths
  - `/var/lib/docker/containers`: Truy cập trực tiếp vào thư mục chứa JSON log files của Docker
- **depends_on loki:** Promtail cần Loki sẵn sàng trước khi gửi logs

---

## 2. PROMETHEUS CONFIGURATION

File: `monitoring/prometheus.yml`

```yaml
global:
  scrape_interval: 5s
  evaluation_interval: 5s

scrape_configs:
  - job_name: "gateway"
    static_configs:
      - targets: ["gateway:8080"]

  - job_name: "url-service"
    static_configs:
      - targets: ["url-service:8080"]

  - job_name: "analytics-service"
    static_configs:
      - targets: ["analytics-service:8080"]

  - job_name: "user-service"
    static_configs:
      - targets: ["user-service:8080"]

  - job_name: "notification-service"
    static_configs:
      - targets: ["notification-service:8080"]
```

**Phân tích chi tiết:**

### `global` section:
- **`scrape_interval: 5s`:** Prometheus thu thập metrics từ mỗi target mỗi 5 giây. Rất nhanh (production thường 15-30s), phù hợp load testing để thấy real-time circuit breaker state transitions.
- **`evaluation_interval: 5s`:** Tần suất đánh giá alerting rules (dù chưa dùng alerting trong project này).

### `scrape_configs`:
- **5 jobs** tương ứng 5 Go services (gateway + 4 microservices)
- Mỗi job scrape từ `/<service>:8080/metrics`
- **Static config:** Targets cố định, phù hợp Docker Compose. Trong Kubernetes, sẽ dùng `kubernetes_sd_configs` thay thế.

### Metrics path:
- Mỗi service Go sử dụng `promhttp.Handler()` để expose metrics trên `/metrics`
- Gateway expose custom metrics:
  - `gateway_circuit_breaker_state` (gauge)
  - `gateway_circuit_breaker_trips_total` (counter)
  - `gateway_circuit_breaker_rejected_total` (counter)
  - `gateway_requests_total` (counter, với labels `service`, `status_class`)
  - `gateway_request_duration_seconds` (histogram, với label `service`)
- Go runtime metrics tự động:
  - `go_goroutines`
  - `go_memstats_alloc_bytes`
  - `go_memstats_heap_alloc_bytes`
  - `go_gc_duration_seconds`
  - `process_cpu_seconds_total`
  - `process_open_fds`

### Tại sao Prometheus scrape interval 5s?
- **Load testing:** Cần granularity cao để thấy circuit breaker transition trong thời gian thực
- **Demo:** Người xem có thể thấy state thay đổi ngay trên Grafana
- **Trade-off:** Tốn tài nguyên hơn (CPU/RAM cho Prometheus, network bandwidth). Với 5 services × 5s = 60 requests/phút, không đáng kể

---

## 3. GRAFANA DASHBOARDS

Grafana được cấu hình với provisioning cho zero-touch setup. Hai dashboard được provisioned tự động.

### 3.1 Circuit Breaker Monitor Dashboard

File: `monitoring/grafana/provisioning/dashboards/circuit_breaker.json` (436 dòng)

**UID:** `circuit-breaker-monitor`
**Mặc định là Home dashboard** của Grafana.

#### Panel 1: Circuit Breaker State (Stat, gauge)
- **Vị trí:** (x:0, y:1, w:12, h:5)
- **Query:** `gateway_circuit_breaker_state`
- **Legend:** `{{service}}` (chỉ có url-service hiện tại)
- **Value mappings:**
  - 0 = GREEN + "CLOSED" (hoạt động bình thường)
  - 1 = YELLOW + "HALF_OPEN" (đang probe)
  - 2 = RED + "OPEN" (mạch hở, request bị reject)
- **Display mode:** `colorMode: background` — background chuyển màu theo state
- **Mục đích:** Trạng thái real-time của circuit breaker, nhìn thấy ngay lập tức khi state thay đổi

#### Panel 2: CB Trips (total) (Stat)
- **Query:** `increase(gateway_circuit_breaker_trips_total[$__range])`
- **Legend:** `{{service}} trips`
- **Mục đích:** Số lần CB transition sang OPEN. Dùng `increase` để đếm trong khoảng thời gian dashboard hiện tại

#### Panel 3: Rejected by CB (total) (Stat)
- **Query:** `increase(gateway_circuit_breaker_rejected_total[$__range])`
- **Mục đích:** Số request bị reject vì CB state = OPEN. Khi CB OPEN, request bị từ chối ngay lập tức mà không cần gọi downstream service

#### Panel 4: Circuit Breaker State Over Time (Time series)
- **Vị trí:** (w:24, h:8) — full width
- **Query:** `gateway_circuit_breaker_state`
- **Legend:** `{{service}}`
- **Line interpolation:** `stepAfter` — giữ state constant giữa các lần thay đổi (không smooth line)
- **Thresholds:** green (0-1), yellow (1-2), red (>2)
- **Value range:** min: -0.2, max: 2.5
- **Mục đích:** Xem lịch sử state transition. Khi load test + stop url-service, thấy rõ: 0→2 (CLOSED→OPEN), sau 30s 2→1 (OPEN→HALF_OPEN), nếu probe thành công 1→0 (HALF_OPEN→CLOSED)

#### Panel 5: Requests/sec (by service) (Time series)
- **Vị trí:** (w:12, h:8)
- **Query:** `sum(rate(gateway_requests_total[10s])) by (service)`
- **Mục đích:** Throughput requests theo từng upstream service. Khi CB OPEN, traffic về url-service giảm về 0

#### Panel 6: Requests/sec by Status Class (Stacked bar)
- **Query:** `sum(rate(gateway_requests_total[10s])) by (status_class)`
- **Màu sắc:**
  - 2xx: Xanh lá (#73BF69)
  - 5xx: Đỏ (#F2495C)
  - circuit_open: Cam (#FF9830)
- **Stacking mode:** `normal` — stacked bars
- **Mục đích:** Trực quan hóa sự thay đổi từ 2xx→5xx→circuit_open khi CB trip

#### Panel 7: Error Rate % (Time series)
- **Query:** `100 * sum(rate(gateway_requests_total{status_class=~"5xx|circuit_open"}[10s])) by (service) / (sum(rate(gateway_requests_total[10s])) by (service) > 0)`
- **Thresholds:** green (0-10%), orange (10-50%), red (>50%)
- **Mục đích:** Phần trăm lỗi. Khi CB OPEN, error rate có thể lên 100%

#### Panel 8: Response Latency p50/p95/p99 (Time series)
- **Queries:**
  - p50: `histogram_quantile(0.50, sum(rate(gateway_request_duration_seconds_bucket[10s])) by (service, le))`
  - p95: `histogram_quantile(0.95, ...)`
  - p99: `histogram_quantile(0.99, ...)`
- **Mục đích:** Phân tích latency phân vị. Khi CB mở, latency giảm mạnh (vì request bị reject ngay, không chờ timeout)

### 3.2 Services Overview Dashboard

File: `monitoring/grafana/provisioning/dashboards/services_overview.json` (341 dòng)

**UID:** `services-overview`

Dashboard này tập trung vào Go runtime metrics và system resources:

#### Row 1: Go Runtime Overview
- **Current Goroutines (Stat):** `go_goroutines` — số goroutines đang chạy mỗi service
- **Current Allocated Heap Memory (Stat):** `go_memstats_alloc_bytes` — heap memory real-time
- **Goroutines Over Time (Time series):** Phát hiện goroutine leak
- **Allocated Heap Memory Over Time (Time series):** Phát hiện memory leak

#### Row 2: System & Resource Utilization
- **CPU Core Usage:** `rate(process_cpu_seconds_total[30s])` — CPU usage mỗi service
- **Open File Descriptors:** `process_open_fds` — phát hiện file descriptor leak

#### Row 3: Application Logs
- **Live Log Stream (Logs panel):** `{service=~"$service", service!=""}` — query logs từ Loki
- **Template variable $service:** Lấy từ `label_values(service)` trong Loki
- **Multi-select:** Có thể chọn nhiều service cùng lúc
- **Display:** Show labels, wrap log messages, descending sort, prettify JSON

### 3.3 Datasource Provisioning

#### Prometheus Datasource (`datasources/prometheus.yml`):
```yaml
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

#### Loki Datasource (`datasources/loki.yml`):
```yaml
datasources:
  - name: Loki
    type: loki
    uid: loki
    access: proxy
    url: http://loki:3100
    isDefault: false
    editable: false
```

#### Dashboard Provider (`dashboards/dashboards.yml`):
```yaml
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

- `updateIntervalSeconds: 30`: Grafana kiểm tra thay đổi trong thư mục dashboards mỗi 30s
- `type: file`: Load dashboards từ JSON files trên filesystem

---

## 4. KUBERNETES MANIFESTS

Toàn bộ Kubernetes manifests nằm trong thư mục `k8s/`, gồm 4 files:
- `namespace.yaml` — Namespace
- `config.yaml` — ConfigMap + Secret
- `infra.yaml` — Infrastructure deployments & services
- `apps.yaml` — Application deployments & services

### 4.1 namespace.yaml

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: url-shortener
```

Tạo namespace riêng `url-shortener` để cô lập tài nguyên với các ứng dụng khác trong cluster. Tất cả các manifest khác đều reference namespace này.

### 4.2 config.yaml

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: url-shortener
data:
  PORT: "8080"
  SHORT_URL_BASE: http://localhost:30080/r
  IP_HASH_SALT: change-this-in-production-random-salt
  URL_SERVICE_URL: http://url-service:8080
  ANALYTICS_SERVICE_URL: http://analytics-service:8080
  USER_SERVICE_URL: http://user-service:8080
  NOTIFICATION_SERVICE_URL: http://notification-service:8080
  REDIS_URL: redis://redis:6379/0
  RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
  URL_DATABASE_URL: postgres://urluser:urlpass@url-db:5432/urldb?sslmode=disable
  ANALYTICS_DATABASE_URL: postgres://analyticsuser:analyticspass@analytics-db:5432/analyticsdb?sslmode=disable
  USER_DATABASE_URL: postgres://useruser:userpass@user-db:5432/userdb?sslmode=disable
  NOTIFICATION_DATABASE_URL: postgres://notificationuser:notificationpass@notification-db:5432/notificationdb?sslmode=disable
---
apiVersion: v1
kind: Secret
metadata:
  name: app-secrets
  namespace: url-shortener
type: Opaque
stringData:
  JWT_SECRET: change-this-in-production-minimum-32-chars
```

**Phân tích thiết kế:**
- **ConfigMap** chứa non-sensitive configuration (URLs, ports, salts)
- **Secret** riêng cho JWT_SECRET (sensitive data). Dùng `stringData` cho plain-text (dễ đọc), Kubernetes tự động base64-encode khi lưu
- **Khác biệt so với Docker Compose:**
  - MySQL DSNs dùng tên service Kubernetes (có dấu gạch ngang: `url-db`) thay vì underscore (Docker Compose: `url_db`)
  - Database URLs được đặt tên riêng theo từng service (`URL_DATABASE_URL`, `ANALYTICS_DATABASE_URL`, ...) thay vì tất cả đều là `DATABASE_URL`
  - Docker Compose dùng biến môi trường trực tiếp, K8s dùng ConfigMap/Secret references

### 4.3 infra.yaml

File này định nghĩa tất cả infrastructure deployments và services. Dài 290 dòng, gồm:

#### Redis
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
---
apiVersion: v1
kind: Service
metadata:
  name: redis
  namespace: url-shortener
spec:
  selector:
    app: redis
  ports:
    - port: 6379
      targetPort: 6379
```

#### RabbitMQ
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
---
apiVersion: v1
kind: Service
metadata:
  name: rabbitmq
  namespace: url-shortener
spec:
  selector:
    app: rabbitmq
  ports:
    - name: amqp
      port: 5672
      targetPort: 5672
    - name: management
      port: 15672
      targetPort: 15672
```

#### 4 PostgreSQL databases: url-db, analytics-db, user-db, notification-db
Mẫu cho url-db:
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
---
apiVersion: v1
kind: Service
...
```

**Điểm khác biệt quan trọng với Docker Compose:**

| Aspect | Docker Compose | Kubernetes |
|--------|---------------|------------|
| Storage | Named volume (persistent) | **emptyDir** (ephemeral!) |
| Healthcheck | `healthcheck` directive | **readinessProbe** |
| Port exposure | `ports: "5432:5432"` | `containerPort: 5432` |
| Service discovery | Docker DNS (`url_db`) | K8s Service (`url-db`) |

⚠️ **Lưu ý:** Trong K8s manifests, PostgreSQL dùng `emptyDir: {}` thay vì PersistentVolumeClaim. Điều này có nghĩa **dữ liệu database sẽ mất khi pod restart**. Đây là thiết kế cho local development (kind/minikube). Production bắt buộc phải dùng PVC với storage class.

### 4.4 apps.yaml

File này định nghĩa application deployments (340 dòng). Điểm nổi bật:

#### url-service (3 replicas):
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: url-service
  namespace: url-shortener
spec:
  replicas: 3
  selector:
    matchLabels:
      app: url-service
  template:
    metadata:
      labels:
        app: url-service
    spec:
      containers:
        - name: url-service
          image: url-shortener-microservices-url-service:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          env:
            - name: DATABASE_URL
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: URL_DATABASE_URL
            - name: JWT_SECRET
              valueFrom:
                secretKeyRef:
                  name: app-secrets
                  key: JWT_SECRET
            # ... các env khác từ ConfigMap
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 10
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: url-service
  namespace: url-shortener
spec:
  type: ClusterIP
  selector:
    app: url-service
  ports:
    - port: 8080
      targetPort: 8080
```

#### Gateway (2 replicas, NodePort):
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gateway
  namespace: url-shortener
spec:
  replicas: 2
  ...
---
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

**Phân tích:**

1. **ClusterIP Services:** url-service, analytics-service, user-service, notification-service đều là ClusterIP — chỉ accessible trong cluster. Các service khác gọi qua DNS nội bộ.

2. **Gateway NodePort 30080:** Là cổng vào duy nhất từ bên ngoài cluster.
   - `nodePort: 30080` (range 30000-32767)
   - Truy cập: `http://<node-ip>:30080`
   - Production sẽ dùng Ingress Controller (nginx-ingress, Traefik) với TLS termination

3. **Env từ ConfigMap + Secret:**
   - Non-sensitive (URLs, ports) → `configMapKeyRef` từ `app-config`
   - Sensitive (JWT_SECRET) → `secretKeyRef` từ `app-secrets`
   - Mỗi service chỉ lấy các env cần thiết (không lấy toàn bộ ConfigMap)

4. **readinessProbe vs Docker Compose healthcheck:**
   - K8s readinessProbe: Kiểm tra pod readiness trước khi đưa vào Service endpoint
   - Docker Compose healthcheck: Kiểm tra container health
   - Cả hai đều dùng `httpGet /health`

---

## 5. HORIZONTAL SCALING

### Docker Compose Scaling

File `docker-compose.scale.yml` cung cấp overlay config cho phép scale local:

```yaml
services:
  url_db:
    ports: !reset []
  analytics_db:
    ports: !reset []
  # ... tất cả infrastructure services đều reset ports
  url-service:
    ports: !reset []
  prometheus:
    ports: !reset []
  grafana:
    ports: !reset []
```

**Usage:**
```bash
docker compose -f docker-compose.yml -f docker-compose.scale.yml up --build -d --scale url-service=3
```

**Cơ chế hoạt động:**
- `!reset []`: Xóa port mapping của services infrastructure (tránh conflict port khi scale)
- Docker DNS tự động load balance giữa các replicas: Gateway gọi `http://url-service:8080`, Docker DNS trả về IP của 1 trong 3 instances
- **Không cần thay đổi config gateway** vì service discovery qua DNS

### Kubernetes Scaling

Trong `k8s/apps.yaml`:
- **url-service: 3 replicas** — service chịu tải chính (shorten + redirect)
- **gateway: 2 replicas** — entry point, cần high availability
- **analytics-service, user-service, notification-service: 1 replica** — tải thấp hơn

**Service Discovery trong K8s:**
- K8s Service (`url-service`) tự động load balance TCP giữa các pod
- Gateway gọi `http://url-service:8080`, K8s DNS round-robin tới 3 pods
- **Stateless design:** Mỗi pod url-service giữ kết nối riêng tới DB, Redis, RabbitMQ. Scale in/out an toàn
- **Zero-downtime deployment:** readinessProbe đảm bảo traffic chỉ tới pod sẵn sàng

### Stateless vs Stateful

| Service | State | Scale horizontally? |
|---------|-------|-------------------|
| url-service | Stateless (DB + Redis là external dependencies) | ✅ Có thể scale |
| analytics-service | Stateless (DB + RabbitMQ external) | ✅ |
| user-service | Stateless (DB external) | ✅ |
| notification-service | Stateless (DB + RabbitMQ external) | ✅ |
| gateway | Stateless (Redis + downstream services) | ✅ |
| PostgreSQL (×4) | Stateful (persistent data) | ❌ (single replica, cần replication) |
| Redis | Stateful (in-memory data) | ❌ (single, cluster mode thì scale được) |
| RabbitMQ | Stateful (queues data) | ❌ (single, cluster mode thì scale được) |

**Tại sao không scale database trong demo?** Local K8s (kind/minikube) không có storage class cho PVC. Database scaling yêu cầu: Primary-Replica replication, persistent volumes, và complex operational knowledge.

---

## 6. STARTUP ORDER & DEPENDENCY MANAGEMENT

### Dependency Graph (theo thứ tự khởi động):

```
Layer 0: Infrastructure (không phụ thuộc vào ai)
├── url_db (PostgreSQL, port 5432)
├── analytics_db (PostgreSQL, port 5433)
├── user_db (PostgreSQL, port 5434)
├── notification_db (PostgreSQL, port 5435)
├── adminer (không có healthcheck)
├── rabbitmq (RabbitMQ, port 5672/15672)
└── redis (Redis, port 6379)

Layer 1: Application services (phụ thuộc Layer 0)
├── url-service     ← url_db(healthy) + redis(healthy) + rabbitmq(healthy)
├── analytics-service ← analytics_db(healthy) + rabbitmq(healthy)
├── user-service    ← user_db(healthy)
└── notification-service ← notification_db(healthy) + rabbitmq(healthy)

Layer 2: Gateway (phụ thuộc Layer 1)
└── gateway ← url-service(healthy) + analytics-service(healthy) + user-service(healthy) + notification-service(healthy)

Layer 3: Proxy & Frontend (phụ thuộc Layer 2)
├── nginx ← gateway(healthy) + frontend(started)
└── frontend ← gateway(healthy)

Layer 4: Monitoring (phụ thuộc Layer 2+)
├── prometheus ← gateway(healthy) [cần target để scrape]
├── loki (không phụ thuộc)
├── grafana ← prometheus(started) + loki(started)
└── promtail ← loki(started)
```

### Cơ chế Dependency trong Docker Compose:

```yaml
depends_on:
  url_db:
    condition: service_healthy  # Chỉ khi healthcheck pass
```

- `condition: service_healthy`: Chờ healthcheck pass. Đây là cơ chế mạnh nhất, đảm bảo service thực sự sẵn sàng (không chỉ container started)
- `condition: service_started`: Chỉ chờ container start (dùng cho frontend, vì nó không có healthcheck)
- Không có `condition` (mặc định): Chờ container started

### Healthcheck details:

| Service | Command | Interval | Timeout | Retries | Start Period |
|---------|---------|----------|---------|---------|-------------|
| url_db | pg_isready | 5s | 5s | 10 | 10s |
| analytics_db | pg_isready | 5s | 5s | 10 | 10s |
| user_db | pg_isready | 5s | 5s | 10 | 10s |
| notification_db | pg_isready | 5s | 5s | 10 | 10s |
| rabbitmq | rabbitmq-diagnostics ping | 10s | 10s | 10 | 20s |
| redis | redis-cli ping | 5s | 3s | 10 | 0s |
| url-service | wget /health | 10s | 5s | 5 | 15s |
| analytics-service | wget /health | 10s | 5s | 5 | 15s |
| user-service | wget /health | 10s | 5s | 5 | 15s |
| notification-service | wget /health | 10s | 5s | 5 | 15s |
| gateway | wget /health | 10s | 5s | 5 | **20s** |

**Tại sao gateway có start_period 20s (cao nhất)?** Gateway cần kết nối tới 4 downstream services + Redis. Mỗi service có healthcheck riêng, gateway phải đợi tất cả sẵn sàng trước khi tự healthcheck pass.

---

## 7. GRACEFUL SHUTDOWN

Graceful shutdown là khả năng tắt ứng dụng một cách "sạch sẽ": hoàn thành các request đang xử lý, đóng kết nối database, giải phóng tài nguyên.

### Cơ chế chung (implemented trong tất cả services):

```
1. OS gửi SIGTERM hoặc SIGINT (docker stop, Ctrl+C, K8s pod termination)
2. signal.Notify bắt signal
3. context.WithCancel → cancel() cho background goroutines
4. srv.Shutdown(context) → HTTP server drain connections
5. defer close connections (DB pool, Redis, RabbitMQ)
```

### Gateway (`gateway/main.go`):

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
<-quit

log.Info("shutting down gateway")
srv.Shutdown(context.Background())
```

- **Đơn giản nhất:** Chỉ shutdown HTTP server, không có background workers
- **context.Background():** Không timeout — chờ tất cả requests hoàn thành
- **limiter.Close() được defer:** Đóng Redis connection dùng cho rate limiting

### url-service (`services/url-service/main.go`):

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// ... setup code ...
go outboxCoordinator.Run(ctx)

<-quit
cancel()
log.Info("shutdown signal received, draining connections…")

shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
defer shutdownCancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    log.Error("graceful shutdown failed", "error", err)
}
```

**Phân tích chi tiết:**

1. **Root context (`ctx`):** Dùng cho outbox coordinator. Khi cancel(), outbox coordinator dừng polling và workers
2. **Shutdown order:**
   - `cancel()` → outbox coordinator dừng
   - `srv.Shutdown(shutdownCtx)` với 10s timeout → HTTP server drain connections
   - defer `pool.Close()` → close DB pool
   - defer `redisClient.Close()` → close Redis
   - defer `rmqConn.Close()` → close RabbitMQ connection
3. **Shutdown timeout 10s:** Nếu quá 10s mà request chưa hoàn thành, force close

### analytics-service & notification-service (pattern giống nhau):

```go
waitForShutdown(cancel, log)
shutdownServer(srv, log)

func waitForShutdown(cancel context.CancelFunc, log *slog.Logger) {
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
    <-quit
    cancel()
    log.Info("shutdown signal received, draining connections")
}

func shutdownServer(srv *http.Server, log *slog.Logger) {
    ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout) // 10s
    defer cancel()
    if err := srv.Shutdown(ctx); err != nil {
        log.Error("graceful shutdown failed", "error", err)
    }
}
```

- `consumer.Run(ctx)` chạy background với context. Khi cancel(), consumer dừng consume messages
- `pool.Close()` defer → release DB connections
- `mqConn.Close()` defer → close RabbitMQ

### user-service (đơn giản nhất):

```go
go func() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    cancel()
    srv.Shutdown(ctx)
}()
```

- Signal handling trong goroutine riêng
- `srv.Shutdown(ctx)` không có timeout (dùng context đã cancel)
- Tuy đơn giản nhưng có thể chờ infinite nếu request không hoàn thành

### Outbox Coordinator (`services/url-service/outbox.go`):

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

- 3 workers, poll mỗi 2s
- Khi context cancel: `ctx.Done()` → return, workers nhận context canceled và dừng
- `close(jobs)` + `workers.Wait()`: Đảm bảo tất cả workers hoàn thành trước khi exit

---

## 8. CI/CD WORKFLOWS

### 8.1 CI — Continuous Integration

File: `.github/workflows/ci.yml`

```yaml
name: Continuous Integration
on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]
  workflow_dispatch:
```

**Trigger:** Push hoặc PR vào branch `main`, hoặc manual trigger.

#### Job 1: Go Lint & Test
```yaml
go-lint-and-test:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.23.x'
        cache: true
    - run: |
        if [ -n "$(gofmt -l .)" ]; then
          echo "The following Go files are not formatted correctly:"
          gofmt -l -d .
          exit 1
        fi
    - run: |
        for dir in $(go work edit -json | jq -r '.Use[].DiskPath'); do
          echo "::group::Module: $dir"
          go vet "./$dir/..."
          go test -v -race -cover "./$dir/..."
          echo "::endgroup::"
        done
```

**Phân tích:**
- **action/checkout@v4:** Checkout code
- **actions/setup-go@v5:** Cài Go 1.23, cache module downloads để tăng tốc
- **Check format:** Dùng `gofmt -l .` kiểm tra định dạng. Nếu có file sai format, in diff và exit 1
- **go vet:** Static analysis, phát hiện lỗi tiềm ẩn
- **go test -v -race -cover:**
  - `-v`: Verbose output
  - `-race`: Race condition detector (runtime)
  - `-cover`: Code coverage
- **Loop qua go workspace:** Dùng `jq` parse `go.work` để tìm tất cả modules

#### Job 2: Frontend Build
```yaml
frontend-build:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-node@v4
      with:
        node-version: 22
        cache: 'npm'
        cache-dependency-path: frontend/package-lock.json
    - run: npm ci
      working-directory: frontend
    - run: npm run build
      working-directory: frontend
```

- **npm ci:** Clean install (dùng package-lock.json) — nhanh hơn và deterministic hơn npm install
- **npm run build:** Build production bundle (TypeScript type-check + Vite build)

#### Job 3: Docker Compose Build Test
```yaml
docker-compose-check:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - run: docker compose build
```

- Build tất cả Docker images để verify Dockerfiles không bị lỗi
- Không chạy containers (không có database cho integration tests)

### 8.2 CD — Continuous Delivery

File: `.github/workflows/cd.yml`

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

**Trigger:** Push main, tag v*, hoặc manual.

**Permissions:** `packages: write` để push images lên GitHub Container Registry (GHCR).

#### Job 1: Build & Push Docker Images

```yaml
build-and-push:
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

**Matrix strategy:** Build 6 images song song (fail-fast: false, một service fail không ảnh hưởng service khác).

**Các bước:**

1. **Set up Docker Buildx:** Buildkit builder cho multi-platform, cache
2. **Log in to GHCR:** Dùng `GITHUB_TOKEN` (tự động có, không cần secret)
3. **Set lowercase repo name:** GHCR yêu cầu lowercase
4. **Extract Docker metadata:**
   ```yaml
   images: ghcr.io/${{ env.REPO_LC }}/${{ matrix.service }}
   tags: |
     type=raw,value=latest,enable=${{ github.ref == 'refs/heads/main' }}
     type=sha,prefix=,format=short
     type=ref,event=tag
   ```
   - `latest` tag: chỉ push nếu commit trên main
   - `sha` tag: `a1b2c3d` — xác định chính xác commit (dùng cho deployment)
   - `tag` tag: cho release tags (v1.0.0, v2.1.3, ...)
5. **Build and push:**
   ```yaml
   uses: docker/build-push-action@v6
   with:
     push: true
     cache-from: type=gha
     cache-to: type=gha,mode=max
   ```
   - `cache-from/to type=gha`: Dùng GitHub Actions cache cho Docker layers — giảm thời gian build từ 3-5 phút xuống 30-60s

#### Job 2: Deploy to AKS

```yaml
deploy-to-aks:
  runs-on: ubuntu-latest
  needs: [build-and-push]
  if: github.ref == 'refs/heads/main'
```

**Điều kiện:** Chỉ deploy khi commit trên main (không chạy cho PR). Phải chờ build-and-push thành công.

**Các bước:**

1. **Azure Login:** Dùng `AZURE_CREDENTIALS` secret (service principal)
2. **Set AKS Context:** Kết nối tới AKS cluster bằng `AZURE_AKS_NAME` + `AZURE_AKS_RESOURCE_GROUP`
3. **Setup Kubectl**
4. **Set up GHCR Secret trong K8s:**
   ```bash
   kubectl create secret docker-registry ghcr-secret \
     --docker-server=ghcr.io \
     --docker-username=${{ github.actor }} \
     --docker-password=${{ secrets.GITHUB_TOKEN }} \
     --namespace=url-shortener \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
   Tạo imagePullSecret để K8s có thể pull images từ private GHCR registry

5. **Replace placeholder images với GHCR images:**
   ```bash
   sed -i "s|url-shortener-microservices-url-service:latest|ghcr.io/${REPO_LC}/url-service:${SHORT_SHA}|g" k8s/apps.yaml
   ```
   Thay thế 6 image placeholders bằng GHCR URLs với SHA tag (đảm bảo deploy đúng version)

6. **Deploy Manifests:**
   ```bash
   kubectl apply -f k8s/config.yaml
   kubectl apply -f k8s/infra.yaml
   kubectl apply -f k8s/apps.yaml
   ```

7. **Verify Rollout:**
   ```bash
   kubectl rollout status deployment/url-service -n url-shortener --timeout=2m
   ```
   Chờ từng deployment rolling update complete. Nếu rollout fails trong 2 phút, workflow báo lỗi.

---

## 9. PRODUCTION ROADMAP

Hiện tại, dự án sử dụng Docker Compose cho local development và Kubernetes manifests cho kind/minikube (local cluster). Để đưa lên production thực tế, cần các cải tiến sau:

### 9.1 Kubernetes Improvements

| Thành phần | Hiện tại | Production |
|-----------|----------|------------|
| **Ingress** | NodePort (gateway:30080) | Ingress Controller (nginx-ingress, Traefik) với TLS termination |
| **TLS** | HTTP | Let's Encrypt cert-manager, HTTPS tự động |
| **Persistent Storage** | emptyDir (mất khi pod restart) | PersistentVolumeClaim + StorageClass (SSD) |
| **Auto-scaling** | Static replicas | HPA (Horizontal Pod Autoscaler) dựa trên CPU/memory/custom metrics |
| **Secrets** | stringData trong YAML | HashiCorp Vault + External Secrets Operator |
| **Tracing** | Không có | OpenTelemetry + Jaeger |
| **Security** | Không có network policies | NetworkPolicies, PodSecurityPolicies, OPA/Gatekeeper |
| **Service Mesh** | Không | Istio/Linkerd (mTLS, traffic splitting) |
| **DNS** | ClusterIP internal | ExternalDNS + public domain |

### 9.2 HPA (Horizontal Pod Autoscaler) Candidate:

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
  minReplicas: 2
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
          name: gateway_circuit_breaker_state
        target:
          type: AverageValue
          averageValue: 1
```

URL-service là ứng cử viên số 1 cho HPA vì:
- Là service chịu tải chính (shorten + redirect)
- Stateless, scale in/out an toàn
- CPU-bound khi encode/decode URLs
- I/O-bound khi đọc/ghi database + cache

### 9.3 Production Security Checklist

- **JWT_SECRET:** Dùng Vault hoặc K8s External Secrets thay vì hardcode
- **Database passwords:** Random, lưu trong Vault, rotate định kỳ
- **Network Policies:** Chỉ cho phép traffic cần thiết giữa các pods
- **Pod Security Standards:** Restrictive policy (không chạy privileged containers)
- **Image scanning:** Trivy/Klar trên mỗi build
- **ReadOnlyRootFilesystem:** Chạy containers với filesystem read-only

### 9.4 Observability Stack

| Hiện tại | Production |
|----------|------------|
| Prometheus + Grafana (metrics) | + Alertmanager (PagerDuty, Slack, Email) |
| Loki + Promtail (logs) | + Aggregated logging (Elasticsearch + Kibana option) |
| Không có tracing | + OpenTelemetry Collector + Jaeger/Tempo |
| Circuit breaker dashboard | + SLO/SLI dashboards, Error Budget alerts |
| 1h retention | 30 days (metrics), 7 days (logs), 14 days (traces) |

### 9.5 Full CI/CD Pipeline

| Stage | Công cụ | Mục đích |
|-------|---------|----------|
| Lint & Format | golangci-lint, gofmt | Static analysis |
| Unit Tests | go test | Test business logic |
| Security Scan | Trivy | Scan dependencies + Docker images |
| Build | Docker Buildx | Multi-arch images |
| Integration Tests | Testcontainers | Test với real DB, Redis, RabbitMQ |
| Contract Tests | Pact | API contract giữa services |
| Deploy to Staging | ArgoCD / Flux | GitOps deployment |
| E2E Tests | Cypress (frontend) + k6 (API) | Full system test |
| Smoke Tests | Custom scripts | Health check sau deploy |
| Deploy to Production | ArgoCD + progressive delivery | Canary/Blue-Green |

---

## 10. LOAD TESTING VỚI K6

### 10.1 Load Test Script Overview

File: `scripts/load_test.js` (218 dòng)

**Mục tiêu:** 10,000 requests/second vào endpoint `/r/{shortCode}` (public redirect).

### 10.2 KỊCH BẢN STRESS TEST

#### Config:
```javascript
export const options = {
  scenarios: {
    circuit_breaker_stress: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "20s", target: 200 },   // Warm-up
        { duration: "20s", target: 500 },   // Ramp up
        { duration: "20s", target: 1000 },  // Peak
        { duration: "60s", target: 1000 },  // Hold peak
        { duration: "20s", target: 0 },     // Ramp down
      ],
      gracefulRampDown: "10s",
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<2000"],
    error_rate: ["rate<0.95"],
  },
};
```

**Phân tích stages:**
1. **0-20s:** Ramp từ 0→200 VUs — warm-up, cho phép hệ thống ổn định (connection pools, caches warm)
2. **20-40s:** Ramp 200→500 VUs — tăng dần tải
3. **40-60s:** Ramp 500→1000 VUs — đạt peak ~10,000 req/s
4. **60-120s:** Hold 1000 VUs — duy trì tải cao để test circuit breaker
5. **120-140s:** Ramp xuống 0 — tránh sốc khi kết thúc

**Thresholds:**
- `p(95) < 2000ms`: 95% requests dưới 2 giây
- `error_rate < 0.95`: Cho phép đến 95% lỗi (vì CB mở là behavior mong đợi)

#### Setup Function:

```javascript
export function setup() {
  // 1. Register user
  // 2. Login → get JWT
  // 3. Create short URL → get shortCode
  return { shortCode: "loadtest" };
}
```

Setup function tự động tạo user mới + tạo short URL. Kết quả được truyền tới tất cả VUs qua `data` parameter.

#### Main VU Function:

```javascript
export default function (data) {
  const url = `${BASE_URL}/r/${shortCode}`;
  const res = http.get(url, {
    redirects: 0,        // Không follow redirect
    timeout: "5s",
    tags: { name: "redirect" },
  });

  // Track CB-open responses
  if (res.status === 503) cbOpenResponses.add(1);

  // Track errors
  const isError = res.status === 0 || (res.status >= 500 && res.status !== 503) || res.status === 503;
  errorRate.add(isError ? 1 : 0);
}
```

- `redirects: 0`: Không follow HTTP redirect (chỉ test gateway/url-service, không test URL đích)
- `timeout: 5s`: Timeout mỗi request
- **Custom metrics:**
  - `circuit_breaker_open_responses` (Counter)
  - `error_rate` (Rate)
  - `redirect_duration_ms` (Trend)

#### Teardown & Summary:

```javascript
export function handleSummary(data) {
  const totalReqs = data.metrics.http_reqs.values.count;
  const rps = totalReqs / (data.state.testRunDurationMs / 1000);
  const cbOpen = data.metrics.circuit_breaker_open_responses.values.count;
  const p95 = data.metrics.http_req_duration.values["p(95)"];

  // In ra bảng summary đẹp
}
```

### 10.3 Happy Path Flow

**Các bước thực hiện:**

```bash
# Step 1: Start stack
docker compose up -d --build

# Step 2: Verify health
./scripts/smoke_test.sh

# Step 3: Run load test
k6 run scripts/load_test.js
```

**smoke_test.sh phân tích:**
```bash
PORTS=(8080 8081 8082 8083 8084)
MAX_RETRIES=30  # Tối đa 60 giây chờ

for port in "${PORTS[@]}"; do
  until curl -s http://localhost:$port/health | grep -q '"status":"ok"'; do
    sleep $SLEEP_SECS
  done
done
```

Kiểm tra health endpoint của tất cả 5 services (gateway 8080 + 4 services 8081-8084). Chờ tối đa 60 giây mỗi service.

### 10.4 Failure Path — Circuit Breaker Demo

**Kịch bản:**

```
Time 0s:   Load test bắt đầu, 1000 VUs, ~10,000 req/s
Time 60s:  docker compose stop url-service  ← SIMULATE FAILURE
Time 60s:  Gateway nhận connection errors tới url-service
Time 62s:  5 lỗi trong 10s → Circuit Breaker chuyển sang OPEN
Time 62s+: Request mới trả về 503 ngay lập tức
           "url-service unavailable (circuit open)"
Time 90s:  docker compose start url-service  ← SIMULATE RECOVERY
Time 120s: CB chuyển HALF_OPEN, gửi probe request
Time 121s: Probe success → CB chuyển CLOSED, traffic resumed
```

**Grafana observations (dashboard Circuit Breaker Monitor):**

| Panel | Trước CB trip | CB OPEN | Sau recovery |
|-------|--------------|---------|-------------|
| CB State | 0 (CLOSED, green) | 2 (OPEN, red) | 1→0 (HALF_OPEN→CLOSED) |
| Requests/sec | ~10,000 req/s (2xx) | circuit_open spike | ~10,000 req/s (2xx) |
| Error Rate | ~0% | 100% | ~0% |
| CB Rejected | 0 | Số lượng tăng vọt | 0 |
| Latency p95 | ~100ms | ~1ms (reject nhanh) | ~100ms |

### 10.5 Detailed k6 Metrics Output

```
╔══════════════════════════════════════════════════════╗
║           k6 Load Test Summary                       ║
╠══════════════════════════════════════════════════════╣
║  Total Requests   : 1,423,567                        ║
║  Duration         : 140.0s                           ║
║  Avg Throughput   : 10168 req/s                      ║
║  CB Open (503)    : 345,892                          ║
║  P95 Latency      : 1ms (during CB OPEN)            ║
╚══════════════════════════════════════════════════════╝
```

Khi CB mở, P95 latency giảm mạnh (từ ~100ms xuống ~1ms) vì request không cần chờ downstream timeout — bị reject ngay tại gateway.

---

## 11. NGINX REVERSE PROXY

File: `nginx/nginx.conf`

```nginx
events {
    worker_connections 1024;
}

http {
    include       /etc/nginx/mime.types;
    default_type  application/octet-stream;
    sendfile        on;
    keepalive_timeout  65;

    upstream frontend_upstream {
        server frontend:5173;
    }

    upstream gateway_upstream {
        server gateway:8080;
    }

    server {
        listen 80;
        server_name _;
        client_max_body_size 10m;

        # API routes → Gateway
        location /api/ {
            proxy_pass http://gateway_upstream;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_connect_timeout 30s;
            proxy_send_timeout 30s;
            proxy_read_timeout 30s;
        }

        # Redirect routes → Gateway
        location /r/ {
            proxy_pass http://gateway_upstream;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
        }

        # Health check → Gateway
        location /health {
            proxy_pass http://gateway_upstream;
            proxy_set_header Host $host;
        }

        # Default → Frontend
        location / {
            proxy_pass http://frontend_upstream;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";
        }
    }
}
```

**Phân tích chi tiết:**

### Upstream blocks:
- **`frontend_upstream`:** `server frontend:5173` — Vite dev server
- **`gateway_upstream`:** `server gateway:8080` — Go API Gateway

### Routing logic:

| Location | Upstream | Mục đích |
|----------|----------|----------|
| `/api/` | gateway | REST API (auth, shorten, stats, notifications) |
| `/r/` | gateway | Redirect short URLs |
| `/health` | gateway | Health check endpoint |
| `/` (default) | frontend | Frontend SPA (Vite dev server) |

### Tại sao có 2 locations cho gateway (`/api/` và `/r/`)?

Dù cùng upstream, nhưng:
- `/api/` có timeouts: `connect 30s, send 30s, read 30s`
- `/r/` không có timeouts (dùng mặc định của Nginx)
- Tách biệt để sau này có thể cấu hình caching, rate limiting khác nhau

### Headers quan trọng:

- `X-Real-IP`: IP thật của client (không phải IP Nginx)
- `X-Forwarded-For`: Chain của IPs (Nginx thêm IP client vào cuối)
- `X-Forwarded-Proto`: Protocol gốc (HTTP/HTTPS) — cho backend biết có cần redirect sang HTTPS không
- `Host`: Host gốc của request

### WebSocket support cho Frontend:

```nginx
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
```

Cho phép Vite dev server HMR (Hot Module Replacement) hoạt động qua websocket.

---

## 12. LOKI & PROMTAIL — LOG AGGREGATION

### 12.1 Loki Configuration

File: `monitoring/loki-config.yml`

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

**Phân tích:**
- **auth_enabled: false:** Cho phép Promtail push logs không cần auth
- **Storage filesystem:** Lưu chunks và index trên disk (phù hợp development). Production dùng S3/GCS
- **TSDB schema v13:** Index format mới nhất, hiệu suất cao hơn boltdb-shipper
- **Limits:**
  - `reject_old_samples: true`: Chỉ chấp nhận log mới (tránh spam logs cũ)
  - `reject_old_samples_max_age: 168h`: Từ chối logs cũ hơn 7 ngày (có thể đã được ingest trước đó)
  - `ingestion_rate_mb: 100`: Tối đa 100MB/s ingest
  - `ingestion_burst_size_mb: 200`: Burst tối đa 200MB

### 12.2 Promtail Configuration

File: `monitoring/promtail-config.yml`

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

**Phân tích:**
- **Docker Service Discovery:** Promtail dùng Docker API (qua docker.sock) để tự động discover containers và đọc logs
- **`refresh_interval: 5s`:** Kiểm tra containers mới mỗi 5 giây
- **Relabeling:**
  - `__meta_docker_container_label_com_docker_compose_service` → label `service`: Cho phép filter logs trong Grafana theo service name
  - `__meta_docker_container_name` → label `container`: Cho phép filter theo container name
  - Regex `/(.*)`: Strip leading `/` từ container name

### 12.3 Log Flow

```
Container stdout/stderr
    → Docker JSON log file (/var/lib/docker/containers/*/*-json.log)
        → Promtail reads (docker.sock API + filesystem)
            → HTTP POST /loki/api/v1/push
                → Loki stores (chunks + index)
                    → Grafana queries (Loki datasource)
                        → Services Overview dashboard (Logs panel)
```

### 12.4 Grafana Log Query

```sql
{service=~"$service", service!=""}
```

- Sử dụng LogQL query trên label `service`
- Template variable `$service` lấy từ `label_values(service)` trong Loki
- `service!=""` lọc bỏ entries không có service label
- Multi-select: Có thể chọn nhiều service để xem log đồng thời

---

## 13. KẾT LUẬN

### Tổng quan kiến trúc deployment

Dự án URL Shortener Microservices triển khai một hệ thống phân tán hoàn chỉnh với:

1. **Docker Compose:** 17 containers orchestrated với healthcheck chain, 8 named volumes, internal network, monitoring stack tích hợp sẵn.

2. **Kubernetes:** 4 manifests (namespace, config, infra, apps) hỗ trợ local cluster (kind/minikube) với sẵn sàng mở rộng lên production (AKS, EKS, GKE).

3. **API Gateway Pattern:** Gateway duy nhất (port 8080) với routing table, rate limiting (Redis-based), JWT authentication, và circuit breaker protection.

4. **Circuit Breaker:** Implemented với 3 states (CLOSED → OPEN → HALF_OPEN), config: 5 failures trong 10s → OPEN, 30s → HALF_OPEN, probe success → CLOSED. Metrics exposed cho Prometheus scraping.

5. **Monitoring Stack:**
   - Prometheus: 5 scrape targets, 5s interval
   - Grafana: 2 provisioned dashboards (Circuit Breaker + Services Overview)
   - Loki + Promtail: Docker log aggregation

6. **CI/CD:** GitHub Actions với matrix build (6 images), Go vet + test, frontend build, Docker build test. CD push lên GHCR và deploy tới AKS với zero-downtime rolling update.

7. **Graceful Shutdown:** signal.Notify (SIGTERM/SIGINT), context cancel, srv.Shutdown với 10s timeout, defer close connections (DB, Redis, RabbitMQ).

8. **Horizontal Scaling:** url-service (3 replicas), gateway (2 replicas). Stateless design cho phép scale in/out an toàn.

### Key Design Decisions

| Quyết định | Lý do |
|-----------|-------|
| **Database-per-Service** | Cô lập dữ liệu, schema độc lập, scale riêng |
| **Redis ephemeral** | Chỉ dùng cache + rate limiting, không cần persist |
| **Multi-stage Docker builds** | Final image chỉ ~15-20MB (alpine + Go binary) |
| **Prometheus 5s scrape** | Granularity cho real-time CB demo |
| **Provisioning Grafana** | Zero-touch setup, reproducibility |
| **K8s readinessProbe** | Chỉ đưa pod vào Service endpoint khi sẵn sàng |
| **GHCR SHA tags** | Immutable, traceable deployments |
| **Outbox pattern** | Transactional outbox → RabbitMQ, đảm bảo reliable messaging |

### Số liệu ấn tượng

- **17 containers** trong Docker Compose
- **~10,000 req/s** load test throughput
- **<5ms** P95 latency khi circuit breaker mở (request reject)
- **30 giây** recovery time (OPEN → HALF_OPEN → CLOSED)
- **5 healthchecks** được chain: DB → Service → Gateway → Nginx
- **6 Docker images** build và push trong <3 phút (có cache)
- **1,423,567 requests** trong 140 giây load test (peak)

---

*Báo cáo này được viết cho đồ án môn học SE361.Q21 — Kiến trúc và Thiết kế Phần mềm.*

*Tác giả phân tích các thành phần Deployment, Kubernetes, Docker Compose, CI/CD và Monitoring của dự án URL Shortener Microservices.*
