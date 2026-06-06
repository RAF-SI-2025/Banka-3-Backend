package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

const auditCols = `id, action, actor_id, actor_kind, actor_name, target_id, target_label, old_value, new_value, note, created_at`

func scanAudit(row interface{ Scan(...any) error }) (*domain.AuditEntry, error) {
	var e domain.AuditEntry
	if err := row.Scan(
		&e.ID, &e.Action, &e.ActorID, &e.ActorKind, &e.ActorName,
		&e.TargetID, &e.TargetLabel, &e.OldValue, &e.NewValue, &e.Note, &e.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// InsertAudit appends one audit entry.
func (s *Store) InsertAudit(ctx context.Context, e *domain.AuditEntry) error {
	const q = `
        insert into "user".audit_log
            (action, actor_id, actor_kind, actor_name, target_id, target_label, old_value, new_value, note)
        values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	if _, err := s.DB.Exec(ctx, q,
		e.Action, e.ActorID, e.ActorKind, e.ActorName,
		e.TargetID, e.TargetLabel, e.OldValue, e.NewValue, e.Note,
	); err != nil {
		return apperr.Internal("insert audit", err)
	}
	return nil
}

// ListAudit returns a page of entries (newest first) matching f, plus
// the total count matching the filter (ignoring pagination).
func (s *Store) ListAudit(ctx context.Context, f domain.AuditFilter, limit, offset int) ([]*domain.AuditEntry, int64, error) {
	var conds []string
	var args []any
	if f.Action != "" {
		args = append(args, f.Action)
		conds = append(conds, fmt.Sprintf("action = $%d", len(args)))
	}
	if f.Actor != "" {
		args = append(args, f.Actor)
		idIdx := len(args)
		args = append(args, "%"+f.Actor+"%")
		conds = append(conds, fmt.Sprintf("(actor_id = $%d or actor_name ilike $%d)", idIdx, len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		conds = append(conds, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		conds = append(conds, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx),
		`select count(*) from "user".audit_log`+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count audit", err)
	}

	args = append(args, limit, offset)
	q := `select ` + auditCols + ` from "user".audit_log` + where +
		fmt.Sprintf(" order by created_at desc limit $%d offset $%d", len(args)-1, len(args))
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, 0, apperr.Internal("list audit", err)
	}
	defer rows.Close()
	var out []*domain.AuditEntry
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan audit", err)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
