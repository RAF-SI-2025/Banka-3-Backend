-- Durable verification request history (spec p.84 "Stranica
-- Verifikacija" — the mobile app shows every request submitted in the
-- client's name, marked successful/unsuccessful). The 6-digit code and
-- its 5-min lifetime stay in Redis (pkg/verification); only the
-- request's existence + outcome is persisted here so it survives the
-- code's TTL and a process restart.
--
-- id is the verification id minted by pkg/verification (text, not a
-- uuid — it is a 16-byte hex string), so a resolve can target the row
-- without a secondary lookup. status starts 'pending'; a consumed code
-- flips it to 'success', an exhausted attempt budget to 'failed'. A
-- row left 'pending' past the code TTL is an expiry — the gateway
-- derives that for display; the user service stays unaware of timing.
create table "user".verification_events (
    id          text primary key,
    user_id     uuid not null,
    action_kind text not null,
    status      text not null default 'pending'
                check (status in ('pending', 'success', 'failed')),
    created_at  timestamptz not null default now(),
    resolved_at timestamptz
);

create index verification_events_user_idx
    on "user".verification_events (user_id, created_at desc);
