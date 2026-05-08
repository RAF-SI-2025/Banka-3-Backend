module github.com/RAF-SI-2025/Banka-3-Backend/services/user

go 1.25

require (
	github.com/RAF-SI-2025/Banka-3-Backend/gen v0.0.0
	github.com/RAF-SI-2025/Banka-3-Backend/pkg v0.0.0
	github.com/jackc/pgx/v5 v5.7.1
	golang.org/x/sync v0.10.0
	google.golang.org/grpc v1.68.0
	google.golang.org/protobuf v1.35.2
)

replace (
	github.com/RAF-SI-2025/Banka-3-Backend/gen => ../../gen
	github.com/RAF-SI-2025/Banka-3-Backend/pkg => ../../pkg
)
