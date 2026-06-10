package store

import (
	"context"
	"fmt"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// =====================================================================
// Inter-bank 2PC transactions (c5 — bank.interbank_protocol_transactions).
// =====================================================================

const interbankTxCols = `
    sender_routing_number, transaction_id,
    direction, local_account_number, remote_account_number,
    currency, amount::text, purpose, transaction_body,
    coalesce(reservation_id::text, ''),
    coalesce(op_id::text, ''),
    status, last_error, created_at, updated_at`

func scanInterbankTx(row pgx.Row) (*domain.InterbankProtocolTransaction, error) {
	var t domain.InterbankProtocolTransaction
	var dir, cur, status string
	if err := row.Scan(
		&t.SenderRoutingNumber, &t.TransactionID,
		&dir, &t.LocalAccountNumber, &t.RemoteAccountNumber,
		&cur, &t.Amount, &t.Purpose, &t.TransactionBody,
		&t.ReservationID,
		&t.OpID,
		&status, &t.LastError, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	t.Direction = domain.InterbankPaymentDirection(dir)
	t.Currency = domain.Currency(cur)
	t.Status = domain.InterbankTxStatus(status)
	return &t, nil
}

// InsertInterbankTx writes a fresh prepared transaction row. Conflict
// on (sender_routing_number, transaction_id) is the idempotent retry
// case — caller handles via GetInterbankTx first.
func (s *Store) InsertInterbankTx(ctx context.Context, tx pgx.Tx, t *domain.InterbankProtocolTransaction) (*domain.InterbankProtocolTransaction, error) {
	const q = `
        insert into "bank".interbank_protocol_transactions (
            sender_routing_number, transaction_id,
            direction, local_account_number, remote_account_number,
            currency, amount, purpose, transaction_body,
            reservation_id, status
        ) values (
            $1, $2, $3, $4, $5,
            $6, $7::numeric, $8, $9,
            nullif($10, '')::uuid, $11
        )
        returning ` + interbankTxCols
	row := execerOrPool(s, tx).QueryRow(
		ctx, q,
		t.SenderRoutingNumber, t.TransactionID,
		string(t.Direction), t.LocalAccountNumber, t.RemoteAccountNumber,
		string(t.Currency), t.Amount, t.Purpose, t.TransactionBody,
		t.ReservationID, string(t.Status),
	)
	out, err := scanInterbankTx(row)
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, apperr.Conflict("transaction already exists")
		}
		logger.From(ctx).ErrorContext(ctx, "insert interbank tx failed", "err", err, "sender_routing", t.SenderRoutingNumber, "tx_id", t.TransactionID)
		return nil, apperr.Internal("insert interbank tx", err)
	}
	return out, nil
}

// GetInterbankTx returns one row by (sender_routing_number, transaction_id).
// Lock=true acquires FOR UPDATE so commit / rollback paths serialize.
func (s *Store) GetInterbankTx(ctx context.Context, tx pgx.Tx, senderRouting int, txID string, lock bool) (*domain.InterbankProtocolTransaction, error) {
	q := `select ` + interbankTxCols + `
	      from "bank".interbank_protocol_transactions
	      where sender_routing_number = $1 and transaction_id = $2`
	if lock {
		q += " for update"
	}
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, q, senderRouting, txID)
	} else {
		row = s.DB.QueryRow(ctx, q, senderRouting, txID)
	}
	out, err := scanInterbankTx(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("interbank transaction not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get interbank tx failed", "err", err, "sender_routing", senderRouting, "tx_id", txID)
		return nil, apperr.Internal("get interbank tx", err)
	}
	return out, nil
}

// MarkInterbankTxStatus flips status + stamps optional op_id and
// last_error. Used by commit (status=committed + op_id set) and
// rollback (status=rolled_back + reason in last_error).
func (s *Store) MarkInterbankTxStatus(ctx context.Context, tx pgx.Tx, senderRouting int, txID string, next domain.InterbankTxStatus, opID, errMsg string) (*domain.InterbankProtocolTransaction, error) {
	const q = `update "bank".interbank_protocol_transactions
	           set status     = $3,
	               op_id      = coalesce(nullif($4, '')::uuid, op_id),
	               last_error = coalesce(nullif($5, ''), last_error),
	               updated_at = now()
	           where sender_routing_number = $1 and transaction_id = $2
	           returning ` + interbankTxCols
	row := tx.QueryRow(ctx, q, senderRouting, txID, string(next), opID, errMsg)
	out, err := scanInterbankTx(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("interbank transaction not found")
		}
		logger.From(ctx).ErrorContext(ctx, "mark interbank tx status failed", "err", err, "sender_routing", senderRouting, "tx_id", txID, "next_status", string(next))
		return nil, apperr.Internal("mark interbank tx status", err)
	}
	return out, nil
}

// =====================================================================
// Inter-bank inbound message audit (c5 — bank.interbank_protocol_messages).
// =====================================================================

const interbankMsgCols = `
    sender_routing_number, idempotence_key,
    message_type, transaction_id,
    response_status, response_body,
    created_at, updated_at`

func scanInterbankMsg(row pgx.Row) (*domain.InterbankProtocolMessage, error) {
	var m domain.InterbankProtocolMessage
	var mt string
	if err := row.Scan(
		&m.SenderRoutingNumber, &m.IdempotenceKey,
		&mt, &m.TransactionID,
		&m.ResponseStatus, &m.ResponseBody,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	m.MessageType = domain.InterbankMessageType(mt)
	return &m, nil
}

// UpsertInterbankMessage records a partner message + the response we
// returned. On conflict — same (sender, key) replayed — the stored
// response wins (`do nothing`); callers should GetInterbankMessage
// first to detect replays.
func (s *Store) UpsertInterbankMessage(ctx context.Context, tx pgx.Tx, m *domain.InterbankProtocolMessage) error {
	const q = `
        insert into "bank".interbank_protocol_messages (
            sender_routing_number, idempotence_key, message_type,
            transaction_id, response_status, response_body
        ) values ($1, $2, $3, $4, $5, $6)
        on conflict (sender_routing_number, idempotence_key) do nothing`
	_, err := execerOrPool(s, tx).Exec(
		ctx, q,
		m.SenderRoutingNumber, m.IdempotenceKey, string(m.MessageType),
		m.TransactionID, m.ResponseStatus, m.ResponseBody,
	)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "upsert interbank message failed", "err", err, "sender_routing", m.SenderRoutingNumber, "idempotence_key", m.IdempotenceKey)
		return apperr.Internal("upsert interbank message", err)
	}
	return nil
}

// GetInterbankMessage returns the cached response for a (sender, key)
// tuple, or NotFound when this is a first-time message.
func (s *Store) GetInterbankMessage(ctx context.Context, senderRouting int, key string) (*domain.InterbankProtocolMessage, error) {
	const q = `select ` + interbankMsgCols + `
	           from "bank".interbank_protocol_messages
	           where sender_routing_number = $1 and idempotence_key = $2`
	out, err := scanInterbankMsg(s.DB.QueryRow(ctx, q, senderRouting, key))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("interbank message not seen")
		}
		logger.From(ctx).ErrorContext(ctx, "get interbank message failed", "err", err, "sender_routing", senderRouting, "key", key)
		return nil, apperr.Internal("get interbank message", err)
	}
	return out, nil
}

// =====================================================================
// Inter-bank supervisor read surface (c5 observability).
// =====================================================================

// InterbankTxFilter scopes ListInterbankTransactions. Zero-valued fields
// are ignored.
type InterbankTxFilter struct {
	SenderRoutingNumber int       // 0 = any partner
	Status              string    // "" = any status
	Direction           string    // "" = any direction
	From                time.Time // zero = no lower bound (on created_at)
	To                  time.Time // zero = no upper bound (on created_at)
}

// ListInterbankTransactions returns transactions matching the filter,
// newest first, with paging. total is the unpaged match count.
func (s *Store) ListInterbankTransactions(ctx context.Context, f InterbankTxFilter, limit, offset int) ([]*domain.InterbankProtocolTransaction, int64, error) {
	where := "where 1=1"
	args := []any{}
	add := func(clause string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" and %s$%d", clause, len(args))
	}
	if f.SenderRoutingNumber != 0 {
		add("sender_routing_number = ", f.SenderRoutingNumber)
	}
	if f.Status != "" {
		add("status = ", f.Status)
	}
	if f.Direction != "" {
		add("direction = ", f.Direction)
	}
	if !f.From.IsZero() {
		add("created_at >= ", f.From)
	}
	if !f.To.IsZero() {
		add("created_at <= ", f.To)
	}

	var total int64
	if err := s.DB.QueryRow(ctx, `select count(*) from "bank".interbank_protocol_transactions `+where, args...).Scan(&total); err != nil {
		logger.From(ctx).ErrorContext(ctx, "count interbank transactions failed", "err", err)
		return nil, 0, apperr.Internal("count interbank transactions", err)
	}

	args = append(args, limit, offset)
	q := `select ` + interbankTxCols + `
	      from "bank".interbank_protocol_transactions ` + where +
		fmt.Sprintf(" order by created_at desc, transaction_id desc limit $%d offset $%d", len(args)-1, len(args))
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list interbank transactions failed", "err", err)
		return nil, 0, apperr.Internal("list interbank transactions", err)
	}
	defer rows.Close()
	out := make([]*domain.InterbankProtocolTransaction, 0, limit)
	for rows.Next() {
		t, serr := scanInterbankTx(rows)
		if serr != nil {
			logger.From(ctx).ErrorContext(ctx, "scan interbank transaction failed", "err", serr)
			return nil, 0, apperr.Internal("scan interbank transaction", serr)
		}
		out = append(out, t)
	}
	if rows.Err() != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate interbank transactions failed", "err", rows.Err())
		return nil, 0, apperr.Internal("iterate interbank transactions", rows.Err())
	}
	return out, total, nil
}

// InterbankMsgFilter scopes ListInterbankMessages (the comms / audit
// history). Zero-valued fields are ignored.
type InterbankMsgFilter struct {
	SenderRoutingNumber int
	MessageType         string
	From                time.Time
	To                  time.Time
}

// ListInterbankMessages returns the inbound-message audit log matching
// the filter, newest first, with paging.
func (s *Store) ListInterbankMessages(ctx context.Context, f InterbankMsgFilter, limit, offset int) ([]*domain.InterbankProtocolMessage, int64, error) {
	where := "where 1=1"
	args := []any{}
	add := func(clause string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" and %s$%d", clause, len(args))
	}
	if f.SenderRoutingNumber != 0 {
		add("sender_routing_number = ", f.SenderRoutingNumber)
	}
	if f.MessageType != "" {
		add("message_type = ", f.MessageType)
	}
	if !f.From.IsZero() {
		add("created_at >= ", f.From)
	}
	if !f.To.IsZero() {
		add("created_at <= ", f.To)
	}

	var total int64
	if err := s.DB.QueryRow(ctx, `select count(*) from "bank".interbank_protocol_messages `+where, args...).Scan(&total); err != nil {
		logger.From(ctx).ErrorContext(ctx, "count interbank messages failed", "err", err)
		return nil, 0, apperr.Internal("count interbank messages", err)
	}

	args = append(args, limit, offset)
	q := `select ` + interbankMsgCols + `
	      from "bank".interbank_protocol_messages ` + where +
		fmt.Sprintf(" order by created_at desc, idempotence_key desc limit $%d offset $%d", len(args)-1, len(args))
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list interbank messages failed", "err", err)
		return nil, 0, apperr.Internal("list interbank messages", err)
	}
	defer rows.Close()
	out := make([]*domain.InterbankProtocolMessage, 0, limit)
	for rows.Next() {
		m, serr := scanInterbankMsg(rows)
		if serr != nil {
			logger.From(ctx).ErrorContext(ctx, "scan interbank message failed", "err", serr)
			return nil, 0, apperr.Internal("scan interbank message", serr)
		}
		out = append(out, m)
	}
	if rows.Err() != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate interbank messages failed", "err", rows.Err())
		return nil, 0, apperr.Internal("iterate interbank messages", rows.Err())
	}
	return out, total, nil
}

// =====================================================================
// Inter-bank blacklist + failure counter (c5 observability & control).
// =====================================================================

const interbankBlacklistCols = `
    sender_routing_number, reason, blocked_by,
    blocked_at, unblocked_at, active`

func scanBlacklistEntry(row pgx.Row) (*domain.InterbankBlacklistEntry, error) {
	var e domain.InterbankBlacklistEntry
	if err := row.Scan(
		&e.SenderRoutingNumber, &e.Reason, &e.BlockedBy,
		&e.BlockedAt, &e.UnblockedAt, &e.Active,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// IsBlacklisted reports whether a routing number is actively blocked.
func (s *Store) IsBlacklisted(ctx context.Context, senderRouting int) (bool, error) {
	const q = `select exists (
	    select 1 from "bank".interbank_blacklist
	    where sender_routing_number = $1 and active)`
	var ok bool
	if err := s.DB.QueryRow(ctx, q, senderRouting).Scan(&ok); err != nil {
		logger.From(ctx).ErrorContext(ctx, "check interbank blacklist failed", "err", err, "sender_routing", senderRouting)
		return false, apperr.Internal("check interbank blacklist", err)
	}
	return ok, nil
}

// BlockBank inserts (or re-activates) a blacklist row. Idempotent on the
// routing number — re-blocking an active row refreshes reason/blocked_by
// and clears any prior unblocked_at.
func (s *Store) BlockBank(ctx context.Context, senderRouting int, reason, blockedBy string) (*domain.InterbankBlacklistEntry, error) {
	const q = `
	    insert into "bank".interbank_blacklist (
	        sender_routing_number, reason, blocked_by, blocked_at, active
	    ) values ($1, $2, $3, now(), true)
	    on conflict (sender_routing_number) do update set
	        reason       = excluded.reason,
	        blocked_by   = excluded.blocked_by,
	        blocked_at   = now(),
	        unblocked_at = null,
	        active       = true
	    returning ` + interbankBlacklistCols
	out, err := scanBlacklistEntry(s.DB.QueryRow(ctx, q, senderRouting, reason, blockedBy))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "block bank failed", "err", err, "sender_routing", senderRouting)
		return nil, apperr.Internal("block bank", err)
	}
	return out, nil
}

// UnblockBank flips an active blacklist row to inactive + stamps
// unblocked_at. NotFound when no active row exists.
func (s *Store) UnblockBank(ctx context.Context, senderRouting int) (*domain.InterbankBlacklistEntry, error) {
	const q = `
	    update "bank".interbank_blacklist
	    set active = false, unblocked_at = now()
	    where sender_routing_number = $1 and active
	    returning ` + interbankBlacklistCols
	out, err := scanBlacklistEntry(s.DB.QueryRow(ctx, q, senderRouting))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("no active blacklist entry for routing number")
		}
		logger.From(ctx).ErrorContext(ctx, "unblock bank failed", "err", err, "sender_routing", senderRouting)
		return nil, apperr.Internal("unblock bank", err)
	}
	return out, nil
}

// ListBlacklist returns blacklist rows. activeOnly restricts to active
// blocks; false returns the full history (incl. unblocked rows).
func (s *Store) ListBlacklist(ctx context.Context, activeOnly bool) ([]*domain.InterbankBlacklistEntry, error) {
	q := `select ` + interbankBlacklistCols + ` from "bank".interbank_blacklist`
	if activeOnly {
		q += ` where active`
	}
	q += ` order by blocked_at desc`
	rows, err := s.DB.Query(ctx, q)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list blacklist failed", "err", err)
		return nil, apperr.Internal("list blacklist", err)
	}
	defer rows.Close()
	out := []*domain.InterbankBlacklistEntry{}
	for rows.Next() {
		e, serr := scanBlacklistEntry(rows)
		if serr != nil {
			logger.From(ctx).ErrorContext(ctx, "scan blacklist entry failed", "err", serr)
			return nil, apperr.Internal("scan blacklist entry", serr)
		}
		out = append(out, e)
	}
	if rows.Err() != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate blacklist failed", "err", rows.Err())
		return nil, apperr.Internal("iterate blacklist", rows.Err())
	}
	return out, nil
}

// RecordPartnerFailure bumps the consecutive-failure counter for a
// routing number and returns the new count. Upserts the row.
func (s *Store) RecordPartnerFailure(ctx context.Context, senderRouting int) (int, error) {
	const q = `
	    insert into "bank".interbank_partner_failures (
	        sender_routing_number, consecutive_failures, last_failure_at, updated_at
	    ) values ($1, 1, now(), now())
	    on conflict (sender_routing_number) do update set
	        consecutive_failures = "bank".interbank_partner_failures.consecutive_failures + 1,
	        last_failure_at      = now(),
	        updated_at           = now()
	    returning consecutive_failures`
	var n int
	if err := s.DB.QueryRow(ctx, q, senderRouting).Scan(&n); err != nil {
		logger.From(ctx).ErrorContext(ctx, "record partner failure failed", "err", err, "sender_routing", senderRouting)
		return 0, apperr.Internal("record partner failure", err)
	}
	return n, nil
}

// ResetPartnerFailures zeroes the consecutive-failure counter for a
// routing number after a successful interaction. No-op when no row
// exists yet.
func (s *Store) ResetPartnerFailures(ctx context.Context, senderRouting int) error {
	const q = `
	    update "bank".interbank_partner_failures
	    set consecutive_failures = 0, updated_at = now()
	    where sender_routing_number = $1 and consecutive_failures <> 0`
	if _, err := s.DB.Exec(ctx, q, senderRouting); err != nil {
		logger.From(ctx).ErrorContext(ctx, "reset partner failures failed", "err", err, "sender_routing", senderRouting)
		return apperr.Internal("reset partner failures", err)
	}
	return nil
}

// execerOrPool returns tx if non-nil, else the pool. Local helper so
// the Insert / Upsert methods don't duplicate the nil-check.
func execerOrPool(s *Store, tx pgx.Tx) execer {
	if tx != nil {
		return tx
	}
	return s.DB
}

// execer is the narrow surface execerOrPool returns. Satisfied by both
// *pgxpool.Pool and pgx.Tx.
type execer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
