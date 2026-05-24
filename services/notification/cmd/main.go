package main

import (
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	internalNotification "github.com/RAF-SI-2025/Banka-3-Backend/services/notification/internal/notification"
)

func main() {
	logger.Init("notification")
	stopMetrics := observability.StartMetricsServer("notification", os.Getenv("METRICS_PORT"))
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
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			logger.UnaryServerInterceptor(),
			observability.UnaryServerInterceptor("notification"),
		),
		grpc.ChainStreamInterceptor(
			logger.StreamServerInterceptor(),
			observability.StreamServerInterceptor("notification"),
		),
	)
	backend := os.Getenv("BACKEND")

	var sender internalNotification.EmailSender
	switch backend {
	case "SMTP":
		sender = &internalNotification.SMTPSender{}
	case "STDOUT":
		sender = &internalNotification.StdoutSender{}
	case "NOOP":
		sender = &internalNotification.NoopSender{}
	default:
		sender = &internalNotification.SMTPSender{}
	}
	server := internalNotification.NewServer(sender)

	notification.RegisterNotificationServiceServer(grpcServer, server)
	reflection.Register(grpcServer)
	logger.L().Info("notification service listening", "port", port)
	if err := grpcServer.Serve(lis); err != nil {
		logger.L().Error("failed to serve", "err", err)
		os.Exit(1)
	}
}
