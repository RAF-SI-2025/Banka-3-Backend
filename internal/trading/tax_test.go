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

// TestLookupPlacerID_ClientFound covers the simple client path: caller is a
// client, placer row already exists. lookupPlacerID returns the placer id and
// does not touch the employees table.
func TestLookupPlacerID_ClientFound(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()

	mock.ExpectQuery(`SELECT id FROM "order_placers" WHERE client_id = \$1 LIMIT \$2`).
		WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	got, err := lookupPlacerID(srv.db, true, 42, "")
	if err != nil {
		t.Fatalf("lookupPlacerID: %v", err)
	}
	if got != 7 {
		t.Errorf("got placer %d, want 7", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestLookupPlacerID_NoPlacer covers the "user has never traded" case —
// lookupPlacerID returns 0, nil so the RPC can collapse to an empty response
// rather than 404. Mirrors the ListHoldings empty-list semantics.
func TestLookupPlacerID_NoPlacer(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()

	mock.ExpectQuery(`SELECT id FROM "order_placers" WHERE client_id = \$1 LIMIT \$2`).
		WithArgs(int64(42), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	got, err := lookupPlacerID(srv.db, true, 42, "")
	if err != nil {
		t.Fatalf("lookupPlacerID: %v", err)
	}
	if got != 0 {
		t.Errorf("got placer %d, want 0", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestLookupPlacerID_EmployeeChain pins the two-step employee resolution: look
// up the employee row by email first, then the placer by employee_id. Each
// step is a separate query so the supervisor portal's existing
// employees-by-email join is not duplicated here.
func TestLookupPlacerID_EmployeeChain(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()

	mock.ExpectQuery(`SELECT id FROM "employees" WHERE email = \$1 LIMIT \$2`).
		WithArgs("agent@banka.raf", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)))
	mock.ExpectQuery(`SELECT id FROM "order_placers" WHERE employee_id = \$1 LIMIT \$2`).
		WithArgs(int64(11), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(99)))

	got, err := lookupPlacerID(srv.db, false, 0, "agent@banka.raf")
	if err != nil {
		t.Fatalf("lookupPlacerID: %v", err)
	}
	if got != 99 {
		t.Errorf("got placer %d, want 99", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestAggregateMyTaxInfo verifies the year-filtered SUM query: paid rows in
// the requested year roll into paid_this_year_rsd, every still-unpaid row
// rolls into unpaid_this_month_rsd. The single-query CASE shape means both
// totals come back from one round trip.
func TestAggregateMyTaxInfo(t *testing.T) {
	srv, mock, done := newTaxTestServer(t)
	defer done()

	mock.ExpectQuery(`FROM "capital_gains" WHERE seller_placer_id = \$2`).
		WithArgs(2026, int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"paid_this_year_rsd", "unpaid_this_month_rsd"}).
			AddRow(int64(1500), int64(450)))

	resp, err := aggregateMyTaxInfo(srv.db, 7, 2026)
	if err != nil {
		t.Fatalf("aggregateMyTaxInfo: %v", err)
	}
	if resp.PaidThisYearRsd != 1500 {
		t.Errorf("paid_this_year_rsd = %d, want 1500", resp.PaidThisYearRsd)
	}
	if resp.UnpaidThisMonthRsd != 450 {
		t.Errorf("unpaid_this_month_rsd = %d, want 450", resp.UnpaidThisMonthRsd)
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
