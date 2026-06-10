package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// Mirror the user service create-time validation.
var (
	apEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	apPhoneRe = regexp.MustCompile(`^\+?[0-9]{6,20}$`)
)

type CreateAuthorizedPersonInput struct {
	CompanyID   string
	FirstName   string
	LastName    string
	DateOfBirth time.Time
	Gender      domain.Gender
	Email       string
	Phone       string
	Address     string
}

func (s *Service) CreateAuthorizedPerson(ctx context.Context, in CreateAuthorizedPersonInput) (*domain.AuthorizedPerson, error) {
	if err := s.requirePermission(ctx, permissions.CompanyWrite); err != nil {
		return nil, err
	}
	if _, err := s.Store.GetCompanyByID(ctx, in.CompanyID); err != nil {
		s.log().WarnContext(ctx, "create authorized person: company lookup failed", "err", err, "company_id", in.CompanyID)
		return nil, err
	}
	if err := s.validateAP(in); err != nil {
		s.log().WarnContext(ctx, "create authorized person validation failed", "err", err, "company_id", in.CompanyID)
		return nil, err
	}
	created, err := s.Store.CreateAuthorizedPerson(ctx, &domain.AuthorizedPerson{
		CompanyID:   in.CompanyID,
		FirstName:   strings.TrimSpace(in.FirstName),
		LastName:    strings.TrimSpace(in.LastName),
		DateOfBirth: in.DateOfBirth,
		Gender:      in.Gender,
		Email:       strings.ToLower(strings.TrimSpace(in.Email)),
		Phone:       strings.TrimSpace(in.Phone),
		Address:     strings.TrimSpace(in.Address),
	})
	if err != nil {
		return nil, err
	}
	s.log().InfoContext(ctx, "authorized person created", "authorized_person_id", created.ID, "company_id", in.CompanyID)
	return created, nil
}

func (s *Service) ListAuthorizedPersons(ctx context.Context, companyID string) ([]*domain.AuthorizedPerson, error) {
	if err := s.requirePermission(ctx, permissions.CompanyRead); err != nil {
		return nil, err
	}
	if companyID == "" {
		return nil, apperr.Validation("company_id is required")
	}
	return s.Store.ListAuthorizedPersonsByCompany(ctx, companyID)
}

func (s *Service) validateAP(in CreateAuthorizedPersonInput) error {
	switch {
	case strings.TrimSpace(in.CompanyID) == "":
		return apperr.Validation("company id is required")
	case strings.TrimSpace(in.FirstName) == "":
		return apperr.Validation("first name is required")
	case strings.TrimSpace(in.LastName) == "":
		return apperr.Validation("last name is required")
	case in.DateOfBirth.IsZero():
		return apperr.Validation("date of birth is required")
	case in.Gender == domain.GenderUnspecified:
		return apperr.Validation("gender is required")
	case strings.TrimSpace(in.Email) == "":
		return apperr.Validation("email is required")
	case strings.TrimSpace(in.Phone) == "":
		return apperr.Validation("phone is required")
	case strings.TrimSpace(in.Address) == "":
		return apperr.Validation("address is required")
	}
	if !apEmailRe.MatchString(strings.TrimSpace(in.Email)) {
		return apperr.Validation("email format is invalid")
	}
	if !apPhoneRe.MatchString(strings.TrimSpace(in.Phone)) {
		return apperr.Validation("phone format is invalid")
	}
	if !in.DateOfBirth.Before(s.now()) {
		return apperr.Validation("date of birth must be in the past")
	}
	return nil
}
