package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/jackc/pgx/v5"
)

const transactionColumns = `
    id, op_id, op_kind, leg_index,
    from_account_id, to_account_id,
    from_amount::text, to_amount::text,
    coalesce(rate::text, '') as rate,
    coalesce(recipient_name, ''),
    coalesce(payment_code, ''),
    coalesce(reference_number, ''),
    coalesce(purpose, ''),
    coalesce(initiator_client_id::text, ''),
    status, created_at
`

func scanTransaction(row interface{ Scan(...any) error }) (*domain.Transaction, error) {
	var t domain.Transaction
	var kind, status string
	if err := row.Scan(
		&t.ID, &t.OpID, &kind, &t.LegIndex,
		&t.FromAccountID, &t.ToAccountID,
		&t.FromAmount, &t.ToAmount, &t.Rate,
		&t.RecipientName, &t.PaymentCode, &t.ReferenceNumber, &t.Purpose,
		&t.InitiatorClientID,
		&status, &t.CreatedAt,
	); err != nil {
		return nil, err
	}
	t.Kind = domain.TransactionKind(kind)
	t.Status = domain.TransactionStatus(status)
	return &t, nil
}

// transactionReadColumns mirrors transactionColumns but qualified to
// the "t" alias and with the two resolved counterparty account numbers
// appended (LEFT JOINed via fa/ta — see transactionReadFrom). The read
// paths use this so the FE can render the 18-digit "Drugi račun"
// instead of a raw account UUID; the INSERT...RETURNING path keeps the
// unqualified transactionColumns (RETURNING can't join).
const transactionReadColumns = `
    t.id, t.op_id, t.op_kind, t.leg_index,
    t.from_account_id, t.to_account_id,
    t.from_amount::text, t.to_amount::text,
    coalesce(t.rate::text, '') as rate,
    coalesce(t.recipient_name, ''),
    coalesce(t.payment_code, ''),
    coalesce(t.reference_number, ''),
    coalesce(t.purpose, ''),
    coalesce(t.initiator_client_id::text, ''),
    t.status, t.created_at,
    coalesce(fa.number, ''),
    coalesce(ta.number, '')
`

const transactionReadFrom = `
    from "bank".transactions t
    left join "bank".accounts fa on fa.id = t.from_account_id
    left join "bank".accounts ta on ta.id = t.to_account_id
`

func scanTransactionWithNumbers(row interface{ Scan(...any) error }) (*domain.Transaction, error) {
	var t domain.Transaction
	var kind, status string
	if err := row.Scan(
		&t.ID, &t.OpID, &kind, &t.LegIndex,
		&t.FromAccountID, &t.ToAccountID,
		&t.FromAmount, &t.ToAmount, &t.Rate,
		&t.RecipientName, &t.PaymentCode, &t.ReferenceNumber, &t.Purpose,
		&t.InitiatorClientID,
		&status, &t.CreatedAt,
		&t.FromAccountNumber, &t.ToAccountNumber,
	); err != nil {
		return nil, err
	}
	t.Kind = domain.TransactionKind(kind)
	t.Status = domain.TransactionStatus(status)
	return &t, nil
}

// ExecuteAtomic runs fn inside a serialized transaction. Used to guard
// the multi-step "debit source, credit destination, write ledger row"
// invariant of every payment/transfer.
func (s *Store) ExecuteAtomic(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		return fn(tx)
	})
}

// ErrInsufficientFunds is the singleton sentinel returned by
// AdjustBalance when the debit would push available_balance below
// zero. Callers that handle the case as a normal business outcome
// (e.g. the installment cron's overdue path) match it with errors.Is;
// the gRPC interceptor maps it to FailedPrecondition either way.
var ErrInsufficientFunds = &apperr.Error{
	Kind:    apperr.KindFailedPrecondition,
	Message: "nedovoljno sredstava na računu",
}

// AdjustBalance increments balance + available_balance by delta (which
// may be negative for a debit). Returns ErrInsufficientFunds when the
// result would underflow available_balance below zero. Caller supplies
// the *pgx.Tx so the debit + credit + ledger row are atomic.
//
// daily_spent / monthly_spent are bumped only when delta < 0 (a debit
// from the account); FX-leg incoming credits don't count toward limits.
func (s *Store) AdjustBalance(ctx context.Context, tx pgx.Tx, accountID, delta string) error {
	const q = `
        update "bank".accounts set
            balance           = balance + $2::numeric,
            available_balance = available_balance + $2::numeric,
            daily_spent       = case when $2::numeric < 0 then daily_spent   + (-$2::numeric) else daily_spent end,
            monthly_spent     = case when $2::numeric < 0 then monthly_spent + (-$2::numeric) else monthly_spent end,
            updated_at        = now()
        where id = $1
          and (available_balance + $2::numeric) >= 0
        returning id`
	var got string
	err := tx.QueryRow(ctx, q, accountID, delta).Scan(&got)
	if err != nil {
		if noRows(err) {
			return ErrInsufficientFunds
		}
		return apperr.Internal("adjust balance", err)
	}
	return nil
}

// AdjustAvailableBalance moves `available_balance` only, leaving
// `balance` untouched. Used by the c4 reservation primitive: the
// reserve step debits available_balance so the client can't double-
// spend the same money before the SAGA commits, but the headline
// balance stays unchanged until commit time (the money is still
// notionally in the account).
//
// daily/monthly spent counters are NOT bumped here — reservations
// aren't outbound payments and the spec p.12 limits apply to settled
// debits only. (The commit's AdjustBalanceOnly stamp also stays clear
// of the counters for the same reason: the limits were already
// satisfied when the reserve happened.)
//
// Surface ErrInsufficientFunds on underflow so the caller can map it
// to FailedPrecondition like the regular AdjustBalance path.
func (s *Store) AdjustAvailableBalance(ctx context.Context, tx pgx.Tx, accountID, delta string) error {
	const q = `
        update "bank".accounts set
            available_balance = available_balance + $2::numeric,
            updated_at        = now()
        where id = $1
          and (available_balance + $2::numeric) >= 0
        returning id`
	var got string
	err := tx.QueryRow(ctx, q, accountID, delta).Scan(&got)
	if err != nil {
		if noRows(err) {
			return ErrInsufficientFunds
		}
		return apperr.Internal("adjust available balance", err)
	}
	return nil
}

// AdjustBalanceOnly moves `balance` only, leaving `available_balance`
// untouched. Used by the c4 reservation commit: available_balance was
// debited at reserve time; the commit step closes the loop by debiting
// balance. The underflow guard checks the resulting balance directly
// — available_balance is already at its post-reserve value.
func (s *Store) AdjustBalanceOnly(ctx context.Context, tx pgx.Tx, accountID, delta string) error {
	const q = `
        update "bank".accounts set
            balance    = balance + $2::numeric,
            updated_at = now()
        where id = $1
          and (balance + $2::numeric) >= 0
        returning id`
	var got string
	err := tx.QueryRow(ctx, q, accountID, delta).Scan(&got)
	if err != nil {
		if noRows(err) {
			return ErrInsufficientFunds
		}
		return apperr.Internal("adjust balance only", err)
	}
	return nil
}

// CheckLimits reports whether `amount` would push the account over its
// daily or monthly limit. Spec p.12: limits are RSD-equivalent and
// the FE shows "preostalo limita". For now we treat the amount as
// already in account-currency since the daily/monthly_spent counters
// are also in account-currency.
//
// A zero limit means "unlimited" — treat as "no constraint".
func (s *Store) CheckLimits(ctx context.Context, tx pgx.Tx, accountID, amount string) error {
	const q = `
        select daily_limit::text, monthly_limit::text,
               daily_spent::text, monthly_spent::text
        from "bank".accounts where id = $1 for update`
	var dl, ml, ds, ms string
	if err := tx.QueryRow(ctx, q, accountID).Scan(&dl, &ml, &ds, &ms); err != nil {
		return apperr.Internal("read limits", err)
	}

	type cmp struct {
		spent, limit, label string
	}
	for _, c := range []cmp{{ds, dl, "dnevni"}, {ms, ml, "mesečni"}} {
		// Limit == 0 means unlimited.
		if c.limit == "0" || c.limit == "0.0000" || c.limit == "0.00" {
			continue
		}
		// new_spent = spent + amount
		// reject if new_spent > limit
		var ok bool
		err := tx.QueryRow(ctx, "select ($1::numeric + $2::numeric) <= $3::numeric", c.spent, amount, c.limit).Scan(&ok)
		if err != nil {
			return apperr.Internal("limit math", err)
		}
		if !ok {
			return apperr.FailedPrecondition(fmt.Sprintf("prekoračen %s limit", c.label))
		}
	}
	return nil
}

// InsertTransaction writes a single ledger leg.
func (s *Store) InsertTransaction(ctx context.Context, tx pgx.Tx, t *domain.Transaction) (*domain.Transaction, error) {
	const q = `
        insert into "bank".transactions (
            op_id, op_kind, leg_index,
            from_account_id, to_account_id,
            from_amount, to_amount, rate,
            recipient_name, payment_code, reference_number, purpose,
            initiator_client_id, status
        ) values (
            $1, $2, $3,
            $4, $5,
            $6::numeric, $7::numeric, nullif($8, '')::numeric,
            nullif($9, ''), nullif($10, ''), nullif($11, ''), nullif($12, ''),
            nullif($13, '')::uuid, $14
        )
        returning ` + transactionColumns

	row := tx.QueryRow(
		ctx, q,
		t.OpID, string(t.Kind), t.LegIndex,
		t.FromAccountID, t.ToAccountID,
		t.FromAmount, t.ToAmount, t.Rate,
		t.RecipientName, t.PaymentCode, t.ReferenceNumber, t.Purpose,
		t.InitiatorClientID, string(t.Status),
	)
	out, err := scanTransaction(row)
	if err != nil {
		return nil, apperr.Internal("insert transaction", err)
	}
	return out, nil
}

func (s *Store) ListTransactions(ctx context.Context, f domain.TransactionFilter, page, pageSize int) ([]*domain.Transaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var conds []string
	var args []any
	// Conditions are qualified to the "t" alias: the read query LEFT
	// JOINs "bank".accounts (which also has a `status` column), so an
	// unqualified `status`/`number` would be ambiguous.
	if f.AccountID != "" {
		args = append(args, f.AccountID)
		conds = append(conds, fmt.Sprintf("(t.from_account_id = $%d or t.to_account_id = $%d)", len(args), len(args)))
	}
	if f.OpKind != "" {
		args = append(args, f.OpKind)
		conds = append(conds, fmt.Sprintf("t.op_kind = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("t.status = $%d", len(args)))
	}
	if f.InitiatorClientID != "" {
		args = append(args, f.InitiatorClientID)
		conds = append(conds, fmt.Sprintf("t.initiator_client_id = $%d", len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		conds = append(conds, fmt.Sprintf("t.created_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		conds = append(conds, fmt.Sprintf("t.created_at <= $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), `select count(*) from "bank".transactions t`+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count transactions", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + transactionReadColumns + transactionReadFrom + where +
		fmt.Sprintf(" order by t.created_at desc, t.leg_index limit $%d offset $%d", len(args)+1, len(args)+2)

	rows, err := s.DB.Query(postgres.WithRead(ctx), listQ, listArgs...)
	if err != nil {
		return nil, 0, apperr.Internal("list transactions", err)
	}
	defer rows.Close()
	var out []*domain.Transaction
	for rows.Next() {
		t, err := scanTransactionWithNumbers(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan transaction", err)
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// GetTransactionsByOpID returns every leg of a single op (UX-level
// payment / transfer / trade). Empty result is not an error — caller
// distinguishes "not yet settled" from "no rows".
func (s *Store) GetTransactionsByOpID(ctx context.Context, opID string) ([]*domain.Transaction, error) {
	q := `select ` + transactionReadColumns + transactionReadFrom + ` where t.op_id = $1 order by t.leg_index`
	rows, err := s.DB.Query(ctx, q, opID)
	if err != nil {
		return nil, apperr.Internal("transactions by op", err)
	}
	defer rows.Close()
	var out []*domain.Transaction
	for rows.Next() {
		t, err := scanTransactionWithNumbers(rows)
		if err != nil {
			return nil, apperr.Internal("scan tx by op", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAccountByNumber returns the account row for an 18-digit number,
// or NotFound. Used by payment flow to resolve the recipient.
func (s *Store) GetAccountByNumber(ctx context.Context, number string) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts where number = $1`
	out, err := scanAccount(s.DB.QueryRow(ctx, q, number))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("primalac (račun) ne postoji")
		}
		return nil, apperr.Internal("get account by number", err)
	}
	return out, nil
}
