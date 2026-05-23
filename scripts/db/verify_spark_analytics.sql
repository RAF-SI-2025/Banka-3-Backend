SELECT
    metric_date,
    payments_count,
    transfers_count,
    orders_created,
    fills_count,
    otc_contracts_created,
    generated_at
FROM analytics_daily_platform_metrics
ORDER BY metric_date DESC
LIMIT 10;

SELECT
    metric_date,
    rank,
    listing_id,
    ticker,
    traded_notional,
    traded_quantity,
    generated_at
FROM analytics_daily_top_listings
ORDER BY metric_date DESC, rank ASC
LIMIT 20;
