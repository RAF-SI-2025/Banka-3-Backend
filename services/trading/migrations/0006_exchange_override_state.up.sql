-- Widens the exchange override knob from a tri-state boolean
-- (NULL / true / false) to a four-state text column so the admin
-- toggle can also force after-hours mode for testing the spec p.56
-- after-hours cadence at any wall-clock.

alter table "trading".exchanges
    add column override_state text
        check (override_state in ('open', 'closed', 'after_hours'));

update "trading".exchanges
   set override_state = case
       when override_open is true then 'open'
       when override_open is false then 'closed'
       else null
   end;

alter table "trading".exchanges drop column override_open;
