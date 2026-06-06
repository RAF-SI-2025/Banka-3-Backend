package store

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// =====================================================================
// OTC offers
// =====================================================================

const otcOfferCols = `
    id, thread_id, security_id, seller_holding_id,
    buyer_id, buyer_kind, buyer_account_id,
    seller_id, seller_kind, seller_account_id,
    quantity, price_per_unit::text, premium::text, currency, settlement_date,
    modified_by, status, created_at, updated_at`

func scanOTCOffer(row pgx.Row) (*domain.OTCOffer, error) {
	var o domain.OTCOffer
	var bk, sk, cur, status string
	if err := row.Scan(
		&o.ID, &o.ThreadID, &o.SecurityID, &o.SellerHoldingID,
		&o.BuyerID, &bk, &o.BuyerAccountID,
		&o.SellerID, &sk, &o.SellerAccountID,
		&o.Quantity, &o.PricePerUnit, &o.Premium, &cur, &o.SettlementDate,
		&o.ModifiedBy, &status, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, err
	}
	o.BuyerKind = domain.UserKind(bk)
	o.SellerKind = domain.UserKind(sk)
	o.Currency = domain.Currency(cur)
	o.Status = domain.OTCStatus(status)
	return &o, nil
}

// InsertOTCOffer writes a new offer iteration. When iter is the
// thread's first row the caller passes thread_id == "" and the store
// derives it from the inserted id (the first iteration's id is the
// thread_id by convention).
func (s *Store) InsertOTCOffer(ctx context.Context, tx pgx.Tx, o *domain.OTCOffer) (*domain.OTCOffer, error) {
	const q = `
        insert into "trading".otc_offers (
            thread_id, security_id, seller_holding_id,
            buyer_id, buyer_kind, buyer_account_id,
            seller_id, seller_kind, seller_account_id,
            quantity, price_per_unit, premium, currency, settlement_date,
            modified_by, status
        ) values (
            coalesce(nullif($1, '')::uuid, gen_random_uuid()),
            $2, $3,
            $4, $5, $6,
            $7, $8, $9,
            $10, $11::numeric, $12::numeric, $13, $14,
            $15, $16
        ) returning ` + otcOfferCols
	var execer interface {
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	} = s.DB
	if tx != nil {
		execer = tx
	}
	row := execer.QueryRow(
		ctx, q,
		o.ThreadID, o.SecurityID, o.SellerHoldingID,
		o.BuyerID, string(o.BuyerKind), o.BuyerAccountID,
		o.SellerID, string(o.SellerKind), o.SellerAccountID,
		o.Quantity, o.PricePerUnit, o.Premium, string(o.Currency), o.SettlementDate,
		o.ModifiedBy, string(o.Status),
	)
	out, err := scanOTCOffer(row)
	if err != nil {
		return nil, apperr.Internal("insert otc offer", err)
	}
	// First-iteration convention: thread_id == id when caller left it
	// empty. Second update; cheap, keeps the column index trustworthy.
	if o.ThreadID == "" {
		const u = `update "trading".otc_offers set thread_id = id where id = $1 returning ` + otcOfferCols
		row := execer.QueryRow(ctx, u, out.ID)
		out, err = scanOTCOffer(row)
		if err != nil {
			return nil, apperr.Internal("update thread_id", err)
		}
	}
	return out, nil
}

// GetOTCOffer returns one iteration by id.
func (s *Store) GetOTCOffer(ctx context.Context, id string) (*domain.OTCOffer, error) {
	const q = `select ` + otcOfferCols + ` from "trading".otc_offers where id = $1`
	out, err := scanOTCOffer(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ponuda ne postoji")
		}
		return nil, apperr.Internal("get otc offer", err)
	}
	return out, nil
}

// GetOpenOTCOfferByThread returns the live (status='open') iteration in
// a thread or NotFound. Used by the accept/withdraw/counter handlers.
// When called inside a tx the open row is locked `for update` so that
// concurrent withdraw+counter on the same thread serialize (otherwise
// both txs read the same row, both decrement reserved_count, and the
// second status flip is a silent no-op against an already-terminal row).
func (s *Store) GetOpenOTCOfferByThread(ctx context.Context, tx pgx.Tx, threadID string) (*domain.OTCOffer, error) {
	const qLock = `select ` + otcOfferCols + ` from "trading".otc_offers
	               where thread_id = $1 and status = 'open' limit 1
	               for update`
	const qRead = `select ` + otcOfferCols + ` from "trading".otc_offers
	               where thread_id = $1 and status = 'open' limit 1`
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, qLock, threadID)
	} else {
		row = s.DB.QueryRow(ctx, qRead, threadID)
	}
	out, err := scanOTCOffer(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("nema aktivne iteracije")
		}
		return nil, apperr.Internal("get open otc offer", err)
	}
	return out, nil
}

// ListOTCThread returns every iteration in a thread ordered oldest-first.
func (s *Store) ListOTCThread(ctx context.Context, threadID string) ([]*domain.OTCOffer, error) {
	const q = `select ` + otcOfferCols + ` from "trading".otc_offers
	           where thread_id = $1 order by created_at`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, threadID)
	if err != nil {
		return nil, apperr.Internal("list otc thread", err)
	}
	defer rows.Close()
	var out []*domain.OTCOffer
	for rows.Next() {
		o, err := scanOTCOffer(rows)
		if err != nil {
			return nil, apperr.Internal("scan otc offer", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OTCThreadFilter narrows ListLatestOTCOffers — the "Aktivne ponude" view.
type OTCThreadFilter struct {
	// PartyID + PartyKind narrow to threads where (buyer == party) OR
	// (seller == party). Empty means "no party filter" (admin/supervisor).
	PartyID   string
	PartyKind domain.UserKind
	// Status: "open" (default) or "any".
	Status string
}

// ListLatestOTCOffers returns one row per thread — the most-recent
// iteration's status row. Sorted newest-first by updated_at.
func (s *Store) ListLatestOTCOffers(ctx context.Context, f OTCThreadFilter) ([]*domain.OTCOffer, error) {
	conds := []string{"true"}
	var args []any
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if f.Status == "" || f.Status == "open" {
		add("o.status = ?", "open")
	}
	if f.PartyID != "" {
		add("(o.buyer_id = ? or o.seller_id = ?)", f.PartyID, f.PartyID)
	}
	if f.PartyKind != "" {
		add("(o.buyer_kind = ? or o.seller_kind = ?)", string(f.PartyKind), string(f.PartyKind))
	}
	// Alias the CTE's thread_id column so the projection's bare
	// `thread_id` resolves unambiguously to the otc_offers row.
	q := `
        with latest as (
            select thread_id as tid, max(created_at) as ts
            from "trading".otc_offers
            group by thread_id
        )
        select ` + otcOfferCols + `
        from "trading".otc_offers o
        join latest l on l.tid = o.thread_id and l.ts = o.created_at
        where ` + strings.Join(conds, " and ") + `
        order by o.updated_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, apperr.Internal("list otc threads", err)
	}
	defer rows.Close()
	var out []*domain.OTCOffer
	for rows.Next() {
		o, err := scanOTCOffer(rows)
		if err != nil {
			return nil, apperr.Internal("scan otc offer", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// MarkOTCOfferStatus flips the status of a single iteration. Inside the
// caller's tx so the same write covers reservation adjustments.
func (s *Store) MarkOTCOfferStatus(ctx context.Context, tx pgx.Tx, offerID string, status domain.OTCStatus) (*domain.OTCOffer, error) {
	const q = `update "trading".otc_offers
	           set status = $2, updated_at = now()
	           where id = $1 returning ` + otcOfferCols
	row := tx.QueryRow(ctx, q, offerID, string(status))
	out, err := scanOTCOffer(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ponuda ne postoji")
		}
		return nil, apperr.Internal("update otc offer status", err)
	}
	return out, nil
}

// SupersedePriorOTCOffers flips every `open` iteration in a thread to
// `superseded`. Used by counter — the new iteration becomes the live
// one; prior iterations stay as audit.
func (s *Store) SupersedePriorOTCOffers(ctx context.Context, tx pgx.Tx, threadID string) error {
	const q = `update "trading".otc_offers
	           set status = 'superseded', updated_at = now()
	           where thread_id = $1 and status = 'open'`
	if _, err := tx.Exec(ctx, q, threadID); err != nil {
		return apperr.Internal("supersede prior otc offers", err)
	}
	return nil
}

// MarkAllOTCOffersAcceptedInThread flips every non-terminal iteration in
// a thread to a terminal status: the live (open) row becomes `accepted`,
// every prior `open` row (there should be none, but defensive) becomes
// `superseded`. Used by the accept saga's create_contract step.
func (s *Store) MarkAllOTCOffersAcceptedInThread(ctx context.Context, tx pgx.Tx, threadID, acceptedOfferID string) error {
	const q1 = `update "trading".otc_offers
	            set status = 'accepted', updated_at = now()
	            where id = $1`
	if _, err := tx.Exec(ctx, q1, acceptedOfferID); err != nil {
		return apperr.Internal("mark otc offer accepted", err)
	}
	const q2 = `update "trading".otc_offers
	            set status = 'superseded', updated_at = now()
	            where thread_id = $1 and id <> $2 and status = 'open'`
	if _, err := tx.Exec(ctx, q2, threadID, acceptedOfferID); err != nil {
		return apperr.Internal("mark prior accepted thread offers", err)
	}
	return nil
}

// =====================================================================
// OTC contracts
// =====================================================================

const otcContractCols = `
    id, thread_id, security_id, seller_holding_id,
    buyer_id, buyer_kind, buyer_account_id,
    seller_id, seller_kind, seller_account_id,
    quantity, strike_price::text, premium_paid::text, currency, settlement_date,
    premium_op_id::text, status,
    coalesce(exercised_op_id::text, ''),
    coalesce(exercise_saga_id::text, ''),
    exercised_at, created_at, updated_at`

func scanOTCContract(row pgx.Row) (*domain.OTCContract, error) {
	var c domain.OTCContract
	var bk, sk, cur, status string
	var exAt *time.Time
	if err := row.Scan(
		&c.ID, &c.ThreadID, &c.SecurityID, &c.SellerHoldingID,
		&c.BuyerID, &bk, &c.BuyerAccountID,
		&c.SellerID, &sk, &c.SellerAccountID,
		&c.Quantity, &c.StrikePrice, &c.PremiumPaid, &cur, &c.SettlementDate,
		&c.PremiumOpID, &status,
		&c.ExercisedOpID, &c.ExerciseSagaID,
		&exAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.BuyerKind = domain.UserKind(bk)
	c.SellerKind = domain.UserKind(sk)
	c.Currency = domain.Currency(cur)
	c.Status = domain.OTCContractStatus(status)
	c.ExercisedAt = exAt
	return &c, nil
}

// InsertOTCContract mints a contract row for a freshly-accepted thread.
// One contract per thread is enforced by the unique index on thread_id;
// a retry of the same accept saga hits the unique constraint and the
// caller maps it to "contract already minted".
func (s *Store) InsertOTCContract(ctx context.Context, tx pgx.Tx, c *domain.OTCContract) (*domain.OTCContract, error) {
	const q = `
        insert into "trading".otc_contracts (
            thread_id, security_id, seller_holding_id,
            buyer_id, buyer_kind, buyer_account_id,
            seller_id, seller_kind, seller_account_id,
            quantity, strike_price, premium_paid, currency, settlement_date,
            premium_op_id, status
        ) values (
            $1, $2, $3,
            $4, $5, $6,
            $7, $8, $9,
            $10, $11::numeric, $12::numeric, $13, $14,
            $15, $16
        ) on conflict (thread_id) do update
            set updated_at = "trading".otc_contracts.updated_at
        returning ` + otcContractCols
	row := tx.QueryRow(
		ctx, q,
		c.ThreadID, c.SecurityID, c.SellerHoldingID,
		c.BuyerID, string(c.BuyerKind), c.BuyerAccountID,
		c.SellerID, string(c.SellerKind), c.SellerAccountID,
		c.Quantity, c.StrikePrice, c.PremiumPaid, string(c.Currency), c.SettlementDate,
		c.PremiumOpID, string(c.Status),
	)
	out, err := scanOTCContract(row)
	if err != nil {
		return nil, apperr.Internal("insert otc contract", err)
	}
	return out, nil
}

// DeleteOTCContractByThread removes the contract for a thread. Used by
// the accept saga's create_contract compensation when the premium leg
// later fails. The thread reverts to `open` separately.
func (s *Store) DeleteOTCContractByThread(ctx context.Context, tx pgx.Tx, threadID string) error {
	const q = `delete from "trading".otc_contracts where thread_id = $1`
	if _, err := tx.Exec(ctx, q, threadID); err != nil {
		return apperr.Internal("delete otc contract by thread", err)
	}
	return nil
}

// GetOTCContract returns one contract by id.
func (s *Store) GetOTCContract(ctx context.Context, id string) (*domain.OTCContract, error) {
	const q = `select ` + otcContractCols + ` from "trading".otc_contracts where id = $1`
	out, err := scanOTCContract(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ugovor ne postoji")
		}
		return nil, apperr.Internal("get otc contract", err)
	}
	return out, nil
}

// GetOTCContractByThread returns the contract on a thread or NotFound.
func (s *Store) GetOTCContractByThread(ctx context.Context, threadID string) (*domain.OTCContract, error) {
	const q = `select ` + otcContractCols + ` from "trading".otc_contracts where thread_id = $1`
	out, err := scanOTCContract(s.DB.QueryRow(ctx, q, threadID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ugovor ne postoji")
		}
		return nil, apperr.Internal("get otc contract by thread", err)
	}
	return out, nil
}

// MarkOTCContractStatus flips a contract's status. Used by exercise
// (`exercised`) + expiry sweep (`expired`). Optionally stamps op_id /
// saga_id / exercised_at when transitioning to exercised.
func (s *Store) MarkOTCContractStatus(
	ctx context.Context, tx pgx.Tx, id string, status domain.OTCContractStatus,
	exercisedOpID, exerciseSagaID string, exercisedAt *time.Time,
) (*domain.OTCContract, error) {
	const q = `update "trading".otc_contracts
	           set status           = $2,
	               exercised_op_id  = case when $3 = '' then exercised_op_id else $3::uuid end,
	               exercise_saga_id = case when $4 = '' then exercise_saga_id else $4::uuid end,
	               exercised_at     = coalesce($5, exercised_at),
	               updated_at       = now()
	           where id = $1
	           returning ` + otcContractCols
	row := tx.QueryRow(ctx, q, id, string(status), exercisedOpID, exerciseSagaID, exercisedAt)
	out, err := scanOTCContract(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ugovor ne postoji")
		}
		return nil, apperr.Internal("update otc contract status", err)
	}
	return out, nil
}

// OTCContractFilter narrows ListOTCContracts.
type OTCContractFilter struct {
	PartyID   string
	PartyKind domain.UserKind
	Status    string // "active" (default) / "any"
}

// ListOTCContracts returns matching contracts ordered newest first.
func (s *Store) ListOTCContracts(ctx context.Context, f OTCContractFilter) ([]*domain.OTCContract, error) {
	var args []any
	conds := []string{"true"}
	add := func(cond string, a ...any) {
		for _, x := range a {
			args = append(args, x)
			cond = strings.Replace(cond, "?", intArg(len(args)), 1)
		}
		conds = append(conds, cond)
	}
	if f.Status == "" || f.Status == "active" {
		add("status = ?", "active")
	}
	if f.PartyID != "" {
		add("(buyer_id = ? or seller_id = ?)", f.PartyID, f.PartyID)
	}
	if f.PartyKind != "" {
		add("(buyer_kind = ? or seller_kind = ?)", string(f.PartyKind), string(f.PartyKind))
	}
	q := `select ` + otcContractCols + ` from "trading".otc_contracts where ` + strings.Join(conds, " and ") + ` order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, apperr.Internal("list otc contracts", err)
	}
	defer rows.Close()
	var out []*domain.OTCContract
	for rows.Next() {
		c, err := scanOTCContract(rows)
		if err != nil {
			return nil, apperr.Internal("scan otc contract", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListExpiredOTCContracts returns active contracts whose settlement_date
// is on or before `today`. Used by the expiry sweep cron.
func (s *Store) ListExpiredOTCContracts(ctx context.Context, today time.Time) ([]*domain.OTCContract, error) {
	const q = `select ` + otcContractCols + ` from "trading".otc_contracts
	           where status = 'active' and settlement_date < $1
	           order by settlement_date`
	rows, err := s.DB.Query(ctx, q, today)
	if err != nil {
		return nil, apperr.Internal("list expired otc contracts", err)
	}
	defer rows.Close()
	var out []*domain.OTCContract
	for rows.Next() {
		c, err := scanOTCContract(rows)
		if err != nil {
			return nil, apperr.Internal("scan otc contract", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListPublicHoldings returns holding rows with public_count > reserved_count
// owned by someone other than `excludeUserID`. Used by the OTC discovery
// board; the service decorates each row with the security + listing.
func (s *Store) ListPublicHoldings(ctx context.Context, excludeUserID string) ([]*domain.Holding, error) {
	const q = `select ` + holdingCols + ` from "trading".portfolio_holdings
	           where public_count > reserved_count
	             and user_id <> $1
	           order by updated_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, excludeUserID)
	if err != nil {
		return nil, apperr.Internal("list public holdings", err)
	}
	defer rows.Close()
	var out []*domain.Holding
	for rows.Next() {
		h, err := scanHolding(rows)
		if err != nil {
			return nil, apperr.Internal("scan holding", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
