SELECT
    snapshot_date,
    cluster_id,
    account_count,
    business_account_count,
    ROUND(avg_activity_score::numeric, 4) AS avg_activity_score,
    ROUND(silhouette_score::numeric, 4) AS silhouette_score,
    generated_at
FROM analytics_account_activity_clusters
ORDER BY snapshot_date DESC, cluster_id ASC
LIMIT 10;

SELECT
    snapshot_date,
    account_number,
    cluster_id,
    currency,
    balance,
    payments_out_count,
    transfers_out_count,
    orders_created,
    fills_count,
    ROUND(activity_score::numeric, 4) AS activity_score,
    generated_at
FROM analytics_account_activity_segments
ORDER BY snapshot_date DESC, activity_score DESC, account_number ASC
LIMIT 20;
