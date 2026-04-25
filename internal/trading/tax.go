package trading

import (
	"context"
	"regexp"
	"strings"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// monthRe pins the month filter to YYYY-MM so a malformed value can't slip
// into the SQL `period = ?` predicate. Empty string is allowed and means
// "every still-unpaid period" (used by manual back-fill runs).
var monthRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// teamFilterClient and teamFilterActuary are the two values the supervisor
// portal uses for its team filter (spec p.63 "filteri po timu korisnika
// (klijent, aktuar)"). Anything else is rejected with InvalidArgument.
const (
	teamFilterClient  = "client"
	teamFilterActuary = "actuary"
)

// RunCapitalGains is the supervisor "Pokreni obračun" button (spec p.63).
// Delegates to bank.CollectCapitalGains so the cron and the manual trigger
// share one code path. Supervisor-only at the trading layer; the gateway
// also gates the route with `secured("supervisor")`.
func (s *Server) RunCapitalGains(_ context.Context, req *tradingpb.RunCapitalGainsRequest) (*tradingpb.RunCapitalGainsResponse, error) {
	if !callerIsSupervisor(s.db, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "supervisor permission required")
	}
	month := strings.TrimSpace(req.Month)
	if month != "" && !monthRe.MatchString(month) {
		return nil, status.Error(codes.InvalidArgument, "month must be YYYY-MM")
	}

	res, err := s.bank.CollectCapitalGains(month)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.RunCapitalGainsResponse{
		Period:       res.Period,
		AccountsPaid: int32(res.AccountsPaid),
		RowsPaid:     int32(res.RowsPaid),
		Insufficient: int32(res.Insufficient),
		TotalDebtRsd: res.TotalDebtRSD,
		CollectedRsd: res.CollectedRSD,
	}, nil
}

// ListTaxDebts powers the supervisor tax portal listing (spec p.63). Returns
// every user that has at least one capital_gains row, with both their unpaid
// and historically-paid RSD totals so the UI can show both columns. Filtering
// scopes the result by team (client/actuary) and by a case-insensitive
// substring on first or last name.
//
// Users with zero rows are intentionally not returned — the spec frames this
// portal as "users who can trade", and listing every dormant client/employee
// in the system would drown the actually-relevant rows.
func (s *Server) ListTaxDebts(_ context.Context, req *tradingpb.ListTaxDebtsRequest) (*tradingpb.ListTaxDebtsResponse, error) {
	if !callerIsSupervisor(s.db, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "supervisor permission required")
	}
	team := strings.ToLower(strings.TrimSpace(req.Team))
	switch team {
	case "", teamFilterClient, teamFilterActuary:
	default:
		return nil, status.Errorf(codes.InvalidArgument, "team must be %q or %q", teamFilterClient, teamFilterActuary)
	}
	name := strings.TrimSpace(req.Name)

	type debtorRow struct {
		UserID    int64
		FirstName string
		LastName  string
		Team      string
		UnpaidRsd int64
		PaidRsd   int64
	}

	// One row per (placer-side identity). The CASE on placer columns derives
	// the team label without an extra join chain. Filters apply at the SQL
	// level so the client-side never sees rows it shouldn't, and the LIKE is
	// parametrized to keep the filter injection-safe.
	var rows []debtorRow
	q := s.db.Table("capital_gains AS cg").
		Select(`
			COALESCE(c.id, e.id) AS user_id,
			COALESCE(c.first_name, e.first_name) AS first_name,
			COALESCE(c.last_name, e.last_name) AS last_name,
			CASE WHEN p.client_id IS NOT NULL THEN 'client' ELSE 'actuary' END AS team,
			COALESCE(SUM(CASE WHEN cg.paid_at IS NULL THEN cg.tax_due ELSE 0 END), 0) AS unpaid_rsd,
			COALESCE(SUM(CASE WHEN cg.paid_at IS NOT NULL THEN cg.tax_due ELSE 0 END), 0) AS paid_rsd
		`).
		Joins("JOIN order_placers p ON p.id = cg.seller_placer_id").
		Joins("LEFT JOIN clients   c ON c.id = p.client_id").
		Joins("LEFT JOIN employees e ON e.id = p.employee_id")

	switch team {
	case teamFilterClient:
		q = q.Where("p.client_id IS NOT NULL")
	case teamFilterActuary:
		q = q.Where("p.employee_id IS NOT NULL")
	}
	if name != "" {
		like := "%" + name + "%"
		q = q.Where(`
			COALESCE(c.first_name, e.first_name) ILIKE ?
			OR COALESCE(c.last_name, e.last_name) ILIKE ?
		`, like, like)
	}
	q = q.Group(`COALESCE(c.id, e.id),
		COALESCE(c.first_name, e.first_name),
		COALESCE(c.last_name, e.last_name),
		CASE WHEN p.client_id IS NOT NULL THEN 'client' ELSE 'actuary' END`).
		Order("unpaid_rsd DESC, last_name ASC, first_name ASC")

	if err := q.Scan(&rows).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.TaxDebtor, 0, len(rows))
	for _, r := range rows {
		out = append(out, &tradingpb.TaxDebtor{
			UserId:    r.UserID,
			FirstName: r.FirstName,
			LastName:  r.LastName,
			Team:      r.Team,
			UnpaidRsd: r.UnpaidRsd,
			PaidRsd:   r.PaidRsd,
		})
	}
	return &tradingpb.ListTaxDebtsResponse{Debtors: out}, nil
}
