-- Fund dividend distribution (todoSpec C4 S69-S72).
--
-- A fund that holds dividend-paying stock receives the quarterly
-- dividend onto its own RSD bank account (S69 — liquid_rsd grows). The
-- mechanism is identical to the individual-holder payout (S72): the same
-- ListDividendCandidates walk hits fund-owned holdings (user_kind='fund'),
-- the same bank.SettleDividend credits the fund's account, and the same
-- dividend_payouts row records the receipt (user_kind='fund').
--
-- Two additive pieces live here:
--
-- 1. reinvest_dividends (S70) — when true, the dividend cron immediately
--    places a MARKET BUY for the received dividend amount through the
--    fund's bank account (the fund's manager's standing instruction to
--    compound). Defaults false (cash sits as liquid_rsd).
--
-- 2. fund_dividend_distributions (S71) — a per-client ledger attributing
--    each fund dividend across the fund's investors proportional to their
--    unit share at payout time. Client A holding 30% of the units is
--    credited 30% of the dividend; B with 70% gets 70%. This is an
--    accounting record on the fund's books — the cash lands on the fund's
--    account (S69), the client's economic share is reflected through the
--    fund's unit price (their position appreciates), and this ledger makes
--    the attribution auditable per the spec's proportional-split intent.

alter table "trading".investment_funds
  add column reinvest_dividends boolean not null default false;

-- Per-client attribution of one fund dividend (S71). One row per
-- (dividend_payout, client) at payout time. amount_rsd is the client's
-- proportional slice of the dividend converted to RSD; share_units /
-- fund_total_units capture the snapshot the split was computed against so
-- the attribution is reproducible. Idempotent on (dividend_payout_id,
-- client_id) so a retried cron run converges without double-recording.
create table "trading".fund_dividend_distributions (
    id                  uuid primary key default gen_random_uuid(),
    fund_id             uuid not null references "trading".investment_funds(id) on delete cascade,
    dividend_payout_id  uuid not null references "trading".dividend_payouts(id) on delete cascade,
    client_id           uuid not null,                              -- user.users.id OR BankAsClientOwnerID
    share_units         numeric(28,8) not null,                     -- client's units at payout time
    fund_total_units    numeric(28,8) not null,                     -- fund total_units at payout time
    amount_rsd          numeric(20,4) not null check (amount_rsd >= 0),
    created_at          timestamptz not null default now(),
    unique (dividend_payout_id, client_id)
);

create index fund_dividend_distributions_fund_idx
    on "trading".fund_dividend_distributions (fund_id, created_at);
create index fund_dividend_distributions_client_idx
    on "trading".fund_dividend_distributions (client_id, created_at);
