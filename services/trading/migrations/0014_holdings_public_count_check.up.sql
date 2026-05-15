-- Bug surfaced post-c4: a seller's `public_count` was decoupled from
-- their `quantity`. `SetPublicCount` clamps at write time (`count <=
-- quantity`), but no path re-clamped when quantity later dropped via
-- `ApplySellFill` — neither a market SELL nor an OTC exercise's
-- seller-side decrement touched the column. The OTC discovery board
-- (which renders `public_count - reserved_count`) then over-reported
-- the seller's available inventory, and `CreateOTCOffer`'s pre-check
-- used the same inflated number.
--
-- The `reserved_count <= quantity` CHECK was the only thing eventually
-- preventing actual over-commitment, but it surfaced too late and only
-- against the *committed* reservation — the UX still lied about what
-- was free.
--
-- Fix: clamp existing rows, then add the matching schema invariant so
-- any future quantity-decreasing path that forgets to clamp gets
-- caught at the DB layer (mirrors how `reserved_count <= quantity`
-- backstops the reservation paths).
update "trading".portfolio_holdings
   set public_count = quantity
 where public_count > quantity;

alter table "trading".portfolio_holdings
    add constraint portfolio_holdings_public_le_qty
        check (public_count <= quantity);
