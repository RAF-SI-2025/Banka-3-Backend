package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/notification/internal/domain"
)

const notifCols = `id, user_id, user_kind, kind, title, body, read_at, created_at`

func scanNotification(row interface{ Scan(...any) error }) (*domain.Notification, error) {
	var n domain.Notification
	var readAt *time.Time
	if err := row.Scan(
		&n.ID, &n.UserID, &n.UserKind, &n.Kind, &n.Title, &n.Body, &readAt, &n.CreatedAt,
	); err != nil {
		return nil, err
	}
	n.ReadAt = readAt
	return &n, nil
}

// Insert writes a new (unread) notification and returns the stored row.
func (s *Store) Insert(ctx context.Context, n *domain.Notification) (*domain.Notification, error) {
	const q = `
        insert into "notification".notifications (user_id, user_kind, kind, title, body)
        values ($1, $2, $3, $4, $5)
        returning ` + notifCols
	out, err := scanNotification(s.Pool.QueryRow(ctx, q,
		n.UserID, n.UserKind, n.Kind, n.Title, n.Body,
	))
	if err != nil {
		return nil, apperr.Internal("insert notification", err)
	}
	return out, nil
}

// ListByUser returns a page of the user's notifications, newest first.
// unreadOnly restricts the result to unread rows.
func (s *Store) ListByUser(ctx context.Context, userID string, unreadOnly bool, limit, offset int) ([]*domain.Notification, error) {
	q := `select ` + notifCols + ` from "notification".notifications where user_id = $1`
	if unreadOnly {
		q += ` and read_at is null`
	}
	q += ` order by created_at desc limit $2 offset $3`
	rows, err := s.Pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, apperr.Internal("list notifications", err)
	}
	defer rows.Close()
	var out []*domain.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, apperr.Internal("scan notification", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CountByUser counts the user's notifications matching the filter (used
// for the paged total).
func (s *Store) CountByUser(ctx context.Context, userID string, unreadOnly bool) (int64, error) {
	q := `select count(*) from "notification".notifications where user_id = $1`
	if unreadOnly {
		q += ` and read_at is null`
	}
	var total int64
	if err := s.Pool.QueryRow(ctx, q, userID).Scan(&total); err != nil {
		return 0, apperr.Internal("count notifications", err)
	}
	return total, nil
}

// CountUnread returns the user's unread total regardless of any filter —
// the FE renders this as the bell badge.
func (s *Store) CountUnread(ctx context.Context, userID string) (int64, error) {
	const q = `select count(*) from "notification".notifications where user_id = $1 and read_at is null`
	var n int64
	if err := s.Pool.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		return 0, apperr.Internal("count unread notifications", err)
	}
	return n, nil
}

// MarkRead flips one notification to read, scoped to the owner so a user
// can only touch their own. Returns NotFound when the id doesn't belong
// to the user (or doesn't exist). Idempotent: already-read rows keep
// their original read_at via coalesce.
func (s *Store) MarkRead(ctx context.Context, userID, id string) (*domain.Notification, error) {
	const q = `
        update "notification".notifications
        set read_at = coalesce(read_at, now())
        where id = $1 and user_id = $2
        returning ` + notifCols
	out, err := scanNotification(s.Pool.QueryRow(ctx, q, id, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("notification not found")
		}
		return nil, apperr.Internal("mark notification read", err)
	}
	return out, nil
}

// MarkAllRead flips every unread notification of the user to read and
// returns the number affected.
func (s *Store) MarkAllRead(ctx context.Context, userID string) (int64, error) {
	const q = `
        update "notification".notifications
        set read_at = now()
        where user_id = $1 and read_at is null`
	tag, err := s.Pool.Exec(ctx, q, userID)
	if err != nil {
		return 0, apperr.Internal("mark all notifications read", err)
	}
	return tag.RowsAffected(), nil
}
