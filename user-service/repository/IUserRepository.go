package repository

import "user-service/models"

type IUserRepository interface {
	CreateEmployee(emp *models.Employee, permissionIDs []uint64) error
	GetEmployeeByID(id uint64) (*models.Employee, error)
	ListEmployees(page, pageSize int) ([]models.Employee, int64, error)
	UpdateEmployee(emp *models.Employee, permissionIDs []uint64) error
	DeleteEmployee(id uint64) error

	CreateClient(cli *models.Client) error
	GetClientByID(id uint64) (*models.Client, error)
	ListClients(page, pageSize int) ([]models.Client, int64, error)
	UpdateClient(cli *models.Client) error
	DeleteClient(id uint64) error

	CreatePermission(p *models.Permission) error
	ListPermissions() ([]models.Permission, error)
}
