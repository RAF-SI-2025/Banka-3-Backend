package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/passwords"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/tokens"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// ActivateAccount consumes an activation token and sets the employee's
// password. Token-not-found / expired / used surface as distinct
// FailedPrecondition messages so the gateway can show the right copy.
// Spec p.10: "Nakon toga dobija confirmation mail."
func (s *Service) ActivateAccount(ctx context.Context, token, newPassword string) error {
	if token == "" {
		return apperr.Validation("token is required")
	}
	if err := passwords.ValidateComplexity(newPassword); err != nil {
		return apperr.Validation(err.Error())
	}

	hash := tokens.Hash(token)
	employeeID, err := s.Store.LookupActivationToken(ctx, hash)
	if err != nil {
		// Used/expired tokens and query errors are logged by the store;
		// an unknown token value is only classified here.
		if isNotFound(err) {
			s.Log.WarnContext(ctx, "activation rejected: unknown token")
		}
		return err
	}

	pwHash, err := passwords.Hash(newPassword)
	if err != nil {
		s.Log.ErrorContext(ctx, "password hash failed", "err", err, "employee_id", employeeID)
		return apperr.Internal("hash password", err)
	}
	if err := s.Store.SetEmployeePasswordHash(ctx, employeeID, pwHash); err != nil {
		return err
	}
	if err := s.Store.MarkActivationTokenUsed(ctx, hash); err != nil {
		return err
	}
	s.Log.InfoContext(ctx, "account activated", "employee_id", employeeID)

	emp, err := s.Store.GetEmployeeByID(ctx, employeeID)
	if err != nil {
		// Activation already succeeded; failing the confirmation email
		// shouldn't roll it back. Log and move on.
		s.Log.WarnContext(ctx, "activation confirmation: load employee failed", "err", err, "employee_id", employeeID)
		return nil
	}
	if err := s.sendActivationConfirmation(ctx, emp); err != nil {
		s.Log.WarnContext(ctx, "activation confirmation email failed", "err", err, "employee_id", employeeID)
	}
	return nil
}

func (s *Service) sendActivationConfirmation(ctx context.Context, e *domain.Employee) error {
	subject := "Nalog je aktiviran – Banka 3"
	body := "Poštovani " + e.FirstName + ",\n\n" +
		"vaš nalog u sistemu Banke 3 je uspešno aktiviran. Od sada se možete " +
		"prijaviti na " + s.Cfg.WebBaseURL + "/login svojom email adresom i novom lozinkom.\n\n" +
		"Ako niste vi izvršili aktivaciju, odmah kontaktirajte podršku.\n\n" +
		"– Banka 3"
	return s.Notifier.Send(ctx, e.Email, subject, body, false)
}

// ResendActivation generates a fresh activation token and emails it,
// invalidating any previously outstanding ones for the employee.
// Permission: employee.write (admin / supervisor that manages staff).
func (s *Service) ResendActivation(ctx context.Context, employeeID string) error {
	if err := s.requirePermission(ctx, permissions.EmployeeWrite); err != nil {
		return err
	}
	emp, err := s.Store.GetEmployeeByID(ctx, employeeID)
	if err != nil {
		return err
	}
	if emp.Activated() {
		s.Log.WarnContext(ctx, "resend activation rejected: already activated", "employee_id", employeeID)
		return apperr.FailedPrecondition("nalog je već aktiviran")
	}
	if err := s.Store.InvalidateActivationTokens(ctx, employeeID); err != nil {
		return err
	}
	if err := s.sendActivationEmail(ctx, emp); err != nil {
		s.Log.ErrorContext(ctx, "activation email send failed", "err", err, "employee_id", employeeID)
		return err
	}
	return nil
}

// RequestPasswordReset emails a reset link if the email belongs to a
// real user. Returns nil even when the email is unknown so callers
// can't probe for accounts. (We bend the spec slightly: spec scenario 4
// just says the reset email is sent for a valid email.)
//
// Email-send failures (SMTP rate limits, transient notification-svc
// errors) are logged and swallowed so the FE still sees the
// "Link važi 15 minuta" success state — the reset token row is already
// persisted, the operator can rebroadcast it later. Surfacing the SMTP
// error to the FE would also leak account existence (the unknown-email
// branch always returns nil).
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if email == "" {
		return apperr.Validation("email is required")
	}

	if emp, err := s.Store.GetEmployeeByEmail(ctx, email); err == nil {
		if serr := s.sendResetEmail(ctx, domain.KindEmployee, emp.ID, emp.Email, emp.FirstName); serr != nil {
			s.Log.WarnContext(ctx, "password reset email failed", "err", serr, "user_kind", "employee", "user_id", emp.ID)
		}
		return nil
	} else if !isNotFound(err) {
		return err
	}
	if cl, err := s.Store.GetClientByEmail(ctx, email); err == nil {
		if serr := s.sendResetEmail(ctx, domain.KindClient, cl.ID, cl.Email, cl.FirstName); serr != nil {
			s.Log.WarnContext(ctx, "password reset email failed", "err", serr, "user_kind", "client", "user_id", cl.ID)
		}
		return nil
	} else if !isNotFound(err) {
		return err
	}
	// Unknown email — silently succeed toward the caller; logged as a
	// probe signal.
	s.Log.WarnContext(ctx, "password reset requested for unknown email", "email", email)
	return nil
}

// ConfirmPasswordReset consumes the token and sets a new password.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, newPassword string) error {
	if token == "" {
		return apperr.Validation("token is required")
	}
	if err := passwords.ValidateComplexity(newPassword); err != nil {
		return apperr.Validation(err.Error())
	}

	hash := tokens.Hash(token)
	kind, userID, err := s.Store.LookupPasswordResetToken(ctx, hash)
	if err != nil {
		// Used/expired tokens and query errors are logged by the store;
		// an unknown token value is only classified here.
		if isNotFound(err) {
			s.Log.WarnContext(ctx, "password reset rejected: unknown token")
		}
		return err
	}

	pwHash, err := passwords.Hash(newPassword)
	if err != nil {
		s.Log.ErrorContext(ctx, "password hash failed", "err", err, "user_id", userID, "kind", kind)
		return apperr.Internal("hash password", err)
	}

	switch kind {
	case domain.KindEmployee:
		if err := s.Store.SetEmployeePasswordHash(ctx, userID, pwHash); err != nil {
			return err
		}
	case domain.KindClient:
		if err := s.Store.SetClientPasswordHash(ctx, userID, pwHash); err != nil {
			return err
		}
	default:
		s.Log.ErrorContext(ctx, "password reset failed: unknown user kind", "user_id", userID, "kind", kind)
		return apperr.Internal("unknown user kind", nil)
	}

	if err := s.Store.MarkPasswordResetTokenUsed(ctx, hash); err != nil {
		return err
	}
	if err := s.Store.RevokeAllRefreshTokens(ctx, kind, userID); err != nil {
		return err
	}
	// Resetting the password clears any brute-force lockout (S10).
	if err := s.Store.ResetFailedLogin(ctx, kind, userID); err != nil {
		s.Log.WarnContext(ctx, "reset failed login on password reset failed", "err", err, "user_kind", string(kind), "user_id", userID)
	}
	s.Log.InfoContext(ctx, "password reset completed", "user_id", userID, "kind", kind)
	return nil
}

// sendInitialPasswordEmail mints a reset token and emails a welcome
// link. Used when an employee creates a new Klijent (spec p.9): the
// client doesn't get a "forgot password" message but a "set your
// password" one. Mechanically identical to the reset flow — the token
// goes through password_reset_tokens and the same /password-reset/confirm
// page consumes it.
func (s *Service) sendInitialPasswordEmail(ctx context.Context, kind domain.UserKind, userID, email, firstName string) error {
	plaintext, hash, err := tokens.Generate(32)
	if err != nil {
		s.Log.ErrorContext(ctx, "initial-password token generate failed", "err", err, "user_id", userID, "kind", kind)
		return err
	}
	if err := s.Store.CreatePasswordResetToken(ctx, kind, userID, hash, s.Clock.Now().Add(s.Cfg.ResetTTL)); err != nil {
		return err
	}
	link := s.Cfg.WebBaseURL + "/password-reset/confirm?token=" + plaintext
	subject := "Dobrodošli u Banku 3 – postavite lozinku"
	body := "Poštovani " + firstName + ",\n\n" +
		"vaš nalog u sistemu Banke 3 je kreiran. Da biste postavili lozinku i " +
		"prvi put se prijavili, otvorite sledeći link u narednih 15 minuta:\n\n" +
		link + "\n\n" +
		"Ako niste očekivali ovu poruku, molimo kontaktirajte podršku.\n\n" +
		"– Banka 3"
	return s.Notifier.Send(ctx, email, subject, body, false)
}

func (s *Service) sendResetEmail(ctx context.Context, kind domain.UserKind, userID, email, firstName string) error {
	plaintext, hash, err := tokens.Generate(32)
	if err != nil {
		s.Log.ErrorContext(ctx, "reset token generate failed", "err", err, "user_id", userID, "kind", kind)
		return err
	}
	if err := s.Store.CreatePasswordResetToken(ctx, kind, userID, hash, s.Clock.Now().Add(s.Cfg.ResetTTL)); err != nil {
		return err
	}
	link := s.Cfg.WebBaseURL + "/password-reset/confirm?token=" + plaintext
	subject := "Reset lozinke – Banka 3"
	body := "Poštovani " + firstName + ",\n\n" +
		"primili smo zahtev za reset lozinke. Otvorite sledeći link u narednih 15 minuta " +
		"da postavite novu lozinku:\n\n" + link + "\n\n" +
		"Ako niste vi tražili reset, molimo ignorišite ovu poruku.\n\n" +
		"– Banka 3"
	return s.Notifier.Send(ctx, email, subject, body, false)
}
