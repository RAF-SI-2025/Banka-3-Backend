module github.com/RAF-SI-2025/Banka-3-Backend/services/gateway

go 1.25.0

require (
	github.com/RAF-SI-2025/Banka-3-Backend/gen v0.0.0
	github.com/RAF-SI-2025/Banka-3-Backend/pkg v0.0.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0
	golang.org/x/sync v0.20.0
	google.golang.org/grpc v1.80.0
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260415201107-50325440f8f2.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/golang-jwt/jwt/v5 v5.2.1 // indirect
	github.com/redis/go-redis/v9 v9.7.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace (
	github.com/RAF-SI-2025/Banka-3-Backend/gen => ../../gen
	github.com/RAF-SI-2025/Banka-3-Backend/pkg => ../../pkg
)
