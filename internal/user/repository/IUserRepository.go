package repository

import "banka-raf/internal/user/models"

type IUserRepository interface {
	CreateEmployee(emp *models.Employee, permissionIDs []uint) error
	GetEmployeeByID(id uint) (*models.Employee, error)
	ListEmployees(page, pageSize int, email, firstName, lastName, position string) ([]models.Employee, int64, error)
	UpdateEmployee(emp *models.Employee, permissionIDs []uint) error
	DeleteEmployee(id uint) error

	CreateClient(cli *models.Client) error
	GetClientByID(id uint) (*models.Client, error)
	ListClients(page, pageSize int, firstName, lastName, email string) ([]models.Client, int64, error)
	UpdateClient(cli *models.Client) error
	DeleteClient(id uint) error

	ListPermissions() ([]models.Permission, error)
	GetPermissionByName(name string) (*models.Permission, error)
}
