package repository

import (
	"banka-raf/internal/user/models"
	"errors"

	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

var _ IUserRepository = &UserRepository{} // ensure implementation

// =================== Employee ===================

func (r *UserRepository) CreateEmployee(emp *models.Employee, permissionIDs []uint) error {
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

func (r *UserRepository) GetEmployeeByID(id uint) (*models.Employee, error) {
	var emp models.Employee
	if err := r.db.Preload("Permissions").First(&emp, id).Error; err != nil {
		return nil, err
	}
	return &emp, nil
}

func (r *UserRepository) ListEmployees(page, pageSize int, email, firstName, lastName, position string) ([]models.Employee, int64, error) {
	var emps []models.Employee
	var total int64

	err := r.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&models.Employee{})

		if email != "" {
			query = query.Where("email LIKE ?", "%"+email+"%")
		}
		if firstName != "" {
			query = query.Where("first_name LIKE ?", "%"+firstName+"%")
		}
		if lastName != "" {
			query = query.Where("last_name LIKE ?", "%"+lastName+"%")
		}
		if position != "" {
			query = query.Where("position LIKE ?", "%"+position+"%")
		}

		if err := query.Count(&total).Error; err != nil {
			return err
		}

		return query.Preload("Permissions").
			Order("id DESC").
			Offset((page - 1) * pageSize).
			Limit(pageSize).
			Find(&emps).Error
	})

	return emps, total, err
}

func (r *UserRepository) UpdateEmployee(emp *models.Employee, permissionIDs []uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(emp).Select("*").Omit("Permissions").Updates(emp).Error; err != nil {
			return err
		}
		if len(permissionIDs) > 0 {
			var perms []models.Permission
			if err := tx.Find(&perms, permissionIDs).Error; err != nil {
				return err
			}
			return tx.Model(emp).Association("Permissions").Replace(perms)
		}
		return tx.Model(emp).Association("Permissions").Clear()
	})
}

func (r *UserRepository) DeleteEmployee(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		emp := &models.Employee{Model: gorm.Model{ID: id}}
		if err := tx.Model(emp).Association("Permissions").Clear(); err != nil {
			return err
		}
		return tx.Delete(emp).Error
	})
}

// =================== Client ===================

func (r *UserRepository) CreateClient(cli *models.Client) error {
	return r.db.Create(cli).Error
}

func (r *UserRepository) GetClientByID(id uint) (*models.Client, error) {
	var cli models.Client
	if err := r.db.First(&cli, id).Error; err != nil {
		return nil, err
	}
	return &cli, nil
}

func (r *UserRepository) ListClients(page, pageSize int, firstName, lastName, email string) ([]models.Client, int64, error) {
	var clients []models.Client
	var total int64

	err := r.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&models.Client{})
		if firstName != "" {
			query = query.Where("first_name LIKE ?", "%"+firstName+"%")
		}
		if lastName != "" {
			query = query.Where("last_name LIKE ?", "%"+lastName+"%")
		}
		if email != "" {
			query = query.Where("email LIKE ?", "%"+email+"%")
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&clients).Error
	})

	return clients, total, err
}

func (r *UserRepository) UpdateClient(cli *models.Client) error {
	return r.db.Save(cli).Error
}

func (r *UserRepository) DeleteClient(id uint) error {
	return r.db.Delete(&models.Client{}, id).Error
}

// =================== Permissions ===================

func (r *UserRepository) ListPermissions() ([]models.Permission, error) {
	var perms []models.Permission
	return perms, r.db.Order("name ASC").Find(&perms).Error
}

func (r *UserRepository) GetPermissionByName(name string) (*models.Permission, error) {
	var perm models.Permission
	if err := r.db.Where("name = ?", name).First(&perm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &perm, nil
}
