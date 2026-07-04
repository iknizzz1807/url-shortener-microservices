module github.com/ikniz/url-shortener/services/url-service

go 1.23.0

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/ikniz/url-shortener/shared/auth v0.0.0-00010101000000-000000000000
	github.com/ikniz/url-shortener/shared/events v0.0.0
	github.com/ikniz/url-shortener/shared/logger v0.0.0
	github.com/jackc/pgx/v5 v5.7.2
	github.com/prometheus/client_golang v1.23.2
	github.com/rabbitmq/amqp091-go v1.10.0
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/sys v0.35.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/google/uuid v1.6.0
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/text v0.28.0 // indirect
)

replace (
	github.com/ikniz/url-shortener/shared/auth => ../../shared/auth
	github.com/ikniz/url-shortener/shared/events => ../../shared/events
	github.com/ikniz/url-shortener/shared/logger => ../../shared/logger
)
