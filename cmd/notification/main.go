package main

import (
	"banka-raf/gen/notification"
	internalNotification "banka-raf/internal/notification"
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	//notification.RegisterNotificationServiceServer(grpcServer, &internalNotification.Server{})
	smtpSender := &internalNotification.SMTPSender{}
	server := internalNotification.NewServer(smtpSender)

	notification.RegisterNotificationServiceServer(grpcServer, server)
	reflection.Register(grpcServer)
	log.Printf("Notification service listening on port %s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
