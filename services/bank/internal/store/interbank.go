package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
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
		return nil, apperr.Internal("get interbank message", err)
	}
	return out, nil
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
