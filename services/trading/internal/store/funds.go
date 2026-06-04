package store

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// =====================================================================
// Funds
// =====================================================================

const fundCols = `
    id, name, description, manager_user_id, bank_account_id,
    minimum_contribution::text, total_units::text, status,
    created_at, updated_at`

func scanFund(row pgx.Row) (*domain.Fund, error) {
	var f domain.Fund
	var status string
	if err := row.Scan(
		&f.ID, &f.Name, &f.Description, &f.ManagerUserID, &f.BankAccountID,
		&f.MinimumContribution, &f.TotalUnits, &status,
		&f.CreatedAt, &f.UpdatedAt,
	); err != nil {
		return nil, err
	}
	f.Status = domain.FundStatus(status)
	return &f, nil
}

// InsertFund mints a new fund. Caller has already created the bank
// account (CreateFundAccount) and passes its id. Idempotent on name —
// the unique index returns Conflict.
func (s *Store) InsertFund(ctx context.Context, f *domain.Fund) (*domain.Fund, error) {
	const q = `
        insert into "trading".investment_funds (
            name, description, manager_user_id, bank_account_id,
            minimum_contribution, total_units, status
        ) values (
            $1, $2, $3, $4,
            $5::numeric, $6::numeric, $7
        ) returning ` + fundCols
	row := s.DB.QueryRow(
		ctx, q,
		f.Name, f.Description, f.ManagerUserID, f.BankAccountID,
		f.MinimumContribution, f.TotalUnits, string(f.Status),
	)
	out, err := scanFund(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("fond sa istim imenom već postoji")
		}
		return nil, apperr.Internal("insert fund", err)
	}
	return out, nil
}

// GetFund returns one fund by id.
func (s *Store) GetFund(ctx context.Context, id string) (*domain.Fund, error) {
	const q = `select ` + fundCols + ` from "trading".investment_funds where id = $1`
	out, err := scanFund(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("fond ne postoji")
		}
		return nil, apperr.Internal("get fund", err)
	}
	return out, nil
}

// GetFundTx is the same as GetFund but reads inside the caller's tx.
// Used by SAGA steps that need a consistent snapshot across multiple
// reads + writes.
func (s *Store) GetFundTx(ctx context.Context, tx pgx.Tx, id string) (*domain.Fund, error) {
	const q = `select ` + fundCols + ` from "trading".investment_funds where id = $1 for update`
	out, err := scanFund(tx.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("fond ne postoji")
		}
		return nil, apperr.Internal("get fund (tx)", err)
	}
	return out, nil
}

// FundFilter narrows ListFunds.
type FundFilter struct {
	Status                 string
	ManagerUserID          string
	MinContributionAtLeast string
	MinContributionAtMost  string
}

// ListFunds returns matching funds ordered by name. Sort + filter on
// total_value is computed at the service layer (needs bank +
// market data joins).
func (s *Store) ListFunds(ctx context.Context, f FundFilter) ([]*domain.Fund, error) {
	conds := []string{"true"}
	var args []any
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if f.Status == "" || f.Status == "active" {
		add("status = ?", "active")
	} else if f.Status != "any" {
		add("status = ?", f.Status)
	}
	if f.ManagerUserID != "" {
		add("manager_user_id = ?", f.ManagerUserID)
	}
	if f.MinContributionAtLeast != "" {
		add("minimum_contribution >= ?::numeric", f.MinContributionAtLeast)
	}
	if f.MinContributionAtMost != "" {
		add("minimum_contribution <= ?::numeric", f.MinContributionAtMost)
	}
	q := `select ` + fundCols + ` from "trading".investment_funds where ` +
		strings.Join(conds, " and ") + ` order by name`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, apperr.Internal("list funds", err)
	}
	defer rows.Close()
	var out []*domain.Fund
	for rows.Next() {
		fnd, err := scanFund(rows)
		if err != nil {
			return nil, apperr.Internal("scan fund", err)
		}
		out = append(out, fnd)
	}
	return out, rows.Err()
}

// SetFundManager flips manager_user_id on a fund (manager-demotion
// cascade hook).
func (s *Store) SetFundManager(ctx context.Context, tx pgx.Tx, fundID, managerID string) error {
	const q = `update "trading".investment_funds
	           set manager_user_id = $2, updated_at = now()
	           where id = $1`
	if _, err := tx.Exec(ctx, q, fundID, managerID); err != nil {
		return apperr.Internal("set fund manager", err)
	}
	return nil
}

// ReassignFundManager flips every active fund managed by `fromID`
// over to `toID` in one statement. Returns the row count. Funds
// already managed by `toID` are unaffected. Used by the
// manager-demotion cascade hook in user-svc.
func (s *Store) ReassignFundManager(ctx context.Context, fromID, toID string) (int64, error) {
	const q = `update "trading".investment_funds
	           set manager_user_id = $2, updated_at = now()
	           where manager_user_id = $1
	             and status = 'active'`
	tag, err := s.DB.Exec(ctx, q, fromID, toID)
	if err != nil {
		return 0, apperr.Internal("reassign fund manager", err)
	}
	return tag.RowsAffected(), nil
}

// AdjustFundUnits adds delta (signed numeric string) to the fund's
// total_units inside the caller's tx. Underflow trips the >= 0 check
// constraint and surfaces as FailedPrecondition.
func (s *Store) AdjustFundUnits(ctx context.Context, tx pgx.Tx, fundID, delta string) error {
	const q = `update "trading".investment_funds
	           set total_units = total_units + $2::numeric,
	               updated_at  = now()
	           where id = $1`
	if _, err := tx.Exec(ctx, q, fundID, delta); err != nil {
		if isCheckViolation(err) {
			return apperr.FailedPrecondition("nedovoljno jedinica fonda")
		}
		return apperr.Internal("adjust fund units", err)
	}
	return nil
}

// =====================================================================
// Positions
// =====================================================================

const fundPositionCols = `
    id, fund_id, client_id, units::text, total_invested_rsd::text,
    created_at, updated_at`

func scanFundPosition(row pgx.Row) (*domain.FundPosition, error) {
	var p domain.FundPosition
	if err := row.Scan(
		&p.ID, &p.FundID, &p.ClientID, &p.Units, &p.TotalInvestedRSD,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetFundPosition returns the (fund, client) position or NotFound.
// Read outside a tx; the saga uses GetFundPositionTx for write paths.
func (s *Store) GetFundPosition(ctx context.Context, fundID, clientID string) (*domain.FundPosition, error) {
	const q = `select ` + fundPositionCols + ` from "trading".client_fund_positions
	           where fund_id = $1 and client_id = $2`
	out, err := scanFundPosition(s.DB.QueryRow(ctx, q, fundID, clientID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("pozicija ne postoji")
		}
		return nil, apperr.Internal("get fund position", err)
	}
	return out, nil
}

// UpsertFundPositionInvest adds units + amount_rsd to the (fund, client)
// position. Inserts a new row if the position doesn't exist yet.
// Idempotency is the caller's responsibility (the saga's record_position
// step runs after the transfer_to_fund step has already moved cash).
func (s *Store) UpsertFundPositionInvest(
	ctx context.Context, tx pgx.Tx,
	fundID, clientID, unitsDelta, amountRSD string,
) (*domain.FundPosition, error) {
	const q = `
        insert into "trading".client_fund_positions
            (fund_id, client_id, units, total_invested_rsd)
        values ($1, $2, $3::numeric, $4::numeric)
        on conflict (fund_id, client_id) do update set
            units              = "trading".client_fund_positions.units + excluded.units,
            total_invested_rsd = "trading".client_fund_positions.total_invested_rsd + excluded.total_invested_rsd,
            updated_at         = now()
        returning ` + fundPositionCols
	out, err := scanFundPosition(tx.QueryRow(ctx, q, fundID, clientID, unitsDelta, amountRSD))
	if err != nil {
		return nil, apperr.Internal("upsert fund position invest", err)
	}
	return out, nil
}

// DecrementFundPositionWithdraw reduces units + total_invested_rsd
// pro-rata. Caller passes the units to remove and the cost-basis to
// drop (which equals position.total_invested_rsd × (units_removed /
// position.units) — computed at the service layer for clarity).
// Returns the updated row.
//
// CHECK constraints on the column (units >= 0, total_invested_rsd >= 0)
// surface a violation as FailedPrecondition.
func (s *Store) DecrementFundPositionWithdraw(
	ctx context.Context, tx pgx.Tx,
	fundID, clientID, unitsRemoved, costBasisRemoved string,
) (*domain.FundPosition, error) {
	const q = `
        update "trading".client_fund_positions
        set units              = units              - $3::numeric,
            total_invested_rsd = total_invested_rsd - $4::numeric,
            updated_at         = now()
        where fund_id = $1 and client_id = $2
        returning ` + fundPositionCols
	out, err := scanFundPosition(tx.QueryRow(ctx, q, fundID, clientID, unitsRemoved, costBasisRemoved))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("pozicija ne postoji")
		}
		if isCheckViolation(err) {
			return nil, apperr.FailedPrecondition("nedovoljno jedinica u poziciji")
		}
		return nil, apperr.Internal("decrement fund position", err)
	}
	return out, nil
}

// FundPositionFilter narrows ListFundPositions.
type FundPositionFilter struct {
	FundID   string
	ClientID string
	Status   string // "active" (units > 0) / "any"
}

// ListFundPositions returns matching rows ordered newest-first.
func (s *Store) ListFundPositions(ctx context.Context, f FundPositionFilter) ([]*domain.FundPosition, error) {
	conds := []string{"true"}
	var args []any
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if f.FundID != "" {
		add("fund_id = ?", f.FundID)
	}
	if f.ClientID != "" {
		add("client_id = ?", f.ClientID)
	}
	if f.Status == "" || f.Status == "active" {
		conds = append(conds, "units > 0")
	}
	q := `select ` + fundPositionCols + ` from "trading".client_fund_positions where ` +
		strings.Join(conds, " and ") + ` order by updated_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, apperr.Internal("list fund positions", err)
	}
	defer rows.Close()
	var out []*domain.FundPosition
	for rows.Next() {
		p, err := scanFundPosition(rows)
		if err != nil {
			return nil, apperr.Internal("scan fund position", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SumPositionsInvestedRSD totals total_invested_rsd across all
// positions in a fund. Used to compute the fund's collective cost
// basis (profit = total_value − this sum).
func (s *Store) SumPositionsInvestedRSD(ctx context.Context, fundID string) (string, error) {
	const q = `select coalesce(sum(total_invested_rsd), 0)::text
	           from "trading".client_fund_positions where fund_id = $1`
	var out string
	if err := s.DB.QueryRow(ctx, q, fundID).Scan(&out); err != nil {
		return "", apperr.Internal("sum fund positions invested", err)
	}
	return out, nil
}

// =====================================================================
// Transactions (audit log)
// =====================================================================

const fundTxCols = `
    id, fund_id, client_id,
    coalesce(initiator_employee_id::text, ''),
    amount_rsd::text, units_delta::text,
    source_or_dest_account_id, is_inflow, status,
    coalesce(saga_id::text, ''),
    coalesce(failure_reason, ''),
    created_at, updated_at`

func scanFundTx(row pgx.Row) (*domain.FundTransaction, error) {
	var t domain.FundTransaction
	var status string
	if err := row.Scan(
		&t.ID, &t.FundID, &t.ClientID,
		&t.InitiatorEmployeeID,
		&t.AmountRSD, &t.UnitsDelta,
		&t.SourceOrDestAccountID, &t.IsInflow, &status,
		&t.SagaID,
		&t.FailureReason,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	t.Status = domain.FundTransactionStatus(status)
	return &t, nil
}

// InsertFundTransaction writes a new audit row. is_inflow drives
// invest vs withdraw; status is typically `pending` at insert time and
// flips to `completed`/`failed` via MarkFundTransactionStatus.
func (s *Store) InsertFundTransaction(ctx context.Context, tx pgx.Tx, t *domain.FundTransaction) (*domain.FundTransaction, error) {
	const q = `
        insert into "trading".client_fund_transactions (
            fund_id, client_id, initiator_employee_id,
            amount_rsd, units_delta, source_or_dest_account_id,
            is_inflow, status, saga_id
        ) values (
            $1, $2, nullif($3, '')::uuid,
            $4::numeric, $5::numeric, $6,
            $7, $8, nullif($9, '')::uuid
        ) returning ` + fundTxCols
	row := tx.QueryRow(
		ctx, q,
		t.FundID, t.ClientID, t.InitiatorEmployeeID,
		t.AmountRSD, t.UnitsDelta, t.SourceOrDestAccountID,
		t.IsInflow, string(t.Status), t.SagaID,
	)
	out, err := scanFundTx(row)
	if err != nil {
		return nil, apperr.Internal("insert fund tx", err)
	}
	return out, nil
}

// MarkFundTransactionStatus flips status and (optionally) units_delta /
// failure_reason. units_delta is non-empty for the illiquid withdraw
// path's completion stage where the exact unit count is known only at
// finalize time. saga_id is propagated when non-empty so the caller can
// stamp the parent saga's id without a second update.
func (s *Store) MarkFundTransactionStatus(
	ctx context.Context, tx pgx.Tx,
	id string, status domain.FundTransactionStatus,
	unitsDelta, sagaID, failureReason string,
) (*domain.FundTransaction, error) {
	const q = `
        update "trading".client_fund_transactions
        set status         = $2,
            units_delta    = case when $3 = '' then units_delta else $3::numeric end,
            saga_id        = case when $4 = '' then saga_id     else $4::uuid end,
            failure_reason = case when $5 = '' then failure_reason else $5 end,
            updated_at     = now()
        where id = $1
        returning ` + fundTxCols
	row := tx.QueryRow(ctx, q, id, string(status), unitsDelta, sagaID, failureReason)
	out, err := scanFundTx(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("fund transaction ne postoji")
		}
		return nil, apperr.Internal("mark fund tx status", err)
	}
	return out, nil
}

// GetFundTransaction returns one row by id.
func (s *Store) GetFundTransaction(ctx context.Context, id string) (*domain.FundTransaction, error) {
	const q = `select ` + fundTxCols + ` from "trading".client_fund_transactions where id = $1`
	out, err := scanFundTx(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("fund transaction ne postoji")
		}
		return nil, apperr.Internal("get fund transaction", err)
	}
	return out, nil
}

// GetFundTransactionBySagaID returns the pending row a given saga is
// driving (used by the recovery worker + the illiquid withdraw finalize
// step). NotFound when no pending row exists for that saga.
func (s *Store) GetFundTransactionBySagaID(ctx context.Context, sagaID string) (*domain.FundTransaction, error) {
	const q = `select ` + fundTxCols + ` from "trading".client_fund_transactions where saga_id = $1`
	out, err := scanFundTx(s.DB.QueryRow(ctx, q, sagaID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("fund transaction za saga ne postoji")
		}
		return nil, apperr.Internal("get fund transaction by saga", err)
	}
	return out, nil
}

// FundTransactionFilter narrows ListFundTransactions.
type FundTransactionFilter struct {
	FundID   string
	ClientID string
	Status   string
}

// ListFundTransactions returns matching rows ordered newest-first plus
// the total count for pagination.
func (s *Store) ListFundTransactions(ctx context.Context, f FundTransactionFilter, page, pageSize int) ([]*domain.FundTransaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	conds := []string{"true"}
	var args []any
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if f.FundID != "" {
		add("fund_id = ?", f.FundID)
	}
	if f.ClientID != "" {
		add("client_id = ?", f.ClientID)
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	where := " where " + strings.Join(conds, " and ")
	countSQL := `select count(*) from "trading".client_fund_transactions` + where
	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), countSQL, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count fund transactions", err)
	}
	args = append(args, pageSize, (page-1)*pageSize)
	pageSQL := `select ` + fundTxCols + ` from "trading".client_fund_transactions` + where +
		` order by created_at desc limit ` + intArg(len(args)-1) + ` offset ` + intArg(len(args))
	rows, err := s.DB.Query(postgres.WithRead(ctx), pageSQL, args...)
	if err != nil {
		return nil, 0, apperr.Internal("list fund transactions", err)
	}
	defer rows.Close()
	var out []*domain.FundTransaction
	for rows.Next() {
		t, err := scanFundTx(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan fund tx", err)
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// =====================================================================
// Performance snapshots
// =====================================================================

// InsertFundPerformanceSnapshot writes one daily row. Idempotent on
// (fund_id, snapshot_at) — a duplicate insert is a no-op.
func (s *Store) InsertFundPerformanceSnapshot(ctx context.Context, snap *domain.FundPerformanceSnapshot) error {
	const q = `
        insert into "trading".fund_performance_snapshots
            (fund_id, snapshot_at, liquid_rsd, holdings_value_rsd)
        values ($1, $2, $3::numeric, $4::numeric)
        on conflict (fund_id, snapshot_at) do nothing`
	if _, err := s.DB.Exec(ctx, q, snap.FundID, snap.SnapshotAt, snap.LiquidRSD, snap.HoldingsValueRSD); err != nil {
		return apperr.Internal("insert fund snapshot", err)
	}
	return nil
}

// ListFundPerformanceSnapshots returns snapshots for a fund within the
// last `days` days, oldest-first.
func (s *Store) ListFundPerformanceSnapshots(ctx context.Context, fundID string, days int) ([]*domain.FundPerformanceSnapshot, error) {
	if days <= 0 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days)
	const q = `
        select fund_id, snapshot_at, liquid_rsd::text, holdings_value_rsd::text
        from "trading".fund_performance_snapshots
        where fund_id = $1 and snapshot_at >= $2
        order by snapshot_at`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, fundID, since)
	if err != nil {
		return nil, apperr.Internal("list fund snapshots", err)
	}
	defer rows.Close()
	var out []*domain.FundPerformanceSnapshot
	for rows.Next() {
		var snap domain.FundPerformanceSnapshot
		if err := rows.Scan(&snap.FundID, &snap.SnapshotAt, &snap.LiquidRSD, &snap.HoldingsValueRSD); err != nil {
			return nil, apperr.Internal("scan fund snapshot", err)
		}
		out = append(out, &snap)
	}
	return out, rows.Err()
}
