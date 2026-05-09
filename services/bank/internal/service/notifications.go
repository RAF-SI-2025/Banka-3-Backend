package service

import (
	"context"
	"fmt"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// notify is a best-effort email send. When Notifier or UserResolver is
// not wired (slice-1 tests, dev without SMTP), we log and move on —
// notifications must never fail a business operation.
func (s *Service) notify(ctx context.Context, clientID, subject, body string) {
	if s.Notifier == nil || s.UserResolver == nil {
		s.Log.Info("notification skipped (no notifier wired)", "client_id", clientID, "subject", subject)
		return
	}
	to, err := s.UserResolver.ClientEmail(ctx, clientID)
	if err != nil || to == "" {
		s.Log.Warn("notify: client email lookup failed", "client_id", clientID, "error", err)
		return
	}
	if err := s.Notifier.Send(ctx, to, subject, body, false); err != nil {
		s.Log.Warn("notify: send failed", "to", to, "subject", subject, "error", err)
	}
}

// notifyCardStatusChanged emits a Serbian notice when a card flips
// to/from blocked or is deactivated. Active→Active is a no-op.
func (s *Service) notifyCardStatusChanged(ctx context.Context, c *domain.Card, oldStatus domain.CardStatus, ownerClientID string) {
	if c.Status == oldStatus {
		return
	}
	var subject, body string
	switch c.Status {
	case domain.CardBlocked:
		subject = "Vaša kartica je blokirana"
		body = fmt.Sprintf("Poštovani,\n\nKartica %s je blokirana. Ako blokadu niste tražili Vi, obratite se najbližoj filijali.\n\nBanka 3", maskCardNumber(c.Number))
	case domain.CardActive:
		if oldStatus == domain.CardBlocked {
			subject = "Vaša kartica je odblokirana"
			body = fmt.Sprintf("Poštovani,\n\nKartica %s je ponovo aktivna.\n\nBanka 3", maskCardNumber(c.Number))
		}
	case domain.CardDeactivated:
		subject = "Vaša kartica je deaktivirana"
		body = fmt.Sprintf("Poštovani,\n\nKartica %s je trajno deaktivirana.\n\nBanka 3", maskCardNumber(c.Number))
	}
	if subject == "" {
		return
	}
	s.notify(ctx, ownerClientID, subject, body)
}

// notifyLoanDecision emits a Serbian notice on approve/reject.
func (s *Service) notifyLoanDecision(ctx context.Context, req *domain.LoanRequest) {
	switch req.Status {
	case domain.RequestApproved:
		body := fmt.Sprintf(
			"Poštovani,\n\nVaš zahtev za %s kredit u iznosu %s %s je odobren. Sredstva su uplaćena na Vaš račun.\n\nBanka 3",
			loanTypeSerbian(req.LoanType), req.Amount, req.Currency,
		)
		s.notify(ctx, req.ClientID, "Vaš zahtev za kredit je odobren", body)
	case domain.RequestRejected:
		reason := req.RejectionReason
		if reason == "" {
			reason = "interno bankarsko utvrđivanje"
		}
		body := fmt.Sprintf(
			"Poštovani,\n\nNažalost, Vaš zahtev za %s kredit u iznosu %s %s je odbijen.\nRazlog: %s.\n\nBanka 3",
			loanTypeSerbian(req.LoanType), req.Amount, req.Currency, reason,
		)
		s.notify(ctx, req.ClientID, "Vaš zahtev za kredit je odbijen", body)
	}
}

// notifyAccountCreated emits the spec-E2E "klijent dobija email
// obaveštenje" notice fired right after a new account is opened.
// Business accounts route through the company's owner client; system
// accounts (no owner) are skipped.
func (s *Service) notifyAccountCreated(ctx context.Context, a *domain.Account) {
	if a == nil || a.OwnerClientID == "" || a.OwnerClientID == domain.SystemOwnerID {
		return
	}
	body := fmt.Sprintf(
		"Poštovani,\n\nVaš novi račun %s (%s) je otvoren. Početno stanje: %s %s.\n\nBanka 3",
		a.Number, a.Name, a.Balance, a.Currency,
	)
	s.notify(ctx, a.OwnerClientID, "Vaš novi račun je otvoren", body)
}

// notifyPaymentSucceeded emits the spec-E2E "Klijent dobija email
// potvrdu" notice fired after a successful payment. Sender side only
// — the recipient of an inter-client payment is *not* notified by
// the bank (no spec scenario for it).
func (s *Service) notifyPaymentSucceeded(ctx context.Context, fromClientID string, fromAccountNumber, toAccountNumber, amount string, currency domain.Currency) {
	if fromClientID == "" || fromClientID == domain.SystemOwnerID {
		return
	}
	body := fmt.Sprintf(
		"Poštovani,\n\nUspešno je realizovano plaćanje sa računa %s na račun %s u iznosu %s %s.\n\nBanka 3",
		fromAccountNumber, toAccountNumber, amount, currency,
	)
	s.notify(ctx, fromClientID, "Potvrda plaćanja", body)
}

// notifyInstallmentMissed runs from the cron when a loan can't pay an
// installment because the account is short.
func (s *Service) notifyInstallmentMissed(ctx context.Context, loan *domain.Loan, inst *domain.LoanInstallment) {
	body := fmt.Sprintf(
		"Poštovani,\n\nUplata %d. rate u iznosu %s %s za kredit %s nije realizovana zbog nedovoljnih sredstava na računu. Molimo Vas da napunite račun kako biste izbegli kašnjenje.\n\nBanka 3",
		inst.SequenceNumber, inst.Amount, inst.Currency, loan.LoanNumber,
	)
	s.notify(ctx, loan.ClientID, "Rata kredita nije naplaćena", body)
}

func loanTypeSerbian(t domain.LoanType) string {
	switch t {
	case domain.LoanTypeCash:
		return "gotovinski"
	case domain.LoanTypeHousing:
		return "stambeni"
	case domain.LoanTypeAuto:
		return "auto"
	case domain.LoanTypeRefinance:
		return "refinansirajući"
	case domain.LoanTypeStudent:
		return "studentski"
	}
	return string(t)
}

// maskCardNumber returns the last-4-only display "****1234".
func maskCardNumber(n string) string {
	if len(n) < 4 {
		return "****"
	}
	return "****" + n[len(n)-4:]
}
