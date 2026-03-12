package repository

import (
	"banka-raf/internal/user/models"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

var _ IUserRepository = &UserRepository{}

// =================== Employee ===================

func (r *UserRepository) CreateEmployee(emp *models.Employee, permissionIDs []uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if emp.Password == nil {
			emp.Password = []byte("")
		}
		if emp.SaltPassword == nil {
			emp.SaltPassword = []byte("")
		}

		emp.Id = 0

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
	if err := r.db.Preload("Permissions").First(&emp, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &emp, nil
}

func (r *UserRepository) ListEmployees(page, pageSize int, email, firstName, lastName, position string) ([]models.Employee, int64, error) {
	var emps []models.Employee
	var total int64

	query := r.db.Model(&models.Employee{})
	if email != "" {
		query = query.Where("email ILIKE ?", "%"+email+"%")
	}
	if firstName != "" {
		query = query.Where("first_name ILIKE ?", "%"+firstName+"%")
	}
	if lastName != "" {
		query = query.Where("last_name ILIKE ?", "%"+lastName+"%")
	}
	if position != "" {
		query = query.Where("position ILIKE ?", "%"+position+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("Permissions").
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&emps).Error

	return emps, total, err
}

func (r *UserRepository) UpdateEmployee(emp *models.Employee, permissionIDs []uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		updateQuery := tx.Model(emp).Omit("Permissions")

		if len(emp.Password) == 0 {
			updateQuery = updateQuery.Omit("password", "salt_password")
		}

		if err := updateQuery.Updates(emp).Error; err != nil {
			return err
		}

		if len(permissionIDs) > 0 {
			var perms []models.Permission
			tx.Find(&perms, permissionIDs)
			return tx.Model(emp).Association("Permissions").Replace(perms)
		}
		return tx.Model(emp).Association("Permissions").Clear()
	})
}

func (r *UserRepository) DeleteEmployee(id uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		emp := &models.Employee{Id: id}
		_ = tx.Model(emp).Association("Permissions").Clear()
		return tx.Delete(&models.Employee{}, id).Error
	})
}

// =================== Client ===================

func (r *UserRepository) CreateClient(cli *models.Client) error {
	cli.Id = 0
	if cli.Password == nil {
		cli.Password = []byte("")
	}
	if cli.SaltPassword == nil {
		cli.SaltPassword = []byte("")
	}
	return r.db.Create(cli).Error
}

func (r *UserRepository) GetClientByID(id uint64) (*models.Client, error) {
	var cli models.Client
	if err := r.db.First(&cli, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &cli, nil
}

func (r *UserRepository) ListClients(page, pageSize int, firstName, lastName, email string) ([]models.Client, int64, error) {
	var clients []models.Client
	var total int64

	query := r.db.Model(&models.Client{})
	if firstName != "" {
		query = query.Where("first_name ILIKE ?", "%"+firstName+"%")
	}
	if lastName != "" {
		query = query.Where("last_name ILIKE ?", "%"+lastName+"%")
	}
	if email != "" {
		query = query.Where("email ILIKE ?", "%"+email+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&clients).Error
	return clients, total, err
}

func (r *UserRepository) UpdateClient(cli *models.Client) error {
	return r.db.Model(cli).Omit("password", "salt_password").Updates(cli).Error
}

func (r *UserRepository) DeleteClient(id uint64) error {
	return r.db.Delete(&models.Client{}, id).Error
}

// =================== Permissions ===================

func (r *UserRepository) ListPermissions() ([]models.Permission, error) {
	var perms []models.Permission
	return perms, r.db.Order("name ASC").Find(&perms).Error
}

func (r *UserRepository) GetPermissionByName(name string) (*models.Permission, error) {
	var perm models.Permission
	err := r.db.Where("name = ?", name).First(&perm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &perm, err
}

// =================== Auth Logic ===================

func (r *UserRepository) FindUserByEmail(email string) (interface{}, error) {
	var emp models.Employee
	if err := r.db.Where("email = ?", email).First(&emp).Error; err == nil {
		return &emp, nil
	}

	var cli models.Client
	if err := r.db.Where("email = ?", email).First(&cli).Error; err == nil {
		return &cli, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func (r *UserRepository) UpsertRefreshToken(employeeID uint64, token string, expiresAt time.Time) error {
	hashed := hashToken(token)
	return r.db.Transaction(func(tx *gorm.DB) error {
		tx.Where("employee_id = ?", employeeID).Delete(&models.RefreshToken{})
		newToken := models.RefreshToken{
			EmployeeId: employeeID,
			Token:      hashed,
			ExpiresAt:  expiresAt,
			Revoked:    false,
		}
		return tx.Create(&newToken).Error
	})
}

func (r *UserRepository) RotateRefreshToken(employeeID uint64, oldToken, newToken string, newExpiry time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var stored models.RefreshToken
		oldHashed := hashToken(oldToken)

		if err := tx.Where("employee_id = ? AND token = ? AND revoked = ? AND expires_at > ?",
			employeeID, oldHashed, false, time.Now()).First(&stored).Error; err != nil {
			tx.Model(&models.RefreshToken{}).Where("employee_id = ?", employeeID).Update("revoked", true)
			return fmt.Errorf("token invalid or reused")
		}

		return tx.Model(&stored).Updates(map[string]interface{}{
			"token":      hashToken(newToken),
			"expires_at": newExpiry,
		}).Error
	})
}

func hashToken(token string) string {
	h := sha256.New()
	h.Write([]byte(token))
	return fmt.Sprintf("%x", h.Sum(nil))
}
