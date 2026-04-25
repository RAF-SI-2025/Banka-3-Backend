package trading

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// newTaxTestServer wires a Server backed by sqlmock for the supervisor-check
// query alone — every other interaction in the tax RPCs lives behind that
// gate, so individual tests only need to set up the supervisor lookup.
func newTaxTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, func()) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: raw}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return &Server{db: gdb}, mock, func() { _ = raw.Close() }
}

// expectSupervisorCheck stubs the callerIsSupervisor query so the caller is
// recognized (count > 0) or rejected (count == 0). The exact SQL is private
// to callerIsSupervisor in orders_portal.go.
func expectSupervisorCheck(mock sqlmock.Sqlmock, email string, allowed bool) {
	count := int64(0)
	if allowed {
		count = 1
	}
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT count(*) FROM "employees" JOIN employee_permissions ep ON ep.employee_id = employees.id JOIN permissions p ON p.id = ep.permission_id WHERE employees.email = $1 AND p.name IN ($2,$3)`)).
		WithArgs(email, "admin", "supervisor").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
}

func TestRunCapitalGains_RejectsNonSupervisor(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()
	expectSupervisorCheck(mock, "agent@banka.raf", false)

	_, err := srv.RunCapitalGains(context.Background(), &tradingpb.RunCapitalGainsRequest{
		CallerEmail: "agent@banka.raf",
		Month:       "2026-04",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("got %s, want PermissionDenied", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRunCapitalGains_RejectsBadMonth(t *testing.T) {
	cases := []string{"2026/04", "2026-13", "2026-1", "abcd-ef", "2026-04-01"}
	for _, m := range cases {
		t.Run(m, func(t *testing.T) {
			srv, mock, done := newTaxTestServer(t)
			defer done()
			expectSupervisorCheck(mock, "sup@banka.raf", true)

			_, err := srv.RunCapitalGains(context.Background(), &tradingpb.RunCapitalGainsRequest{
				CallerEmail: "sup@banka.raf",
				Month:       m,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("month=%q: got %s, want InvalidArgument", m, status.Code(err))
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}
}

func TestListTaxDebts_RejectsNonSupervisor(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()
	expectSupervisorCheck(mock, "client@banka.raf", false)

	_, err := srv.ListTaxDebts(context.Background(), &tradingpb.ListTaxDebtsRequest{
		CallerEmail: "client@banka.raf",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("got %s, want PermissionDenied", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListTaxDebts_RejectsBadTeam(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()
	expectSupervisorCheck(mock, "sup@banka.raf", true)

	_, err := srv.ListTaxDebts(context.Background(), &tradingpb.ListTaxDebtsRequest{
		CallerEmail: "sup@banka.raf",
		Team:        "supervisor",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("got %s, want InvalidArgument", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestMonthRegex pins the YYYY-MM filter so a regex tweak doesn't accidentally
// admit malformed periods (which would silently match zero rows in the
// `period = ?` predicate at collection time).
func TestMonthRegex(t *testing.T) {
	good := []string{"2026-01", "2026-12", "1999-09", "0000-10"}
	bad := []string{"", "2026-00", "2026-13", "2026-1", "2026-001", "2026/01", "abc-12"}
	for _, m := range good {
		if !monthRe.MatchString(m) {
			t.Errorf("month %q should match", m)
		}
	}
	for _, m := range bad {
		if monthRe.MatchString(m) {
			t.Errorf("month %q should NOT match", m)
		}
	}
}
