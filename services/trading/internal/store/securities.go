package store

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const securityCols = `id, ticker, name, type, exchange_mic, currency,
    outstanding_shares, dividend_yield::text,
    contract_size::text, contract_unit, settlement_date,
    base_currency, quote_currency, liquidity,
    underlying_security_id, option_type, strike_price::text,
    implied_volatility::text, premium::text, open_interest,
    created_at, updated_at`

// UpsertSecurity creates or updates a row; identity is by id when
// supplied, otherwise by (ticker, type).
func (s *Store) UpsertSecurity(ctx context.Context, in *domain.Security) (*domain.Security, error) {
	if in.ID == "" {
		const q = `
            insert into "trading".securities
                (ticker, name, type, exchange_mic, currency,
                 outstanding_shares, dividend_yield,
                 contract_size, contract_unit, settlement_date,
                 base_currency, quote_currency, liquidity,
                 underlying_security_id, option_type, strike_price,
                 implied_volatility, premium, open_interest)
            values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
            on conflict (ticker, type) do update set
                name              = excluded.name,
                exchange_mic      = excluded.exchange_mic,
                currency          = excluded.currency,
                outstanding_shares = excluded.outstanding_shares,
                dividend_yield    = excluded.dividend_yield,
                contract_size     = excluded.contract_size,
                contract_unit     = excluded.contract_unit,
                settlement_date   = excluded.settlement_date,
                base_currency     = excluded.base_currency,
                quote_currency    = excluded.quote_currency,
                liquidity         = excluded.liquidity,
                underlying_security_id = excluded.underlying_security_id,
                option_type       = excluded.option_type,
                strike_price      = excluded.strike_price,
                implied_volatility = excluded.implied_volatility,
                premium           = excluded.premium,
                open_interest     = excluded.open_interest,
                updated_at        = now()
            returning ` + securityCols
		row := s.Pool.QueryRow(ctx, q,
			in.Ticker, in.Name, string(in.Type), nullableText(in.ExchangeMIC), string(in.Currency),
			nullableInt64(in.OutstandingShares), nullableNumeric(in.DividendYield),
			nullableNumeric(in.ContractSize), nullableText(in.ContractUnit), in.SettlementDate,
			nullableText(string(in.BaseCurrency)), nullableText(string(in.QuoteCurrency)), nullableText(in.Liquidity),
			nullableText(in.UnderlyingSecurityID), nullableText(string(in.OptionType)), nullableNumeric(in.StrikePrice),
			nullableNumeric(in.ImpliedVolatility), nullableNumeric(in.Premium), nullableInt64(in.OpenInterest),
		)
		out, err := scanSecurity(row)
		if err != nil {
			return nil, apperr.Internal("insert security", err)
		}
		return out, nil
	}
	const q = `
        update "trading".securities set
            ticker = $2, name = $3, type = $4, exchange_mic = $5, currency = $6,
            outstanding_shares = $7, dividend_yield = $8,
            contract_size = $9, contract_unit = $10, settlement_date = $11,
            base_currency = $12, quote_currency = $13, liquidity = $14,
            underlying_security_id = $15, option_type = $16, strike_price = $17,
            implied_volatility = $18, premium = $19, open_interest = $20,
            updated_at = now()
        where id = $1
        returning ` + securityCols
	row := s.Pool.QueryRow(ctx, q,
		in.ID, in.Ticker, in.Name, string(in.Type), nullableText(in.ExchangeMIC), string(in.Currency),
		nullableInt64(in.OutstandingShares), nullableNumeric(in.DividendYield),
		nullableNumeric(in.ContractSize), nullableText(in.ContractUnit), in.SettlementDate,
		nullableText(string(in.BaseCurrency)), nullableText(string(in.QuoteCurrency)), nullableText(in.Liquidity),
		nullableText(in.UnderlyingSecurityID), nullableText(string(in.OptionType)), nullableNumeric(in.StrikePrice),
		nullableNumeric(in.ImpliedVolatility), nullableNumeric(in.Premium), nullableInt64(in.OpenInterest),
	)
	out, err := scanSecurity(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("security not found")
		}
		return nil, apperr.Internal("update security", err)
	}
	return out, nil
}

// GetSecurity returns one row by id.
func (s *Store) GetSecurity(ctx context.Context, id string) (*domain.Security, error) {
	q := `select ` + securityCols + ` from "trading".securities where id = $1`
	out, err := scanSecurity(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("security not found")
		}
		return nil, apperr.Internal("get security", err)
	}
	return out, nil
}

// GetSecurityByTicker returns one row by (ticker, type) — the natural
// key. Returns NotFound on miss.
func (s *Store) GetSecurityByTicker(ctx context.Context, ticker string, t domain.SecurityType) (*domain.Security, error) {
	q := `select ` + securityCols + ` from "trading".securities where ticker = $1 and type = $2`
	out, err := scanSecurity(s.Pool.QueryRow(ctx, q, ticker, string(t)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("security not found")
		}
		return nil, apperr.Internal("get security by ticker", err)
	}
	return out, nil
}

// securityColsQ is securityCols qualified to the "s" alias, for the
// listings-joined ListSecurities query (exchange_mic exists on both
// tables, so bare columns there would be ambiguous).
const securityColsQ = `s.id, s.ticker, s.name, s.type, s.exchange_mic, s.currency,
    s.outstanding_shares, s.dividend_yield::text,
    s.contract_size::text, s.contract_unit, s.settlement_date,
    s.base_currency, s.quote_currency, s.liquidity,
    s.underlying_security_id, s.option_type, s.strike_price::text,
    s.implied_volatility::text, s.premium::text, s.open_interest,
    s.created_at, s.updated_at`

// SecurityFilter narrows ListSecurities. Range bounds are decimal
// strings; empty means unbounded. Price/ask/bid/volume filter against
// the security's listing row (LEFT JOINed); rows without a listing
// fall out of any price/volume bound (NULL comparisons are false).
type SecurityFilter struct {
	Type          domain.SecurityType
	Search        string
	ExchangeMIC   string
	MinSettlement *time.Time
	MaxSettlement *time.Time
	MinPrice      string
	MaxPrice      string
	MinAsk        string
	MaxAsk        string
	MinBid        string
	MaxBid        string
	MinVolume     string
	MaxVolume     string
	// SortBy: "price" | "volume" | "" (default). SortDesc flips order.
	SortBy   string
	SortDesc bool
}

// ListSecurities returns rows joined with their listing (when present).
// Range filters on price/ask/bid/volume happen at the listings level.
func (s *Store) ListSecurities(ctx context.Context, f SecurityFilter, page, pageSize int) ([]*domain.Security, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if page <= 0 {
		page = 1
	}

	const fromJoin = ` from "trading".securities s
        left join "trading".listings l on l.security_id = s.id`

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
		add("s.exchange_mic = ?", f.ExchangeMIC)
	}
	if strings.TrimSpace(f.Search) != "" {
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(f.Search))+"%")
		conds = append(conds, "(lower(s.ticker) like "+intArg(len(args))+" or lower(s.name) like "+intArg(len(args))+")")
	}
	if f.MinSettlement != nil {
		add("s.settlement_date >= ?", *f.MinSettlement)
	}
	if f.MaxSettlement != nil {
		add("s.settlement_date <= ?", *f.MaxSettlement)
	}
	// Price/ask/bid/volume live on the listing row. A row with no
	// listing has NULL there, so any bound naturally excludes it.
	for _, rb := range []struct {
		val, col, op string
	}{
		{f.MinPrice, "l.price", ">="}, {f.MaxPrice, "l.price", "<="},
		{f.MinAsk, "l.ask", ">="}, {f.MaxAsk, "l.ask", "<="},
		{f.MinBid, "l.bid", ">="}, {f.MaxBid, "l.bid", "<="},
		{f.MinVolume, "l.volume", ">="}, {f.MaxVolume, "l.volume", "<="},
	} {
		if strings.TrimSpace(rb.val) != "" {
			add(rb.col+" "+rb.op+" ?::numeric", strings.TrimSpace(rb.val))
		}
	}

	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.Pool.QueryRow(ctx, "select count(*)"+fromJoin+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count securities", err)
	}

	dir := "asc"
	if f.SortDesc {
		dir = "desc"
	}
	order := " order by s.ticker asc"
	switch f.SortBy {
	case "price":
		order = " order by l.price " + dir + " nulls last, s.ticker asc"
	case "volume":
		order = " order by l.volume " + dir + " nulls last, s.ticker asc"
	}

	q := "select " + securityColsQ + fromJoin + where +
		order + " limit " + intArg(len(args)+1) + " offset " + intArg(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, apperr.Internal("list securities", err)
	}
	defer rows.Close()

	var out []*domain.Security
	for rows.Next() {
		sec, err := scanSecurity(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan security", err)
		}
		out = append(out, sec)
	}
	return out, total, rows.Err()
}

// ListOptionsForUnderlying returns every option whose underlying is
// the given stock id, ordered by (settlement_date, strike, type).
func (s *Store) ListOptionsForUnderlying(ctx context.Context, stockID string, settlement *time.Time) ([]*domain.Security, error) {
	args := []any{stockID}
	q := `select ` + securityCols + ` from "trading".securities
	      where type = 'option' and underlying_security_id = $1`
	if settlement != nil {
		q += ` and settlement_date = $2`
		args = append(args, *settlement)
	}
	q += ` order by settlement_date asc, strike_price asc, option_type asc`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, apperr.Internal("list options", err)
	}
	defer rows.Close()
	var out []*domain.Security
	for rows.Next() {
		sec, err := scanSecurity(rows)
		if err != nil {
			return nil, apperr.Internal("scan option", err)
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

func scanSecurity(row pgx.Row) (*domain.Security, error) {
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
	)
	if err := row.Scan(
		&sec.ID, &sec.Ticker, &sec.Name, &t, &exchange, &cur,
		&outstand, &divYield,
		&contrSize, &contrUnit, &settle,
		&base, &quote, &liquidity,
		&underly, &optType, &strike,
		&impliedVol, &premium, &openInt,
		&sec.CreatedAt, &sec.UpdatedAt,
	); err != nil {
		return nil, err
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
	return &sec, nil
}

// nullableText returns nil for empty strings (so the DB stores NULL).
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableNumeric returns nil for empty strings.
func nullableNumeric(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableInt64 returns nil for zero.
func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
