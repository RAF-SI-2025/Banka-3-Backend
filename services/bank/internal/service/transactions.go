package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// ListTransactions returns ledger rows. Clients are auto-scoped: they
// see only rows that touch one of their own accounts (filter applied
// by joining on account_id; we fetch their account list once and pass
// the IDs through). Employees see whatever the filter says.
//
// For slice 1 simplicity, when a client passes no account_id we filter
// by initiator_client_id, which gives them their own outgoing ops; an
// account-touch filter would also include incoming legs of FX
// settlements where they're the recipient on a 2-leg path. The FE
// spec says "Pregled plaćanja" — the typical user case is "my own
// initiated payments" — so initiator-scoped is the right default.
func (s *Service) ListTransactions(ctx context.Context, f domain.TransactionFilter, page, pageSize int) ([]*domain.Transaction, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if p.UserKind == auth.KindClient {
		if f.AccountID == "" {
			f.InitiatorClientID = p.UserID
		} else {
			// AccountID was supplied — verify it belongs to caller.
			a, err := s.Store.GetAccountByID(ctx, f.AccountID)
			if err != nil {
				return nil, 0, err
			}
			if a.OwnerClientID != p.UserID {
				s.log().WarnContext(ctx, "list transactions denied: account not owned by caller",
					"account_id", f.AccountID, "user_id", p.UserID)
				return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
			}
		}
	} else if !permissions.HasAny(p.Permissions, permissions.AccountRead, permissions.Admin) {
		return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.ListTransactions(ctx, f, page, pageSize)
}
