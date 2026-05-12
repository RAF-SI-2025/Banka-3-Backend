-- c4 PR3 — allow realized_gains rows that aren't tied to a security.
-- Fund-withdrawal taxation (EDGE-3) writes one realized_gains row per
-- withdraw with proceeds=amount_rsd, cost_basis=pro-rata invested. The
-- "security" in question is the fund itself; we record fund_id on a
-- separate column so the existing security_id FK can stay strict while
-- not blocking the insert.
alter table "trading".realized_gains
    alter column security_id drop not null;

-- Distinguish fund-withdrawal rows from on-exchange/OTC rows so the
-- supervisor "Realizovani PnL" table can render the right label.
-- Nullable + FK so trading still owns the integrity. NULL = legacy
-- on-exchange/OTC row.
alter table "trading".realized_gains
    add column fund_id uuid references "trading".investment_funds(id) on delete restrict;

create index realized_gains_fund_idx
    on "trading".realized_gains (fund_id)
    where fund_id is not null;
