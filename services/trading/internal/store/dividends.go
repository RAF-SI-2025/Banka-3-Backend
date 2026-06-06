package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const dividendPayoutCols = `
    id, user_id, user_kind, security_id, quantity,
    price::text, gross_amount::text, currency, account_id,
    tax_rsd::text, op_id::text, status, paid_at, created_at`

// InsertDividendPayout records one credited dividend (todoSpec C3 S54-
// S59). The unique index on op_id makes a retry after a partial cron
// failure a no-op; on conflict we return the existing row so the cron
// converges without double-counting.
func (s *Store) InsertDividendPayout(ctx context.Context, d *domain.DividendPayout) (*domain.DividendPayout, error) {
	const q = `
        insert into "trading".dividend_payouts
            (user_id, user_kind, security_id, quantity, price,
             gross_amount, currency, account_id, tax_rsd, op_id, status, paid_at)
        values ($1, $2, $3, $4, $5::numeric,
                $6::numeric, $7, $8, $9::numeric, $10::uuid, $11, $12)
        on conflict (op_id) do nothing
        returning ` + dividendPayoutCols
	row := s.DB.QueryRow(ctx, q,
		d.UserID, string(d.UserKind), d.SecurityID, d.Quantity, d.Price,
		d.GrossAmount, string(d.Currency), d.AccountID, d.TaxRSD, d.OpID, d.Status, d.PaidAt)
	out, err := scanDividendPayout(row)
	if err != nil {
		if noRows(err) {
			// Lost the op_id conflict — fetch the winning row.
			return s.GetDividendPayoutByOpID(ctx, d.OpID)
		}
		return nil, apperr.Internal("insert dividend payout", err)
	}
	return out, nil
}

// GetDividendPayoutByOpID returns the payout for a deterministic op_id,
// or NotFound. Used to converge after an on-conflict insert.
func (s *Store) GetDividendPayoutByOpID(ctx context.Context, opID string) (*domain.DividendPayout, error) {
	q := `select ` + dividendPayoutCols + ` from "trading".dividend_payouts where op_id = $1`
	out, err := scanDividendPayout(s.DB.QueryRow(ctx, q, opID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("dividend payout ne postoji")
		}
		return nil, apperr.Internal("get dividend payout by op_id", err)
	}
	return out, nil
}

// ListDividendPayoutsByUser returns every payout for one (user, kind),
// newest first. Drives the portfolio dividend-history view (S59).
func (s *Store) ListDividendPayoutsByUser(ctx context.Context, userID string, kind domain.UserKind) ([]*domain.DividendPayout, error) {
	q := `select ` + dividendPayoutCols + ` from "trading".dividend_payouts
	      where user_id = $1 and user_kind = $2 order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID, string(kind))
	if err != nil {
		return nil, apperr.Internal("list dividend payouts", err)
	}
	defer rows.Close()
	return scanDividendPayouts(rows)
}

// ListDividendPayoutsByPosition returns payouts for one holder's single
// security, newest first (S59 — per-position history).
func (s *Store) ListDividendPayoutsByPosition(ctx context.Context, userID string, kind domain.UserKind, securityID string) ([]*domain.DividendPayout, error) {
	q := `select ` + dividendPayoutCols + ` from "trading".dividend_payouts
	      where user_id = $1 and user_kind = $2 and security_id = $3
	      order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID, string(kind), securityID)
	if err != nil {
		return nil, apperr.Internal("list dividend payouts by position", err)
	}
	defer rows.Close()
	return scanDividendPayouts(rows)
}

// DividendCandidate is one stock holding eligible for a quarterly
// dividend, joined with its security's yield/currency and the current
// listing price in one round-trip. The cron walks these.
type DividendCandidate struct {
	HoldingID     string
	UserID        string
	UserKind      domain.UserKind
	SecurityID    string
	AccountID     string
	Quantity      int32
	Currency      domain.Currency
	DividendYield string // decimal fraction, e.g. "0.005"
	Price         string // current listing price, security currency
}

// ListDividendCandidates returns every stock holding (quantity > 0)
// whose security carries a positive dividend_yield, joined with the
// security currency/yield and the current listing price. Rows without a
// listing are skipped (no price → no payout). Stays on the primary
// (cron read).
func (s *Store) ListDividendCandidates(ctx context.Context) ([]*DividendCandidate, error) {
	const q = `
        select h.id, h.user_id, h.user_kind, h.security_id, h.account_id,
               h.quantity, sec.currency, sec.dividend_yield::text, l.price::text
        from "trading".portfolio_holdings h
        join "trading".securities sec on sec.id = h.security_id
        join "trading".listings l on l.security_id = h.security_id
        where h.quantity > 0
          and sec.type = 'stock'
          and sec.dividend_yield is not null
          and sec.dividend_yield > 0
        order by h.user_id, h.security_id`
	rows, err := s.DB.Query(ctx, q)
	if err != nil {
		return nil, apperr.Internal("list dividend candidates", err)
	}
	defer rows.Close()
	var out []*DividendCandidate
	for rows.Next() {
		var (
			c  DividendCandidate
			k  string
			c2 string
		)
		if err := rows.Scan(&c.HoldingID, &c.UserID, &k, &c.SecurityID, &c.AccountID,
			&c.Quantity, &c2, &c.DividendYield, &c.Price); err != nil {
			return nil, apperr.Internal("scan dividend candidate", err)
		}
		c.UserKind = domain.UserKind(k)
		c.Currency = domain.Currency(c2)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func scanDividendPayout(row pgx.Row) (*domain.DividendPayout, error) {
	var (
		d   domain.DividendPayout
		k   string
		cur string
	)
	if err := row.Scan(
		&d.ID, &d.UserID, &k, &d.SecurityID, &d.Quantity,
		&d.Price, &d.GrossAmount, &cur, &d.AccountID,
		&d.TaxRSD, &d.OpID, &d.Status, &d.PaidAt, &d.CreatedAt,
	); err != nil {
		return nil, err
	}
	d.UserKind = domain.UserKind(k)
	d.Currency = domain.Currency(cur)
	return &d, nil
}

func scanDividendPayouts(rows pgx.Rows) ([]*domain.DividendPayout, error) {
	var out []*domain.DividendPayout
	for rows.Next() {
		d, err := scanDividendPayout(rows)
		if err != nil {
			return nil, apperr.Internal("scan dividend payout", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
