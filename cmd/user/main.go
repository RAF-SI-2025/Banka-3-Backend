package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	"github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	internalUser "github.com/RAF-SI-2025/Banka-3-Backend/internal/user"
)

func connect_to_db_gorm() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	gorm_db, gorm_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if gorm_err != nil {
		log.Fatal("pgx", dsn)
	}
	return gorm_db
}

func connectToDB() *sql.DB {
	connStr := os.Getenv("DATABASE_URL")
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("connected to database...")
	return db
}

func connect() (*internalUser.Connections, error) {
	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if notificationAddr == "" {
		notificationAddr = "notification:50051"
	}
	notificationConn, err := grpc.NewClient(notificationAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	db := connectToDB()
	gorm := connect_to_db_gorm()
	return &internalUser.Connections{
		NotificationClient: notification.NewNotificationServiceClient(notificationConn),
		Sql_db:             db,
		Gorm:               gorm,
	}, nil
}

func main() {
	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	connections, err := connect()
	if err != nil {
		log.Fatalf("couldn't connect to services")
	}

	accessJwtSecret, accessSecretSet := os.LookupEnv("ACCESS_JWT_SECRET")
	refreshJwtSecret, refreshSecretSet := os.LookupEnv("REFRESH_JWT_SECRET")
	if !accessSecretSet || !refreshSecretSet {
		log.Fatalf("JWT secrets not set, exiting...")
	}

	userService := internalUser.NewServer(accessJwtSecret, refreshJwtSecret, connections)
	totpService := internalUser.NewTotpServer(connections)

	srv := grpc.NewServer()
	user.RegisterUserServiceServer(srv, userService)
	user.RegisterTOTPServiceServer(srv, totpService)
	reflection.Register(srv)

	log.Printf("user service listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
