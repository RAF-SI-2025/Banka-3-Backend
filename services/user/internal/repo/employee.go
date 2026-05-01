package repo

import (
	"errors"
	"fmt"
	"sort"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/model"
	"gorm.io/gorm"
)

// GetAllEmployees retrieves all employees with optional restrictions, preloading Permissions.
func (r *Repository) GetAllEmployees(constraints UserRestrictions) ([]model.Employee, error) {
	addConstraints := func(query *gorm.DB, restrictions UserRestrictions) *gorm.DB {
		keys := make([]string, 0, len(restrictions))
		for k := range restrictions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := restrictions[key]
			if value == "" {
				continue
			}
			if key != "" {
				switch key {
				case "email", "position":
					query = query.Where(key+" = ?", value)
				default:
					query = query.Where(key+" ILIKE ?", value)
				}
			}
		}
		return query
	}

	var employees []model.Employee
	query := r.Gorm.Model(&model.Employee{}).Preload("Permissions")
	query = addConstraints(query, constraints)
	err := query.Find(&employees).Error
	if err != nil {
		return nil, err
	}
	return employees, nil
}

// CreateEmployee creates a new employee.
func (r *Repository) CreateEmployee(employee model.Employee) error {
	result := r.Gorm.Create(&employee)
	if result.Error != nil {
		logger.L().Error("create employee failed", "err", result.Error)
		return result.Error
	}
	return nil
}

// GetEmployeeByAttribute retrieves an employee by a specific attribute, preloading Permissions.
func (r *Repository) GetEmployeeByAttribute(attributeName string, attributeValue any) (*model.Employee, error) {
	var employee model.Employee
	err := r.Gorm.Preload("Permissions").Where(attributeName+" = ?", attributeValue).First(&employee).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		logger.L().Error("GetEmployeeByAttribute failed", "err", err)
		return nil, err
	}
	logger.L().Debug("GetEmployeeByAttribute result", "value", employee)
	return &employee, nil
}

// DeleteEmployee deletes an employee.
func (r *Repository) DeleteEmployee(employee model.Employee) error {
	result := r.Gorm.Delete(&employee)
	if result.RowsAffected == 0 {
		return ErrEmployeeNotFound
	}
	if result.Error != nil {
		logger.L().Error("DeleteEmployee failed", "err", result.Error)
		return result.Error
	}
	return nil
}

// EmployeeExists checks if an employee exists.
func (r *Repository) EmployeeExists(employee model.Employee) bool {
	result := r.Gorm.First(&employee)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false
	}
	if result.Error != nil {
		logger.L().Error("EmployeeExists failed", "err", result.Error)
		return false
	}
	return true
}

// UpdateEmployee updates an existing employee, resolving permission IDs.
func (r *Repository) UpdateEmployee(employee model.Employee) (*model.Employee, error) {
	// find permission IDs by name
	findPermByName := func(permName string) uint64 {
		var perm model.Permission
		r.Gorm.First(&perm, "name = ?", permName)
		return perm.Id
	}
	for i, val := range employee.Permissions {
		employee.Permissions[i].Id = findPermByName(val.Name)
	}

	if !r.EmployeeExists(employee) {
		return nil, ErrEmployeeNotFound
	}
	result := r.Gorm.Model(&employee).Updates(employee)
	if result.Error != nil {
		return nil, fmt.Errorf("updating employee: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, ErrEmployeeNotFound
	}
	return &employee, nil
}
