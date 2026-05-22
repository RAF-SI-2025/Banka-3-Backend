package repo

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/model"
	"gorm.io/gorm"
)

// GetAllClients retrieves all clients with optional restrictions.
func (r *Repository) GetAllClients(constraints UserRestrictions) ([]model.Client, error) {
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
				case "email":
					query = query.Where(key+" = ?", value)
				default:
					query = query.Where(key+" ILIKE ?", value)
				}
			}
		}
		return query
	}

	var clients []model.Client
	query := r.Gorm.Model(&model.Client{})
	query = addConstraints(query, constraints)
	err := query.Find(&clients).Error
	if err != nil {
		return nil, err
	}
	return clients, nil
}

// CreateClient creates a new client.
func (r *Repository) CreateClient(client model.Client) error {
	result := r.Gorm.Create(&client)
	if result.Error != nil {
		logger.L().Error("create client failed", "err", result.Error)
		if isUniqueViolation(result.Error) {
			return ErrClientEmailExists
		}
		return result.Error
	}
	return nil
}

// GetClientByAttribute retrieves a client by a specific attribute (e.g., "email").
func (r *Repository) GetClientByAttribute(attributeName string, attributeValue any) (*model.Client, error) {
	var client model.Client
	err := r.Gorm.Model(&model.Client{}).Where(attributeName+" = ?", attributeValue).First(&client).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		logger.L().Error("GetClientByAttribute failed", "err", err)
		return nil, err
	}
	logger.L().Debug("GetClientByAttribute result", "value", client)
	return &client, nil
}

// DeleteClient deletes a client.
func (r *Repository) DeleteClient(client model.Client) error {
	result := r.Gorm.Delete(&client)
	if result.RowsAffected == 0 {
		return ErrClientNotFound
	}
	if result.Error != nil {
		logger.L().Error("DeleteClient failed", "err", result.Error)
		return result.Error
	}
	return nil
}

// ClientExists checks if a client exists.
func (r *Repository) ClientExists(client model.Client) bool {
	result := r.Gorm.First(&client)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false
	}
	if result.Error != nil {
		logger.L().Error("ClientExists failed", "err", result.Error)
		return false
	}
	return true
}

// UpdateClient updates an existing client.
func (r *Repository) UpdateClient(client model.Client) (*model.Client, error) {
	if !r.ClientExists(client) {
		return nil, ErrClientNotFound
	}

	updates := map[string]any{
		"margin_enabled": client.Margin_enabled,
		"updated_at":     time.Now(),
	}
	if client.First_name != "" {
		updates["first_name"] = client.First_name
	}
	if client.Last_name != "" {
		updates["last_name"] = client.Last_name
	}
	if !client.Date_of_birth.IsZero() {
		updates["date_of_birth"] = client.Date_of_birth
	}
	if client.Gender != "" {
		updates["gender"] = client.Gender
	}
	if client.Email != "" {
		updates["email"] = client.Email
	}
	if client.Phone_number != "" {
		updates["phone_number"] = client.Phone_number
	}
	if client.Address != "" {
		updates["address"] = client.Address
	}

	result := r.Gorm.Model(&model.Client{}).Where("id = ?", client.Id).Updates(updates)
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return nil, ErrClientEmailExists
		}
		return nil, fmt.Errorf("updating client: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, ErrClientNotFound
	}
	return &client, nil
}
