package repository

import (
	"banka-raf/internal/user/models"
	"time"
)

type IUserRepository interface {
	CreateEmployee(emp *models.Employee, permissionIDs []uint64) error
	GetEmployeeByID(id uint64) (*models.Employee, error)
	ListEmployees(page, pageSize int, email, firstName, lastName, position string) ([]models.Employee, int64, error)
	UpdateEmployee(emp *models.Employee, permissionIDs []uint64) error
	DeleteEmployee(id uint64) error

	CreateClient(cli *models.Client) error
	GetClientByID(id uint64) (*models.Client, error)
	ListClients(page, pageSize int, firstName, lastName, email string) ([]models.Client, int64, error)
	UpdateClient(cli *models.Client) error
	DeleteClient(id uint64) error

	ListPermissions() ([]models.Permission, error)
	GetPermissionByName(name string) (*models.Permission, error)

	FindUserByEmail(email string) (interface{}, error)
	UpsertRefreshToken(employeeID uint64, token string, expiresAt time.Time) error
	RotateRefreshToken(employeeID uint64, oldToken, newToken string, newExpiry time.Time) error
}
