-- c5 BE-7b — relax external_otc_contracts.premium_op_id so it can be
-- NULL on inbound contracts.
--
-- The outbound flow's accept SAGA mints the contract AFTER the bank-
-- side 2PC commit succeeds, so it always has a real op_id to stamp.
-- The inbound flow mirrors a partner-initiated accept; the partner
-- separately drives the cross-bank premium 2PC against our bank, and
-- the contract has to exist before that lands (so the FE can render
-- "Sklopljeni eksterni ugovori" with the right status). premium_op_id
-- is NULL until BE-7c wires a hook that stamps it post-commit.

alter table "trading".external_otc_contracts
  alter column premium_op_id drop not null;
