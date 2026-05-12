alter table "trading".portfolio_holdings
    drop constraint if exists portfolio_holdings_reserved_le_qty,
    drop column if exists reserved_count;
