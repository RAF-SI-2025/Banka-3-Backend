alter table "bank".accounts
  drop constraint accounts_kind_check,
  add constraint accounts_kind_check
    check (kind in (
      'personal_checking_rsd', 'personal_fx',
      'business_checking_rsd', 'business_fx',
      'system', 'state_tax', 'forex_book'
    ));
