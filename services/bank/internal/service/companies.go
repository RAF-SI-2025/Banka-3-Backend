package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// Spec p.14: matični broj is 8 digits, PIB is 9 digits, šifra delatnosti
// follows NN.N or NN.NN (left side always 2 digits per the official
// register; right side 1-2 — the spec example "01.9" is one digit).
// Mirrors the proto buf.validate.field pattern so the gateway and the
// service layer reject the same inputs.
var (
	registryRe = regexp.MustCompile(`^[0-9]{8}$`)
	taxIDRe    = regexp.MustCompile(`^[0-9]{9}$`)
	activityRe = regexp.MustCompile(`^[0-9]{2}\.[0-9]{1,2}$`)
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
		s.log().WarnContext(ctx, "create company validation failed",
			"err", err, "owner_client_id", in.OwnerClientID, "registry_id", in.RegistryID)
		return nil, err
	}
	created, err := s.Store.CreateCompany(ctx, &domain.Company{
		Name:          strings.TrimSpace(in.Name),
		RegistryID:    strings.TrimSpace(in.RegistryID),
		TaxID:         strings.TrimSpace(in.TaxID),
		ActivityCode:  strings.TrimSpace(in.ActivityCode),
		Address:       strings.TrimSpace(in.Address),
		OwnerClientID: strings.TrimSpace(in.OwnerClientID),
	})
	if err != nil {
		return nil, err
	}
	s.log().InfoContext(ctx, "company created",
		"company_id", created.ID, "owner_client_id", created.OwnerClientID, "registry_id", created.RegistryID)
	return created, nil
}

func (s *Service) GetCompany(ctx context.Context, id string) (*domain.Company, error) {
	c, err := s.Store.GetCompanyByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// The company's owning client may always read it — needed for the
	// /banking/racuni/$id Poslovni view to display "Firma" without
	// granting clients the broader CompanyRead permission.
	if p, ok := auth.PrincipalFrom(ctx); ok && p.UserID == c.OwnerClientID {
		return c, nil
	}
	if err := s.requirePermission(ctx, permissions.CompanyRead); err != nil {
		return nil, err
	}
	return c, nil
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
			s.log().WarnContext(ctx, "update company validation failed: bad activity code", "company_id", in.ID)
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
	updated, err := s.Store.UpdateCompany(ctx, target)
	if err != nil {
		return nil, err
	}
	s.log().InfoContext(ctx, "company updated", "company_id", updated.ID)
	return updated, nil
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
