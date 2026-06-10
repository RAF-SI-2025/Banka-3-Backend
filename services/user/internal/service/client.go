package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// clientEmailRe / clientPhoneRe mirror the employee create-time
// validation so client records aren't held to a weaker standard.
var (
	clientEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	clientPhoneRe = regexp.MustCompile(`^\+?[0-9]{6,20}$`)
)

// CreateClientInput is the validated payload for CreateClient. The
// password is set later by the client through the reset flow (we
// reuse it as the initial-password flow per spec p.9: "Zaposleni
// kreira Klijenta prilikom kreiranja računa").
type CreateClientInput struct {
	Email       string
	FirstName   string
	LastName    string
	DateOfBirth time.Time
	Gender      domain.Gender
	Phone       string
	Address     string
}

// CreateClient inserts the client and emails an initial-password link.
// Permission: ClientWrite (the bank service / employee portal calls this
// during the account-creation flow).
func (s *Service) CreateClient(ctx context.Context, in CreateClientInput) (*domain.Client, error) {
	if err := s.requirePermission(ctx, permissions.ClientWrite); err != nil {
		return nil, err
	}
	if err := validateCreateClient(in); err != nil {
		return nil, err
	}

	c, err := s.Store.CreateClient(ctx, &domain.Client{
		Email:       strings.ToLower(strings.TrimSpace(in.Email)),
		FirstName:   strings.TrimSpace(in.FirstName),
		LastName:    strings.TrimSpace(in.LastName),
		DateOfBirth: in.DateOfBirth,
		Gender:      in.Gender,
		Phone:       strings.TrimSpace(in.Phone),
		Address:     strings.TrimSpace(in.Address),
		Active:      true,
		Permissions: append([]string{}, permissions.RoleClientBasic...),
	})
	if err != nil {
		return nil, err
	}

	s.Log.InfoContext(ctx, "client created", "client_id", c.ID, "email", c.Email)

	if err := s.sendInitialPasswordEmail(ctx, domain.KindClient, c.ID, c.Email, c.FirstName); err != nil {
		// Don't roll back — the employee portal can resend by triggering
		// the regular reset flow. Logged for ops visibility.
		s.Log.ErrorContext(ctx, "send initial-password email failed", "err", err, "client_id", c.ID)
	}

	return c, nil
}

func validateCreateClient(in CreateClientInput) error {
	switch {
	case in.Email == "":
		return apperr.Validation("email is required")
	case in.FirstName == "":
		return apperr.Validation("first name is required")
	case in.LastName == "":
		return apperr.Validation("last name is required")
	case in.DateOfBirth.IsZero():
		return apperr.Validation("date of birth is required")
	case in.Gender == domain.GenderUnspecified:
		return apperr.Validation("gender is required")
	case in.Phone == "":
		return apperr.Validation("phone is required")
	case in.Address == "":
		return apperr.Validation("address is required")
	}
	if !clientEmailRe.MatchString(strings.TrimSpace(in.Email)) {
		return apperr.Validation("email format is invalid")
	}
	if !clientPhoneRe.MatchString(strings.TrimSpace(in.Phone)) {
		return apperr.Validation("phone format is invalid (expected +<digits> or <digits>)")
	}
	if !in.DateOfBirth.Before(time.Now()) {
		return apperr.Validation("date of birth must be in the past")
	}
	return nil
}

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
