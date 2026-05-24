package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/server"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
)

// dsnWithExecMode forces pgx's `default_query_exec_mode=exec` unless the
// caller has already pinned it. See bank/cmd/main.go for the rationale.
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

	logger.L().Info("connected to database")
	return db
}

func connectReadDB() *sql.DB {
	connStr := strings.TrimSpace(os.Getenv("DATABASE_READ_URL"))
	if connStr == "" {
		connStr = os.Getenv("DATABASE_URL")
	}
	db, err := sql.Open("pgx", dsnWithExecMode(connStr))
	if err != nil {
		logger.L().Error("read-replica sql open failed", "err", err)
		os.Exit(1)
	}
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
	readDB := connectReadDB()
	readGorm := connect_read_db_gorm()
	return &server.Connections{
		NotificationClient: notification.NewNotificationServiceClient(notificationConn),
		Sql_db:             db,
		ReadSql_db:         readDB,
		Gorm:               dbGorm,
		ReadGorm:           readGorm,
	}, nil
}

func main() {
	logger.Init("user")
	stopMetrics := observability.StartMetricsServer("user", os.Getenv("METRICS_PORT"))
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
		grpc.ChainUnaryInterceptor(
			logger.UnaryServerInterceptor(),
			observability.UnaryServerInterceptor("user"),
		),
		grpc.ChainStreamInterceptor(
			logger.StreamServerInterceptor(),
			observability.StreamServerInterceptor("user"),
		),
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
