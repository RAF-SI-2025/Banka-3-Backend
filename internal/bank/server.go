package bank

import (
	"database/sql"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"gorm.io/gorm"
)

type Server struct {
	bankpb.UnimplementedBankServiceServer
	database *sql.DB
	db_gorm  *gorm.DB
}

func NewServer(database *sql.DB, gorm_db *gorm.DB) *Server {
	return &Server{
		database: database,
		db_gorm:  gorm_db,
	}
}
