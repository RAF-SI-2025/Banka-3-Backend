package main

import (
	"context"
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
	// DB
	db := initDB()

	// DI
	userRepo := repository.NewUserRepository(db)
	userSvc := service.NewUserService(userRepo)

	// gRPC server
	lis, _ := net.Listen("tcp", ":50051")
	grpcServer := grpc.NewServer()
	pb.RegisterUserServiceServer(grpcServer, userSvc)
	go grpcServer.Serve(lis)

	// HTTP Gateway
	runGateway()
}

func initDB() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	db, _ := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	db.AutoMigrate(&models.Employee{}, &models.Client{})
	return db
}

func runGateway() {
	conn, _ := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	mux := runtime.NewServeMux()
	pb.RegisterUserServiceHandler(context.Background(), mux, conn)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
