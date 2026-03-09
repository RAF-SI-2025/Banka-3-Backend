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

// #region Employee

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
	if err := r.db.Preload("Permissions").First(&emp, id).Error; err != nil {
		return nil, err
	}
	return &emp, nil
}

func (r *UserRepository) ListEmployees(page, pageSize int) ([]models.Employee, int64, error) {
	var emps []models.Employee
	var total int64

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Employee{}).Count(&total).Error; err != nil {
			return err
		}
		return tx.Offset((page - 1) * pageSize).
			Limit(pageSize).
			Preload("Permissions").
			Order("id DESC").
			Find(&emps).Error
	})

	return emps, total, err
}

func (r *UserRepository) UpdateEmployee(emp *models.Employee, permissionIDs []uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(emp).Select("*").Omit("Permissions").Updates(emp).Error; err != nil {
			return err
		}

		var perms []models.Permission
		if len(permissionIDs) > 0 {
			if err := tx.Find(&perms, permissionIDs).Error; err != nil {
				return err
			}
		}

		return tx.Model(emp).Association("Permissions").Replace(perms)
	})
}

func (r *UserRepository) DeleteEmployee(id uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		emp := &models.Employee{Model: gorm.Model{ID: uint(id)}}
		if err := tx.Model(emp).Association("Permissions").Clear(); err != nil {
			return err
		}
		return tx.Delete(emp).Error
	})
}

// #endregion

// #region Client

func (r *UserRepository) CreateClient(cli *models.Client) error {
	return r.db.Create(cli).Error
}

func (r *UserRepository) GetClientByID(id uint64) (*models.Client, error) {
	var cli models.Client
	if err := r.db.First(&cli, id).Error; err != nil {
		return nil, err
	}
	return &cli, nil
}

func (r *UserRepository) ListClients(page, pageSize int) ([]models.Client, int64, error) {
	var clients []models.Client
	var total int64

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Client{}).Count(&total).Error; err != nil {
			return err
		}
		return tx.Offset((page - 1) * pageSize).
			Limit(pageSize).
			Order("id DESC").
			Find(&clients).Error
	})

	return clients, total, err
}

func (r *UserRepository) UpdateClient(cli *models.Client) error {
	return r.db.Save(cli).Error
}

func (r *UserRepository) DeleteClient(id uint64) error {
	return r.db.Delete(&models.Client{}, id).Error
}

// #endregion

// #region Permission

func (r *UserRepository) CreatePermission(p *models.Permission) error {
	return r.db.Create(p).Error
}

func (r *UserRepository) ListPermissions() ([]models.Permission, error) {
	var perms []models.Permission
	err := r.db.Order("name ASC").Find(&perms).Error
	return perms, err
}

// #endregion
