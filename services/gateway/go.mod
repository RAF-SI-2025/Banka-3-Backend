module github.com/RAF-SI-2025/Banka-3-Backend/services/gateway

go 1.25

require (
	github.com/RAF-SI-2025/Banka-3-Backend/gen v0.0.0
	github.com/RAF-SI-2025/Banka-3-Backend/pkg v0.0.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.24.0
	golang.org/x/sync v0.10.0
	google.golang.org/grpc v1.68.0
)

replace (
	github.com/RAF-SI-2025/Banka-3-Backend/gen => ../../gen
	github.com/RAF-SI-2025/Banka-3-Backend/pkg => ../../pkg
)
