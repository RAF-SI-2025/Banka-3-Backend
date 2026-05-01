package test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/server"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTogglesTradingRoleDetectsGrantRevoke(t *testing.T) {
	tests := []struct {
		name string
		old  []string
		new  []string
		want bool
	}{
		{"no change, no trading role", []string{"manage_loans"}, []string{"manage_loans", "manage_cards"}, false},
		{"grants agent", []string{"manage_loans"}, []string{"manage_loans", "agent"}, true},
		{"revokes agent", []string{"agent", "trade_stocks"}, []string{"trade_stocks"}, true},
		{"grants supervisor", []string{}, []string{"supervisor"}, true},
		{"revokes supervisor", []string{"supervisor"}, []string{}, true},
		{"agent stays set", []string{"agent"}, []string{"agent", "view_stocks"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := server.TogglesTradingRole(server.NamesToSet(tc.old), server.NamesToSet(tc.new))
			if got != tc.want {
				t.Fatalf("togglesTradingRole(%v -> %v) = %v, want %v", tc.old, tc.new, got, tc.want)
			}
		})
	}
}

// UpdateEmployeeTradingLimit validates its arguments before touching the DB, so
// we can exercise those branches without plumbing gorm/sqlmock expectations.
func TestUpdateEmployeeTradingLimitArgValidation(t *testing.T) {
	gormTestServer, _, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	cases := []struct {
		name string
		req  *userpb.UpdateEmployeeTradingLimitRequest
		code codes.Code
	}{
		{
			name: "zero id",
			req:  &userpb.UpdateEmployeeTradingLimitRequest{Id: 0},
			code: codes.InvalidArgument,
		},
		{
			name: "nothing to update",
			req:  &userpb.UpdateEmployeeTradingLimitRequest{Id: 1},
			code: codes.InvalidArgument,
		},
		{
			name: "negative limit",
			req:  &userpb.UpdateEmployeeTradingLimitRequest{Id: 1, Limit: ptr64(-1)},
			code: codes.InvalidArgument,
		},
		{
			name: "negative used_limit",
			req:  &userpb.UpdateEmployeeTradingLimitRequest{Id: 1, UsedLimit: ptr64(-5)},
			code: codes.InvalidArgument,
		},
		{
			// permission check runs after validation; empty CallerEmail means no admin/supervisor
			// caller found → PermissionDenied without any DB round-trips.
			name: "caller not supervisor or admin",
			req:  &userpb.UpdateEmployeeTradingLimitRequest{Id: 1, Limit: ptr64(500)},
			code: codes.PermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gormTestServer.UpdateEmployeeTradingLimit(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected error with code %s, got nil", tc.code)
			}
			if status.Code(err) != tc.code {
				t.Fatalf("got code %s, want %s (err=%v)", status.Code(err), tc.code, err)
			}
		})
	}
}

func ptr64(v int64) *int64 { return &v }

// Spec p.38: granting `admin` implicitly grants `supervisor`. Revoking `admin`
// does not touch `supervisor`. ensureAdminImpliesSupervisor is the pure helper
// behind both CreateEmployeeAccount and UpdateEmployee; covering it here pins
// the invariant without needing a full DB harness.
func TestEnsureAdminImpliesSupervisor(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"no admin, untouched", []string{"manage_loans"}, []string{"manage_loans"}},
		{"admin without supervisor → appends", []string{"admin"}, []string{"admin", "supervisor"}},
		{"admin with supervisor → idempotent", []string{"admin", "supervisor"}, []string{"admin", "supervisor"}},
		{"supervisor alone stays alone", []string{"supervisor"}, []string{"supervisor"}},
		{"nil in, nil out", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := server.EnsureAdminImpliesSupervisor(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v want %v", got, tc.want)
			}
			gotSet := server.NamesToSet(got)
			for _, p := range tc.want {
				if _, ok := gotSet[p]; !ok {
					t.Fatalf("missing %q in result %v", p, got)
				}
			}
		})
	}
}

// Spec p.38: an admin may not edit another admin. The server must short-circuit
// before touching the permissions payload.
func TestUpdateEmployeeBlocksAdminOnAdmin(t *testing.T) {
	testServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	birth := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()

	// getUserByAttribute(Employee, "id", 1): returns a target who already has `admin`.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "employees" WHERE id = $1 ORDER BY "employees"."id" LIMIT $2`)).
		WithArgs(int64(1), 1).
		WillReturnRows(sqlmockEmployeeRows().
			AddRow(uint64(1), "Target", "Admin", birth, "M", "target-admin@banka.raf", "+381", "addr",
				"tadmin", []byte("pw"), []byte("salt"), "Admin", "IT", true, int64(0), int64(0), false, now, now))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "employee_permissions" WHERE "employee_permissions"."employee_id" = $1`)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"employee_id", "permission_id"}).AddRow(uint64(1), uint64(1)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "permissions" WHERE "permissions"."id" = $1`)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(uint64(1), "admin"))

	_, err := testServer.UpdateEmployee(context.Background(), &userpb.UpdateEmployeeRequest{
		Id:          1,
		Active:      true,
		CallerEmail: "someone-else@banka.raf",
		Permissions: []string{"admin"},
	})
	if err == nil {
		t.Fatal("expected PermissionDenied, got nil")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got code %s, want PermissionDenied (err=%v)", status.Code(err), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
