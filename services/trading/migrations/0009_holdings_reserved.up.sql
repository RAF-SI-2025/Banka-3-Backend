-- c4 (spec p.68): a seller's holdings can be partly committed against
-- outstanding OTC offers + active OTC contracts. The seller's portfolio
-- still owns the shares — they're just not free to trade or post on
-- another offer. The 12 = 3 + 7 + 2 worked example on spec p.68 is the
-- load-bearing invariant: Σ(open offer qty) + Σ(active contract qty)
-- ≤ quantity.
--
-- public_count is *visibility* (how many of these shares appear on the
-- OTC discovery board). reserved_count is *commitment* (how many are
-- locked behind in-flight negotiations + already-signed contracts).
-- Discovery shows public_count − reserved_count as "available now".
--
-- The CHECK constraint is the backstop. The store-layer increment +
-- decrement helpers compose with the SAGA orchestrator so a withdrawn
-- offer or expired contract reliably gives the shares back; the DB
-- refuses to let either path drop below zero or above the holding's
-- quantity.
alter table "trading".portfolio_holdings
    add column reserved_count integer not null default 0
        check (reserved_count >= 0),
    add constraint portfolio_holdings_reserved_le_qty
        check (reserved_count <= quantity);
