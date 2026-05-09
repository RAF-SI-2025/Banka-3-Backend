package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/loans"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SubmitLoanRequestInput is the validated payload for the client
// submitting a Zahtev za kredit (spec p.31).
type SubmitLoanRequestInput struct {
	AccountID                string
	LoanType                 domain.LoanType
	InterestType             domain.InterestType
	Amount                   string
	Currency                 domain.Currency
	Purpose                  string
	MonthlySalary            string
	EmploymentStatus         domain.EmploymentStatus
	EmploymentDurationMonths int
	InstallmentsTotal        int
	ContactPhone             string
}

// SubmitLoanRequest creates a pending request. Client-only path —
// employees making requests on behalf of clients aren't a spec
// scenario.
func (s *Service) SubmitLoanRequest(ctx context.Context, in SubmitLoanRequestInput) (*domain.LoanRequest, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserKind != auth.KindClient {
		return nil, apperr.PermissionDenied("samo klijent može da podnese zahtev za kredit")
	}
	if err := s.requirePermission(ctx, permissions.LoanWrite); err != nil {
		return nil, err
	}
	if err := validateLoanRequest(in); err != nil {
		return nil, err
	}
	a, err := s.Store.GetAccountByID(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if a.OwnerClientID != p.UserID {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	if a.Currency != in.Currency {
		return nil, apperr.Validation("valuta računa mora se poklapati sa valutom kredita")
	}
	if a.Status != domain.AccountActive {
		return nil, apperr.FailedPrecondition("račun nije aktivan")
	}

	return s.Store.CreateLoanRequest(ctx, &domain.LoanRequest{
		ClientID:                 p.UserID,
		AccountID:                in.AccountID,
		LoanType:                 in.LoanType,
		InterestType:             in.InterestType,
		Amount:                   strings.TrimSpace(in.Amount),
		Currency:                 in.Currency,
		Purpose:                  strings.TrimSpace(in.Purpose),
		MonthlySalary:            strings.TrimSpace(in.MonthlySalary),
		EmploymentStatus:         in.EmploymentStatus,
		EmploymentDurationMonths: in.EmploymentDurationMonths,
		InstallmentsTotal:        in.InstallmentsTotal,
		ContactPhone:             strings.TrimSpace(in.ContactPhone),
	})
}

// DecideLoanRequest is the employee's "Odobri" / "Odbij" path. On
// approve: the loan is created, principal disbursed to the client's
// account, the next installment scheduled.
func (s *Service) DecideLoanRequest(ctx context.Context, requestID string, approve bool, reason string) (*domain.LoanRequest, error) {
	if err := s.requirePermission(ctx, permissions.LoanWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserKind != auth.KindEmployee {
		return nil, apperr.PermissionDenied("samo zaposleni odlučuje o zahtevu")
	}

	var decided *domain.LoanRequest
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		req, err := s.Store.GetLoanRequestByID(ctx, requestID)
		if err != nil {
			return err
		}
		if !approve {
			out, err := s.Store.DecideLoanRequest(ctx, tx, requestID, p.UserID, domain.RequestRejected, strings.TrimSpace(reason))
			if err != nil {
				return err
			}
			decided = out
			return nil
		}
		out, err := s.Store.DecideLoanRequest(ctx, tx, requestID, p.UserID, domain.RequestApproved, "")
		if err != nil {
			return err
		}
		decided = out

		_, err = s.materializeLoan(ctx, tx, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.notifyLoanDecision(ctx, decided)
	return decided, nil
}

// materializeLoan derives the rate, computes the installment, inserts
// the loan + first installment, and disburses the principal from the
// bank's currency-house account into the client's account.
func (s *Service) materializeLoan(ctx context.Context, tx pgx.Tx, req *domain.LoanRequest) (*domain.Loan, error) {
	principal, err := money.Parse(req.Amount)
	if err != nil {
		return nil, apperr.Internal("parse amount", err)
	}

	// Convert principal to RSD-equivalent for bracket lookup
	// (spec p.33). For RSD loans skip the conversion. Uses the ASK
	// rate to stay consistent with the menjačnica policy (spec p.26).
	amountRSD := principal
	if req.Currency != domain.CurrencyRSD {
		_, ask, err := s.Rates.Quote(ctx, req.Currency, domain.CurrencyRSD)
		if err != nil {
			return nil, apperr.Internal("rate lookup", err)
		}
		askR, perr := money.Parse(ask)
		if perr != nil {
			return nil, apperr.Internal("parse rate", perr)
		}
		amountRSD = money.Mul(principal, askR)
	}

	base := loans.BaseRate(amountRSD)
	margin := loans.Margin(loans.Type(req.LoanType))
	offset := money.MustParse("0")
	if req.InterestType == domain.InterestVariable {
		offset = randomPomeraj()
	}

	monthly := loans.MonthlyRate(base, offset, margin)
	annuity := loans.Annuity(principal, monthly, req.InstallmentsTotal)
	annuityStr := money.FormatAmount(annuity)

	now := s.now()
	firstDue := now.AddDate(0, 1, 0)
	matures := now.AddDate(0, req.InstallmentsTotal, 0)
	loanNum := s.generateLoanNumber()

	loan := &domain.Loan{
		LoanNumber:            loanNum,
		RequestID:             req.ID,
		ClientID:              req.ClientID,
		AccountID:             req.AccountID,
		LoanType:              req.LoanType,
		InterestType:          req.InterestType,
		Principal:             money.FormatAmount(principal),
		Currency:              req.Currency,
		BaseRate:              money.Format(base, 4),
		Margin:                money.Format(margin, 4),
		CurrentOffset:         money.Format(offset, 4),
		InstallmentsTotal:     req.InstallmentsTotal,
		InstallmentAmount:     annuityStr,
		RemainingPrincipal:    money.FormatAmount(principal),
		NextInstallmentDate:   &firstDue,
		NextInstallmentAmount: annuityStr,
		Status:                domain.LoanApproved,
		MaturesAt:             &matures,
	}
	created, err := s.Store.CreateLoan(ctx, tx, loan)
	if err != nil {
		return nil, err
	}

	// Schedule the first installment row. Subsequent installments are
	// generated lazily by the daily job after each one is paid (so
	// variable-rate updates between installments are honoured).
	if _, err := s.Store.CreateInstallment(ctx, tx, &domain.LoanInstallment{
		LoanID:            created.ID,
		SequenceNumber:    1,
		Amount:            annuityStr,
		InterestRateAtDue: money.Format(money.Add(money.Add(base, offset), margin), 4),
		Currency:          req.Currency,
		ExpectedDueDate:   firstDue,
		Status:            domain.InstallmentUnpaid,
	}); err != nil {
		return nil, err
	}

	// Disburse: bank's currency-house → client account.
	bankHouse, err := s.Store.GetSystemAccount(ctx, req.Currency)
	if err != nil {
		return nil, err
	}
	negPrincipal := money.FormatAmount(money.Sub(money.MustParse("0"), principal))
	if err := s.Store.AdjustBalance(ctx, tx, bankHouse.ID, negPrincipal); err != nil {
		return nil, err
	}
	if err := s.Store.AdjustBalance(ctx, tx, req.AccountID, money.FormatAmount(principal)); err != nil {
		return nil, err
	}
	op := uuid.NewString()
	if _, err := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
		OpID:              op,
		Kind:              domain.TransactionKind("loan_disbursement"),
		LegIndex:          1,
		FromAccountID:     bankHouse.ID,
		ToAccountID:       req.AccountID,
		FromAmount:        money.FormatAmount(principal),
		ToAmount:          money.FormatAmount(principal),
		Purpose:           "Isplata kredita " + loanNum,
		InitiatorClientID: req.ClientID,
		Status:            domain.TxStatusRealized,
	}); err != nil {
		return nil, err
	}
	return created, nil
}

// =====================================================================
// Read paths
// =====================================================================

func (s *Service) ListLoanRequests(ctx context.Context, f domain.LoanRequestFilter, page, pageSize int) ([]*domain.LoanRequest, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if p.UserKind == auth.KindClient {
		f.ClientID = p.UserID // clients see only their own
	} else if !permissions.HasAny(p.Permissions, permissions.LoanRead, permissions.Admin) {
		return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.ListLoanRequests(ctx, f, page, pageSize)
}

func (s *Service) ListLoans(ctx context.Context, f domain.LoanFilter, page, pageSize int) ([]*domain.Loan, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if p.UserKind == auth.KindClient {
		f.ClientID = p.UserID
	} else if !permissions.HasAny(p.Permissions, permissions.LoanRead, permissions.Admin) {
		return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.ListLoans(ctx, f, page, pageSize)
}

func (s *Service) GetLoan(ctx context.Context, id string) (*domain.Loan, []*domain.LoanInstallment, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
	loan, err := s.Store.GetLoanByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if p.UserKind == auth.KindClient {
		if loan.ClientID != p.UserID {
			return nil, nil, apperr.PermissionDenied("nedovoljne permisije")
		}
	} else if !permissions.HasAny(p.Permissions, permissions.LoanRead, permissions.Admin) {
		return nil, nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	installments, err := s.Store.ListInstallmentsByLoan(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return loan, installments, nil
}

// =====================================================================
// Cron entry points
// =====================================================================

// InstallmentJobResult captures the daily job's outcome.
type InstallmentJobResult struct {
	Processed int
	Paid      int
	Missed    int // first-attempt failures (status flipped to overdue)
	Penalised int // 72h-retry failures that triggered the +0.05% bump
	Overdue   int // failures that didn't pay this run (Missed + Penalised + repeat retries)
}

// RunInstallmentJob is the admin-gated manual entry point; the cron
// uses RunInstallmentJobAuto. Both share runInstallmentJob.
func (s *Service) RunInstallmentJob(ctx context.Context, dueOn time.Time) (*InstallmentJobResult, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	if dueOn.IsZero() {
		dueOn = s.now()
	}
	return s.runInstallmentJob(ctx, dueOn)
}

// RunInstallmentJobAuto is the un-authenticated cron entry.
func (s *Service) RunInstallmentJobAuto(ctx context.Context) error {
	res, err := s.runInstallmentJob(ctx, s.now())
	if err != nil {
		return err
	}
	if res.Processed > 0 {
		s.Log.Info("installment job ran",
			"processed", res.Processed,
			"paid", res.Paid, "missed", res.Missed,
			"penalised", res.Penalised, "overdue", res.Overdue)
	}
	return nil
}

// runInstallmentJob iterates installments the cron should attempt and
// dispatches each through collectOneInstallment. The store-side query
// already filters to (a) unpaid+due-today and (b) overdue+72h-elapsed,
// so we don't re-check timing here.
func (s *Service) runInstallmentJob(ctx context.Context, dueOn time.Time) (*InstallmentJobResult, error) {
	due, err := s.Store.ListInstallmentsDueOn(ctx, dueOn)
	if err != nil {
		return nil, err
	}
	res := &InstallmentJobResult{Processed: len(due)}
	for _, inst := range due {
		outcome, err := s.collectOneInstallment(ctx, inst)
		if err != nil {
			s.Log.Warn("installment job: unexpected error",
				"installment_id", inst.ID, "loan_id", inst.LoanID, "error", err)
			res.Overdue++
			continue
		}
		switch outcome {
		case installmentPaid:
			res.Paid++
		case installmentMissed:
			res.Missed++
			res.Overdue++
			s.notifyMiss(ctx, inst)
		case installmentPenalised:
			res.Penalised++
			res.Overdue++
			s.notifyMiss(ctx, inst)
		case installmentRetryFailed:
			res.Overdue++
			s.notifyMiss(ctx, inst)
		}
	}
	return res, nil
}

// installmentOutcome reports what the per-row debit actually did.
type installmentOutcome int

const (
	installmentPaid        installmentOutcome = iota // debit succeeded
	installmentMissed                                // first-attempt failure: row marked overdue
	installmentPenalised                             // retry-after-72h failure: +0.05% bump applied
	installmentRetryFailed                           // retry-after-72h failure, penalty already applied
)

// collectOneInstallment attempts to debit `inst` and returns the
// outcome. Insufficient funds is a normal business case (committed as
// overdue, no error); errors are reserved for unexpected DB failures.
//
// State machine, keyed on (inst.Status, inst.OverdueSince, loan.LatePenaltyApplied):
//
//	unpaid, _, _       → try debit. Success → Paid. Fail → Missed (mark overdue).
//	overdue, set, false → 72h+ retry. Success → Paid (loan flips back to approved).
//	                       Fail → apply +0.05% bump, reschedule retry. → Penalised.
//	overdue, set, true  → 72h+ retry. Success → Paid. Fail → reschedule
//	                       retry, no further bump. → RetryFailed.
//
// Spec p.35.
func (s *Service) collectOneInstallment(ctx context.Context, inst *domain.LoanInstallment) (installmentOutcome, error) {
	var outcome installmentOutcome
	err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		loan, err := s.Store.GetLoanByID(ctx, inst.LoanID)
		if err != nil {
			return err
		}
		bankHouse, err := s.Store.GetSystemAccount(ctx, loan.Currency)
		if err != nil {
			return err
		}

		amt, _ := money.Parse(inst.Amount)
		neg := money.FormatAmount(money.Sub(money.MustParse("0"), amt))
		if err := s.Store.AdjustBalance(ctx, tx, loan.AccountID, neg); err != nil {
			if !errors.Is(err, store.ErrInsufficientFunds) {
				return err
			}
			// Insufficient funds → record overdue / apply penalty.
			// We commit (return nil) so the bookkeeping isn't rolled
			// back along with the failed debit.
			oc, herr := s.handleInsufficientFunds(ctx, tx, loan, inst)
			if herr != nil {
				return herr
			}
			outcome = oc
			return nil
		}

		// Successful debit: credit the bank's currency-house, mark
		// installment paid, write the ledger leg, and schedule the next.
		if err := s.Store.AdjustBalance(ctx, tx, bankHouse.ID, inst.Amount); err != nil {
			return err
		}
		if err := s.Store.MarkInstallmentPaid(ctx, tx, inst.ID); err != nil {
			return err
		}
		if _, err := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
			OpID:              uuid.NewString(),
			Kind:              domain.TransactionKind("loan_installment"),
			LegIndex:          1,
			FromAccountID:     loan.AccountID,
			ToAccountID:       bankHouse.ID,
			FromAmount:        inst.Amount,
			ToAmount:          inst.Amount,
			Purpose:           "Naplata rate " + loan.LoanNumber,
			InitiatorClientID: loan.ClientID,
			Status:            domain.TxStatusRealized,
		}); err != nil {
			return err
		}

		// remaining_principal -= principal-part of this installment.
		// principal-part = amount − (remaining × monthlyRate-at-due).
		remaining, _ := money.Parse(loan.RemainingPrincipal)
		annualPct, _ := money.Parse(inst.InterestRateAtDue)
		monthlyRate, _ := money.Div(annualPct, money.MustParse("1200"))
		interestPart := money.Mul(remaining, monthlyRate)
		principalPart := money.Sub(amt, interestPart)
		newRemaining := money.Sub(remaining, principalPart)
		if newRemaining.Sign() < 0 {
			newRemaining = money.MustParse("0")
		}

		nextStatus := domain.LoanApproved
		var nextDate *time.Time
		nextAmount := loan.NextInstallmentAmount
		if newRemaining.Sign() == 0 || inst.SequenceNumber >= loan.InstallmentsTotal {
			nextStatus = domain.LoanPaidOff
			nextAmount = ""
		} else {
			d := inst.ExpectedDueDate.AddDate(0, 1, 0)
			nextDate = &d
			rateAtDue := money.Format(
				money.Add(money.Add(money.MustParse(loan.BaseRate), money.MustParse(loan.CurrentOffset)), money.MustParse(loan.Margin)),
				4,
			)
			if _, err := s.Store.CreateInstallment(ctx, tx, &domain.LoanInstallment{
				LoanID:            loan.ID,
				SequenceNumber:    inst.SequenceNumber + 1,
				Amount:            loan.InstallmentAmount,
				InterestRateAtDue: rateAtDue,
				Currency:          loan.Currency,
				ExpectedDueDate:   d,
				Status:            domain.InstallmentUnpaid,
			}); err != nil {
				return err
			}
			nextAmount = loan.InstallmentAmount
		}
		if err := s.Store.UpdateLoanAfterInstallment(ctx, tx, loan.ID, money.FormatAmount(newRemaining), nextAmount, nextDate, nextStatus); err != nil {
			return err
		}
		outcome = installmentPaid
		return nil
	})
	return outcome, err
}

// handleInsufficientFunds runs the spec p.35 missed-payment branch
// inside the same tx as the failed debit. Either marks the row
// overdue for the first time, applies the +0.05% bump on the second
// failure, or just reschedules subsequent failures.
func (s *Service) handleInsufficientFunds(
	ctx context.Context, tx pgx.Tx,
	loan *domain.Loan, inst *domain.LoanInstallment,
) (installmentOutcome, error) {
	if inst.OverdueSince == nil {
		// First failure on this row.
		if err := s.Store.MarkInstallmentOverdue(ctx, tx, inst.ID); err != nil {
			return 0, err
		}
		if loan.Status != domain.LoanOverdue {
			if err := s.Store.UpdateLoanAfterInstallment(ctx, tx, loan.ID,
				loan.RemainingPrincipal, loan.NextInstallmentAmount,
				loan.NextInstallmentDate, domain.LoanOverdue); err != nil {
				return 0, err
			}
		}
		return installmentMissed, nil
	}
	// Already overdue → this is a 72h-retry that also failed.
	if !loan.LatePenaltyApplied {
		if err := s.applyLatePenalty(ctx, tx, loan); err != nil {
			return 0, err
		}
		if err := s.Store.RescheduleOverdueRetry(ctx, tx, inst.ID); err != nil {
			return 0, err
		}
		return installmentPenalised, nil
	}
	// Penalty already applied — just push the next retry 72h out.
	if err := s.Store.RescheduleOverdueRetry(ctx, tx, inst.ID); err != nil {
		return 0, err
	}
	return installmentRetryFailed, nil
}

// applyLatePenalty bumps the loan's base rate by +0.05% and recomputes
// the installment amount over the remaining schedule. Uses the count
// of paid installments to determine the remaining horizon.
func (s *Service) applyLatePenalty(ctx context.Context, tx pgx.Tx, loan *domain.Loan) error {
	paid, err := s.countPaidInstallments(ctx, loan.ID)
	if err != nil {
		return err
	}
	nRemaining := loan.InstallmentsTotal - paid
	if nRemaining <= 0 {
		// No future installments — penalty is moot. Still flip the flag
		// so we don't keep evaluating.
		return s.Store.ApplyLatePenalty(ctx, tx, loan.ID, loans.LatePenaltyBump,
			loan.InstallmentAmount, "0")
	}
	bump := money.MustParse(loans.LatePenaltyBump)
	newBase := money.Add(money.MustParse(loan.BaseRate), bump)
	margin := money.MustParse(loan.Margin)
	offset := money.MustParse(loan.CurrentOffset)
	monthly := loans.MonthlyRate(newBase, offset, margin)
	remaining := money.MustParse(loan.RemainingPrincipal)
	annuity := loans.Annuity(remaining, monthly, nRemaining)
	newRateAtDue := money.Format(money.Add(money.Add(newBase, offset), margin), 4)
	return s.Store.ApplyLatePenalty(ctx, tx, loan.ID, loans.LatePenaltyBump,
		money.FormatAmount(annuity), newRateAtDue)
}

// notifyMiss is the missed-payment notification hook, factored out so
// the cron loop reads cleanly. Best-effort; logged on failure.
func (s *Service) notifyMiss(ctx context.Context, inst *domain.LoanInstallment) {
	loan, err := s.Store.GetLoanByID(ctx, inst.LoanID)
	if err != nil {
		s.Log.Warn("notify miss: get loan failed", "loan_id", inst.LoanID, "error", err)
		return
	}
	s.notifyInstallmentMissed(ctx, loan, inst)
}

// VariableRateJobResult captures the monthly cron's outcome. Mirrors
// InstallmentJobResult so the admin RPC can render the same kind of
// summary regardless of which cron the operator ran.
type VariableRateJobResult struct {
	Processed int // active variable-rate loans seen
	Updated   int // refreshed successfully
	Failed    int // failed to refresh (logged; job continues)
}

// RunVariableRateJob is the admin-gated manual entry point; the cron
// uses RunVariableRateJobAuto. Both share runVariableRateJob.
//
// Spec p.34 "Naša simulacija → Opcija 1": pomeraj in [-1.50%, +1.50%].
// One bad loan must not abort the whole job — both entry points run
// best-effort and surface failure counts via the result.
func (s *Service) RunVariableRateJob(ctx context.Context) (*VariableRateJobResult, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	return s.runVariableRateJob(ctx)
}

// RunVariableRateJobAuto is the un-authenticated cron entry.
func (s *Service) RunVariableRateJobAuto(ctx context.Context) error {
	res, err := s.runVariableRateJob(ctx)
	if err != nil {
		return err
	}
	if res.Processed > 0 {
		s.Log.Info("variable-rate job ran",
			"processed", res.Processed,
			"updated", res.Updated,
			"failed", res.Failed)
	}
	return nil
}

// runVariableRateJob loads every active variable-rate loan and re-rolls
// the pomeraj on each. Per-loan errors are logged and counted; only a
// failure to load the list itself bubbles up.
func (s *Service) runVariableRateJob(ctx context.Context) (*VariableRateJobResult, error) {
	loansList, err := s.Store.ListActiveVariableLoans(ctx)
	if err != nil {
		return nil, err
	}
	res := &VariableRateJobResult{Processed: len(loansList)}
	for _, l := range loansList {
		if err := s.refreshOneVariableLoan(ctx, l); err != nil {
			s.Log.Warn("variable-rate refresh failed", "loan_id", l.ID, "error", err)
			res.Failed++
			continue
		}
		res.Updated++
	}
	return res, nil
}

// refreshOneVariableLoan re-rolls pomeraj and re-amortises the loan
// over its remaining installments at the new rate.
func (s *Service) refreshOneVariableLoan(ctx context.Context, l *domain.Loan) error {
	offset := randomPomeraj()
	base, _ := money.Parse(l.BaseRate)
	margin, _ := money.Parse(l.Margin)
	monthly := loans.MonthlyRate(base, offset, margin)
	paid, err := s.countPaidInstallments(ctx, l.ID)
	if err != nil {
		return err
	}
	nRemaining := l.InstallmentsTotal - paid
	if nRemaining <= 0 {
		return nil
	}
	remaining, _ := money.Parse(l.RemainingPrincipal)
	annuity := loans.Annuity(remaining, monthly, nRemaining)
	newAmount := money.FormatAmount(annuity)
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		return s.Store.UpdateVariableRate(ctx, tx, l.ID, money.Format(offset, 4), newAmount)
	})
}

func (s *Service) countPaidInstallments(ctx context.Context, loanID string) (int, error) {
	return s.Store.CountPaidInstallments(ctx, loanID)
}

// =====================================================================
// helpers
// =====================================================================

// randomPomeraj returns a value in [-1.50, +1.50] (inclusive of 0,
// uniformly-ish distributed in 0.01 increments).
func randomPomeraj() *big.Rat {
	// 301 steps from -150 to +150 (including 0). Random step,
	// divided by 100.
	v, err := rand.Int(rand.Reader, big.NewInt(301))
	if err != nil {
		return money.MustParse("0")
	}
	step := v.Int64() - 150
	return big.NewRat(step, 100)
}

func (s *Service) generateLoanNumber() string {
	// Loan number: timestamp-suffixed random for uniqueness +
	// readability. Spec p.32 example shows "17629" — short numeric;
	// we use 10 digits for headroom.
	v, err := rand.Int(rand.Reader, big.NewInt(1_000_000_0000))
	if err != nil {
		return s.now().Format("20060102150405")
	}
	return fmt.Sprintf("%010d", v.Int64())
}

func validateLoanRequest(in SubmitLoanRequestInput) error {
	if !loans.IsAllowedInstallments(loans.Type(in.LoanType), in.InstallmentsTotal) {
		return apperr.Validation(fmt.Sprintf("rok otplate %d nije dozvoljen za tip %s", in.InstallmentsTotal, in.LoanType))
	}
	switch in.LoanType {
	case domain.LoanTypeCash, domain.LoanTypeHousing, domain.LoanTypeAuto, domain.LoanTypeRefinance, domain.LoanTypeStudent:
	default:
		return apperr.Validation("nepoznata vrsta kredita")
	}
	switch in.InterestType {
	case domain.InterestFixed, domain.InterestVariable:
	default:
		return apperr.Validation("nepoznat tip kamatne stope")
	}
	switch in.EmploymentStatus {
	case domain.EmploymentPermanent, domain.EmploymentTemporary, domain.EmploymentUnemployed:
	default:
		return apperr.Validation("nepoznat status zaposlenja")
	}
	if !in.Currency.Supported() {
		return apperr.Validation("nepodržana valuta")
	}
	amt, err := money.Parse(in.Amount)
	if err != nil {
		return apperr.Validation(err.Error())
	}
	if !money.IsPositive(amt) {
		return apperr.Validation("iznos kredita mora biti pozitivan")
	}
	return nil
}
