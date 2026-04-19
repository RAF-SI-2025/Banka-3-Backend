package main

import (
	"fmt"
	"log"
	"net"
	"os"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func connectGorm() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("gorm open: %v", err)
	}
	return db
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

	db := connectGorm()
	log.Println("connected to database...")

	srv := grpc.NewServer()
	tradingpb.RegisterTradingServiceServer(srv, trading.NewServer(db))
	reflection.Register(srv)

	log.Printf("trading service listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
