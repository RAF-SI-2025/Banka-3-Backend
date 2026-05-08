package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// Spec p.14: matični broj is 8 digits, PIB is 9 digits, šifra delatnosti
// follows xx.xx (each side 1-2 digits — the spec example "01.9" is one
// digit on the right). We're permissive about šifra delatnosti since
// the table of valid codes lives outside this slice.
var (
	registryRe = regexp.MustCompile(`^[0-9]{8}$`)
	taxIDRe    = regexp.MustCompile(`^[0-9]{9}$`)
	activityRe = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)
)

type CreateCompanyInput struct {
	Name          string
	RegistryID    string
	TaxID         string
	ActivityCode  string
	Address       string
	OwnerClientID string
}

func (s *Service) CreateCompany(ctx context.Context, in CreateCompanyInput) (*domain.Company, error) {
	if err := s.requirePermission(ctx, permissions.CompanyWrite); err != nil {
		return nil, err
	}
	if err := validateCompany(in); err != nil {
		return nil, err
	}
	return s.Store.CreateCompany(ctx, &domain.Company{
		Name:          strings.TrimSpace(in.Name),
		RegistryID:    strings.TrimSpace(in.RegistryID),
		TaxID:         strings.TrimSpace(in.TaxID),
		ActivityCode:  strings.TrimSpace(in.ActivityCode),
		Address:       strings.TrimSpace(in.Address),
		OwnerClientID: strings.TrimSpace(in.OwnerClientID),
	})
}

func (s *Service) GetCompany(ctx context.Context, id string) (*domain.Company, error) {
	if err := s.requirePermission(ctx, permissions.CompanyRead); err != nil {
		return nil, err
	}
	return s.Store.GetCompanyByID(ctx, id)
}

func (s *Service) ListCompanies(ctx context.Context, f domain.CompanyFilter, page, pageSize int) ([]*domain.Company, int64, error) {
	if err := s.requirePermission(ctx, permissions.CompanyRead); err != nil {
		return nil, 0, err
	}
	return s.Store.ListCompanies(ctx, f, page, pageSize)
}

type UpdateCompanyInput struct {
	ID            string
	Name          string
	ActivityCode  string
	Address       string
	OwnerClientID string
}

// UpdateCompany applies an empty-string-means-unchanged patch. Matični
// broj and PIB are immutable (national identifiers — spec p.14
// "Ne menja se").
func (s *Service) UpdateCompany(ctx context.Context, in UpdateCompanyInput) (*domain.Company, error) {
	if err := s.requirePermission(ctx, permissions.CompanyWrite); err != nil {
		return nil, err
	}
	target, err := s.Store.GetCompanyByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(in.Name); v != "" {
		target.Name = v
	}
	if v := strings.TrimSpace(in.ActivityCode); v != "" {
		if !activityRe.MatchString(v) {
			return nil, apperr.Validation("activity code must match xx.xx")
		}
		target.ActivityCode = v
	}
	if v := strings.TrimSpace(in.Address); v != "" {
		target.Address = v
	}
	if v := strings.TrimSpace(in.OwnerClientID); v != "" {
		target.OwnerClientID = v
	}
	return s.Store.UpdateCompany(ctx, target)
}

func validateCompany(in CreateCompanyInput) error {
	switch {
	case strings.TrimSpace(in.Name) == "":
		return apperr.Validation("name is required")
	case strings.TrimSpace(in.RegistryID) == "":
		return apperr.Validation("registry id is required")
	case strings.TrimSpace(in.TaxID) == "":
		return apperr.Validation("tax id is required")
	case strings.TrimSpace(in.ActivityCode) == "":
		return apperr.Validation("activity code is required")
	case strings.TrimSpace(in.Address) == "":
		return apperr.Validation("address is required")
	case strings.TrimSpace(in.OwnerClientID) == "":
		return apperr.Validation("owner client id is required")
	}
	if !registryRe.MatchString(strings.TrimSpace(in.RegistryID)) {
		return apperr.Validation("registry id must be 8 digits")
	}
	if !taxIDRe.MatchString(strings.TrimSpace(in.TaxID)) {
		return apperr.Validation("tax id must be 9 digits")
	}
	if !activityRe.MatchString(strings.TrimSpace(in.ActivityCode)) {
		return apperr.Validation("activity code must match xx.xx")
	}
	return nil
}
