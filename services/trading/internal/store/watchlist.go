package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const watchlistCols = `id, user_id, user_kind, name, created_at`

// CreateWatchlist inserts one named watchlist for a user and returns it.
func (s *Store) CreateWatchlist(ctx context.Context, w *domain.Watchlist) (*domain.Watchlist, error) {
	const q = `
        insert into "trading".watchlists (user_id, user_kind, name)
        values ($1, $2, $3)
        returning ` + watchlistCols
	row := s.DB.QueryRow(ctx, q, w.UserID, string(w.UserKind), w.Name)
	out, err := scanWatchlist(row)
	if err != nil {
		return nil, apperr.Internal("insert watchlist", err)
	}
	return out, nil
}

// ListWatchlists returns every watchlist owned by one user, newest first.
// Items are not joined here — the service layer hydrates them per list
// via ListItems.
func (s *Store) ListWatchlists(ctx context.Context, userID string) ([]*domain.Watchlist, error) {
	q := `select ` + watchlistCols + ` from "trading".watchlists
	      where user_id = $1 order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID)
	if err != nil {
		return nil, apperr.Internal("list watchlists", err)
	}
	defer rows.Close()
	var out []*domain.Watchlist
	for rows.Next() {
		w, err := scanWatchlist(rows)
		if err != nil {
			return nil, apperr.Internal("scan watchlist", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWatchlist returns one watchlist by id (without items). NotFound on
// miss. The service layer asserts ownership before mutating.
func (s *Store) GetWatchlist(ctx context.Context, id string) (*domain.Watchlist, error) {
	q := `select ` + watchlistCols + ` from "trading".watchlists where id = $1`
	out, err := scanWatchlist(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("lista za praćenje nije pronađena")
		}
		return nil, apperr.Internal("get watchlist", err)
	}
	return out, nil
}

// DeleteWatchlist removes one watchlist row; items cascade-delete.
func (s *Store) DeleteWatchlist(ctx context.Context, id string) error {
	const q = `delete from "trading".watchlists where id = $1`
	if _, err := s.DB.Exec(ctx, q, id); err != nil {
		return apperr.Internal("delete watchlist", err)
	}
	return nil
}

// AddItem adds a security to a watchlist. The unique (watchlist_id,
// security_id) constraint makes re-adding an existing security a no-op
// (on conflict do nothing) rather than an error.
func (s *Store) AddItem(ctx context.Context, watchlistID, securityID string) (*domain.WatchlistItem, error) {
	const q = `
        insert into "trading".watchlist_items (watchlist_id, security_id)
        values ($1, $2)
        on conflict (watchlist_id, security_id) do update
            set security_id = excluded.security_id
        returning id, security_id, created_at`
	var it domain.WatchlistItem
	if err := s.DB.QueryRow(ctx, q, watchlistID, securityID).
		Scan(&it.ID, &it.SecurityID, &it.CreatedAt); err != nil {
		return nil, apperr.Internal("add watchlist item", err)
	}
	return &it, nil
}

// RemoveItem deletes a security from a watchlist (S37). Idempotent.
func (s *Store) RemoveItem(ctx context.Context, watchlistID, securityID string) error {
	const q = `delete from "trading".watchlist_items
	           where watchlist_id = $1 and security_id = $2`
	if _, err := s.DB.Exec(ctx, q, watchlistID, securityID); err != nil {
		return apperr.Internal("remove watchlist item", err)
	}
	return nil
}

// ListItems returns the raw items on a watchlist, newest first. The
// service layer decorates each with security + listing data.
func (s *Store) ListItems(ctx context.Context, watchlistID string) ([]*domain.WatchlistItem, error) {
	const q = `select id, security_id, created_at
	           from "trading".watchlist_items
	           where watchlist_id = $1 order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, watchlistID)
	if err != nil {
		return nil, apperr.Internal("list watchlist items", err)
	}
	defer rows.Close()
	var out []*domain.WatchlistItem
	for rows.Next() {
		var it domain.WatchlistItem
		if err := rows.Scan(&it.ID, &it.SecurityID, &it.CreatedAt); err != nil {
			return nil, apperr.Internal("scan watchlist item", err)
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

func scanWatchlist(row pgx.Row) (*domain.Watchlist, error) {
	var (
		w    domain.Watchlist
		kind string
	)
	if err := row.Scan(&w.ID, &w.UserID, &kind, &w.Name, &w.CreatedAt); err != nil {
		return nil, err
	}
	w.UserKind = domain.UserKind(kind)
	return &w, nil
}
