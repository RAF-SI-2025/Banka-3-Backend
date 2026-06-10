package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

const recipientColumns = `id, client_id, name, account_number, created_at`

func scanRecipient(row interface{ Scan(...any) error }) (*domain.PaymentRecipient, error) {
	var r domain.PaymentRecipient
	if err := row.Scan(&r.ID, &r.ClientID, &r.Name, &r.AccountNumber, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) UpsertPaymentRecipient(ctx context.Context, r *domain.PaymentRecipient) (*domain.PaymentRecipient, error) {
	const q = `
        insert into "bank".payment_recipients (client_id, name, account_number)
        values ($1, $2, $3)
        on conflict (client_id, account_number) do update
            set name = excluded.name
        returning ` + recipientColumns
	out, err := scanRecipient(s.DB.QueryRow(ctx, q, r.ClientID, r.Name, r.AccountNumber))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "upsert recipient failed", "err", err, "client_id", r.ClientID)
		return nil, apperr.Internal("upsert recipient", err)
	}
	return out, nil
}

func (s *Store) ListPaymentRecipients(ctx context.Context, clientID string) ([]*domain.PaymentRecipient, error) {
	const q = `select ` + recipientColumns + ` from "bank".payment_recipients where client_id = $1 order by lower(name)`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, clientID)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list recipients failed", "err", err, "client_id", clientID)
		return nil, apperr.Internal("list recipients", err)
	}
	defer rows.Close()
	var out []*domain.PaymentRecipient
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan recipient failed", "err", err)
			return nil, apperr.Internal("scan recipient", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate recipients failed", "err", err)
		return out, err
	}
	return out, nil
}

func (s *Store) UpdatePaymentRecipient(ctx context.Context, r *domain.PaymentRecipient) (*domain.PaymentRecipient, error) {
	const q = `
        update "bank".payment_recipients set name = $2, account_number = $3
        where id = $1 and client_id = $4
        returning ` + recipientColumns
	out, err := scanRecipient(s.DB.QueryRow(ctx, q, r.ID, r.Name, r.AccountNumber, r.ClientID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("primalac ne postoji")
		}
		logger.From(ctx).ErrorContext(ctx, "update recipient failed", "err", err, "recipient_id", r.ID, "client_id", r.ClientID)
		return nil, apperr.Internal("update recipient", err)
	}
	return out, nil
}

func (s *Store) DeletePaymentRecipient(ctx context.Context, id, clientID string) error {
	const q = `delete from "bank".payment_recipients where id = $1 and client_id = $2`
	tag, err := s.DB.Exec(ctx, q, id, clientID)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "delete recipient failed", "err", err, "recipient_id", id, "client_id", clientID)
		return apperr.Internal("delete recipient", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("primalac ne postoji")
	}
	return nil
}
