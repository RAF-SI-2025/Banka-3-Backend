//go:build integration

// Package service integration tests exercise the user service against
// real Postgres + Redis. They are gated behind the `integration` build
// tag so `go test ./...` stays fast.
//
// Run with the local compose stack up:
//
//	task up CELINA=c1
//	task test:integration
//
// Override INTEGRATION_DATABASE_URL / INTEGRATION_REDIS_ADDR /
// INTEGRATION_REDIS_PASSWORD if your dev stack uses non-default ports.
//
// Each top-level Test* helper resets the user schema before running so
// tests are independent. Schema reset is a TRUNCATE of every c1 table —
// not a DROP — so any concurrent migrations stay intact.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/passwords"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/store"
)

// =====================================================================
// Shared fixture
// =====================================================================

var (
	fixOnce sync.Once
	fixPool *pgxpool.Pool
	fixRDB  *redis.Client
	fixSkip string
)

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// setup connects (lazily) to Postgres and Redis. Returns a skip reason
// if the stack isn't reachable so tests are skipped rather than failed
// when run outside the dev compose.
func setup(t *testing.T) (*Service, *spyNotifier) {
	t.Helper()
	fixOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		dbURL := envOr("INTEGRATION_DATABASE_URL", "postgres://banka:banka@localhost:5432/banka?sslmode=disable")
		redisAddr := envOr("INTEGRATION_REDIS_ADDR", "localhost:6379")
		redisPW := envOr("INTEGRATION_REDIS_PASSWORD", "banka")

		pool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			fixSkip = fmt.Sprintf("postgres connect: %v", err)
			return
		}
		if err := pool.Ping(ctx); err != nil {
			fixSkip = fmt.Sprintf("postgres ping: %v", err)
			return
		}
		// Verify the c1 schema exists. If migrations haven't been
		// applied, skip with a useful message rather than fail.
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from information_schema.tables where table_schema='user' and table_name='employees'`).Scan(&n); err != nil || n == 0 {
			fixSkip = "user.employees missing — run migrations first (task migrate)"
			return
		}

		rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPW})
		if err := rdb.Ping(ctx).Err(); err != nil {
			fixSkip = fmt.Sprintf("redis ping: %v", err)
			return
		}

		fixPool = pool
		fixRDB = rdb
	})
	if fixSkip != "" {
		t.Skipf("integration stack unavailable: %s", fixSkip)
	}

	resetSchema(t)
	flushRedis(t)

	st := store.New(fixPool)
	notif := &spyNotifier{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := New(st, notif, fixRDB, Config{
		JWTSigningKey: []byte("integration-test-key-not-for-prod"),
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    24 * time.Hour,
		ActivationTTL: time.Hour,
		ResetTTL:      15 * time.Minute,
		WebBaseURL:    "http://localhost:5173",
	}, logger)
	return svc, notif
}

func resetSchema(t *testing.T) {
	t.Helper()
	_, err := fixPool.Exec(context.Background(), `
        truncate
            "user".employees,
            "user".clients,
            "user".refresh_tokens,
            "user".activation_tokens,
            "user".password_reset_tokens
        restart identity cascade`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func flushRedis(t *testing.T) {
	t.Helper()
	// Only delete keys we own, not the whole DB — avoid stomping on
	// anything else sharing the dev Redis (e.g. usv:* from a running
	// gateway).
	keys, err := fixRDB.Keys(context.Background(), "login:*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	if len(keys) > 0 {
		if err := fixRDB.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatalf("redis del: %v", err)
		}
	}
}

// adminCtx returns a context carrying an admin principal, which the
// service's permission checks will accept for any operation.
func adminCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: append([]string{}, permissions.RoleEmployeeAdmin...),
	})
}

func principalCtx(id string, kind auth.UserKind, perms []string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    kind,
		Permissions: perms,
	})
}

type spyNotifier struct {
	mu  sync.Mutex
	out []sentEmail
}
type sentEmail struct{ To, Subject, Body string }

func (s *spyNotifier) Send(_ context.Context, to, subject, body string, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, sentEmail{to, subject, body})
	return nil
}
func (s *spyNotifier) sentTo(addr string) []sentEmail {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sentEmail
	for _, e := range s.out {
		if e.To == addr {
			out = append(out, e)
		}
	}
	return out
}

// makeAdmin inserts a hashed-password admin directly so tests can call
// CreateEmployee under that admin's principal.
func makeAdmin(t *testing.T, svc *Service) *domain.Employee {
	t.Helper()
	hash, err := passwords.Hash("Admin123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Insert via SQL because CreateEmployee requires an admin principal.
	const q = `
        insert into "user".employees (
            email, username, password_hash,
            first_name, last_name, date_of_birth, gender, phone, address,
            position, department, active, permissions
        ) values (
            'admin@banka.local','admin',$1,
            'Admin','Banka 3','1990-01-01','other','+381','Beograd',
            'Administrator','IT', true,
            array['admin','employee.read','employee.write','client.read','client.write','permission.grant']
        ) returning id`
	var id string
	if err := fixPool.QueryRow(context.Background(), q, hash).Scan(&id); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	return &domain.Employee{ID: id, Email: "admin@banka.local"}
}

// =====================================================================
// Authentication
// =====================================================================

func TestIntegration_Login_HappyPath(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)

	r, err := svc.Login(context.Background(), admin.Email, "Admin123!")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if r.UserID != admin.ID {
		t.Errorf("UserID: got %q want %q", r.UserID, admin.ID)
	}
	if r.UserKind != domain.KindEmployee {
		t.Errorf("UserKind: %q", r.UserKind)
	}
	if r.AccessToken == "" || r.RefreshToken == "" {
		t.Error("missing token")
	}
	if !contains(r.Permissions, permissions.Admin) {
		t.Errorf("admin perm missing: %v", r.Permissions)
	}
}

func TestIntegration_Login_WrongPasswordCounts(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)

	for i := 0; i < 2; i++ {
		_, err := svc.Login(context.Background(), admin.Email, "Wrong123")
		if !isApperr(err, apperr.KindUnauthenticated) {
			t.Fatalf("attempt %d: want Unauthenticated, got %v", i, err)
		}
	}
	// Third strike — apperr.PermissionDenied with the lock message.
	_, err := svc.Login(context.Background(), admin.Email, "Wrong123")
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("attempt 3: want PermissionDenied, got %v", err)
	}
	if msg := apperrMsg(err); !contains([]string{msg}, "privremeno zaključan") {
		t.Errorf("unexpected lock message: %q", msg)
	}

	// Even correct password is locked out now.
	_, err = svc.Login(context.Background(), admin.Email, "Admin123!")
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("locked + correct pw: want PermissionDenied, got %v", err)
	}
}

func TestIntegration_Login_SuccessClearsCounter(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)

	// Two wrong attempts, then a correct one.
	for i := 0; i < 2; i++ {
		_, _ = svc.Login(context.Background(), admin.Email, "Wrong123")
	}
	if _, err := svc.Login(context.Background(), admin.Email, "Admin123!"); err != nil {
		t.Fatalf("correct after 2 wrong: %v", err)
	}
	// One more wrong attempt should NOT immediately lock — counter was
	// cleared on success.
	_, err := svc.Login(context.Background(), admin.Email, "Wrong123")
	if !isApperr(err, apperr.KindUnauthenticated) {
		t.Fatalf("post-clear single wrong: want Unauthenticated, got %v", err)
	}
}

func TestIntegration_Login_UnknownUser(t *testing.T) {
	svc, _ := setup(t)
	makeAdmin(t, svc)

	_, err := svc.Login(context.Background(), "nobody@banka.local", "Anything1")
	if !isApperr(err, apperr.KindNotFound) {
		t.Fatalf("want NotFound, got %v", err)
	}
	if msg := apperrMsg(err); msg != "Korisnik ne postoji" {
		t.Errorf("want spec-exact 'Korisnik ne postoji', got %q", msg)
	}
}

func TestIntegration_Login_DeactivatedAccount(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)
	if _, err := fixPool.Exec(context.Background(), `update "user".employees set active=false where id=$1`, admin.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	_, err := svc.Login(context.Background(), admin.Email, "Admin123!")
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

func TestIntegration_RefreshRotation(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)

	first, err := svc.Login(context.Background(), admin.Email, "Admin123!")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	second, err := svc.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh did not rotate the token")
	}
	// Old refresh must now be invalid.
	if _, err := svc.Refresh(context.Background(), first.RefreshToken); !isApperr(err, apperr.KindUnauthenticated) {
		t.Fatalf("old refresh after rotation: want Unauthenticated, got %v", err)
	}
}

// =====================================================================
// Activation
// =====================================================================

func TestIntegration_Activation_Flow(t *testing.T) {
	svc, notif := setup(t)
	admin := makeAdmin(t, svc)

	emp := createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)
	if emp.Activated() {
		t.Fatal("freshly created employee should be unactivated")
	}

	// Pull the activation token out of the DB by hash. Plaintext was
	// embedded in the email body; we can recover via that.
	plaintext := extractToken(t, notif, "marko@banka.local", "/activate?token=")
	if err := svc.ActivateAccount(context.Background(), plaintext, "Marko123"); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// Confirmation email sent.
	confirms := filterSubject(notif.sentTo("marko@banka.local"), "aktiviran")
	if len(confirms) == 0 {
		t.Error("activation confirmation email was not sent")
	}

	// New employee can log in with the new password.
	if _, err := svc.Login(context.Background(), "marko@banka.local", "Marko123"); err != nil {
		t.Fatalf("post-activation login: %v", err)
	}

	// Activation token is single-use.
	err := svc.ActivateAccount(context.Background(), plaintext, "Other123")
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("token reuse: want FailedPrecondition, got %v", err)
	}
}

func TestIntegration_Activation_PasswordTooWeak(t *testing.T) {
	svc, notif := setup(t)
	admin := makeAdmin(t, svc)
	createEmployee(t, svc, admin.ID, "luka@banka.local", "luka", "agent", true)
	plaintext := extractToken(t, notif, "luka@banka.local", "/activate?token=")

	err := svc.ActivateAccount(context.Background(), plaintext, "weak")
	if !isApperr(err, apperr.KindValidation) {
		t.Fatalf("want Validation, got %v", err)
	}
}

// =====================================================================
// Password reset
// =====================================================================

func TestIntegration_ResetFlow(t *testing.T) {
	svc, notif := setup(t)
	admin := makeAdmin(t, svc)

	if err := svc.RequestPasswordReset(context.Background(), admin.Email); err != nil {
		t.Fatalf("request: %v", err)
	}
	plaintext := extractToken(t, notif, admin.Email, "/password-reset/confirm?token=")
	if err := svc.ConfirmPasswordReset(context.Background(), plaintext, "NewAdmin123"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Old password rejected, new accepted.
	if _, err := svc.Login(context.Background(), admin.Email, "Admin123!"); !isApperr(err, apperr.KindUnauthenticated) {
		t.Errorf("old pw still valid: %v", err)
	}
	if _, err := svc.Login(context.Background(), admin.Email, "NewAdmin123"); err != nil {
		t.Errorf("new pw rejected: %v", err)
	}
	// Reset token is single-use.
	err := svc.ConfirmPasswordReset(context.Background(), plaintext, "Other123Pass")
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Errorf("token reuse: want FailedPrecondition, got %v", err)
	}
}

func TestIntegration_ResetUnknownEmailSilentlySucceeds(t *testing.T) {
	svc, notif := setup(t)
	makeAdmin(t, svc)

	// Unknown email should not error (we don't leak existence on reset).
	if err := svc.RequestPasswordReset(context.Background(), "nobody@banka.local"); err != nil {
		t.Errorf("want nil for unknown email, got %v", err)
	}
	if len(notif.sentTo("nobody@banka.local")) != 0 {
		t.Error("no email should have been sent")
	}
}

// =====================================================================
// Employee management
// =====================================================================

func TestIntegration_CreateEmployee_DuplicateEmailRejected(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)
	createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)

	// Same email under a different username — should still conflict.
	_, err := svc.CreateEmployee(adminCtx(admin.ID), CreateEmployeeInput{
		Email: "marko@banka.local", Username: "marko2",
		FirstName: "Marko", LastName: "Marković",
		DateOfBirth: time.Now().AddDate(-30, 0, 0), Gender: domain.GenderMale,
		Phone: "+381645555555", Address: "X", Position: "P", Department: "D", Role: "agent", Active: true,
	})
	if !isApperr(err, apperr.KindConflict) {
		t.Fatalf("want Conflict, got %v", err)
	}
}

func TestIntegration_AdminOnAdminGuard(t *testing.T) {
	svc, _ := setup(t)
	admin1 := makeAdmin(t, svc)

	// Create a second admin via the service so role bundle is correct.
	admin2 := createEmployee(t, svc, admin1.ID, "admin2@banka.local", "admin2", "admin", true)

	// admin1 cannot patch admin2.
	_, err := svc.UpdateEmployee(adminCtx(admin1.ID), UpdateEmployeeInput{ID: admin2.ID, Phone: "+999"})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Errorf("admin update admin: want PermissionDenied, got %v", err)
	}
	// admin1 cannot deactivate admin2.
	_, err = svc.SetEmployeeActive(adminCtx(admin1.ID), admin2.ID, false)
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Errorf("admin deactivate admin: want PermissionDenied, got %v", err)
	}
	// admin1 cannot strip admin2's permissions.
	_, err = svc.SetEmployeePermissions(adminCtx(admin1.ID), admin2.ID, []string{permissions.EmployeeRead})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Errorf("admin set-perms admin: want PermissionDenied, got %v", err)
	}
	// admin1 CAN edit themselves.
	if _, err := svc.UpdateEmployee(adminCtx(admin1.ID), UpdateEmployeeInput{ID: admin1.ID, Phone: "+1"}); err != nil {
		t.Errorf("self-edit must be allowed: %v", err)
	}
}

func TestIntegration_UpdateEmployeeSendsDiffEmail(t *testing.T) {
	svc, notif := setup(t)
	admin := makeAdmin(t, svc)
	emp := createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)

	beforeCount := len(notif.sentTo(emp.Email))
	_, err := svc.UpdateEmployee(adminCtx(admin.ID), UpdateEmployeeInput{
		ID: emp.ID, Phone: "+999", Department: "IT",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	afterCount := len(notif.sentTo(emp.Email))
	if afterCount != beforeCount+1 {
		t.Fatalf("expected one new email; before=%d after=%d", beforeCount, afterCount)
	}
	body := notif.sentTo(emp.Email)[afterCount-1].Body
	for _, want := range []string{"Telefon", "Departman", "+999", "IT"} {
		if !contains([]string{body}, want) {
			t.Errorf("change email missing %q:\n%s", want, body)
		}
	}
}

func TestIntegration_UpdateEmployeeNoopSkipsEmail(t *testing.T) {
	svc, notif := setup(t)
	admin := makeAdmin(t, svc)
	emp := createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)

	beforeCount := len(notif.sentTo(emp.Email))
	if _, err := svc.UpdateEmployee(adminCtx(admin.ID), UpdateEmployeeInput{ID: emp.ID}); err != nil {
		t.Fatalf("noop update: %v", err)
	}
	if got := len(notif.sentTo(emp.Email)); got != beforeCount {
		t.Errorf("expected no new email on noop update, got %d new", got-beforeCount)
	}
}

func TestIntegration_DeactivateRevokesRefreshTokensAndBumpsSV(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)
	emp := createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)
	// Activate marko then log in.
	hash, _ := passwords.Hash("Marko123")
	_, _ = fixPool.Exec(context.Background(), `update "user".employees set password_hash=$2 where id=$1`, emp.ID, hash)

	logged, err := svc.Login(context.Background(), emp.Email, "Marko123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// Read pre-deactivation session_version.
	var svBefore int64
	_ = fixPool.QueryRow(context.Background(), `select session_version from "user".employees where id=$1`, emp.ID).Scan(&svBefore)

	if _, err := svc.SetEmployeeActive(adminCtx(admin.ID), emp.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	var svAfter int64
	_ = fixPool.QueryRow(context.Background(), `select session_version from "user".employees where id=$1`, emp.ID).Scan(&svAfter)
	if svAfter <= svBefore {
		t.Errorf("session_version did not bump on deactivation: %d → %d", svBefore, svAfter)
	}
	// Refresh tokens revoked.
	if _, err := svc.Refresh(context.Background(), logged.RefreshToken); !isApperr(err, apperr.KindUnauthenticated) {
		t.Errorf("refresh after deactivation: want Unauthenticated, got %v", err)
	}
}

func TestIntegration_SetPermissionsBumpsSessionVersion(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)
	emp := createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)

	var svBefore int64
	_ = fixPool.QueryRow(context.Background(), `select session_version from "user".employees where id=$1`, emp.ID).Scan(&svBefore)

	ctx := principalCtx(admin.ID, auth.KindEmployee, []string{permissions.PermissionGrant})
	if _, err := svc.SetEmployeePermissions(ctx, emp.ID, []string{permissions.EmployeeRead}); err != nil {
		t.Fatalf("set perms: %v", err)
	}
	var svAfter int64
	_ = fixPool.QueryRow(context.Background(), `select session_version from "user".employees where id=$1`, emp.ID).Scan(&svAfter)
	if svAfter <= svBefore {
		t.Errorf("session_version did not bump on permission change: %d → %d", svBefore, svAfter)
	}
}

func TestIntegration_PermissionsEnforcement(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)
	emp := createEmployee(t, svc, admin.ID, "marko@banka.local", "marko", "agent", true)

	// Marko (no employee.write) tries to create another employee.
	mctx := principalCtx(emp.ID, auth.KindEmployee, []string{permissions.EmployeeRead, permissions.ClientRead})
	_, err := svc.CreateEmployee(mctx, CreateEmployeeInput{
		Email: "x@x.local", Username: "x", FirstName: "X", LastName: "Y",
		DateOfBirth: time.Now().AddDate(-30, 0, 0), Gender: domain.GenderMale,
		Phone: "+381645555555", Address: "X", Position: "P", Department: "D", Role: "agent", Active: true,
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("non-admin create: want PermissionDenied, got %v", err)
	}

	// Marko (no permission.grant) tries to set perms on someone.
	_, err = svc.SetEmployeePermissions(mctx, admin.ID, []string{permissions.EmployeeRead})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("non-grant set perms: want PermissionDenied, got %v", err)
	}
}

// Caller has employee.write + permission.grant but isn't an admin —
// they must not be able to mint admins through CreateEmployee or to
// elevate someone via SetEmployeePermissions. Spec p.9: only the
// administrator can grant the admin permission.
func TestIntegration_AdminGrantRequiresAdmin(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)
	supervisor := createEmployee(t, svc, admin.ID, "sv@banka.local", "sv", "agent", true)

	// Hand-craft a principal that has write + grant but no admin perm.
	supCtx := principalCtx(supervisor.ID, auth.KindEmployee,
		[]string{permissions.EmployeeRead, permissions.EmployeeWrite, permissions.PermissionGrant, permissions.ClientRead})

	// Cannot create an admin via the role bundle.
	_, err := svc.CreateEmployee(supCtx, CreateEmployeeInput{
		Email: "evil-admin@banka.local", Username: "evil",
		FirstName: "E", LastName: "A",
		DateOfBirth: time.Now().AddDate(-30, 0, 0), Gender: domain.GenderMale,
		Phone: "+381645555555", Address: "X", Position: "P", Department: "D",
		Role: "admin", Active: true,
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("non-admin minting admin via Create: want PermissionDenied, got %v", err)
	}

	// Create a regular target then try to elevate them via SetPermissions.
	target := createEmployee(t, svc, admin.ID, "target@banka.local", "target", "agent", true)
	_, err = svc.SetEmployeePermissions(supCtx, target.ID,
		[]string{permissions.Admin, permissions.EmployeeRead, permissions.EmployeeWrite, permissions.ClientRead, permissions.ClientWrite, permissions.PermissionGrant})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("non-admin elevating via SetPermissions: want PermissionDenied, got %v", err)
	}

	// Sanity: a real admin can do both.
	if _, err := svc.CreateEmployee(adminCtx(admin.ID), CreateEmployeeInput{
		Email: "admin2@banka.local", Username: "admin2",
		FirstName: "A", LastName: "Two",
		DateOfBirth: time.Now().AddDate(-30, 0, 0), Gender: domain.GenderMale,
		Phone: "+381645555555", Address: "X", Position: "P", Department: "D",
		Role: "admin", Active: true,
	}); err != nil {
		t.Fatalf("admin minting admin: %v", err)
	}
}

func TestIntegration_ListEmployees_FilterAndPaginate(t *testing.T) {
	svc, _ := setup(t)
	admin := makeAdmin(t, svc)

	// Create a small population.
	for i, name := range []string{"Marko", "Marija", "Ivan", "Ivana", "Petar"} {
		createEmployee(t, svc,
			admin.ID,
			fmt.Sprintf("emp%d@banka.local", i),
			fmt.Sprintf("emp%d", i),
			"agent", true,
		)
		// Override last name via direct UPDATE so filter has something to match.
		_, _ = fixPool.Exec(context.Background(),
			`update "user".employees set first_name=$2 where email=$1`,
			fmt.Sprintf("emp%d@banka.local", i), name)
	}

	t.Run("filter by first-name substring", func(t *testing.T) {
		list, total, err := svc.ListEmployees(adminCtx(admin.ID), domain.EmployeeFilter{Name: "ivan"}, 1, 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 2 {
			t.Fatalf("name=ivan: want 2 (Ivan + Ivana), got %d", total)
		}
		if len(list) != 2 {
			t.Errorf("len mismatch: %d", len(list))
		}
	})

	t.Run("pagination", func(t *testing.T) {
		page1, total, err := svc.ListEmployees(adminCtx(admin.ID), domain.EmployeeFilter{}, 1, 2)
		if err != nil {
			t.Fatalf("list page 1: %v", err)
		}
		if total != 6 { // 5 created + 1 admin
			t.Fatalf("total: want 6, got %d", total)
		}
		if len(page1) != 2 {
			t.Errorf("page 1 size: want 2, got %d", len(page1))
		}
		page2, _, _ := svc.ListEmployees(adminCtx(admin.ID), domain.EmployeeFilter{}, 2, 2)
		if len(page2) != 2 || page2[0].ID == page1[0].ID {
			t.Errorf("page 2 overlaps page 1 or wrong size: page1=%v page2=%v",
				idsOf(page1), idsOf(page2))
		}
	})
}

// =====================================================================
// helpers
// =====================================================================

func createEmployee(t *testing.T, svc *Service, adminID, email, username, role string, active bool) *domain.Employee {
	t.Helper()
	emp, err := svc.CreateEmployee(adminCtx(adminID), CreateEmployeeInput{
		Email:       email,
		Username:    username,
		FirstName:   "First",
		LastName:    "Last",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Gender:      domain.GenderMale,
		Phone:       "+381645555555",
		Address:     "Beograd",
		Position:    "Position",
		Department:  "Department",
		Role:        role,
		Active:      active,
	})
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return emp
}

func extractToken(t *testing.T, notif *spyNotifier, to, marker string) string {
	t.Helper()
	for _, e := range notif.sentTo(to) {
		idx := indexOf(e.Body, marker)
		if idx < 0 {
			continue
		}
		rest := e.Body[idx+len(marker):]
		end := indexOfAny(rest, "\n \t")
		if end < 0 {
			end = len(rest)
		}
		return rest[:end]
	}
	t.Fatalf("no email with marker %q sent to %s", marker, to)
	return ""
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func indexOfAny(s, chars string) int {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return i
			}
		}
	}
	return -1
}
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
		// substring match too
		if indexOf(h, needle) >= 0 {
			return true
		}
	}
	return false
}
func filterSubject(emails []sentEmail, sub string) []sentEmail {
	var out []sentEmail
	for _, e := range emails {
		if indexOf(e.Subject, sub) >= 0 {
			out = append(out, e)
		}
	}
	return out
}
func idsOf(emps []*domain.Employee) []string {
	out := make([]string, len(emps))
	for i, e := range emps {
		out[i] = e.ID
	}
	return out
}
func isApperr(err error, kind apperr.Kind) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Kind == kind
}

func apperrMsg(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
