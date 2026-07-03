# URL Shortener Microservices

A URL shortener built with Go, using a microservices architecture.

## Services

- `url-service`: Handles URL creation and redirection.
- `user-service`: Manages users and authentication.
- `analytics-service`: Tracks click metrics and events.
- `notification-service`: Sends notifications for milestone events.
- `gateway`: The API gateway routing requests to underlying services.

## Development

Run `docker compose up --build` to start all services and databases locally.

To verify health, run `./scripts/smoke_test.sh`.

Run the full integration flow with `./scripts/e2e_test.sh` after the stack is healthy.

## Horizontal Scaling Demo

The application services are stateless. Gateway auth uses local JWT verification, rate limits are shared through Redis, and URL events are handed off through PostgreSQL outbox plus RabbitMQ.

To demonstrate Docker Compose service discovery and load balancing for `url-service`, run:

```bash
docker compose -f docker-compose.yml -f docker-compose.scale.yml up --build -d --scale url-service=3
docker compose ps url-service gateway
./scripts/e2e_test.sh
```

`docker-compose.scale.yml` removes the host port binding from `url-service` so multiple replicas can run. The gateway still routes to `http://url-service:8080`; Docker DNS resolves that service name to the available replicas on the internal network.

## Kubernetes Demo

Kubernetes manifests are available in `k8s/`. They deploy `url-service` with 3 replicas behind a `ClusterIP` service and expose the gateway through `NodePort 30080`.

See `k8s/README.md` for kind/minikube commands.

## Load Testing & Monitoring

For load testing with **k6** and monitoring Circuit Breaker states in **Grafana** / **Prometheus**, refer to `LOAD_TESTING.md`.

## Dev docs

Check `/dev-docs`
