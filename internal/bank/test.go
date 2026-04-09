package bank

import (
	"database/sql"
	"net"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type TestNotificationServer struct {
	notificationpb.UnimplementedNotificationServiceServer
}

func newTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	server, _ := NewServer(db, nil, nil)
	return server, mock, db
}

func newGormTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, *sql.DB) {
	return newGormTestServerWithNotificationClient(t, nil)
}

func newGormTestServerWithNotificationClient(t *testing.T, notificationClient notificationpb.NotificationServiceClient) (*Server, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: db,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	server, _ := NewServer(db, gormDB, notificationClient)
	return server, mock, db
}

//lint:ignore U1000 reasons for ignoring
func startNotificationTestServer(t *testing.T, handler notificationpb.NotificationServiceServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	notificationpb.RegisterNotificationServiceServer(srv, handler)
	go func() {
		_ = srv.Serve(lis)
	}()
	return lis.Addr().String(), func() {
		srv.Stop()
		_ = lis.Close()
	}
}

//lint:ignore U1000 reasons for ignoring
func startUserTestServer(t *testing.T, handler userpb.UserServiceServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	userpb.RegisterUserServiceServer(srv, handler)
	go func() {
		_ = srv.Serve(lis)
	}()
	return lis.Addr().String(), func() {
		srv.Stop()
		_ = lis.Close()
	}
}
