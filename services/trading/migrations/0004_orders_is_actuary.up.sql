-- BE-10: persist whether the order was placed by an actuary at create
-- time. Was previously derived from `user_kind = 'employee'` on every
-- settle, which over-includes any non-actuary employee. Frozen on the
-- row so audit / FX-commission policy reads the same answer the
-- service evaluated when it accepted the order.
alter table "trading".orders
    add column is_actuary boolean not null default false;

-- Backfill the existing dev rows: assume any employee-placed order was
-- an actuary order (true under c1+c2+c3 today — there's no employee
-- with trading.client and not Actuary*). Production has no rows yet.
update "trading".orders
set is_actuary = true
where user_kind = 'employee';
