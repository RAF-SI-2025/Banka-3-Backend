package service

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/tokens"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// CreateEmployeeInput is the validated payload for CreateEmployee. The
// password is set later via the activation flow.
type CreateEmployeeInput struct {
	Email       string
	Username    string
	FirstName   string
	LastName    string
	DateOfBirth time.Time
	Gender      domain.Gender
	Phone       string
	Address     string
	Position    string
	Department  string
	Active      bool
	Role        string // "admin" | "supervisor" | "agent" | "basic" — defaults to "basic"
}

// CreateEmployee inserts the employee, applies the role's permission
// bundle, and emails an activation link.
func (s *Service) CreateEmployee(ctx context.Context, in CreateEmployeeInput) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeWrite); err != nil {
		return nil, err
	}
	if err := validateCreateEmployee(in); err != nil {
		return nil, err
	}

	perms := permissionsForRole(in.Role)

	emp, err := s.Store.CreateEmployee(ctx, &domain.Employee{
		Email:       strings.ToLower(strings.TrimSpace(in.Email)),
		Username:    strings.TrimSpace(in.Username),
		FirstName:   strings.TrimSpace(in.FirstName),
		LastName:    strings.TrimSpace(in.LastName),
		DateOfBirth: in.DateOfBirth,
		Gender:      in.Gender,
		Phone:       strings.TrimSpace(in.Phone),
		Address:     strings.TrimSpace(in.Address),
		Position:    strings.TrimSpace(in.Position),
		Department:  strings.TrimSpace(in.Department),
		Active:      in.Active,
		Permissions: perms,
	})
	if err != nil {
		return nil, err
	}

	if err := s.sendActivationEmail(ctx, emp); err != nil {
		// Don't roll back the employee — admin can resend manually.
		s.Log.Error("send activation email failed", "employee_id", emp.ID, "error", err)
	}

	return emp, nil
}

// ListEmployees applies the filter + pagination.
func (s *Service) ListEmployees(ctx context.Context, f domain.EmployeeFilter, page, pageSize int) ([]*domain.Employee, int64, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeRead); err != nil {
		return nil, 0, err
	}
	return s.Store.ListEmployees(ctx, f, page, pageSize)
}

// GetEmployee returns one by ID.
func (s *Service) GetEmployee(ctx context.Context, id string) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeRead); err != nil {
		return nil, err
	}
	return s.Store.GetEmployeeByID(ctx, id)
}

// UpdateEmployeeInput is the editable subset of the employee profile.
// Empty strings (and zero time) mean "leave unchanged".
type UpdateEmployeeInput struct {
	ID          string
	Email       string
	Username    string
	FirstName   string
	LastName    string
	DateOfBirth time.Time
	Gender      domain.Gender
	Phone       string
	Address     string
	Position    string
	Department  string
}

// UpdateEmployee enforces "admin cannot edit another admin" (spec
// scenario 15).
func (s *Service) UpdateEmployee(ctx context.Context, in UpdateEmployeeInput) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeWrite); err != nil {
		return nil, err
	}
	target, err := s.Store.GetEmployeeByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := s.guardAdminOnAdmin(ctx, target); err != nil {
		return nil, err
	}
	applyEmployeePatch(target, in)
	return s.Store.UpdateEmployeeProfile(ctx, target)
}

// SetEmployeeActive toggles active. On deactivation, all refresh tokens
// are revoked and session_version is bumped so existing access tokens
// are rejected on next request.
func (s *Service) SetEmployeeActive(ctx context.Context, id string, active bool) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeWrite); err != nil {
		return nil, err
	}
	target, err := s.Store.GetEmployeeByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.guardAdminOnAdmin(ctx, target); err != nil {
		return nil, err
	}

	updated, err := s.Store.SetEmployeeActive(ctx, id, active)
	if err != nil {
		return nil, err
	}
	if !active {
		if _, err := s.Store.IncrementEmployeeSessionVersion(ctx, id); err != nil {
			s.Log.Error("bump session version failed", "employee_id", id, "error", err)
		}
		if err := s.Store.RevokeAllRefreshTokens(ctx, domain.KindEmployee, id); err != nil {
			s.Log.Error("revoke refresh tokens failed", "employee_id", id, "error", err)
		}
	}
	return updated, nil
}

// SetEmployeePermissions replaces the permission set. Bumps
// session_version (handled by store) so existing tokens revalidate.
// Requires PermissionGrant.
func (s *Service) SetEmployeePermissions(ctx context.Context, id string, perms []string) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.PermissionGrant); err != nil {
		return nil, err
	}
	target, err := s.Store.GetEmployeeByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.guardAdminOnAdmin(ctx, target); err != nil {
		return nil, err
	}
	return s.Store.SetEmployeePermissions(ctx, id, perms)
}

// guardAdminOnAdmin rejects edits to another admin (spec scenario 15).
// Self-edits are still permitted.
func (s *Service) guardAdminOnAdmin(ctx context.Context, target *domain.Employee) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if !permissions.Has(target.Permissions, permissions.Admin) {
		return nil
	}
	if p.UserID == target.ID {
		return nil
	}
	return apperr.PermissionDenied("admin nije ovlašćen da menja drugog admina")
}

func (s *Service) requirePermission(ctx context.Context, perm string) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if permissions.Has(p.Permissions, perm) || permissions.Has(p.Permissions, permissions.Admin) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije")
}

func validateCreateEmployee(in CreateEmployeeInput) error {
	switch {
	case in.Email == "":
		return apperr.Validation("email is required")
	case in.Username == "":
		return apperr.Validation("username is required")
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
	case in.Position == "":
		return apperr.Validation("position is required")
	case in.Department == "":
		return apperr.Validation("department is required")
	}
	return nil
}

func applyEmployeePatch(e *domain.Employee, in UpdateEmployeeInput) {
	if in.Email != "" {
		e.Email = strings.ToLower(strings.TrimSpace(in.Email))
	}
	if in.Username != "" {
		e.Username = strings.TrimSpace(in.Username)
	}
	if in.FirstName != "" {
		e.FirstName = strings.TrimSpace(in.FirstName)
	}
	if in.LastName != "" {
		e.LastName = strings.TrimSpace(in.LastName)
	}
	if !in.DateOfBirth.IsZero() {
		e.DateOfBirth = in.DateOfBirth
	}
	if in.Gender != domain.GenderUnspecified {
		e.Gender = in.Gender
	}
	if in.Phone != "" {
		e.Phone = strings.TrimSpace(in.Phone)
	}
	if in.Address != "" {
		e.Address = strings.TrimSpace(in.Address)
	}
	if in.Position != "" {
		e.Position = strings.TrimSpace(in.Position)
	}
	if in.Department != "" {
		e.Department = strings.TrimSpace(in.Department)
	}
}

func permissionsForRole(role string) []string {
	switch strings.ToLower(role) {
	case "admin":
		return append([]string{}, permissions.RoleEmployeeAdmin...)
	case "supervisor":
		return append([]string{}, permissions.RoleEmployeeSupervisor...)
	case "agent":
		return append([]string{}, permissions.RoleEmployeeAgent...)
	default:
		return append([]string{}, permissions.RoleEmployeeBasic...)
	}
}

func (s *Service) sendActivationEmail(ctx context.Context, e *domain.Employee) error {
	plaintext, hash, err := tokens.Generate(32)
	if err != nil {
		return err
	}
	if err := s.Store.CreateActivationToken(ctx, e.ID, hash, s.Clock.Now().Add(s.Cfg.ActivationTTL)); err != nil {
		return err
	}
	link := s.Cfg.WebBaseURL + "/aktivacija?token=" + plaintext
	subject := "Aktivacija naloga – Banka 3"
	body := "Poštovani " + e.FirstName + ",\n\n" +
		"vaš nalog u sistemu Banke 3 je kreiran. Da biste ga aktivirali i postavili lozinku, " +
		"otvorite sledeći link u narednih 24 sata:\n\n" + link + "\n\n" +
		"Ako niste očekivali ovu poruku, molimo ignorišite je.\n\n" +
		"– Banka 3"
	return s.Notifier.Send(ctx, e.Email, subject, body, false)
}
