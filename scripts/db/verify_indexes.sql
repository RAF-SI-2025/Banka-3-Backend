SELECT
    tablename,
    indexname,
    indexdef
FROM pg_indexes
WHERE schemaname = 'public'
  AND indexname IN (
      'idx_accounts_owner',
      'idx_accounts_owner_currency',
      'idx_cards_account_number',
      'idx_payments_from_account_ts_desc',
      'idx_payments_to_account_ts_desc',
      'idx_transfers_from_account_ts_desc',
      'idx_transfers_to_account_ts_desc',
      'idx_loans_account_id',
      'idx_loans_interest_rate_status',
      'idx_loans_status_next_payment_due',
      'idx_loan_request_status_submission',
      'idx_loan_request_account_submission',
      'idx_listing_daily_price_info_listing_date_desc',
      'idx_orders_placer_created_at',
      'idx_orders_status_created_at',
      'idx_orders_execution_queue',
      'external_otc_threads_local_user_status_idx',
      'external_otc_contracts_local_user_status_idx'
  )
ORDER BY tablename, indexname;
