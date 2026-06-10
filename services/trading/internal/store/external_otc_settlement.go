package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// =====================================================================
// External OTC option settlement tracking — celina 5 (Banka-4 §2/§3).
// =====================================================================

const externalOTCSettlementCols = `
    sender_routing_number, transaction_id, kind, status, option_ref,
    coalesce(contract_id::text, ''), quantity,
    cash_amount::text, cash_currency, coalesce(op_id::text, '')`

func scanExternalOTCSettlement(row pgx.Row) (*domain.ExternalOTCSettlement, error) {
	var m domain.ExternalOTCSettlement
	if err := row.Scan(
		&m.SenderRoutingNumber, &m.TransactionID, &m.Kind, &m.Status, &m.OptionRef,
		&m.ContractID, &m.Quantity, &m.CashAmount, &m.CashCurrency, &m.OpID,
	); err != nil {
		return nil, err
	}
	return &m, nil
}

// InsertExternalOTCSettlement records a freshly-prepared settlement.
// Idempotent: a re-delivered NEW_TX (same sender + transaction_id) returns
// the existing row instead of inserting a duplicate.
func (s *Store) InsertExternalOTCSettlement(ctx context.Context, tx pgx.Tx, m *domain.ExternalOTCSettlement) (*domain.ExternalOTCSettlement, error) {
	const q = `
        insert into "trading".external_otc_settlements (
            sender_routing_number, transaction_id, kind, status, option_ref,
            contract_id, quantity, cash_amount, cash_currency
        ) values (
            $1, $2, $3, $4, $5,
            nullif($6, '')::uuid, $7, $8::numeric, $9
        )
        on conflict (sender_routing_number, transaction_id) do nothing
        returning ` + externalOTCSettlementCols
	out, err := scanExternalOTCSettlement(s.execer(tx).QueryRow(ctx, q,
		m.SenderRoutingNumber, m.TransactionID, m.Kind, m.Status, m.OptionRef,
		m.ContractID, m.Quantity, m.CashAmount, m.CashCurrency,
	))
	if err != nil {
		if noRows(err) {
			// Conflict — the row already existed; return it unchanged.
			return s.GetExternalOTCSettlement(ctx, tx, m.SenderRoutingNumber, m.TransactionID)
		}
		logger.From(ctx).ErrorContext(ctx, "insert external otc settlement failed", "err", err)
		return nil, apperr.Internal("insert external otc settlement", err)
	}
	return out, nil
}

// GetExternalOTCSettlement returns the settlement row for a partner tx, or
// (nil, nil) when none exists — so commit/rollback routing can tell an OTC
// settlement from a plain cash transfer.
func (s *Store) GetExternalOTCSettlement(ctx context.Context, tx pgx.Tx, senderRouting int, txID string) (*domain.ExternalOTCSettlement, error) {
	const q = `select ` + externalOTCSettlementCols + `
	           from "trading".external_otc_settlements
	           where sender_routing_number = $1 and transaction_id = $2`
	out, err := scanExternalOTCSettlement(s.execer(tx).QueryRow(ctx, q, senderRouting, txID))
	if err != nil {
		if noRows(err) {
			return nil, nil
		}
		logger.From(ctx).ErrorContext(ctx, "get external otc settlement failed", "err", err, "tx_id", txID)
		return nil, apperr.Internal("get external otc settlement", err)
	}
	return out, nil
}

// SetExternalOTCSettlementStatus advances a settlement to committed or
// rolled_back, optionally stamping the local contract id and the bank
// cash-leg op id (empty strings leave the existing value).
func (s *Store) SetExternalOTCSettlementStatus(ctx context.Context, tx pgx.Tx, senderRouting int, txID, status, contractID, opID string) error {
	const q = `
        update "trading".external_otc_settlements
           set status = $3,
               contract_id = coalesce(nullif($4, '')::uuid, contract_id),
               op_id = coalesce(nullif($5, '')::uuid, op_id),
               updated_at = now()
         where sender_routing_number = $1 and transaction_id = $2
         returning transaction_id`
	var got string
	err := s.execer(tx).QueryRow(ctx, q, senderRouting, txID, status, contractID, opID).Scan(&got)
	if err != nil {
		if noRows(err) {
			return apperr.NotFound("settlement ne postoji")
		}
		logger.From(ctx).ErrorContext(ctx, "set external otc settlement status failed", "err", err, "tx_id", txID, "contract_id", contractID)
		return apperr.Internal("set external otc settlement status", err)
	}
	return nil
}
