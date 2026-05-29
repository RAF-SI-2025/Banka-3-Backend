package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const exchangeCols = `mic, name, acronym, polity, currency, timezone,
    to_char(open_local,'HH24:MI'), to_char(close_local,'HH24:MI'),
    override_state, updated_at`

// UpsertExchange writes one row keyed by MIC.
func (s *Store) UpsertExchange(ctx context.Context, e *domain.Exchange) (*domain.Exchange, error) {
	const q = `
        insert into "trading".exchanges (mic, name, acronym, polity, currency, timezone, open_local, close_local, created_at, updated_at)
        values ($1,$2,$3,$4,$5,$6,$7,$8, now(), now())
        on conflict (mic) do update
          set name        = excluded.name,
              acronym     = excluded.acronym,
              polity      = excluded.polity,
              currency    = excluded.currency,
              timezone    = excluded.timezone,
              open_local  = excluded.open_local,
              close_local = excluded.close_local,
              updated_at  = now()
        returning ` + exchangeCols
	row := s.Pool.QueryRow(ctx, q,
		e.MIC, e.Name, e.Acronym, e.Polity, string(e.Currency), e.Timezone, e.OpenLocal, e.CloseLocal,
	)
	out, err := scanExchange(row)
	if err != nil {
		return nil, apperr.Internal("upsert exchange", err)
	}
	return out, nil
}

// SetExchangeOverride writes the override_state column. state==nil
// clears the override (use schedule); else writes one of the three
// supported values. Returns the updated row or NotFound.
func (s *Store) SetExchangeOverride(ctx context.Context, mic string, state *domain.ExchangeOverrideState) (*domain.Exchange, error) {
	if state == nil {
		q := `update "trading".exchanges set override_state = NULL, updated_at = now() where mic = $1
		      returning ` + exchangeCols
		out, err := scanExchange(s.Pool.QueryRow(ctx, q, mic))
		return wrapExchange(out, err)
	}
	q := `update "trading".exchanges set override_state = $2, updated_at = now() where mic = $1
	      returning ` + exchangeCols
	out, err := scanExchange(s.Pool.QueryRow(ctx, q, mic, string(*state)))
	return wrapExchange(out, err)
}

func wrapExchange(out *domain.Exchange, err error) (*domain.Exchange, error) {
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("exchange not found")
		}
		return nil, apperr.Internal("exchange", err)
	}
	return out, nil
}

// GetExchange returns one row by MIC or NotFound.
func (s *Store) GetExchange(ctx context.Context, mic string) (*domain.Exchange, error) {
	const q = `select ` + exchangeCols + ` from "trading".exchanges where mic = $1`
	out, err := scanExchange(s.Pool.QueryRow(ctx, q, mic))
	return wrapExchange(out, err)
}

// ListExchanges returns every row, ordered alphabetically by MIC.
func (s *Store) ListExchanges(ctx context.Context) ([]*domain.Exchange, error) {
	const q = `select ` + exchangeCols + ` from "trading".exchanges order by mic`
	rows, err := s.Pool.Query(ctx, q)
	if err != nil {
		return nil, apperr.Internal("list exchanges", err)
	}
	defer rows.Close()
	var out []*domain.Exchange
	for rows.Next() {
		e, err := scanExchange(rows)
		if err != nil {
			return nil, apperr.Internal("scan exchange", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanExchange(row pgx.Row) (*domain.Exchange, error) {
	var (
		e        domain.Exchange
		cur      string
		override *string
	)
	if err := row.Scan(&e.MIC, &e.Name, &e.Acronym, &e.Polity, &cur, &e.Timezone, &e.OpenLocal, &e.CloseLocal, &override, &e.UpdatedAt); err != nil {
		return nil, err
	}
	e.Currency = domain.Currency(cur)
	if override != nil {
		s := domain.ExchangeOverrideState(*override)
		e.OverrideState = &s
	}
	return &e, nil
}
