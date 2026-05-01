package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/server"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
)

func connect_to_db_gorm() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	gorm_db, gorm_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if gorm_err != nil {
		logger.L().Error("gorm open failed", "err", gorm_err)
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

	logger.L().Info("connected to database")
	return db
}

func connect() (*server.Connections, error) {
	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if notificationAddr == "" {
		notificationAddr = "notification:50051"
	}
	notificationConn, err := grpc.NewClient(
		notificationAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(logger.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(logger.StreamClientInterceptor()),
	)
	if err != nil {
		return nil, err
	}

	db := connectToDB()
	dbGorm := connect_to_db_gorm()
	return &server.Connections{
		NotificationClient: notification.NewNotificationServiceClient(notificationConn),
		Sql_db:             db,
		Gorm:               dbGorm,
	}, nil
}

func main() {
	logger.Init("user")

	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		logger.L().Error("failed to listen", "port", port, "err", err)
		os.Exit(1)
	}

	connections, err := connect()
	if err != nil {
		logger.L().Error("couldn't connect to services", "err", err)
		os.Exit(1)
	}

	accessJwtSecret, accessSecretSet := os.LookupEnv("ACCESS_JWT_SECRET")
	refreshJwtSecret, refreshSecretSet := os.LookupEnv("REFRESH_JWT_SECRET")
	if !accessSecretSet || !refreshSecretSet {
		logger.L().Error("JWT secrets not set, exiting")
		os.Exit(1)
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.L().Error("failed to connect to redis", "addr", redisAddr, "err", err)
		os.Exit(1)
	}
	logger.L().Info("connected to redis", "addr", redisAddr)

	connections.Rdb = rdb

	userService := server.NewServer(accessJwtSecret, refreshJwtSecret, connections)
	totpService := server.NewTotpServer(connections)

	databaseURL := os.Getenv("DATABASE_URL")
	go server.StartPGListener(context.Background(), databaseURL, userService)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(logger.UnaryServerInterceptor()),
		grpc.StreamInterceptor(logger.StreamServerInterceptor()),
	)
	user.RegisterUserServiceServer(srv, userService)
	user.RegisterTOTPServiceServer(srv, totpService)
	reflection.Register(srv)

	logger.L().Info("user service listening", "port", port)
	if err := srv.Serve(lis); err != nil {
		logger.L().Error("failed to serve", "err", err)
		os.Exit(1)
	}
}
