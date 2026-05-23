SELECT
    parent.relname AS parent_table,
    child.relname AS partition_name,
    pg_get_expr(child.relpartbound, child.oid) AS partition_rule
FROM pg_inherits
JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
JOIN pg_class child ON child.oid = pg_inherits.inhrelid
WHERE parent.relname IN (
    'listing_daily_price_info',
    'interbank_protocol_transactions',
    'interbank_protocol_messages'
)
ORDER BY parent.relname, child.relname;
