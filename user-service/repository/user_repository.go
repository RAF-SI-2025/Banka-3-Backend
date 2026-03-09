package repository

import (
	"user-service/models"

	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// --- Employee ---

func (r *UserRepository) CreateEmployee(emp *models.Employee, permissionIDs []uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if len(permissionIDs) > 0 {
			var perms []models.Permission
			if err := tx.Find(&perms, permissionIDs).Error; err != nil {
				return err
			}
			emp.Permissions = perms
		}
		return tx.Create(emp).Error
	})
}

func (r *UserRepository) GetEmployeeByID(id uint64) (*models.Employee, error) {
	var emp models.Employee
	err := r.db.Preload("Permissions").First(&emp, id).Error
	return &emp, err
}

func (r *UserRepository) ListEmployees(page, pageSize int) ([]models.Employee, int64, error) {
	var emps []models.Employee
	var total int64
	r.db.Model(&models.Employee{}).Count(&total)
	err := r.db.Offset((page - 1) * pageSize).Limit(pageSize).Preload("Permissions").Find(&emps).Error
	return emps, total, err
}

func (r *UserRepository) UpdateEmployee(emp *models.Employee, permissionIDs []uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(emp).Association("Permissions").Replace(permissionIDs); err != nil {
			return err
		}
		return tx.Save(emp).Error
	})
}

func (r *UserRepository) DeleteEmployee(id uint64) error {
	return r.db.Delete(&models.Employee{}, id).Error
}

// --- Client ---

func (r *UserRepository) CreateClient(cli *models.Client) error {
	return r.db.Create(cli).Error
}

func (r *UserRepository) GetClientByID(id uint64) (*models.Client, error) {
	var cli models.Client
	err := r.db.First(&cli, id).Error
	return &cli, err
}

func (r *UserRepository) ListClients(page, pageSize int) ([]models.Client, int64, error) {
	var clients []models.Client
	var total int64
	r.db.Model(&models.Client{}).Count(&total)
	err := r.db.Offset((page - 1) * pageSize).Limit(pageSize).Find(&clients).Error
	return clients, total, err
}

func (r *UserRepository) UpdateClient(cli *models.Client) error {
	return r.db.Save(cli).Error
}

func (r *UserRepository) DeleteClient(id uint64) error {
	return r.db.Delete(&models.Client{}, id).Error
}

// --- Permission ---

func (r *UserRepository) CreatePermission(p *models.Permission) error {
	return r.db.Create(p).Error
}

func (r *UserRepository) ListPermissions() ([]models.Permission, error) {
	var perms []models.Permission
	err := r.db.Find(&perms).Error
	return perms, err
}
