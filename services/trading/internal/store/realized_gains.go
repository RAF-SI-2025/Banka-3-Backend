package store

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const realizedGainCols = `
    id, user_id, user_kind, security_id, account_id, quantity,
    cost_basis_amt::text, proceeds_amt::text, currency,
    gain_native::text, gain_rsd::text,
    realized_at, taxed, taxed_at, coalesce(tax_op_id::text, '')`

// InsertRealizedGain writes one closing-sell row inside the caller's
// tx. The caller has already computed both native + RSD values so the
// store stays I/O-only.
func (s *Store) InsertRealizedGain(ctx context.Context, tx pgx.Tx, g *domain.RealizedGain) (*domain.RealizedGain, error) {
	const q = `
        insert into "trading".realized_gains
            (user_id, user_kind, security_id, account_id, quantity,
             cost_basis_amt, proceeds_amt, currency,
             gain_native, gain_rsd)
        values ($1,$2,$3,$4,$5,
                $6::numeric,$7::numeric,$8,
                $9::numeric,$10::numeric)
        returning ` + realizedGainCols
	row := tx.QueryRow(ctx, q,
		g.UserID, string(g.UserKind), g.SecurityID, g.AccountID, g.Quantity,
		g.CostBasisAmt, g.ProceedsAmt, string(g.Currency),
		g.GainNative, g.GainRSD,
	)
	out, err := scanRealizedGain(row)
	if err != nil {
		return nil, apperr.Internal("insert realized gain", err)
	}
	return out, nil
}

// RealizedGainFilter narrows ListRealizedGains.
type RealizedGainFilter struct {
	UserID    string
	UserKind  domain.UserKind
	OnlyTaxed *bool
}

// ListRealizedGains returns matching rows. Used by both the tax-position
// view and the end-of-month tax-cron.
func (s *Store) ListRealizedGains(ctx context.Context, f RealizedGainFilter) ([]*domain.RealizedGain, error) {
	var args []any
	var conds []string
	add := func(cond string, a any) {
		args = append(args, a)
		conds = append(conds, strings.ReplaceAll(cond, "?", intArg(len(args))))
	}
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.UserKind != "" {
		add("user_kind = ?", string(f.UserKind))
	}
	if f.OnlyTaxed != nil {
		add("taxed = ?", *f.OnlyTaxed)
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}
	q := `select ` + realizedGainCols + ` from "trading".realized_gains` + where + ` order by realized_at desc`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, apperr.Internal("list realized gains", err)
	}
	defer rows.Close()
	var out []*domain.RealizedGain
	for rows.Next() {
		g, err := scanRealizedGain(rows)
		if err != nil {
			return nil, apperr.Internal("scan realized gain", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// MarkRealizedGainsTaxed flips taxed=true and stamps tax_op_id +
// taxed_at on the rows whose ids appear in `ids`. Used by the end-of-
// month tax cron after the bank-side debit succeeds.
func (s *Store) MarkRealizedGainsTaxed(ctx context.Context, tx pgx.Tx, ids []string, taxOpID string) error {
	if len(ids) == 0 {
		return nil
	}
	const q = `
        update "trading".realized_gains
        set taxed = true, taxed_at = now(), tax_op_id = $2::uuid
        where id = any($1::uuid[]) and taxed = false`
	if _, err := tx.Exec(ctx, q, ids, taxOpID); err != nil {
		return apperr.Internal("mark gains taxed", err)
	}
	return nil
}

func scanRealizedGain(row pgx.Row) (*domain.RealizedGain, error) {
	var (
		g   domain.RealizedGain
		t   string
		cur string
	)
	if err := row.Scan(
		&g.ID, &g.UserID, &t, &g.SecurityID, &g.AccountID, &g.Quantity,
		&g.CostBasisAmt, &g.ProceedsAmt, &cur,
		&g.GainNative, &g.GainRSD,
		&g.RealizedAt, &g.Taxed, &g.TaxedAt, &g.TaxOpID,
	); err != nil {
		return nil, err
	}
	g.UserKind = domain.UserKind(t)
	g.Currency = domain.Currency(cur)
	return &g, nil
}
