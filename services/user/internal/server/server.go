package server

import (
	"database/sql"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/repo"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
)

const (
	PasswordActionReset      = "reset"
	PasswordActionInitialSet = "initial_set"

	resetPasswordTokenTTL  = 30 * time.Minute
	initialSetPasswordTTL  = 24 * time.Hour
	defaultNotificationURL = "notification:50051"
)

type Connections struct {
	NotificationClient notificationpb.NotificationServiceClient
	Sql_db             *sql.DB
	Gorm               *gorm.DB
	Rdb                *redis.Client
}

type Server struct {
	userpb.UnimplementedUserServiceServer
	userpb.UnimplementedTOTPServiceServer
	accessJwtSecret  string
	refreshJwtSecret string
	rdb              *redis.Client
	repo             repo.Repository
}

func NewServer(accessJwtSecret string, refreshJwtSecret string, conn *Connections) *Server {
	return &Server{
		accessJwtSecret:  accessJwtSecret,
		refreshJwtSecret: refreshJwtSecret,
		rdb:              conn.Rdb,
		repo: repo.Repository{
			Database: conn.Sql_db,
			Gorm:     conn.Gorm,
		},
	}
}
