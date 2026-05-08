package service

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// ListClients returns a page of clients.
func (s *Service) ListClients(ctx context.Context, f domain.ClientFilter, page, pageSize int) ([]*domain.Client, int64, error) {
	if err := s.requirePermission(ctx, permissions.ClientRead); err != nil {
		return nil, 0, err
	}
	return s.Store.ListClients(ctx, f, page, pageSize)
}

// GetClient returns one by ID.
func (s *Service) GetClient(ctx context.Context, id string) (*domain.Client, error) {
	if err := s.requirePermission(ctx, permissions.ClientRead); err != nil {
		return nil, err
	}
	return s.Store.GetClientByID(ctx, id)
}

// UpdateClientInput mirrors UpdateEmployeeInput but for clients (no
// position/department/username).
type UpdateClientInput struct {
	ID          string
	Email       string
	FirstName   string
	LastName    string
	DateOfBirth time.Time
	Gender      domain.Gender
	Phone       string
	Address     string
}

// UpdateClient applies the patch.
func (s *Service) UpdateClient(ctx context.Context, in UpdateClientInput) (*domain.Client, error) {
	if err := s.requirePermission(ctx, permissions.ClientWrite); err != nil {
		return nil, err
	}
	target, err := s.Store.GetClientByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	applyClientPatch(target, in)
	return s.Store.UpdateClientProfile(ctx, target)
}

func applyClientPatch(c *domain.Client, in UpdateClientInput) {
	if in.Email != "" {
		c.Email = strings.ToLower(strings.TrimSpace(in.Email))
	}
	if in.FirstName != "" {
		c.FirstName = strings.TrimSpace(in.FirstName)
	}
	if in.LastName != "" {
		c.LastName = strings.TrimSpace(in.LastName)
	}
	if !in.DateOfBirth.IsZero() {
		c.DateOfBirth = in.DateOfBirth
	}
	if in.Gender != domain.GenderUnspecified {
		c.Gender = in.Gender
	}
	if in.Phone != "" {
		c.Phone = strings.TrimSpace(in.Phone)
	}
	if in.Address != "" {
		c.Address = strings.TrimSpace(in.Address)
	}
}
