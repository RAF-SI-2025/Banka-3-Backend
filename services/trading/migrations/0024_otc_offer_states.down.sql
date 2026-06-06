drop index if exists "trading".otc_offers_open_updated;

-- Revert any offers that used the new terminal states so the narrower
-- check constraint can be re-applied without violation.
update "trading".otc_offers set status = 'withdrawn'
    where status in ('cancelled','rejected');

alter table "trading".otc_offers
    drop constraint if exists otc_offers_status_check;

alter table "trading".otc_offers
    add constraint otc_offers_status_check check (status in (
        'open','superseded','accepted','withdrawn','expired'
    ));
