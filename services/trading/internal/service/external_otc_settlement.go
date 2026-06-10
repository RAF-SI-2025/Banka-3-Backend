package service

import (
	"context"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// isAppKind reports whether err is an *apperr.Error of the given kind.
func isAppKind(err error, kind apperr.Kind) bool {
	var e *apperr.Error
	return errors.As(err, &e) && e.Kind == kind
}

// =====================================================================
// Cross-bank OTC option settlement — seller side (Banka-4 §2/§3).
//
// The gateway parses an inbound NEW_TX/COMMIT_TX/ROLLBACK_TX envelope and
// drives this in 2PC phases. This method owns only the SHARE + CONTRACT
// effects; the premium/strike CASH leg rides the existing bank inbound
// 2PC (the gateway resolves the seller account via SellerAccountNumber
// returned here and credits it through bank.InterbankProtocolService).
//
// Scope: the partner is the buyer and WE are the seller's bank. The local
// contract is minted by ReceiveExternalOTCAccept (the §3.6 GET accept that
// precedes this NEW_TX); here we reserve / transfer the seller's shares.
// =====================================================================

// SettleExternalOTCOptionInput is the parsed seller-side settlement intent.
type SettleExternalOTCOptionInput struct {
	Phase          string // "prepare" | "commit" | "rollback"
	Kind           string // "accept" | "exercise"
	SenderBankCode string
	TransactionID  string
	OptionRef      string // negotiationId (accept) / contractId (exercise) — both = our thread id
	SellerUserRef  string
	CashAmount     string
	CashCurrency   string
	Ticker         string
	Quantity       int64
}

// SettleExternalOTCOptionResult carries the vote + the seller account the
// gateway needs for the bank cash leg.
type SettleExternalOTCOptionResult struct {
	Accepted            bool
	Reason              string
	Handled             bool
	SellerAccountNumber string
}

func (s *Service) SettleExternalOTCOption(ctx context.Context, in SettleExternalOTCOptionInput) (*SettleExternalOTCOptionResult, error) {
	senderRouting, aerr := strconv.Atoi(in.SenderBankCode)
	if aerr != nil {
		// Routing 0 makes the contract lookup miss and the system vote NO
		// with "contract not found" — surface the real cause here.
		s.log().WarnContext(ctx, "external otc settlement: malformed sender bank code, routing treated as 0",
			"err", aerr, "sender_bank_code", in.SenderBankCode, "transaction_id", in.TransactionID)
	}
	switch strings.ToLower(in.Phase) {
	case "prepare":
		return s.settleOTCPrepare(ctx, senderRouting, in)
	case "commit":
		return s.settleOTCCommit(ctx, senderRouting, in)
	case "rollback":
		return s.settleOTCRollback(ctx, senderRouting, in)
	default:
		s.log().WarnContext(ctx, "external otc settlement: unknown phase",
			"phase", in.Phase, "transaction_id", in.TransactionID,
			"remote_bank_code", in.SenderBankCode)
		return nil, apperr.Validation("nepoznata settlement faza")
	}
}

func no(reason string) *SettleExternalOTCOptionResult {
	return &SettleExternalOTCOptionResult{Handled: true, Accepted: false, Reason: reason}
}

// settleOTCPrepare validates the contract and (accept) reserves the
// seller's shares, recording a prepared settlement row. Idempotent.
func (s *Service) settleOTCPrepare(ctx context.Context, senderRouting int, in SettleExternalOTCOptionInput) (*SettleExternalOTCOptionResult, error) {
	res := &SettleExternalOTCOptionResult{Handled: true}
	contract, err := s.Store.GetExternalOTCContractByThread(ctx, in.OptionRef)
	if err != nil {
		if isAppKind(err, apperr.KindNotFound) {
			s.log().WarnContext(ctx, "external otc settlement prepare: negotiation not found, voting no",
				"transaction_id", in.TransactionID, "option_ref", in.OptionRef,
				"remote_bank_code", in.SenderBankCode)
			return no("OPTION_NEGOTIATION_NOT_FOUND"), nil
		}
		s.log().ErrorContext(ctx, "external otc settlement prepare: contract lookup failed",
			"err", err, "transaction_id", in.TransactionID, "option_ref", in.OptionRef,
			"remote_bank_code", in.SenderBankCode)
		return nil, err
	}
	res.SellerAccountNumber = contract.LocalAccountNumber

	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		existing, err := s.Store.GetExternalOTCSettlement(ctx, tx, senderRouting, in.TransactionID)
		if err != nil {
			return err
		}
		if existing != nil {
			// Replay — keep the prior vote (rolled_back → NO).
			res.Accepted = existing.Status != domain.ExternalOTCSettlementRolledBack
			return nil
		}

		kind := domain.ExternalOTCSettlementKindAccept
		if strings.EqualFold(in.Kind, domain.ExternalOTCSettlementKindExercise) {
			kind = domain.ExternalOTCSettlementKindExercise
		}

		switch kind {
		case domain.ExternalOTCSettlementKindAccept:
			if contract.Status != domain.ExternalOTCContractActive {
				s.log().WarnContext(ctx, "external otc settlement prepare: contract not active, voting no",
					"transaction_id", in.TransactionID, "contract_id", contract.ID,
					"status", string(contract.Status), "remote_bank_code", in.SenderBankCode)
				res.Accepted = false
				res.Reason = "OPTION_USED_OR_EXPIRED"
				return nil
			}
			// Reserve the seller's shares for the life of the contract.
			if _, err := s.Store.IncrementReservedHolding(ctx, tx, contract.SellerHoldingRef, contract.Quantity); err != nil {
				if isAppKind(err, apperr.KindFailedPrecondition) {
					s.log().WarnContext(ctx, "external otc settlement prepare: insufficient asset, voting no",
						"err", err, "transaction_id", in.TransactionID, "contract_id", contract.ID,
						"quantity", contract.Quantity, "remote_bank_code", in.SenderBankCode)
					res.Accepted = false
					res.Reason = "INSUFFICIENT_ASSET"
					return nil
				}
				return err
			}
		case domain.ExternalOTCSettlementKindExercise:
			if contract.Status != domain.ExternalOTCContractActive {
				s.log().WarnContext(ctx, "external otc settlement prepare: contract not active, voting no",
					"transaction_id", in.TransactionID, "contract_id", contract.ID,
					"status", string(contract.Status), "remote_bank_code", in.SenderBankCode)
				res.Accepted = false
				res.Reason = "OPTION_USED_OR_EXPIRED"
				return nil
			}
			h, err := s.Store.GetHoldingByID(ctx, contract.SellerHoldingRef)
			if err != nil {
				return err
			}
			if h.ReservedCount < contract.Quantity || h.Quantity < contract.Quantity {
				s.log().WarnContext(ctx, "external otc settlement prepare: holding no longer covers contract, voting no",
					"transaction_id", in.TransactionID, "contract_id", contract.ID,
					"quantity", contract.Quantity, "reserved", h.ReservedCount,
					"held", h.Quantity, "remote_bank_code", in.SenderBankCode)
				res.Accepted = false
				res.Reason = "INSUFFICIENT_ASSET"
				return nil
			}
		}

		if _, err := s.Store.InsertExternalOTCSettlement(ctx, tx, &domain.ExternalOTCSettlement{
			SenderRoutingNumber: senderRouting,
			TransactionID:       in.TransactionID,
			Kind:                kind,
			Status:              domain.ExternalOTCSettlementPrepared,
			OptionRef:           in.OptionRef,
			ContractID:          contract.ID,
			Quantity:            int64(contract.Quantity),
			CashAmount:          in.CashAmount,
			CashCurrency:        in.CashCurrency,
		}); err != nil {
			return err
		}
		res.Accepted = true
		return nil
	})
	if err != nil {
		s.log().ErrorContext(ctx, "external otc settlement prepare failed",
			"err", err, "transaction_id", in.TransactionID, "contract_id", contract.ID,
			"kind", in.Kind, "remote_bank_code", in.SenderBankCode)
		return nil, err
	}
	if res.Accepted {
		s.log().InfoContext(ctx, "external otc settlement prepared",
			"transaction_id", in.TransactionID, "contract_id", contract.ID,
			"kind", in.Kind, "remote_bank_code", in.SenderBankCode)
	}
	return res, nil
}

// settleOTCCommit finalizes: accept keeps the reservation; exercise
// releases it, transfers the shares out, writes the realized gain and
// marks the contract exercised. Idempotent.
func (s *Service) settleOTCCommit(ctx context.Context, senderRouting int, in SettleExternalOTCOptionInput) (*SettleExternalOTCOptionResult, error) {
	res := &SettleExternalOTCOptionResult{}
	// Captured for the post-tx log lines.
	var logContractID, logKind string
	err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		st, err := s.Store.GetExternalOTCSettlement(ctx, tx, senderRouting, in.TransactionID)
		if err != nil {
			return err
		}
		if st == nil {
			return nil // not ours — gateway falls back to the bank cash 2PC
		}
		res.Handled = true
		if st.Status == domain.ExternalOTCSettlementCommitted {
			res.Accepted = true
			return nil // idempotent
		}

		contract, err := s.Store.GetExternalOTCContractByThread(ctx, st.OptionRef)
		if err != nil {
			return err
		}
		logContractID, logKind = contract.ID, st.Kind

		if st.Kind == domain.ExternalOTCSettlementKindExercise {
			h, err := s.Store.GetHoldingByID(ctx, contract.SellerHoldingRef)
			if err != nil {
				return err
			}
			// Release the reservation, then decrement quantity (the CHECK
			// constraint reserved_count <= quantity requires this order).
			if _, err := s.Store.DecrementReservedHolding(ctx, tx, contract.SellerHoldingRef, int32(st.Quantity)); err != nil {
				return err
			}
			avg, _, err := s.Store.ApplySellFill(ctx, tx,
				h.UserID, string(h.UserKind), h.SecurityID, h.AccountID, int32(st.Quantity))
			if err != nil {
				return err
			}
			// Seller realized gain (spec p.62): qty*(strike-cost), RSD via ASK.
			strike, _ := money.Parse(contract.StrikePrice)
			cost, _ := money.Parse(avg)
			qr := new(big.Rat).SetInt64(st.Quantity)
			gainNative := money.Mul(qr, money.Sub(strike, cost))
			gainRSD := new(big.Rat).Set(gainNative)
			cur := contract.Currency
			if cur != domain.CurrencyRSD && s.Rates != nil {
				if _, ask, err := s.Rates.Quote(ctx, cur, domain.CurrencyRSD); err == nil {
					if r, perr := money.Parse(ask); perr == nil {
						gainRSD = money.Mul(gainNative, r)
					}
				}
			}
			if _, err := s.Store.InsertRealizedGain(ctx, tx, &domain.RealizedGain{
				UserID:       h.UserID,
				UserKind:     h.UserKind,
				SecurityID:   h.SecurityID,
				AccountID:    h.AccountID,
				Quantity:     int32(st.Quantity),
				CostBasisAmt: money.FormatAmount(money.Mul(qr, cost)),
				ProceedsAmt:  money.FormatAmount(money.Mul(qr, strike)),
				Currency:     cur,
				GainNative:   money.FormatAmount(gainNative),
				GainRSD:      money.FormatAmount(gainRSD),
			}); err != nil {
				return err
			}
			opID := deriveExternalExerciseOpID(in.SenderBankCode, in.TransactionID)
			if _, err := s.Store.SetExternalOTCContractExercised(ctx, tx, contract.ID, opID, s.now()); err != nil {
				return err
			}
		}

		if err := s.Store.SetExternalOTCSettlementStatus(ctx, tx, senderRouting, in.TransactionID,
			domain.ExternalOTCSettlementCommitted, contract.ID, ""); err != nil {
			return err
		}
		res.Accepted = true
		return nil
	})
	if err != nil {
		s.log().ErrorContext(ctx, "external otc settlement commit failed",
			"err", err, "transaction_id", in.TransactionID, "contract_id", logContractID,
			"kind", logKind, "remote_bank_code", in.SenderBankCode)
		return nil, err
	}
	if res.Handled && res.Accepted {
		s.log().InfoContext(ctx, "external otc settlement committed",
			"transaction_id", in.TransactionID, "contract_id", logContractID,
			"kind", logKind, "remote_bank_code", in.SenderBankCode)
	}
	return res, nil
}

// settleOTCRollback releases the prepare-phase share reservation (accept)
// and marks the settlement rolled back. Idempotent.
func (s *Service) settleOTCRollback(ctx context.Context, senderRouting int, in SettleExternalOTCOptionInput) (*SettleExternalOTCOptionResult, error) {
	res := &SettleExternalOTCOptionResult{}
	err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		st, err := s.Store.GetExternalOTCSettlement(ctx, tx, senderRouting, in.TransactionID)
		if err != nil {
			return err
		}
		if st == nil {
			return nil // not ours
		}
		res.Handled = true
		if st.Status != domain.ExternalOTCSettlementPrepared {
			res.Accepted = true
			return nil // already committed or rolled back — nothing to undo
		}
		if st.Kind == domain.ExternalOTCSettlementKindAccept {
			contract, err := s.Store.GetExternalOTCContractByThread(ctx, st.OptionRef)
			if err != nil {
				return err
			}
			if _, err := s.Store.DecrementReservedHolding(ctx, tx, contract.SellerHoldingRef, int32(st.Quantity)); err != nil {
				return err
			}
		}
		if err := s.Store.SetExternalOTCSettlementStatus(ctx, tx, senderRouting, in.TransactionID,
			domain.ExternalOTCSettlementRolledBack, "", ""); err != nil {
			return err
		}
		res.Accepted = true
		return nil
	})
	if err != nil {
		s.log().ErrorContext(ctx, "external otc settlement rollback failed",
			"err", err, "transaction_id", in.TransactionID,
			"remote_bank_code", in.SenderBankCode)
		return nil, err
	}
	if res.Handled {
		s.log().InfoContext(ctx, "external otc settlement rolled back",
			"transaction_id", in.TransactionID, "remote_bank_code", in.SenderBankCode)
	}
	return res, nil
}
