# Kubernetes Demo

This folder contains a lightweight Kubernetes deployment for the URL shortener.
It is intended for local demo with `kind` or `minikube`, not production.

## What It Shows

- `Deployment` replicas for stateless services.
- `ClusterIP` service discovery/load balancing for internal traffic.
- `url-service` runs with `replicas: 3` behind the `url-service` service.
- `gateway` runs with `replicas: 2` and is exposed through `NodePort 30080`.
- Redis, RabbitMQ, and PostgreSQL run in-cluster for a self-contained demo.

## Build Images

Build the same local images used by Docker Compose:

```bash
docker compose -f docker-compose.yml -f docker-compose.scale.yml build
```

For `kind`, load the images into the cluster:

```bash
kind load docker-image url-shortener-microservices-url-service:latest
kind load docker-image url-shortener-microservices-user-service:latest
kind load docker-image url-shortener-microservices-analytics-service:latest
kind load docker-image url-shortener-microservices-notification-service:latest
kind load docker-image url-shortener-microservices-gateway:latest
```

For `minikube`, build with Minikube's Docker daemon instead:

```bash
eval $(minikube docker-env)
docker compose -f docker-compose.yml -f docker-compose.scale.yml build
```

## Apply

```bash
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/config.yaml
kubectl apply -f k8s/infra.yaml
kubectl apply -f k8s/apps.yaml
kubectl -n url-shortener rollout status deploy/url-service
kubectl -n url-shortener rollout status deploy/gateway
```

## Verify Service Discovery And Replicas

```bash
kubectl -n url-shortener get deploy,svc,pod
```

Expected highlights:

```text
deployment.apps/url-service   3/3
deployment.apps/gateway       2/2
service/url-service           ClusterIP
service/gateway               NodePort 30080
```

Run E2E through the gateway NodePort:

```bash
BASE_URL=http://localhost:30080 ./scripts/e2e_test.sh
```

If your local Kubernetes driver does not expose NodePorts on `localhost`, port-forward instead:

```bash
kubectl -n url-shortener port-forward svc/gateway 8080:8080
BASE_URL=http://localhost:8080 ./scripts/e2e_test.sh
```

## Cleanup

```bash
kubectl delete namespace url-shortener
```
