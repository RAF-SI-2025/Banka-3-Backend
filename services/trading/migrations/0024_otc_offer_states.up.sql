-- todoSpec C4 "Automatska promena stanja pregovora" — extend the OTC
-- offer state machine.
--
-- Active (status='open') offers can now also transition to:
--   * 'cancelled' — the originator cancels their own open offer.
--   * 'rejected'  — the counterparty declines the latest open offer.
-- ('expired' already existed in the check list but was unused for offers;
--  the inactivity sweep now drives open → expired after 3 business days.)
--
-- All existing values are preserved; this only widens the check
-- constraint. updated_at already tracks last activity on a thread's open
-- row, so it doubles as the 3-business-day inactivity clock — no new
-- column is needed.

alter table "trading".otc_offers
    drop constraint if exists otc_offers_status_check;

alter table "trading".otc_offers
    add constraint otc_offers_status_check check (status in (
        'open','superseded','accepted','withdrawn','expired','cancelled','rejected'
    ));

-- Inactivity sweep hot path: open offers ordered by last activity.
create index if not exists otc_offers_open_updated
    on "trading".otc_offers (updated_at) where status = 'open';
