alter table "trading".portfolio_holdings
    drop constraint if exists portfolio_holdings_public_le_qty;
