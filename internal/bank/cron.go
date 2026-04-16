package bank

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"gorm.io/gorm"
)

// kicks off background jobs for loan stuff, returns a cancel func for cleanup
func (s *Server) StartScheduler() func() {
	ctx, cancel := context.WithCancel(context.Background())

	go s.runOnSchedule(ctx, 2, isFirstOfMonth, s.RunMonthlyVariableRateUpdate)
	go s.runOnSchedule(ctx, 6, always, s.RunDailyInstallmentCollection)

	return cancel
}

func always(time.Time) bool           { return true }
func isFirstOfMonth(t time.Time) bool { return t.Day() == 1 }

// poor man's cron - wakes up at the target hour, runs fn if filter says yes
func (s *Server) runOnSchedule(ctx context.Context, hour int, filter func(time.Time) bool, fn func()) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case t := <-timer.C:
			if filter(t) {
				fn()
			}
		}
	}
}

// recalculates rates for variable loans on the 1st of each month
func (s *Server) RunMonthlyVariableRateUpdate() {
	log.Println("[Cron] Running monthly variable rate update")

	loans, err := s.getApprovedVariableLoans()
	if err != nil {
		log.Printf("[Cron] ERROR fetching variable loans: %v", err)
		return
	}

	for _, loan := range loans {
		currencyLabel, err := s.getCurrencyLabelByID(loan.Currency_id)
		if err != nil {
			log.Printf("[Cron] ERROR getting currency for loan %d: %v", loan.Id, err)
			continue
		}

		rateToRSD, err := s.getExchangeRateToRSD(currencyLabel)
		if err != nil {
			log.Printf("[Cron] ERROR getting exchange rate for %s: %v", currencyLabel, err)
			continue
		}

		amountRSD := int64(float64(loan.Amount) * rateToRSD)
		baseRate := BaseAnnualRate(amountRSD)
		// spec says random for simulation, should probably be tied to EURIBOR or something
		offset := -1.50 + rand.Float64()*3.0
		newAnnualRate := baseRate + offset + MarginForLoanType(loan.Type)

		remainingMonths := loan.Installments - int64(s.countPaidInstallments(loan.Id))
		if remainingMonths <= 0 {
			continue
		}

		newPayment := CalculateAnnuity(loan.Remaining_debt, newAnnualRate, remainingMonths)

		err = s.db_gorm.Model(&Loan{}).Where("id = ?", loan.Id).Updates(map[string]any{
			"interest_rate":   float32(newAnnualRate),
			"monthly_payment": newPayment,
		}).Error
		if err != nil {
			log.Printf("[Cron] ERROR updating loan %d: %v", loan.Id, err)
			continue
		}

		log.Printf("[Cron] Updated variable loan %d: rate=%.2f%%, payment=%d", loan.Id, newAnnualRate, newPayment)
	}
}

// daily job: collect payments from due loans, retry late ones after 3 days
func (s *Server) RunDailyInstallmentCollection() {
	log.Println("[Cron] Running daily installment collection")
	today := time.Now().Truncate(24 * time.Hour)

	loans, err := s.getLoansDueForCollection(today)
	if err != nil {
		log.Printf("[Cron] ERROR fetching due loans: %v", err)
		return
	}

	for i := range loans {
		s.processLoanPayment(&loans[i], today, false)
	}

	// retry late ones - give them 3 days grace period before we bug them again
	var lateInstallments []LoanInstallment
	err = s.db_gorm.
		Where("status = ? AND due_date <= ?", Installment_Late, today.AddDate(0, 0, -3)).
		Find(&lateInstallments).Error
	if err != nil {
		log.Printf("[Cron] ERROR fetching late installments for retry: %v", err)
		return
	}

	retried := make(map[int64]bool)
	for _, inst := range lateInstallments {
		if retried[inst.Loan_id] {
			continue
		}
		retried[inst.Loan_id] = true

		var loan Loan
		if err := s.db_gorm.First(&loan, inst.Loan_id).Error; err != nil {
			log.Printf("[Cron] ERROR fetching loan %d for retry: %v", inst.Loan_id, err)
			continue
		}
		s.processLoanPayment(&loan, today, true)
	}
}

func (s *Server) processLoanPayment(loan *Loan, today time.Time, isRetry bool) {
	// Deduct the installment from the client's account
	result := s.db_gorm.Model(&Account{}).
		Where("id = ? AND balance >= ?", loan.Account_id, loan.Monthly_payment).
		Update("balance", gorm.Expr("balance - ?", loan.Monthly_payment))
	paymentSucceeded := result.Error == nil && result.RowsAffected > 0

	if paymentSucceeded {
		log.Printf("[Cron] Deducted %d from account %d (loan %d)", loan.Monthly_payment, loan.Account_id, loan.Id)
	} else {
		log.Printf("[Cron] Failed to deduct %d from account %d (loan %d, insufficient funds or error)", loan.Monthly_payment, loan.Account_id, loan.Id)
	}

	if paymentSucceeded {
		installment := LoanInstallment{
			Loan_id:            loan.Id,
			Installment_amount: loan.Monthly_payment,
			Interest_rate:      loan.Interest_rate,
			Currency_id:        loan.Currency_id,
			Due_date:           loan.Next_payment_due,
			Paid_date:          today,
			Status:             Installment_Paid,
		}
		if err := s.db_gorm.Create(&installment).Error; err != nil {
			log.Printf("[Cron] ERROR creating paid installment for loan %d: %v", loan.Id, err)
			return
		}

		newDebt := loan.Remaining_debt - loan.Monthly_payment
		if newDebt < 0 {
			newDebt = 0
		}

		updates := map[string]any{
			"remaining_debt":   newDebt,
			"next_payment_due": loan.Next_payment_due.AddDate(0, 1, 0),
		}
		if newDebt <= 0 {
			updates["loan_status"] = Paid
		}

		if err := s.db_gorm.Model(&Loan{}).Where("id = ?", loan.Id).Updates(updates).Error; err != nil {
			log.Printf("[Cron] ERROR updating loan %d after payment: %v", loan.Id, err)
		}

		log.Printf("[Cron] Loan %d: payment recorded, remaining_debt=%d", loan.Id, newDebt)

		currencyLabel, _ := s.getCurrencyLabelByID(loan.Currency_id)
		email, _ := s.getClientEmailByAccountID(loan.Account_id)
		if email != "" {
			_ = s.sendLoanPaymentSuccessEmail(
				context.Background(),
				email,
				fmt.Sprintf("%d", loan.Id),
				fmt.Sprintf("%d", loan.Monthly_payment),
				currencyLabel,
			)
		}
	} else {
		installment := LoanInstallment{
			Loan_id:            loan.Id,
			Installment_amount: loan.Monthly_payment,
			Interest_rate:      loan.Interest_rate,
			Currency_id:        loan.Currency_id,
			Due_date:           loan.Next_payment_due,
			Paid_date:          time.Time{},
			Status:             Installment_Late,
		}
		if err := s.db_gorm.Create(&installment).Error; err != nil {
			log.Printf("[Cron] ERROR creating late installment for loan %d: %v", loan.Id, err)
		}

		s.db_gorm.Model(&Loan{}).Where("id = ?", loan.Id).Update("loan_status", Late)

		// bijemo reket
		if isRetry {
			newRate := float32(float64(loan.Interest_rate) + 0.05)
			s.db_gorm.Model(&Loan{}).Where("id = ?", loan.Id).Update("interest_rate", newRate)
			log.Printf("[Cron] Loan %d: penalty applied, new rate=%.2f%%", loan.Id, newRate)
		}

		// let them know their payment bounced
		currencyLabel, _ := s.getCurrencyLabelByID(loan.Currency_id)
		email, _ := s.getClientEmailByAccountID(loan.Account_id)
		if email != "" {
			_ = s.sendLoanPaymentFailedEmail(
				context.Background(),
				email,
				fmt.Sprintf("%d", loan.Id),
				fmt.Sprintf("%d", loan.Monthly_payment),
				currencyLabel,
				loan.Next_payment_due.Format("2006-01-02"),
			)
		}

		log.Printf("[Cron] Loan %d: payment FAILED, status set to late", loan.Id)
	}
}
