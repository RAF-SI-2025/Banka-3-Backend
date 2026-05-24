package main

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/exchange"
	internalExchange "github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/exchange"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// dsnWithExecMode forces pgx's `default_query_exec_mode=exec` unless the
// caller has already pinned it. See bank/cmd/main.go for the rationale —
// the schema-then-connection ordering produces the same "cached plan must
// not change result type" pgx error here too when /listings is queried.
func dsnWithExecMode(dsn string) string {
	if strings.Contains(dsn, "default_query_exec_mode") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "default_query_exec_mode=exec"
}

func connect_to_db_gorm() *gorm.DB {
	dsn := dsnWithExecMode(os.Getenv("DATABASE_URL"))
	gorm_db, gorm_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if gorm_err != nil {
		logger.L().Error("gorm open failed", "err", gorm_err)
		os.Exit(1)
	}
	return gorm_db
}

func connect_read_db_gorm() *gorm.DB {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_READ_URL"))
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	gorm_db, gorm_err := gorm.Open(postgres.Open(dsnWithExecMode(dsn)), &gorm.Config{})
	if gorm_err != nil {
		logger.L().Error("read-replica gorm open failed", "err", gorm_err)
		os.Exit(1)
	}
	return gorm_db
}

func connectToDB() *sql.DB {
	connStr := dsnWithExecMode(os.Getenv("DATABASE_URL"))
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		logger.L().Error("sql open failed", "err", err)
		os.Exit(1)
	}
	return db
}

func main() {
	logger.Init("exchange")
	stopMetrics := observability.StartMetricsServer("exchange", os.Getenv("METRICS_PORT"))
	defer stopMetrics()

	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		logger.L().Error("failed to listen", "port", port, "err", err)
		os.Exit(1)
	}

	db := connectToDB()
	gorm_db := connect_to_db_gorm()
	read_gorm_db := connect_read_db_gorm()
	logger.L().Info("connected to database")
	defer func() { _ = db.Close() }()

	exchangeService := internalExchange.NewServer(gorm_db)
	exchangeService.ConfigureReadReplica(read_gorm_db)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			logger.UnaryServerInterceptor(),
			observability.UnaryServerInterceptor("exchange"),
		),
		grpc.ChainStreamInterceptor(
			logger.StreamServerInterceptor(),
			observability.StreamServerInterceptor("exchange"),
		),
	)
	exchange.RegisterExchangeServiceServer(srv, exchangeService)
	reflection.Register(srv)

	logger.L().Info("exchange service listening", "port", port)
	if err := srv.Serve(lis); err != nil {
		logger.L().Error("failed to serve", "err", err)
		os.Exit(1)
	}
}
