package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

const cardColumns = `
    id, number, cvv_hash, brand, name,
    account_id, coalesce(authorized_person_id::text, ''),
    card_limit::text, expires_at, status, created_at, updated_at
`

func scanCard(row interface{ Scan(...any) error }) (*domain.Card, error) {
	var c domain.Card
	var brand, status string
	if err := row.Scan(
		&c.ID, &c.Number, &c.CVVHash, &brand, &c.Name,
		&c.AccountID, &c.AuthorizedPersonID,
		&c.CardLimit, &c.ExpiresAt, &status, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.Brand = domain.CardBrand(brand)
	c.Status = domain.CardStatus(status)
	return &c, nil
}

func (s *Store) CreateCard(ctx context.Context, c *domain.Card) (*domain.Card, error) {
	const q = `
        insert into "bank".cards
            (number, cvv_hash, brand, name, account_id, authorized_person_id,
             card_limit, expires_at, status)
        values
            ($1,$2,$3,$4,$5,nullif($6,'')::uuid,$7::numeric,$8,$9)
        returning ` + cardColumns
	out, err := scanCard(s.DB.QueryRow(
		ctx, q,
		c.Number, c.CVVHash, string(c.Brand), c.Name, c.AccountID, c.AuthorizedPersonID,
		c.CardLimit, c.ExpiresAt, string(c.Status),
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("card number collision")
		}
		return nil, apperr.Internal("create card", err)
	}
	return out, nil
}

func (s *Store) GetCardByID(ctx context.Context, id string) (*domain.Card, error) {
	const q = `select ` + cardColumns + ` from "bank".cards where id = $1`
	out, err := scanCard(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("kartica ne postoji")
		}
		return nil, apperr.Internal("get card", err)
	}
	return out, nil
}

func (s *Store) ListCardsByAccount(ctx context.Context, accountID string) ([]*domain.Card, error) {
	const q = `select ` + cardColumns + ` from "bank".cards where account_id = $1 order by created_at`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, accountID)
	if err != nil {
		return nil, apperr.Internal("list cards", err)
	}
	defer rows.Close()
	var out []*domain.Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, apperr.Internal("scan card", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCardsByOwner returns every card whose owning account belongs to
// the given client.
func (s *Store) ListCardsByOwner(ctx context.Context, ownerClientID string) ([]*domain.Card, error) {
	const q = `select c.id, c.number, c.cvv_hash, c.brand, c.name,
	           c.account_id, coalesce(c.authorized_person_id::text, ''),
	           c.card_limit::text, c.expires_at, c.status, c.created_at, c.updated_at
	           from "bank".cards c
	           join "bank".accounts a on a.id = c.account_id
	           where a.owner_client_id = $1
	           order by c.created_at`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, ownerClientID)
	if err != nil {
		return nil, apperr.Internal("list cards by owner", err)
	}
	defer rows.Close()
	var out []*domain.Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, apperr.Internal("scan card", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListAllCards returns every card in the system. Reserved for
// employee-side card administration; the service layer enforces
// CardRead/Admin.
func (s *Store) ListAllCards(ctx context.Context) ([]*domain.Card, error) {
	const q = `select ` + cardColumns + ` from "bank".cards order by created_at`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q)
	if err != nil {
		return nil, apperr.Internal("list all cards", err)
	}
	defer rows.Close()
	var out []*domain.Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, apperr.Internal("scan card", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountActiveCards returns the count of non-deactivated cards on
// account, optionally narrowed to a specific OvlascenoLice. Used by
// the service to enforce spec p.27 limits (max 2 lični, max 1 per
// OvlascenoLice on poslovni).
func (s *Store) CountActiveCards(ctx context.Context, accountID, authorizedPersonID string) (int, error) {
	q := `select count(*) from "bank".cards
          where account_id = $1 and status <> 'deactivated'`
	args := []any{accountID}
	if authorizedPersonID != "" {
		q += ` and authorized_person_id = $2`
		args = append(args, authorizedPersonID)
	} else {
		q += ` and authorized_person_id is null`
	}
	var n int
	if err := s.DB.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, apperr.Internal("count cards", err)
	}
	return n, nil
}

func (s *Store) SetCardStatus(ctx context.Context, id string, status domain.CardStatus) (*domain.Card, error) {
	const q = `
        update "bank".cards set status = $2, updated_at = now()
        where id = $1
        returning ` + cardColumns
	out, err := scanCard(s.DB.QueryRow(ctx, q, id, string(status)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("kartica ne postoji")
		}
		return nil, apperr.Internal("set card status", err)
	}
	return out, nil
}

func (s *Store) UpdateCardLimit(ctx context.Context, id, cardLimit string) (*domain.Card, error) {
	const q = `
        update "bank".cards set card_limit = $2::numeric, updated_at = now()
        where id = $1
        returning ` + cardColumns
	out, err := scanCard(s.DB.QueryRow(ctx, q, id, cardLimit))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("kartica ne postoji")
		}
		return nil, apperr.Internal("update card limit", err)
	}
	return out, nil
}
