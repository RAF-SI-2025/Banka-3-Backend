// shared testing utils

package user

import (
	"database/sql"
	"net"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	"google.golang.org/grpc"
)

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

func newTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	return NewServer("access", "refresh", db, nil), mock, db
}
