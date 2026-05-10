alter table "trading".exchanges add column override_open boolean;

update "trading".exchanges
   set override_open = case
       when override_state = 'open' then true
       when override_state = 'closed' then false
       else null
   end;

alter table "trading".exchanges drop column override_state;
