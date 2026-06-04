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

const listingCols = `id, security_id, exchange_mic,
    price::text, ask::text, bid::text, volume,
    change_amt::text, contract_size::text,
    last_refresh, created_at`

// UpsertListing writes the live-price row keyed by security_id.
func (s *Store) UpsertListing(ctx context.Context, l *domain.Listing) (*domain.Listing, error) {
	// volume preserves the existing row's value when the caller passes
	// zero. The admin price-override dialog only ships (price, ask, bid)
	// and would otherwise zero out the listing's volume — which then
	// makes the cadence formula clamp to its 1-tick floor (still fine
	// in expectation) but loses the AV refresh's daily volume snapshot.
	// AV/refresh paths always pass a real volume so the COALESCE only
	// fires on the manual-edit path.
	const q = `
        insert into "trading".listings
            (security_id, exchange_mic, price, ask, bid, volume, change_amt, contract_size, last_refresh)
        values ($1,$2,$3,$4,$5,$6,$7,$8, now())
        on conflict (security_id) do update set
            exchange_mic  = excluded.exchange_mic,
            price         = excluded.price,
            ask           = excluded.ask,
            bid           = excluded.bid,
            volume        = case when excluded.volume = 0
                                 then "trading".listings.volume
                                 else excluded.volume end,
            change_amt    = excluded.change_amt,
            contract_size = excluded.contract_size,
            last_refresh  = now()
        returning ` + listingCols
	row := s.DB.QueryRow(
		ctx, q,
		l.SecurityID, nullableText(l.ExchangeMIC), l.Price, l.Ask, l.Bid, l.Volume, l.ChangeAmt, defaultStr(l.ContractSize, "1"),
	)
	out, err := scanListing(row)
	if err != nil {
		return nil, apperr.Internal("upsert listing", err)
	}
	return out, nil
}

// GetListing returns one listing by id.
func (s *Store) GetListing(ctx context.Context, id string) (*domain.Listing, error) {
	q := `select ` + listingCols + ` from "trading".listings where id = $1`
	out, err := scanListing(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("listing not found")
		}
		return nil, apperr.Internal("get listing", err)
	}
	return out, nil
}

// GetListingBySecurityID returns the live-price row for a security or
// NotFound.
func (s *Store) GetListingBySecurityID(ctx context.Context, securityID string) (*domain.Listing, error) {
	q := `select ` + listingCols + ` from "trading".listings where security_id = $1`
	out, err := scanListing(s.DB.QueryRow(postgres.WithRead(ctx), q, securityID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("listing not found")
		}
		return nil, apperr.Internal("get listing by security", err)
	}
	return out, nil
}

// ListListingsRow joins a listing with its security so the catalog UI
// can render rows in one round-trip.
type ListListingsRow struct {
	Security *domain.Security
	Listing  *domain.Listing
}

// ListingFilter narrows ListListings.
type ListingFilter struct {
	Type        domain.SecurityType
	ExchangeMIC string
	Search      string
	// SortBy: "price", "volume", "maintenance_margin". Empty = ticker.
	SortBy   string
	SortDesc bool
}

// ListListings returns rows from the join of securities + listings.
// Securities of types that don't carry a listing (none today, but
// kept structural for future "options aren't listings") are filtered
// to those that do.
func (s *Store) ListListings(ctx context.Context, f ListingFilter, page, pageSize int) ([]*ListListingsRow, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if page <= 0 {
		page = 1
	}

	var args []any
	var conds []string
	add := func(cond string, a any) {
		args = append(args, a)
		conds = append(conds, strings.ReplaceAll(cond, "?", intArg(len(args))))
	}
	if f.Type != "" {
		add("s.type = ?", string(f.Type))
	}
	if f.ExchangeMIC != "" {
		add("l.exchange_mic = ?", f.ExchangeMIC)
	}
	if strings.TrimSpace(f.Search) != "" {
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(f.Search))+"%")
		conds = append(conds, "(lower(s.ticker) like "+intArg(len(args))+" or lower(s.name) like "+intArg(len(args))+")")
	}

	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(
		postgres.WithRead(ctx),
		`select count(*) from "trading".listings l join "trading".securities s on s.id = l.security_id`+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count listings", err)
	}

	order := "s.ticker"
	switch f.SortBy {
	case "price":
		order = "l.price"
	case "volume":
		order = "l.volume"
	case "maintenance_margin":
		// 50% * price for stocks; 10% * contract_size * price for futures and forex.
		// Compose the same fallback the FE uses. Options (no listing row) aren't ranked.
		order = "case s.type when 'stock' then l.price * 0.5 else l.price * l.contract_size * 0.1 end"
	}
	if f.SortDesc {
		order += " desc"
	} else {
		order += " asc"
	}

	q := `select ` + qualifiedCols(securityCols, "s") + `, ` + qualifiedCols(listingCols, "l") + `
	      from "trading".listings l join "trading".securities s on s.id = l.security_id` + where +
		` order by ` + order +
		` limit ` + intArg(len(args)+1) + ` offset ` + intArg(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, 0, apperr.Internal("list listings", err)
	}
	defer rows.Close()

	var out []*ListListingsRow
	for rows.Next() {
		sec, list, err := scanSecurityWithListing(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan listing row", err)
		}
		out = append(out, &ListListingsRow{Security: sec, Listing: list})
	}
	return out, total, rows.Err()
}

// GetListingDailyHistory returns daily history rows in date asc order
// for the given listing in [from, to]. Either bound may be zero.
func (s *Store) GetListingDailyHistory(ctx context.Context, listingID string, from, to time.Time) ([]*domain.ListingDailyPrice, error) {
	const base = `select date, price::text, ask::text, bid::text, change_amt::text, volume
	              from "trading".listing_daily_price_info where listing_id = $1`
	args := []any{listingID}
	q := base
	if !from.IsZero() {
		args = append(args, from)
		q += " and date >= $" + intArg(len(args))[1:]
	}
	if !to.IsZero() {
		args = append(args, to)
		q += " and date <= $" + intArg(len(args))[1:]
	}
	q += " order by date asc"
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, apperr.Internal("listing daily", err)
	}
	defer rows.Close()
	var out []*domain.ListingDailyPrice
	for rows.Next() {
		var r domain.ListingDailyPrice
		if err := rows.Scan(&r.Date, &r.Price, &r.Ask, &r.Bid, &r.ChangeAmt, &r.Volume); err != nil {
			return nil, apperr.Internal("scan daily", err)
		}
		r.ListingID = listingID
		out = append(out, &r)
	}
	return out, rows.Err()
}

// UpsertListingDaily writes one historical row per (listing, date).
func (s *Store) UpsertListingDaily(ctx context.Context, r *domain.ListingDailyPrice) error {
	const q = `
        insert into "trading".listing_daily_price_info
            (listing_id, date, price, ask, bid, change_amt, volume)
        values ($1,$2,$3,$4,$5,$6,$7)
        on conflict (listing_id, date) do update set
            price = excluded.price, ask = excluded.ask, bid = excluded.bid,
            change_amt = excluded.change_amt, volume = excluded.volume`
	if _, err := s.DB.Exec(ctx, q, r.ListingID, r.Date, r.Price, r.Ask, r.Bid, r.ChangeAmt, r.Volume); err != nil {
		return apperr.Internal("upsert listing daily", err)
	}
	return nil
}

func scanListing(row pgx.Row) (*domain.Listing, error) {
	var (
		l        domain.Listing
		exchange *string
	)
	if err := row.Scan(&l.ID, &l.SecurityID, &exchange, &l.Price, &l.Ask, &l.Bid, &l.Volume, &l.ChangeAmt, &l.ContractSize, &l.LastRefresh, &l.CreatedAt); err != nil {
		return nil, err
	}
	if exchange != nil {
		l.ExchangeMIC = *exchange
	}
	return &l, nil
}

func scanSecurityWithListing(row pgx.Row) (*domain.Security, *domain.Listing, error) {
	var (
		sec        domain.Security
		t          string
		exchange   *string
		cur        string
		outstand   *int64
		divYield   *string
		contrSize  *string
		contrUnit  *string
		settle     *time.Time
		base       *string
		quote      *string
		liquidity  *string
		underly    *string
		optType    *string
		strike     *string
		impliedVol *string
		premium    *string
		openInt    *int64

		l         domain.Listing
		lExchange *string
	)
	if err := row.Scan(
		&sec.ID, &sec.Ticker, &sec.Name, &t, &exchange, &cur,
		&outstand, &divYield,
		&contrSize, &contrUnit, &settle,
		&base, &quote, &liquidity,
		&underly, &optType, &strike,
		&impliedVol, &premium, &openInt,
		&sec.CreatedAt, &sec.UpdatedAt,

		&l.ID, &l.SecurityID, &lExchange, &l.Price, &l.Ask, &l.Bid, &l.Volume, &l.ChangeAmt, &l.ContractSize, &l.LastRefresh, &l.CreatedAt,
	); err != nil {
		return nil, nil, err
	}
	sec.Type = domain.SecurityType(t)
	if exchange != nil {
		sec.ExchangeMIC = *exchange
	}
	sec.Currency = domain.Currency(cur)
	if outstand != nil {
		sec.OutstandingShares = *outstand
	}
	if divYield != nil {
		sec.DividendYield = *divYield
	}
	if contrSize != nil {
		sec.ContractSize = *contrSize
	}
	if contrUnit != nil {
		sec.ContractUnit = *contrUnit
	}
	sec.SettlementDate = settle
	if base != nil {
		sec.BaseCurrency = domain.Currency(*base)
	}
	if quote != nil {
		sec.QuoteCurrency = domain.Currency(*quote)
	}
	if liquidity != nil {
		sec.Liquidity = *liquidity
	}
	if underly != nil {
		sec.UnderlyingSecurityID = *underly
	}
	if optType != nil {
		sec.OptionType = domain.OptionType(*optType)
	}
	if strike != nil {
		sec.StrikePrice = *strike
	}
	if impliedVol != nil {
		sec.ImpliedVolatility = *impliedVol
	}
	if premium != nil {
		sec.Premium = *premium
	}
	if openInt != nil {
		sec.OpenInterest = *openInt
	}
	if lExchange != nil {
		l.ExchangeMIC = *lExchange
	}
	return &sec, &l, nil
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// qualifiedCols rewrites a column list ("id, ticker, ...") to prefix
// every bare identifier with the given alias. Casts and parens pass
// through unchanged. We use this when joining `securities` + `listings`
// because both tables carry overlapping column names (id, exchange_mic,
// contract_size, created_at) and an unqualified select would either
// be ambiguous or return only the left-side row.
func qualifiedCols(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		ident, rest, hadCast := strings.Cut(strings.TrimSpace(p), "::")
		ident = strings.TrimSpace(ident)
		out := alias + "." + ident
		if hadCast {
			out += "::" + rest
		}
		parts[i] = out
	}
	return strings.Join(parts, ", ")
}
