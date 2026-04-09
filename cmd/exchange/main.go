package main

import (
	"database/sql"
	"fmt"
	"net"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	internalExchange "github.com/RAF-SI-2025/Banka-3-Backend/internal/exchange"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func connect_to_db_gorm() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	gorm_db, gorm_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if gorm_err != nil {
		logger.L().Error("gorm open failed", "err", gorm_err, "dsn", dsn)
		os.Exit(1)
	}
	return gorm_db
}

func connectToDB() *sql.DB {
	connStr := os.Getenv("DATABASE_URL")
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		logger.L().Error("sql open failed", "err", err)
		os.Exit(1)
	}
	return db
}

func main() {
	logger.Init("exchange")

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
	logger.L().Info("connected to database")
	defer func() { _ = db.Close() }()

	exchangeService := internalExchange.NewServer(gorm_db)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(logger.UnaryServerInterceptor()),
		grpc.StreamInterceptor(logger.StreamServerInterceptor()),
	)
	exchange.RegisterExchangeServiceServer(srv, exchangeService)
	reflection.Register(srv)

	logger.L().Info("exchange service listening", "port", port)
	if err := srv.Serve(lis); err != nil {
		logger.L().Error("failed to serve", "err", err)
		os.Exit(1)
	}
}
