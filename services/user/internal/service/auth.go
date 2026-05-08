package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/passwords"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/tokens"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// LoginResult is what Login returns. Server layer maps to proto.
type LoginResult struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresIn  time.Duration
	RefreshExpiresIn time.Duration
	UserKind         domain.UserKind
	UserID           string
	Permissions      []string
}

// Login authenticates by email + password against employees first, then
// clients. Wrong-password attempts are counted; after loginMaxFailures
// in loginFailureWindow, the account is locked for loginLockDuration.
//
// Spec scenarios (PDF + E2E):
//   - "Korisnik ne postoji" when the email isn't in the system.
//   - "Neispravni kredencijali" when the email exists but the password is wrong.
//   - Lock message after 3 strikes; subsequent attempts return it directly.
func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, apperr.Validation("email and password are required")
	}

	if locked, retry, err := s.isLoginLocked(ctx, email); err == nil && locked {
		return nil, apperr.PermissionDenied(formatLockMessage(retry))
	}

	emp, err := s.Store.GetEmployeeByEmail(ctx, email)
	if err == nil {
		return s.completeLogin(ctx, emp.ID, emp.Email, domain.KindEmployee, emp.PasswordHash, emp.Active, emp.Activated(), emp.Permissions, emp.SessionVersion, password)
	}
	if !isNotFound(err) {
		return nil, err
	}

	cl, err := s.Store.GetClientByEmail(ctx, email)
	if err == nil {
		return s.completeLogin(ctx, cl.ID, cl.Email, domain.KindClient, cl.PasswordHash, cl.Active, cl.PasswordHash != "", cl.Permissions, cl.SessionVersion, password)
	}
	if !isNotFound(err) {
		return nil, err
	}

	// Spec wants this exact message — leak that the user doesn't exist.
	// In a real bank we'd avoid email enumeration, but the test scenario
	// is explicit about "Korisnik ne postoji". Unknown emails don't count
	// toward the lockout: they have no password to be wrong against.
	return nil, apperr.NotFound("Korisnik ne postoji")
}

func (s *Service) completeLogin(
	ctx context.Context,
	userID, email string,
	kind domain.UserKind,
	passwordHash string,
	active, activated bool,
	perms []string,
	sessionVersion int64,
	password string,
) (*LoginResult, error) {
	if !active {
		return nil, apperr.PermissionDenied("nalog je deaktiviran")
	}
	if !activated {
		return nil, apperr.FailedPrecondition("nalog nije aktiviran")
	}
	ok, err := passwords.Verify(password, passwordHash)
	if err != nil || !ok {
		s.recordLoginFailure(ctx, email)
		if locked, retry, lerr := s.isLoginLocked(ctx, email); lerr == nil && locked {
			return nil, apperr.PermissionDenied(formatLockMessage(retry))
		}
		return nil, apperr.Unauthenticated("Neispravni kredencijali")
	}
	s.clearLoginFailures(ctx, email)
	return s.issueTokens(ctx, kind, userID, perms, sessionVersion)
}

// issueTokens signs a fresh access JWT and creates a refresh token row.
func (s *Service) issueTokens(ctx context.Context, kind domain.UserKind, userID string, perms []string, sv int64) (*LoginResult, error) {
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
	if err := s.Store.CreateRefreshToken(ctx, kind, userID, refreshHash, s.Clock.Now().Add(s.Cfg.RefreshTTL)); err != nil {
		return nil, err
	}

	return &LoginResult{
		AccessToken:      access,
		RefreshToken:     refreshPlain,
		AccessExpiresIn:  s.Cfg.AccessTTL,
		RefreshExpiresIn: s.Cfg.RefreshTTL,
		UserKind:         kind,
		UserID:           userID,
		Permissions:      perms,
	}, nil
}

// Refresh rotates the refresh token. The presented one is revoked
// immediately on use; if it's already revoked or expired we reject.
func (s *Service) Refresh(ctx context.Context, refreshPlain string) (*LoginResult, error) {
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
	return s.issueTokens(ctx, kind, userID, perms, sv)
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

func isNotFound(err error) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Kind == apperr.KindNotFound
}
