package store

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// =====================================================================
// External OTC threads — celina 5.
// Mirrors store/otc.go's shape but for cross-bank threads.
// =====================================================================

const externalOTCThreadCols = `
    id, direction,
    remote_bank_code, remote_thread_id, remote_user_ref,
    remote_display_name, remote_account_ref,
    local_user_id, local_user_kind, local_account_id,
    local_account_number, local_role,
    coalesce(security_id::text, ''),
    security_ticker, seller_holding_ref,
    quantity, price_per_unit::text, premium::text, currency, settlement_date,
    modified_by_side, status, created_at, updated_at`

func scanExternalOTCThread(row pgx.Row) (*domain.ExternalOTCThread, error) {
	var t domain.ExternalOTCThread
	var dir, uk, role, side, status, cur string
	if err := row.Scan(
		&t.ID, &dir,
		&t.RemoteBankCode, &t.RemoteThreadID, &t.RemoteUserRef,
		&t.RemoteDisplayName, &t.RemoteAccountRef,
		&t.LocalUserID, &uk, &t.LocalAccountID,
		&t.LocalAccountNumber, &role,
		&t.SecurityID,
		&t.SecurityTicker, &t.SellerHoldingRef,
		&t.Quantity, &t.PricePerUnit, &t.Premium, &cur, &t.SettlementDate,
		&side, &status, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	t.Direction = domain.ExternalOTCDirection(dir)
	t.LocalUserKind = domain.UserKind(uk)
	t.LocalRole = domain.ExternalOTCRole(role)
	t.Currency = domain.Currency(cur)
	t.ModifiedBySide = domain.ExternalOTCSide(side)
	t.Status = domain.ExternalOTCThreadStatus(status)
	return &t, nil
}

// InsertExternalOTCThread writes a new thread. Caller sets every field
// except id+timestamps.
func (s *Store) InsertExternalOTCThread(ctx context.Context, tx pgx.Tx, t *domain.ExternalOTCThread) (*domain.ExternalOTCThread, error) {
	const q = `
        insert into "trading".external_otc_threads (
            direction,
            remote_bank_code, remote_thread_id, remote_user_ref,
            remote_display_name, remote_account_ref,
            local_user_id, local_user_kind, local_account_id,
            local_account_number, local_role,
            security_id, security_ticker, seller_holding_ref,
            quantity, price_per_unit, premium, currency, settlement_date,
            modified_by_side, status
        ) values (
            $1,
            $2, $3, $4,
            $5, $6,
            $7, $8, $9,
            $10, $11,
            nullif($12, '')::uuid, $13, $14,
            $15, $16::numeric, $17::numeric, $18, $19,
            $20, $21
        ) returning ` + externalOTCThreadCols
	row := s.execer(tx).QueryRow(ctx, q,
		string(t.Direction),
		t.RemoteBankCode, t.RemoteThreadID, t.RemoteUserRef,
		t.RemoteDisplayName, t.RemoteAccountRef,
		t.LocalUserID, string(t.LocalUserKind), t.LocalAccountID,
		t.LocalAccountNumber, string(t.LocalRole),
		t.SecurityID, t.SecurityTicker, t.SellerHoldingRef,
		t.Quantity, t.PricePerUnit, t.Premium, string(t.Currency), t.SettlementDate,
		string(t.ModifiedBySide), string(t.Status),
	)
	out, err := scanExternalOTCThread(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("nit sa istom partner-bankom već postoji")
		}
		return nil, apperr.Internal("insert external otc thread", err)
	}
	return out, nil
}

// GetExternalOTCThread returns one thread by local id.
func (s *Store) GetExternalOTCThread(ctx context.Context, id string) (*domain.ExternalOTCThread, error) {
	const q = `select ` + externalOTCThreadCols + ` from "trading".external_otc_threads where id = $1`
	out, err := scanExternalOTCThread(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("eksterna ponuda ne postoji")
		}
		return nil, apperr.Internal("get external otc thread", err)
	}
	return out, nil
}

// GetExternalOTCThreadByRemote finds the local mirror row for a partner-
// originated thread. Used by the inbound Receive* handlers to resolve
// the partner's (bank_code, thread_id) tuple to our local uuid.
func (s *Store) GetExternalOTCThreadByRemote(ctx context.Context, tx pgx.Tx, bankCode, remoteThreadID string) (*domain.ExternalOTCThread, error) {
	const qLock = `select ` + externalOTCThreadCols + `
	               from "trading".external_otc_threads
	               where remote_bank_code = $1 and remote_thread_id = $2
	               for update`
	const qRead = `select ` + externalOTCThreadCols + `
	               from "trading".external_otc_threads
	               where remote_bank_code = $1 and remote_thread_id = $2`
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, qLock, bankCode, remoteThreadID)
	} else {
		row = s.Pool.QueryRow(ctx, qRead, bankCode, remoteThreadID)
	}
	out, err := scanExternalOTCThread(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("eksterna nit ne postoji")
		}
		return nil, apperr.Internal("get external otc thread by remote", err)
	}
	return out, nil
}

// ExternalOTCThreadFilter narrows ListExternalOTCThreads. Mirrors
// OTCThreadFilter — the FE board is per-user with an optional status.
type ExternalOTCThreadFilter struct {
	LocalUserID string
	// Status="" defaults to "any" (no filter). Pass "open" to get only
	// active negotiations.
	Status string
}

// ListExternalOTCThreads returns threads the caller is the local party
// on, newest first.
func (s *Store) ListExternalOTCThreads(ctx context.Context, f ExternalOTCThreadFilter) ([]*domain.ExternalOTCThread, error) {
	conds := []string{"true"}
	var args []any
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if f.LocalUserID != "" {
		add("local_user_id = ?", f.LocalUserID)
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	q := `select ` + externalOTCThreadCols + `
	      from "trading".external_otc_threads
	      where ` + strings.Join(conds, " and ") + `
	      order by updated_at desc`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, apperr.Internal("list external otc threads", err)
	}
	defer rows.Close()
	var out []*domain.ExternalOTCThread
	for rows.Next() {
		t, err := scanExternalOTCThread(rows)
		if err != nil {
			return nil, apperr.Internal("scan external otc thread", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateExternalOTCThreadTerms applies a counter-offer to an existing
// thread. status stays 'open'; modified_by_side flips. Used by both the
// outbound counter (we move) and the inbound receive-counter (partner
// moves) paths.
func (s *Store) UpdateExternalOTCThreadTerms(
	ctx context.Context, tx pgx.Tx, id string,
	quantity int32, pricePerUnit, premium string, settlementDate time.Time,
	movedBy domain.ExternalOTCSide,
) (*domain.ExternalOTCThread, error) {
	const q = `update "trading".external_otc_threads
	           set quantity        = $2,
	               price_per_unit  = $3::numeric,
	               premium         = $4::numeric,
	               settlement_date = $5,
	               modified_by_side = $6,
	               updated_at       = now()
	           where id = $1 and status = 'open'
	           returning ` + externalOTCThreadCols
	row := tx.QueryRow(ctx, q, id, quantity, pricePerUnit, premium, settlementDate, string(movedBy))
	out, err := scanExternalOTCThread(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nit više nije otvorena")
		}
		return nil, apperr.Internal("update external otc thread terms", err)
	}
	return out, nil
}

// SetExternalOTCThreadStatus flips the lifecycle state of a thread.
func (s *Store) SetExternalOTCThreadStatus(
	ctx context.Context, tx pgx.Tx, id string, status domain.ExternalOTCThreadStatus,
) (*domain.ExternalOTCThread, error) {
	const q = `update "trading".external_otc_threads
	           set status = $2, updated_at = now()
	           where id = $1
	           returning ` + externalOTCThreadCols
	row := tx.QueryRow(ctx, q, id, string(status))
	out, err := scanExternalOTCThread(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("nit ne postoji")
		}
		return nil, apperr.Internal("set external otc thread status", err)
	}
	return out, nil
}

// SetExternalOTCThreadRemoteThreadID stamps the partner-assigned thread
// id on an outgoing mirror row. Called once, when the partner's
// response to our CreateOffer comes back with a remote_thread_id.
func (s *Store) SetExternalOTCThreadRemoteThreadID(
	ctx context.Context, tx pgx.Tx, id, remoteThreadID string,
) (*domain.ExternalOTCThread, error) {
	const q = `update "trading".external_otc_threads
	           set remote_thread_id = $2, updated_at = now()
	           where id = $1 and remote_thread_id = ''
	           returning ` + externalOTCThreadCols
	row := s.execer(tx).QueryRow(ctx, q, id, remoteThreadID)
	out, err := scanExternalOTCThread(row)
	if err != nil {
		if noRows(err) {
			// Already stamped — return the live row.
			return s.GetExternalOTCThread(ctx, id)
		}
		return nil, apperr.Internal("set external otc thread remote id", err)
	}
	return out, nil
}

// SetExternalOTCThreadRemoteIdentity stamps the partner-assigned thread
// id, account ref, and display name on an outgoing mirror row. Bank A
// needs the partner's account number to drive the cross-bank premium
// 2PC at accept time, so this batches the three "what the partner told
// us about themselves" fields into one update.
func (s *Store) SetExternalOTCThreadRemoteIdentity(
	ctx context.Context, tx pgx.Tx, id, remoteThreadID, remoteAccountRef, remoteDisplayName string,
) (*domain.ExternalOTCThread, error) {
	const q = `update "trading".external_otc_threads
	           set remote_thread_id = case when remote_thread_id = '' then $2 else remote_thread_id end,
	               remote_account_ref = case when remote_account_ref = '' then $3 else remote_account_ref end,
	               remote_display_name = case when remote_display_name = '' then $4 else remote_display_name end,
	               updated_at = now()
	           where id = $1
	           returning ` + externalOTCThreadCols
	row := s.execer(tx).QueryRow(ctx, q, id, remoteThreadID, remoteAccountRef, remoteDisplayName)
	out, err := scanExternalOTCThread(row)
	if err != nil {
		if noRows(err) {
			return s.GetExternalOTCThread(ctx, id)
		}
		return nil, apperr.Internal("set external otc thread remote identity", err)
	}
	return out, nil
}

// =====================================================================
// External OTC iterations.
// =====================================================================

const externalOTCIterationCols = `
    id, thread_id, proposed_by_side,
    quantity, price_per_unit::text, premium::text, settlement_date, created_at`

func scanExternalOTCIteration(row pgx.Row) (*domain.ExternalOTCIteration, error) {
	var it domain.ExternalOTCIteration
	var side string
	if err := row.Scan(
		&it.ID, &it.ThreadID, &side,
		&it.Quantity, &it.PricePerUnit, &it.Premium, &it.SettlementDate, &it.CreatedAt,
	); err != nil {
		return nil, err
	}
	it.ProposedBySide = domain.ExternalOTCSide(side)
	return &it, nil
}

// InsertExternalOTCIteration appends one iteration row to a thread.
func (s *Store) InsertExternalOTCIteration(ctx context.Context, tx pgx.Tx, it *domain.ExternalOTCIteration) (*domain.ExternalOTCIteration, error) {
	const q = `
        insert into "trading".external_otc_iterations (
            thread_id, proposed_by_side, quantity, price_per_unit, premium, settlement_date
        ) values ($1, $2, $3, $4::numeric, $5::numeric, $6)
        returning ` + externalOTCIterationCols
	row := s.execer(tx).QueryRow(ctx, q,
		it.ThreadID, string(it.ProposedBySide),
		it.Quantity, it.PricePerUnit, it.Premium, it.SettlementDate,
	)
	out, err := scanExternalOTCIteration(row)
	if err != nil {
		return nil, apperr.Internal("insert external otc iteration", err)
	}
	return out, nil
}

// ListExternalOTCIterations returns every iteration in a thread, oldest
// first.
func (s *Store) ListExternalOTCIterations(ctx context.Context, threadID string) ([]*domain.ExternalOTCIteration, error) {
	const q = `select ` + externalOTCIterationCols + `
	           from "trading".external_otc_iterations
	           where thread_id = $1 order by created_at`
	rows, err := s.Pool.Query(ctx, q, threadID)
	if err != nil {
		return nil, apperr.Internal("list external otc iterations", err)
	}
	defer rows.Close()
	var out []*domain.ExternalOTCIteration
	for rows.Next() {
		it, err := scanExternalOTCIteration(rows)
		if err != nil {
			return nil, apperr.Internal("scan external otc iteration", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// =====================================================================
// External OTC contracts.
// =====================================================================

const externalOTCContractCols = `
    id, thread_id, direction,
    remote_bank_code, remote_thread_id, remote_user_ref,
    remote_display_name, remote_account_ref,
    local_user_id, local_user_kind, local_account_id,
    local_account_number, local_role,
    coalesce(security_id::text, ''),
    security_ticker, seller_holding_ref,
    quantity, strike_price::text, premium_paid::text, currency, settlement_date,
    accepted_by_side, status,
    coalesce(premium_op_id::text, ''),
    coalesce(exercise_op_id::text, ''),
    exercised_at, created_at, updated_at`

func scanExternalOTCContract(row pgx.Row) (*domain.ExternalOTCContract, error) {
	var c domain.ExternalOTCContract
	var dir, uk, role, side, status, cur string
	var exAt *time.Time
	if err := row.Scan(
		&c.ID, &c.ThreadID, &dir,
		&c.RemoteBankCode, &c.RemoteThreadID, &c.RemoteUserRef,
		&c.RemoteDisplayName, &c.RemoteAccountRef,
		&c.LocalUserID, &uk, &c.LocalAccountID,
		&c.LocalAccountNumber, &role,
		&c.SecurityID,
		&c.SecurityTicker, &c.SellerHoldingRef,
		&c.Quantity, &c.StrikePrice, &c.PremiumPaid, &cur, &c.SettlementDate,
		&side, &status,
		&c.PremiumOpID,
		&c.ExerciseOpID,
		&exAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.Direction = domain.ExternalOTCDirection(dir)
	c.LocalUserKind = domain.UserKind(uk)
	c.LocalRole = domain.ExternalOTCRole(role)
	c.Currency = domain.Currency(cur)
	c.AcceptedBySide = domain.ExternalOTCSide(side)
	c.Status = domain.ExternalOTCContractStatus(status)
	c.ExercisedAt = exAt
	return &c, nil
}

// InsertExternalOTCContract mints a contract row at accept time. One
// per thread (unique constraint); a retry of the same accept saga gets
// the existing row back without re-charging.
func (s *Store) InsertExternalOTCContract(ctx context.Context, tx pgx.Tx, c *domain.ExternalOTCContract) (*domain.ExternalOTCContract, error) {
	const q = `
        insert into "trading".external_otc_contracts (
            thread_id, direction,
            remote_bank_code, remote_thread_id, remote_user_ref,
            remote_display_name, remote_account_ref,
            local_user_id, local_user_kind, local_account_id,
            local_account_number, local_role,
            security_id, security_ticker, seller_holding_ref,
            quantity, strike_price, premium_paid, currency, settlement_date,
            accepted_by_side, status, premium_op_id
        ) values (
            $1, $2,
            $3, $4, $5,
            $6, $7,
            $8, $9, $10,
            $11, $12,
            nullif($13, '')::uuid, $14, $15,
            $16, $17::numeric, $18::numeric, $19, $20,
            $21, $22, nullif($23, '')::uuid
        )
        on conflict (thread_id) do update
            set updated_at = "trading".external_otc_contracts.updated_at
        returning ` + externalOTCContractCols
	row := tx.QueryRow(ctx, q,
		c.ThreadID, string(c.Direction),
		c.RemoteBankCode, c.RemoteThreadID, c.RemoteUserRef,
		c.RemoteDisplayName, c.RemoteAccountRef,
		c.LocalUserID, string(c.LocalUserKind), c.LocalAccountID,
		c.LocalAccountNumber, string(c.LocalRole),
		c.SecurityID, c.SecurityTicker, c.SellerHoldingRef,
		c.Quantity, c.StrikePrice, c.PremiumPaid, string(c.Currency), c.SettlementDate,
		string(c.AcceptedBySide), string(c.Status), c.PremiumOpID,
	)
	out, err := scanExternalOTCContract(row)
	if err != nil {
		return nil, apperr.Internal("insert external otc contract", err)
	}
	return out, nil
}

// GetExternalOTCContract returns one contract by local id.
func (s *Store) GetExternalOTCContract(ctx context.Context, id string) (*domain.ExternalOTCContract, error) {
	const q = `select ` + externalOTCContractCols + ` from "trading".external_otc_contracts where id = $1`
	out, err := scanExternalOTCContract(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("eksterni ugovor ne postoji")
		}
		return nil, apperr.Internal("get external otc contract", err)
	}
	return out, nil
}

// GetExternalOTCContractByThread returns the contract for a thread, or
// NotFound when none has been minted yet.
func (s *Store) GetExternalOTCContractByThread(ctx context.Context, threadID string) (*domain.ExternalOTCContract, error) {
	const q = `select ` + externalOTCContractCols + `
	           from "trading".external_otc_contracts where thread_id = $1`
	out, err := scanExternalOTCContract(s.Pool.QueryRow(ctx, q, threadID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ugovor nije sklopljen")
		}
		return nil, apperr.Internal("get external otc contract by thread", err)
	}
	return out, nil
}

// ListExternalOTCContracts returns contracts the caller is the local
// party on, newest first. Status="" returns all states.
func (s *Store) ListExternalOTCContracts(ctx context.Context, localUserID, status string) ([]*domain.ExternalOTCContract, error) {
	conds := []string{"true"}
	var args []any
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if localUserID != "" {
		add("local_user_id = ?", localUserID)
	}
	if status != "" {
		add("status = ?", status)
	}
	q := `select ` + externalOTCContractCols + `
	      from "trading".external_otc_contracts
	      where ` + strings.Join(conds, " and ") + `
	      order by updated_at desc`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, apperr.Internal("list external otc contracts", err)
	}
	defer rows.Close()
	var out []*domain.ExternalOTCContract
	for rows.Next() {
		c, err := scanExternalOTCContract(rows)
		if err != nil {
			return nil, apperr.Internal("scan external otc contract", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetExternalOTCContractStatus flips status (active → settling → exercised
// or active → expired). The exercise paths also call
// SetExternalOTCContractExercised below to stamp op_id + timestamp.
func (s *Store) SetExternalOTCContractStatus(
	ctx context.Context, tx pgx.Tx, id string, status domain.ExternalOTCContractStatus,
) (*domain.ExternalOTCContract, error) {
	const q = `update "trading".external_otc_contracts
	           set status = $2, updated_at = now()
	           where id = $1
	           returning ` + externalOTCContractCols
	row := tx.QueryRow(ctx, q, id, string(status))
	out, err := scanExternalOTCContract(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ugovor ne postoji")
		}
		return nil, apperr.Internal("set external otc contract status", err)
	}
	return out, nil
}

// SetExternalOTCContractExercised stamps exercise_op_id + exercised_at
// and flips status to 'exercised'. Idempotent — replays with the same
// exercise_op_id no-op.
func (s *Store) SetExternalOTCContractExercised(
	ctx context.Context, tx pgx.Tx, id, exerciseOpID string, exercisedAt time.Time,
) (*domain.ExternalOTCContract, error) {
	const q = `update "trading".external_otc_contracts
	           set status = 'exercised',
	               exercise_op_id = $2::uuid,
	               exercised_at   = $3,
	               updated_at     = now()
	           where id = $1
	             and (exercise_op_id is null or exercise_op_id = $2::uuid)
	           returning ` + externalOTCContractCols
	row := tx.QueryRow(ctx, q, id, exerciseOpID, exercisedAt)
	out, err := scanExternalOTCContract(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("ugovor je već iskorišćen drugim op_id-om")
		}
		return nil, apperr.Internal("set external otc contract exercised", err)
	}
	return out, nil
}

// execer returns the tx if non-nil, otherwise the pool. Local helper
// shared by every Insert/Update on this file.
func (s *Store) execer(tx pgx.Tx) interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
} {
	if tx != nil {
		return tx
	}
	return s.Pool
}
