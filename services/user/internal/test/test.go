// shared testing utils

package test

import (
	"database/sql"
	"net"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/server"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func StartNotificationTestServer(t *testing.T, handler notificationpb.NotificationServiceServer) (string, func()) {
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

func NewTestServer(t *testing.T) (*server.Server, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	conn := server.Connections{
		Sql_db:             db,
		Gorm:               nil,
		NotificationClient: nil,
		Rdb:                nil,
	}

	return server.NewServer("access", "refresh", &conn), mock, db
}

func NewGormTestServer(t *testing.T) (*server.Server, sqlmock.Sqlmock, *sql.DB) {
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

	conn := server.Connections{
		Sql_db:             db,
		Gorm:               gormDB,
		NotificationClient: nil,
		Rdb:                nil,
	}

	return server.NewServer("access", "refresh", &conn), mock, db
}

// NewFullTestServer creates a test server with sqlmock, gorm, and miniredis.
func NewFullTestServer(t *testing.T) (*server.Server, sqlmock.Sqlmock, *sql.DB) {
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

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	conn := server.Connections{
		Sql_db:             db,
		Gorm:               gormDB,
		NotificationClient: nil,
		Rdb:                rdb,
	}

	return server.NewServer("access", "refresh", &conn), mock, db
}
