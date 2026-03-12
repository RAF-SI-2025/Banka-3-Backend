package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"banka-raf/gen/user"
	"banka-raf/internal/user/models"
	"banka-raf/internal/user/repository"
	"banka-raf/internal/user/service"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	// 1. Config & Environment
	accessSecret := getRequiredEnv("ACCESS_JWT_SECRET")
	refreshSecret := getRequiredEnv("REFRESH_JWT_SECRET")
	dbURL := getRequiredEnv("DATABASE_URL")
	grpcPort := getEnv("GRPC_PORT", "50051")
	httpPort := getEnv("HTTP_PORT", "8080")

	// 2. Database Initialization
	db := setupDatabase(dbURL)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// 3. Service Layer Setup
	repo := repository.NewUserRepository(db)
	userSvc := service.NewUserService(repo, accessSecret, refreshSecret)

	// 4. Start gRPC Server
	grpcServer := grpc.NewServer()
	user.RegisterUserServiceServer(grpcServer, userSvc)
	reflection.Register(grpcServer)

	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Fatalf("Critical: Failed to listen on gRPC port: %v", err)
	}

	log.Printf("🚀 gRPC Server running on :%s", grpcPort)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC Server crashed: %v", err)
		}
	}()

	// 5. Start HTTP Gateway (REST)
	log.Printf("🌐 HTTP Gateway running on :%s", httpPort)
	go startGateway(grpcPort, httpPort)

	// 6. Graceful Shutdown
	handleShutdown(grpcServer)
}

// setupDatabase handles connection and migration
func setupDatabase(dsn string) *gorm.DB {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		PrepareStmt: true,
	})
	if err != nil {
		log.Fatalf("Critical: Failed to connect to DB: %v", err)
	}

	log.Println("🛠️ Running database migrations...")
	err = db.AutoMigrate(
		&models.Permission{},
		&models.Employee{},
		&models.Client{},
		&models.RefreshToken{},
	)
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	return db
}

// startGateway initializes the REST proxy
func startGateway(grpcPort, httpPort string) {
	ctx := context.Background()
	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	// Important: Use "localhost" if running locally, or "0.0.0.0" depending on environment
	err := user.RegisterUserServiceHandlerFromEndpoint(ctx, mux, "localhost:"+grpcPort, opts)
	if err != nil {
		log.Fatalf("Failed to register gateway: %v", err)
	}

	if err := http.ListenAndServe(":"+httpPort, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP Gateway crashed: %v", err)
	}
}

// handleShutdown blocks until a signal is received
func handleShutdown(server *grpc.Server) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down servers...")
	server.GracefulStop()
	log.Println("👋 Server exited")
}

// Helpers
func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getRequiredEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Critical: Environment variable %s is not set", key)
	}
	return v
}
