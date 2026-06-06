package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/bizmetric"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/passwords"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/tokens"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// Brute-force login lockout (todoSpec S7–S11): after maxFailedLogins
// consecutive failed attempts the account is locked for lockoutDuration.
// Applies to both employees and clients.
const (
	maxFailedLogins = 5
	lockoutDuration = 15 * time.Minute
)

// LoginOption tweaks token issuance. Variadic so the existing
// (ctx,email,password) / (ctx,token) call sites — including the c1
// test suite — stay source-compatible; only the mobile path passes one.
type LoginOption func(*sessionOpts)

type sessionOpts struct{ longLived bool }

// LongLived makes the issued refresh token long-lived (mobile, spec
// p.84 "no session interval"). Web logins omit it → normal RefreshTTL.
func LongLived() LoginOption { return func(o *sessionOpts) { o.longLived = true } }

func resolveOpts(opts []LoginOption) sessionOpts {
	var s sessionOpts
	for _, o := range opts {
		o(&s)
	}
	return s
}

// LoginResult is what Login returns. Server layer maps to proto.
type LoginResult struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresIn  time.Duration
	RefreshExpiresIn time.Duration
	UserKind         domain.UserKind
	UserID           string
	Permissions      []string
	FirstName        string
	LastName         string
}

// Login authenticates by email + password against employees first, then
// clients.
//
// Both "email not found" and "wrong password" return the same
// "Neispravni kredencijali" message so an attacker can't probe for
// valid email addresses by observing different error responses. The
// spec calls out the wrong-password copy explicitly (E2E p.4) and we
// apply it uniformly to the unknown-email path too.
func (s *Service) Login(ctx context.Context, email, password string, opts ...LoginOption) (res *LoginResult, err error) {
	// banka_user_logins_total — emit on every return so the business
	// dashboard sees the success/failure mix. Reason mapping is in
	// loginResult below; keep it stable, the dashboard colorises by it.
	defer func() { bizmetric.UserLogin(ctx, loginResult(err)) }()

	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		s.Log.WarnContext(ctx, "login rejected: missing credentials")
		return nil, apperr.Validation("email and password are required")
	}
	longLived := resolveOpts(opts).longLived

	emp, err := s.Store.GetEmployeeByEmail(ctx, email)
	if err == nil {
		return s.completeLogin(ctx, emp.ID, emp.Email, domain.KindEmployee, emp.PasswordHash, emp.Active, emp.Activated(), emp.Permissions, emp.SessionVersion, emp.FirstName, emp.LastName, emp.FailedLoginAttempts, emp.LockedUntil, password, longLived)
	}
	if !isNotFound(err) {
		return nil, err
	}

	cl, err := s.Store.GetClientByEmail(ctx, email)
	if err == nil {
		return s.completeLogin(ctx, cl.ID, cl.Email, domain.KindClient, cl.PasswordHash, cl.Active, cl.PasswordHash != "", cl.Permissions, cl.SessionVersion, cl.FirstName, cl.LastName, cl.FailedLoginAttempts, cl.LockedUntil, password, longLived)
	}
	if !isNotFound(err) {
		return nil, err
	}

	// Email not in the system: return the same "Neispravni kredencijali"
	// the wrong-password path uses. See the doc comment above for why we
	// don't surface "Korisnik ne postoji" anymore.
	return nil, apperr.Unauthenticated("Neispravni kredencijali")
}

func (s *Service) completeLogin(
	ctx context.Context,
	userID, email string,
	kind domain.UserKind,
	passwordHash string,
	active, activated bool,
	perms []string,
	sessionVersion int64,
	firstName, lastName string,
	failedAttempts int,
	lockedUntil *time.Time,
	password string,
	longLived bool,
) (*LoginResult, error) {
	if !active {
		s.Log.WarnContext(ctx, "login rejected: account disabled", "user_id", userID, "kind", kind)
		return nil, apperr.PermissionDenied("nalog je deaktiviran")
	}
	if !activated {
		s.Log.WarnContext(ctx, "login rejected: account not activated", "user_id", userID, "kind", kind)
		return nil, apperr.FailedPrecondition("nalog nije aktiviran")
	}
	// Brute-force lockout (S7–S8): refuse a still-locked account before
	// even checking the password.
	if lockedUntil != nil && lockedUntil.After(s.Clock.Now()) {
		return nil, apperr.PermissionDenied("Nalog je privremeno zaključan zbog previše neuspešnih pokušaja. Pokušajte ponovo kasnije.")
	}
	ok, err := passwords.Verify(password, passwordHash)
	if err != nil || !ok {
		s.Log.WarnContext(ctx, "login rejected: bad password", "user_id", userID, "kind", kind)
		// Wrong password: bump the counter and lock once the threshold
		// is reached (S7–S8).
		newCount, _ := s.Store.IncrementFailedLogin(ctx, kind, userID)
		if newCount >= maxFailedLogins {
			if lerr := s.Store.LockUser(ctx, kind, userID, s.Clock.Now().Add(lockoutDuration)); lerr != nil {
				s.Log.Warn("lock user failed", "user_kind", string(kind), "user_id", userID, "error", lerr)
			}
			s.sendLockoutEmail(ctx, email, firstName)
			return nil, apperr.PermissionDenied("Nalog je zaključan zbog previše neuspešnih pokušaja prijave. Proverite email za uputstvo o resetovanju lozinke.")
		}
		return nil, apperr.Unauthenticated("Neispravni kredencijali")
	}
	// Successful login clears any accumulated failures / lock (S9, S11).
	if failedAttempts > 0 || lockedUntil != nil {
		if rerr := s.Store.ResetFailedLogin(ctx, kind, userID); rerr != nil {
			s.Log.Warn("reset failed login failed", "user_kind", string(kind), "user_id", userID, "error", rerr)
		}
	}
	r, err := s.issueTokens(ctx, kind, userID, perms, sessionVersion, longLived)
	if err != nil {
		return nil, err
	}
	r.FirstName = firstName
	r.LastName = lastName
	return r, nil
}

// issueTokens signs a fresh access JWT and creates a refresh token row.
func (s *Service) issueTokens(ctx context.Context, kind domain.UserKind, userID string, perms []string, sv int64, longLived bool) (*LoginResult, error) {
	access, err := auth.Sign(auth.Claims{
		UserID:         userID,
		UserKind:       auth.UserKind(kind),
		Permissions:    perms,
		SessionVersion: sv,
	}, s.Cfg.JWTSigningKey, s.Cfg.AccessTTL)
	if err != nil {
		return nil, apperr.Internal("sign access", err)
	}

	refreshPlain, refreshHash, err := tokens.Generate(32)
	if err != nil {
		return nil, apperr.Internal("generate refresh", err)
	}
	// Mobile (spec p.84 "no session interval") gets the long-lived
	// lifetime; web keeps the standard RefreshTTL. The access token
	// stays short-lived in both cases — only the refresh window grows.
	refreshTTL := s.Cfg.RefreshTTL
	if longLived {
		refreshTTL = s.Cfg.MobileRefreshTTL
	}
	if err := s.Store.CreateRefreshToken(ctx, kind, userID, refreshHash, s.Clock.Now().Add(refreshTTL)); err != nil {
		return nil, err
	}

	return &LoginResult{
		AccessToken:      access,
		RefreshToken:     refreshPlain,
		AccessExpiresIn:  s.Cfg.AccessTTL,
		RefreshExpiresIn: refreshTTL,
		UserKind:         kind,
		UserID:           userID,
		Permissions:      perms,
	}, nil
}

// sendLockoutEmail notifies the user (Serbian) that their account was
// locked after too many failed login attempts (S8). Best-effort: a nil
// notifier or a send error never fails the login flow.
func (s *Service) sendLockoutEmail(ctx context.Context, to, firstName string) {
	if s.Notifier == nil {
		return
	}
	subject := "Nalog je privremeno zaključan"
	body := "Poštovani " + firstName + ",\n\n" +
		"vaš nalog je privremeno zaključan jer je zabeleženo previše neuspešnih " +
		"pokušaja prijave. Nalog će se automatski otključati nakon 15 minuta.\n\n" +
		"Ako želite, već sada možete resetovati lozinku na sledećem linku:\n\n" +
		s.Cfg.WebBaseURL + "/password-reset\n\n" +
		"Ako niste vi pokušavali da se prijavite, preporučujemo da odmah resetujete " +
		"lozinku i kontaktirate podršku.\n\n" +
		"– Banka 3"
	if err := s.Notifier.Send(ctx, to, subject, body, false); err != nil {
		s.Log.Warn("lockout email failed", "to", to, "error", err)
	}
}

// Refresh rotates the refresh token. The presented one is revoked
// immediately on use; if it's already revoked or expired we reject.
func (s *Service) Refresh(ctx context.Context, refreshPlain string, opts ...LoginOption) (*LoginResult, error) {
	if refreshPlain == "" {
		return nil, apperr.Unauthenticated("missing refresh token")
	}
	hash := tokens.Hash(refreshPlain)
	kind, userID, err := s.Store.LookupRefreshToken(ctx, hash)
	if err != nil {
		return nil, err
	}
	// Look up current permissions and session_version (they may have
	// changed since the access token was issued).
	perms, sv, active, err := s.lookupAuthState(ctx, kind, userID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, apperr.PermissionDenied("nalog je deaktiviran")
	}
	// Rotate: revoke old, issue new.
	if err := s.Store.RevokeRefreshToken(ctx, hash); err != nil {
		return nil, err
	}
	return s.issueTokens(ctx, kind, userID, perms, sv, resolveOpts(opts).longLived)
}

// Logout revokes one refresh token. Idempotent.
func (s *Service) Logout(ctx context.Context, refreshPlain string) error {
	if refreshPlain == "" {
		return nil
	}
	return s.Store.RevokeRefreshToken(ctx, tokens.Hash(refreshPlain))
}

// MeResult is the authenticated user's view of itself. Exactly one of
// Employee / Client is set.
type MeResult struct {
	Employee *domain.Employee
	Client   *domain.Client
}

// Me returns the authenticated principal's full profile. Reads the
// principal from ctx (set by the gateway middleware).
func (s *Service) Me(ctx context.Context) (*MeResult, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, apperr.Unauthenticated("not authenticated")
	}
	switch p.UserKind {
	case auth.KindEmployee:
		e, err := s.Store.GetEmployeeByID(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		return &MeResult{Employee: e}, nil
	case auth.KindClient:
		c, err := s.Store.GetClientByID(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		return &MeResult{Client: c}, nil
	default:
		return nil, apperr.Internal("unknown user kind", nil)
	}
}

func (s *Service) lookupAuthState(ctx context.Context, kind domain.UserKind, userID string) (perms []string, sv int64, active bool, err error) {
	switch kind {
	case domain.KindEmployee:
		e, err := s.Store.GetEmployeeByID(ctx, userID)
		if err != nil {
			return nil, 0, false, err
		}
		return e.Permissions, e.SessionVersion, e.Active, nil
	case domain.KindClient:
		c, err := s.Store.GetClientByID(ctx, userID)
		if err != nil {
			return nil, 0, false, err
		}
		return c.Permissions, c.SessionVersion, c.Active, nil
	}
	return nil, 0, false, apperr.Internal("unknown user kind", nil)
}

// loginResult maps a Login() return into a stable label for the
// banka_user_logins_total counter. The label set is small and shared
// with the business dashboard's colour overrides.
func loginResult(err error) string {
	if err == nil {
		return "ok"
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return "internal"
	}
	switch ae.Kind {
	case apperr.KindUnauthenticated:
		return "bad_password" // includes "email not found" — indistinguishable by design
	case apperr.KindValidation:
		return "validation"
	case apperr.KindPermissionDenied:
		return "disabled"
	case apperr.KindFailedPrecondition:
		return "not_activated"
	default:
		return "internal"
	}
}

func isNotFound(err error) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Kind == apperr.KindNotFound
}
