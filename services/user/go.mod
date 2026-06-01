module github.com/RAF-SI-2025/Banka-3-Backend/services/user

go 1.25.0

require (
	github.com/RAF-SI-2025/Banka-3-Backend/gen v0.0.0
	github.com/RAF-SI-2025/Banka-3-Backend/pkg v0.0.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/redis/go-redis/v9 v9.20.0
	golang.org/x/sync v0.20.0
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260415201107-50325440f8f2.1 // indirect
	buf.build/go/protovalidate v1.2.0 // indirect
	cel.dev/expr v0.25.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/exp v0.0.0-20250813145105-42675adae3e6 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
)

replace (
	github.com/RAF-SI-2025/Banka-3-Backend/gen => ../../gen
	github.com/RAF-SI-2025/Banka-3-Backend/pkg => ../../pkg
)
