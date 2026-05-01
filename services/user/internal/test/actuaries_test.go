package test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UpdateEmployeeNeedApproval shares the validation/permission shape of the
// trading-limit RPC; we cover the same arg-rejection paths so a future refactor
// of the gate doesn't silently widen who can flip the flag.
func TestUpdateEmployeeNeedApprovalArgValidation(t *testing.T) {
	gormTestServer, _, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	cases := []struct {
		name string
		req  *userpb.UpdateEmployeeNeedApprovalRequest
		code codes.Code
	}{
		{
			name: "zero id",
			req:  &userpb.UpdateEmployeeNeedApprovalRequest{Id: 0, NeedApproval: true},
			code: codes.InvalidArgument,
		},
		{
			// permission check runs before any DB lookup; empty CallerEmail can't be admin/supervisor.
			name: "caller not supervisor or admin",
			req:  &userpb.UpdateEmployeeNeedApprovalRequest{Id: 1, NeedApproval: true},
			code: codes.PermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gormTestServer.UpdateEmployeeNeedApproval(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected error with code %s, got nil", tc.code)
			}
			if status.Code(err) != tc.code {
				t.Fatalf("got code %s, want %s (err=%v)", status.Code(err), tc.code, err)
			}
		})
	}
}

// GetActuaries pulls every employee row through the standard filter pipeline
// then drops anyone without the `agent` permission. This test checks that drop:
// two employees come back from the DB, only the one tagged `agent` survives,
// and used_limit / need_approval flow through to the response.
func TestGetActuariesKeepsOnlyAgents(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	birth := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "employees"`)).
		WillReturnRows(sqlmockEmployeeRows().
			AddRow(uint64(1), "Ana", "Agent", birth, "F", "ana@banka.raf", "+381600000001", "Adresa 1",
				"ana", []byte("pw"), []byte("salt"), "Actuary", "Trading", true, int64(1000000), int64(250000), true, now, now).
			AddRow(uint64(2), "Boris", "Boss", birth, "M", "boris@banka.raf", "+381600000002", "Adresa 2",
				"boris", []byte("pw"), []byte("salt"), "Manager", "Trading", true, int64(0), int64(0), false, now, now))

	// Preload("Permissions") on a many2many issues two follow-up queries: one against
	// the join table, then one against permissions filtered by the join rows.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "employee_permissions" WHERE "employee_permissions"."employee_id" IN ($1,$2)`)).
		WithArgs(uint64(1), uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"employee_id", "permission_id"}).
			AddRow(uint64(1), uint64(10)).
			AddRow(uint64(2), uint64(11)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "permissions" WHERE "permissions"."id" IN ($1,$2)`)).
		WithArgs(uint64(10), uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
			AddRow(uint64(10), "agent").
			AddRow(uint64(11), "supervisor"))

	resp, err := gormTestServer.GetActuaries(context.Background(), &userpb.GetEmployeesRequest{})
	if err != nil {
		t.Fatalf("GetActuaries returned error: %v", err)
	}
	if len(resp.Employees) != 1 {
		t.Fatalf("expected 1 actuary (agent only), got %d", len(resp.Employees))
	}
	got := resp.Employees[0]
	if got.Email != "ana@banka.raf" {
		t.Fatalf("unexpected actuary email: %s", got.Email)
	}
	if got.Limit != 1000000 || got.UsedLimit != 250000 {
		t.Fatalf("limit/used_limit not propagated: limit=%d used_limit=%d", got.Limit, got.UsedLimit)
	}
	if !got.NeedApproval {
		t.Fatalf("expected NeedApproval=true to flow through")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func sqlmockEmployeeRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "first_name", "last_name", "date_of_birth", "gender", "email", "phone_number", "address",
		"username", "password", "salt_password", "position", "department", "active",
		"limit", "used_limit", "need_approval", "created_at", "updated_at",
	})
}
