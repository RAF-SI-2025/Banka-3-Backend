package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/tokens"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// emailRe and phoneRe enforce minimal create-time format checks. The DB
// has the unique constraint on email; these are user-facing filters so
// the activation email doesn't end up holding garbage.
var (
	emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	phoneRe = regexp.MustCompile(`^\+?[0-9]{6,20}$`)
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
// bundle, and emails an activation link. Granting the admin role
// requires the caller to *be* an admin (spec p.9: "može i da dodeli
// admin permisiju") — EmployeeWrite alone is not enough, otherwise a
// future role with EmployeeWrite could mint admins.
func (s *Service) CreateEmployee(ctx context.Context, in CreateEmployeeInput) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeWrite); err != nil {
		return nil, err
	}
	if err := validateCreateEmployee(in); err != nil {
		return nil, err
	}

	perms := permissionsForRole(in.Role)
	if permissions.Has(perms, permissions.Admin) {
		if err := s.requirePermission(ctx, permissions.Admin); err != nil {
			return nil, apperr.PermissionDenied("samo administrator može dodeliti admin permisiju")
		}
	}

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
// scenario 15) and emails the employee a summary of what changed
// (E2E: "Zaposleni dobija email obaveštenje o promenama").
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
	before := *target
	applyEmployeePatch(target, in)
	updated, err := s.Store.UpdateEmployeeProfile(ctx, target)
	if err != nil {
		return nil, err
	}
	if changes := diffEmployee(&before, updated); len(changes) > 0 {
		// Send to whichever email is reachable: if email itself changed,
		// notify the new address — that's where future correspondence
		// goes — and the old one too so the previous account holder sees
		// the swap.
		if err := s.sendProfileChangeEmail(ctx, updated, changes); err != nil {
			s.Log.Warn("profile change email failed", "employee_id", updated.ID, "error", err)
		}
		if before.Email != updated.Email {
			if err := s.sendProfileChangeEmailTo(ctx, before.Email, updated.FirstName, changes); err != nil {
				s.Log.Warn("profile change email (old address) failed", "employee_id", updated.ID, "error", err)
			}
		}
	}
	return updated, nil
}

// SetEmployeeActive toggles active. On deactivation, all refresh tokens
// are revoked and session_version is bumped so existing access tokens
// are rejected on next request — including invalidating the gateway's
// Redis cache so revocation is immediate, not bounded by the cache TTL.
// Self-deactivation is rejected (an admin cannot lock themselves out).
func (s *Service) SetEmployeeActive(ctx context.Context, id string, active bool) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.EmployeeWrite); err != nil {
		return nil, err
	}
	if !active {
		if err := s.guardSelf(ctx, id, "deaktivaciju"); err != nil {
			return nil, err
		}
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
		s.invalidateSessionCache(ctx, domain.KindEmployee, id)
	}
	return updated, nil
}

// SetEmployeePermissions replaces the permission set. Bumps
// session_version (handled by store) so existing tokens revalidate;
// invalidates the gateway's session cache for immediate effect.
// Requires PermissionGrant; granting admin additionally requires Admin.
// Self-edit is rejected — see guardSelf.
func (s *Service) SetEmployeePermissions(ctx context.Context, id string, perms []string) (*domain.Employee, error) {
	if err := s.requirePermission(ctx, permissions.PermissionGrant); err != nil {
		return nil, err
	}
	if err := s.guardSelf(ctx, id, "permisije"); err != nil {
		return nil, err
	}
	if permissions.Has(perms, permissions.Admin) {
		if err := s.requirePermission(ctx, permissions.Admin); err != nil {
			return nil, apperr.PermissionDenied("samo administrator može dodeliti admin permisiju")
		}
	}
	target, err := s.Store.GetEmployeeByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.guardAdminOnAdmin(ctx, target); err != nil {
		return nil, err
	}
	// When funds.manage.supervisor is being revoked, reassign the
	// demoted employee's funds to the acting admin BEFORE
	// persisting the new permission set, so no fund can ever be left
	// without a manager. If the trading-side cascade fails we abort the
	// permission write entirely — partial state would be worse than no
	// change.
	losingFundsManager := permissions.Has(target.Permissions, permissions.FundsManageSupervisor) &&
		!permissions.Has(perms, permissions.FundsManageSupervisor)
	if losingFundsManager && s.FundReassigner != nil {
		caller, ok := auth.PrincipalFrom(ctx)
		if !ok {
			return nil, apperr.Unauthenticated("not authenticated")
		}
		n, err := s.FundReassigner.Reassign(ctx, id, caller.UserID)
		if err != nil {
			return nil, apperr.Internal("reassign supervisor assets", err)
		}
		if n > 0 && s.Notifier != nil && target.Email != "" {
			subject := "Fondovi su preusmereni"
			body := "Poštovani,\n\n" +
				"Pošto Vam je oduzeta uloga supervizora fondova, " +
				"upravljanje Vašim postojećim fondovima je preneto na drugog supervizora.\n\n" +
				"Banka 3"
			if err := s.Notifier.Send(ctx, target.Email, subject, body, false); err != nil {
				s.Log.Warn("send handover email failed", "to", target.Email, "err", err.Error())
			}
		}
	} else if losingFundsManager && s.FundReassigner == nil {
		s.Log.Warn("funds.manage.supervisor revoked but FundReassigner not wired — funds may be orphaned",
			"user_id", id)
	}
	updated, err := s.Store.SetEmployeePermissions(ctx, id, perms)
	if err != nil {
		return nil, err
	}
	s.invalidateSessionCache(ctx, domain.KindEmployee, id)
	return updated, nil
}

// invalidateSessionCache removes the gateway's cached session_version
// entry for the user. Best-effort: failures are logged but not surfaced
// — the cache will fall back to the user service on next lookup, so the
// worst case is one stale read inside the cache TTL.
func (s *Service) invalidateSessionCache(ctx context.Context, kind domain.UserKind, id string) {
	if s.Redis == nil {
		return
	}
	if err := s.Redis.Del(ctx, "usv:"+string(kind)+":"+id).Err(); err != nil {
		s.Log.Warn("invalidate session cache", "user_id", id, "error", err)
	}
}

// guardSelf rejects an action that targets the caller. Used to keep
// admins from locking themselves out by stripping their own permissions
// or deactivating their own account — there's nobody else to undo it
// since admin is sole-maintainer (CLAUDE.md).
func (s *Service) guardSelf(ctx context.Context, targetID, action string) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if p.UserID == targetID {
		return apperr.PermissionDenied("ne možete promeniti " + action + " sopstvenog naloga")
	}
	return nil
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
	if !emailRe.MatchString(strings.TrimSpace(in.Email)) {
		return apperr.Validation("email format is invalid")
	}
	if !phoneRe.MatchString(strings.TrimSpace(in.Phone)) {
		return apperr.Validation("phone format is invalid (expected +<digits> or <digits>)")
	}
	if !in.DateOfBirth.Before(time.Now()) {
		return apperr.Validation("date of birth must be in the past")
	}
	return nil
}

// applyEmployeePatch copies the editable fields from in onto e. Spec p.8
// marks Username and Datum rođenja as "Ne menja se" — those fields are
// silently ignored even if the proto carries them, so a stale or
// malicious client cannot rewrite them post-create.
func applyEmployeePatch(e *domain.Employee, in UpdateEmployeeInput) {
	if in.Email != "" {
		e.Email = strings.ToLower(strings.TrimSpace(in.Email))
	}
	if in.FirstName != "" {
		e.FirstName = strings.TrimSpace(in.FirstName)
	}
	if in.LastName != "" {
		e.LastName = strings.TrimSpace(in.LastName)
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

// diffEmployee returns a list of "Polje: stara → nova" lines for fields
// that changed. Only fields the admin can edit are compared.
func diffEmployee(before, after *domain.Employee) []string {
	var changes []string
	add := func(label, oldV, newV string) {
		if oldV != newV {
			changes = append(changes, label+": "+oldV+" → "+newV)
		}
	}
	add("Email", before.Email, after.Email)
	add("Korisničko ime", before.Username, after.Username)
	add("Ime", before.FirstName, after.FirstName)
	add("Prezime", before.LastName, after.LastName)
	if !before.DateOfBirth.Equal(after.DateOfBirth) {
		changes = append(changes,
			"Datum rođenja: "+before.DateOfBirth.Format("2006-01-02")+" → "+after.DateOfBirth.Format("2006-01-02"))
	}
	add("Pol", string(before.Gender), string(after.Gender))
	add("Telefon", before.Phone, after.Phone)
	add("Adresa", before.Address, after.Address)
	add("Pozicija", before.Position, after.Position)
	add("Departman", before.Department, after.Department)
	return changes
}

func (s *Service) sendProfileChangeEmail(ctx context.Context, e *domain.Employee, changes []string) error {
	return s.sendProfileChangeEmailTo(ctx, e.Email, e.FirstName, changes)
}

func (s *Service) sendProfileChangeEmailTo(ctx context.Context, to, firstName string, changes []string) error {
	subject := "Izmena podataka naloga – Banka 3"
	body := "Poštovani " + firstName + ",\n\n" +
		"administrator je ažurirao podatke vašeg naloga. Promenjeno je:\n\n" +
		"  - " + strings.Join(changes, "\n  - ") + "\n\n" +
		"Ako ove izmene nisu očekivane, molimo kontaktirajte podršku.\n\n" +
		"– Banka 3"
	return s.Notifier.Send(ctx, to, subject, body, false)
}

func (s *Service) sendActivationEmail(ctx context.Context, e *domain.Employee) error {
	plaintext, hash, err := tokens.Generate(32)
	if err != nil {
		return err
	}
	if err := s.Store.CreateActivationToken(ctx, e.ID, hash, s.Clock.Now().Add(s.Cfg.ActivationTTL)); err != nil {
		return err
	}
	link := s.Cfg.WebBaseURL + "/activate?token=" + plaintext
	subject := "Aktivacija naloga – Banka 3"
	body := "Poštovani " + e.FirstName + ",\n\n" +
		"vaš nalog u sistemu Banke 3 je kreiran. Da biste ga aktivirali i postavili lozinku, " +
		"otvorite sledeći link u narednih 24 sata:\n\n" + link + "\n\n" +
		"Ako niste očekivali ovu poruku, molimo ignorišite je.\n\n" +
		"– Banka 3"
	return s.Notifier.Send(ctx, e.Email, subject, body, false)
}
