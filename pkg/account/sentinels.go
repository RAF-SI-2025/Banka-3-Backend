package account

// Sentinel UUIDs used as owner_client_id (bank) or client_id (trading)
// to identify bank-owned positions across the system. Kept in a shared
// package so both bank and trading services agree on the values without
// cross-importing each other's `internal/domain` package.
//
// Each ID maps 1:1 to a class of bank-managed accounts/positions:
//
//   - SystemOwnerID     — menjačnica house accounts (one per currency).
//   - StateTaxOwnerID   — the state's RSD capital-gains-tax account.
//   - ForexBookOwnerID  — bank's per-currency forex inventory book.
//   - BankAsClientOwnerID — client_id stamped on client_fund_positions
//     when the bank itself holds a stake in one of its own funds
//     (spec p.75 Napomena 2).
//   - FundsOwnerID      — owner_client_id stamped on every bank account
//     created to hold an investment fund's liquidity (spec p.74).
//
// Bank service's internal/domain re-exports the same values; the
// duplicate constants there exist for backwards-compat with code
// written before this shared package landed. New code references
// pkg/account.<Sentinel>.
const (
	SystemOwnerID       = "00000000-0000-0000-0000-000000000000"
	StateTaxOwnerID     = "00000000-0000-0000-0000-000000000010"
	ForexBookOwnerID    = "00000000-0000-0000-0000-000000000020"
	BankAsClientOwnerID = "00000000-0000-0000-0000-000000000030"
	FundsOwnerID        = "00000000-0000-0000-0000-000000000040"
)
