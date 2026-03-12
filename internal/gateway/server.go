package gateway

import (
	"os"

	notificationpb "banka-raf/gen/notification"
	userpb "banka-raf/gen/user"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Server struct {
	UserClient         userpb.UserServiceClient
	NotificationClient notificationpb.NotificationServiceClient
}

func NewServer() (*Server, error) {
	userAddr := os.Getenv("USER_GRPC_ADDR")
	if userAddr == "" {
		userAddr = "user:50051"
	}

	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if notificationAddr == "" {
		notificationAddr = "notification:50051"
	}

	userConn, err := grpc.Dial(userAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	notificationConn, err := grpc.Dial(notificationAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = userConn.Close()
		return nil, err
	}

	return &Server{
		UserClient:         userpb.NewUserServiceClient(userConn),
		NotificationClient: notificationpb.NewNotificationServiceClient(notificationConn),
	}, nil
}
