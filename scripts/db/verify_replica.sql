SELECT
    pg_is_in_recovery() AS replica_read_only,
    current_setting('transaction_read_only') AS transaction_read_only,
    current_setting('hot_standby') AS hot_standby_enabled;
