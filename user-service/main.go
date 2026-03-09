package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"user-service/models"
	"user-service/pb"
	"user-service/repository"
	"user-service/service"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	db := initDB()

	userRepo := repository.NewUserRepository(db)
	userSvc := service.NewUserService(userRepo)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterUserServiceServer(grpcServer, userSvc)

	log.Println("Starting gRPC server on :50051")
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve gRPC: %v", err)
		}
	}()

	runGateway()
}

func initDB() *gorm.DB {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		os.Getenv("DB_HOST"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"), os.Getenv("DB_PORT"))

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	db.AutoMigrate(&models.Employee{}, &models.Client{}, &models.Permission{})
	return db
}

func runGateway() {
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "0.0.0.0:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to dial gRPC server: %v", err)
	}

	mux := runtime.NewServeMux()
	if err := pb.RegisterUserServiceHandler(ctx, mux, conn); err != nil {
		log.Fatalf("Failed to register gateway: %v", err)
	}

	log.Println("Starting HTTP Gateway on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
