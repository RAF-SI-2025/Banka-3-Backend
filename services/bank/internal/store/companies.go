package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

const companyColumns = `
    id, name, registry_id, tax_id, activity_code, address,
    owner_client_id, created_at, updated_at
`

func scanCompany(row interface{ Scan(...any) error }) (*domain.Company, error) {
	var c domain.Company
	if err := row.Scan(
		&c.ID, &c.Name, &c.RegistryID, &c.TaxID, &c.ActivityCode, &c.Address,
		&c.OwnerClientID, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CreateCompany(ctx context.Context, c *domain.Company) (*domain.Company, error) {
	const q = `
        insert into "bank".companies
            (name, registry_id, tax_id, activity_code, address, owner_client_id)
        values ($1,$2,$3,$4,$5,$6)
        returning ` + companyColumns
	out, err := scanCompany(s.DB.QueryRow(
		ctx, q,
		c.Name, c.RegistryID, c.TaxID, c.ActivityCode, c.Address, c.OwnerClientID,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("company with this matični broj or PIB already exists")
		}
		logger.From(ctx).ErrorContext(ctx, "create company failed", "err", err, "owner_client_id", c.OwnerClientID)
		return nil, apperr.Internal("create company", err)
	}
	return out, nil
}

func (s *Store) GetCompanyByID(ctx context.Context, id string) (*domain.Company, error) {
	const q = `select ` + companyColumns + ` from "bank".companies where id = $1`
	out, err := scanCompany(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("company not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get company failed", "err", err, "company_id", id)
		return nil, apperr.Internal("get company", err)
	}
	return out, nil
}

func (s *Store) UpdateCompany(ctx context.Context, c *domain.Company) (*domain.Company, error) {
	const q = `
        update "bank".companies set
            name = $2, activity_code = $3, address = $4, owner_client_id = $5,
            updated_at = now()
        where id = $1
        returning ` + companyColumns
	out, err := scanCompany(s.DB.QueryRow(
		ctx, q,
		c.ID, c.Name, c.ActivityCode, c.Address, c.OwnerClientID,
	))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("company not found")
		}
		logger.From(ctx).ErrorContext(ctx, "update company failed", "err", err, "company_id", c.ID)
		return nil, apperr.Internal("update company", err)
	}
	return out, nil
}

func (s *Store) ListCompanies(ctx context.Context, f domain.CompanyFilter, page, pageSize int) ([]*domain.Company, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	var conds []string
	var args []any
	if f.Name != "" {
		args = append(args, f.Name)
		conds = append(conds, fmt.Sprintf("lower(name) like '%%' || lower($%d) || '%%'", len(args)))
	}
	if f.RegistryID != "" {
		args = append(args, f.RegistryID)
		conds = append(conds, fmt.Sprintf("registry_id like '%%' || $%d || '%%'", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), `select count(*) from "bank".companies`+where, args...).Scan(&total); err != nil {
		logger.From(ctx).ErrorContext(ctx, "count companies failed", "err", err)
		return nil, 0, apperr.Internal("count companies", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + companyColumns + ` from "bank".companies` + where +
		fmt.Sprintf(" order by lower(name) limit $%d offset $%d", len(args)+1, len(args)+2)

	rows, err := s.DB.Query(postgres.WithRead(ctx), listQ, listArgs...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list companies failed", "err", err)
		return nil, 0, apperr.Internal("list companies", err)
	}
	defer rows.Close()
	var out []*domain.Company
	for rows.Next() {
		c, err := scanCompany(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan company failed", "err", err)
			return nil, 0, apperr.Internal("scan company", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate companies failed", "err", err)
		return out, total, err
	}
	return out, total, nil
}
