-- c4 PR3 — fund account kind. Investment funds (spec p.71-75) hold their
-- liquidity in a bank account that the trading service creates at
-- CreateFund time, owned by the FundsOwnerID sentinel. Bank-side this
-- needs a new account kind so the seeded fund accounts don't get pulled
-- into general listings (the existing employee Računi page already
-- filters out system/state_tax/forex_book; adding 'fund' to that list
-- keeps the same UX).
alter table "bank".accounts
  drop constraint accounts_kind_check,
  add constraint accounts_kind_check
    check (kind in (
      'personal_checking_rsd', 'personal_fx',
      'business_checking_rsd', 'business_fx',
      'system', 'state_tax', 'forex_book', 'fund'
    ));
