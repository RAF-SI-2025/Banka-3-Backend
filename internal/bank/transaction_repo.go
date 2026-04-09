package bank

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
)

// TransactionRow mapira uniju tabela payments i transfers
type TransactionRow struct {
	ID              int64     `gorm:"column:id"`
	Type            string    `gorm:"column:type"`
	FromAccount     string    `gorm:"column:from_account"`
	ToAccount       string    `gorm:"column:to_account"`
	InitialAmount   float64   `gorm:"column:initial_amount"`
	FinalAmount     float64   `gorm:"column:final_amount"`
	Fee             float64   `gorm:"column:fee"`
	Currency        string    `gorm:"column:currency"`
	PaymentCode     string    `gorm:"column:payment_code"`
	ReferenceNumber string    `gorm:"column:reference_number"`
	Purpose         string    `gorm:"column:purpose"`
	Status          string    `gorm:"column:status"`
	Timestamp       time.Time `gorm:"column:timestamp"`
	RecipientID     int64     `gorm:"column:recipient_id"`
	StartCurrencyID int64     `gorm:"column:start_currency_id"`
	ExchangeRate    float64   `gorm:"column:exchange_rate"`
}

func (s *Server) GetFilteredTransactionsRepo(accNumbers []string, req *bankpb.GetTransactionsRequest) ([]TransactionRow, int64, error) {
	var results []TransactionRow
	var total int64

	// Definisanje pod-upita za Payments
	paymentSub := s.db_gorm.Table("payments p").
		Select(`p.transaction_id AS id, 'payment' AS type, p.from_account, p.to_account, 
                p.start_amount AS initial_amount, p.end_amount AS final_amount, p.commission AS fee, 
                c.code AS currency, COALESCE(p.transcaction_code::text, '') AS payment_code, 
                COALESCE(p.call_number, '') AS reference_number, COALESCE(p.reason, '') AS purpose, 
                p.status, p.timestamp, p.recipient_id, 0 AS start_currency_id, 0 AS exchange_rate`).
		Joins("JOIN accounts a ON a.number = p.from_account").
		Joins("JOIN currencies c ON c.id = a.currency_id").
		Where("p.from_account IN ? OR p.to_account IN ?", accNumbers, accNumbers)

	// Definisanje pod-upita za Transfers
	transferSub := s.db_gorm.Table("transfers t").
		Select(`t.transaction_id AS id, 'transfer' AS type, t.from_account, t.to_account, 
                t.start_amount AS initial_amount, t.end_amount AS final_amount, t.commission AS fee, 
                c.code AS currency, '' AS payment_code, '' AS reference_number, '' AS purpose, 
                t.status, t.timestamp, 0 AS recipient_id, t.start_currency_id, t.exchange_rate`).
		Joins("JOIN currencies c ON c.id = t.start_currency_id").
		Where("t.from_account IN ? OR t.to_account IN ?", accNumbers, accNumbers)

	// Kreiranje baznog upita nad unijom
	unionQuery := s.db_gorm.Raw("? UNION ALL ?", paymentSub, transferSub)
	query := s.db_gorm.Table("(?) AS tx", unionQuery)

	// Primena filtera iz Request-a
	if req.AccountNumber != "" {
		query = query.Where("(tx.from_account = ? OR tx.to_account = ?)", req.AccountNumber, req.AccountNumber)
	}
	if req.DateFrom != "" {
		query = query.Where("tx.timestamp >= ?", req.DateFrom)
	}
	if req.DateTo != "" {
		query = query.Where("tx.timestamp <= ?", req.DateTo)
	}
	if req.AmountFrom > 0 {
		query = query.Where("tx.initial_amount >= ?", req.AmountFrom)
	}
	if req.AmountTo > 0 {
		query = query.Where("tx.initial_amount <= ?", req.AmountTo)
	}
	if req.Status != "" {
		query = query.Where("tx.status = ?", req.Status)
	}

	// Count i Paginacija
	query.Count(&total)

	// Default sortiranje ako nije prosleđeno
	sortBy := "timestamp"
	if req.SortBy != "" {
		sortBy = req.SortBy
	}
	sortOrder := "DESC"
	if req.SortOrder != "" {
		sortOrder = req.SortOrder
	}

	offset := (req.Page - 1) * req.PageSize
	err := query.Order(fmt.Sprintf("tx.%s %s", sortBy, sortOrder)).
		Limit(int(req.PageSize)).Offset(int(offset)).Scan(&results).Error

	return results, total, err
}

// GetSingleTransactionRepo vraća jednu transakciju iz specifične tabele
func (s *Server) GetSingleTransactionRepo(id int64, txType string) (TransactionRow, error) {
	var row TransactionRow
	table := "payments"
	if txType == "transfer" {
		table = "transfers"
	}

	err := s.db_gorm.Table(table).
		Select("*, transaction_id as id").
		Where("transaction_id = ?", id).
		First(&row).Error

	return row, err
}

func (s *Server) GetClientAccountNumbers(clientID int64) ([]string, error) {
	var accountNumbers []string

	// Koristimo Pluck da bismo izvukli samo kolonu 'number' u slice stringova
	err := s.db_gorm.Table("accounts").
		Where("owner = ?", clientID).
		Pluck("number", &accountNumbers).Error

	if err != nil {
		return nil, err
	}

	return accountNumbers, nil
}

func (s *Server) CheckTransactionOwnership(clientID int64, fromAccount string, toAccount string) (bool, error) {
	var count int64

	// Proveravamo da li u tabeli accounts postoji red gde je owner naš klijent
	// i gde je broj računa ili onaj sa kojeg je poslato ili onaj na koji je primljeno.
	err := s.db_gorm.Table("accounts").
		Where("owner = ? AND (number = ? OR number = ?)", clientID, fromAccount, toAccount).
		Count(&count).Error

	if err != nil {
		return false, err
	}

	// Ako je count > 0, znači da klijent poseduje bar jedan od ta dva računa
	return count > 0, nil
}

// ==================================================================================

func (s *Server) ProcessPayment(from_account string, to_account string, amount int64, transaction_code int64, call_number string, reason string) (*Payment, *Currency, error) {

	fromAcc, err := s.GetAccountByNumberRecord(from_account)
	if err != nil {
		return nil, nil, ErrAccountNotFound
	}
	toAcc, err := s.GetAccountByNumberRecord(to_account)
	if err != nil {
		return nil, nil, ErrAccountNotFound
	}

	var finalAmount = amount
	var commission int64 = 0

	// 1. Logika konverzije
	if fromAcc.Currency != toAcc.Currency {
		ctx := context.Background()
		// EUR -> RSD
		resp1, err := s.ExchangeService.ConvertMoney(ctx, &exchange.ConversionRequest{
			FromCurrency: fromAcc.Currency,
			ToCurrency:   "RSD",
			Amount:       float64(amount),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("exchange error (hop 1): %v", err)
		}
		// RSD -> USD
		resp2, err := s.ExchangeService.ConvertMoney(ctx, &exchange.ConversionRequest{
			FromCurrency: "RSD",
			ToCurrency:   toAcc.Currency,
			Amount:       resp1.ConvertedAmount,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("exchange error (hop 2): %v", err)
		}

		commission = int64(math.Round(float64(amount) * commission_rate))
		finalAmount = int64(math.Round(resp2.ConvertedAmount))
	}

	tx, err := s.database.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("start tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 2. Ažuriranje balansa
	if fromAcc.Currency != toAcc.Currency {
		// Razlicita valuta:

		systemEmail := "system@banka3.rs"
		// A. Skini platiocu (Source)
		if _, err := s.DecreaseAccountBalance(tx, from_account, amount); err != nil {
			return nil, nil, err
		}

		// B. Dodaj banci (Source)
		_, err = tx.Exec(`UPDATE accounts SET balance = balance + $1 WHERE currency = $2 AND owner = (SELECT id FROM clients WHERE email = $3)`,
			amount, fromAcc.Currency, systemEmail)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to credit bank source account: %w", err)
		}

		// C. Skini banci (Target)
		_, err = tx.Exec(`UPDATE accounts SET balance = balance - $1 WHERE currency = $2 AND owner = (SELECT id FROM clients WHERE email = $3)`,
			finalAmount, toAcc.Currency, systemEmail)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to debit bank target account: %w", err)
		}

		// D. Dodaj primaocu (Target)
		if _, err := s.IncreaseAccountBalance(tx, to_account, finalAmount); err != nil {
			return nil, nil, err
		}

	} else {
		// Ista valuta: Direktno
		if _, err := s.DecreaseAccountBalance(tx, from_account, amount); err != nil {
			return nil, nil, err
		}
		if _, err := s.IncreaseAccountBalance(tx, to_account, amount); err != nil {
			return nil, nil, err
		}
	}

	// 3. Kreiraj zapis o plaćanju
	payment, err := s.CreatePayment(tx, from_account, to_account, amount, finalAmount, commission, transaction_code, call_number, reason)
	if err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}

	currency, err := s.getCurrencyByLabel(fromAcc.Currency)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get currency id: %v", err)
	}

	return payment, currency, nil
}

func (s *Server) CreateTransfer(fromAccount, toAccount string, amount int64) (*Transfer, error) {
	if fromAccount == toAccount {
		return nil, errors.New("cannot transfer to same account")
	}

	fromAcc, err := s.GetAccountByNumberRecord(fromAccount)
	if err != nil {
		return nil, err
	}
	toAcc, err := s.GetAccountByNumberRecord(toAccount)
	if err != nil {
		return nil, err
	}

	var finalAmount = amount
	var exchangeRate = 1.0
	var commission int64 = 0

	// Multi-currency logic: Always route through RSD if currencies differ
	if fromAcc.Currency != toAcc.Currency {
		ctx := context.Background()

		// Source -> RSD
		resp1, err := s.ExchangeService.ConvertMoney(ctx, &exchange.ConversionRequest{
			FromCurrency: fromAcc.Currency,
			ToCurrency:   "RSD",
			Amount:       float64(amount),
		})
		if err != nil {
			return nil, fmt.Errorf("exchange error (source to RSD): %v", err)
		}

		// RSD -> Target
		resp2, err := s.ExchangeService.ConvertMoney(ctx, &exchange.ConversionRequest{
			FromCurrency: "RSD",
			ToCurrency:   toAcc.Currency,
			Amount:       resp1.ConvertedAmount,
		})
		if err != nil {
			return nil, fmt.Errorf("exchange error (RSD to destination): %v", err)
		}

		commission = int64(float64(amount) * commission_rate)
		finalAmount = int64(resp2.ConvertedAmount)
		exchangeRate = resp2.ExchangeRate
	}

	currency, err := s.getCurrencyByLabel(fromAcc.Currency)
	if err != nil {
		return nil, err
	}

	tx, err := s.database.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if fromAcc.Balance < amount {
		return nil, errors.New("insufficient funds")
	}

	row := tx.QueryRow(`
    INSERT INTO transfers (
        from_account, to_account, start_amount, end_amount,
        start_currency_id, exchange_rate, commission, status
    )
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    RETURNING transaction_id, from_account, to_account,
              start_amount, end_amount,
              start_currency_id, exchange_rate,
              commission, status, timestamp
`, fromAccount, toAccount, amount, finalAmount, currency.Id, exchangeRate, commission, pending)

	transfer, err := scanTransfer(row)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return transfer, nil
}

func (s *Server) ConfirmTransfer(transferID int64, verificationCode string) error {
	if verificationCode == "" {
		return errors.New("verification code required")
	}

	tx, err := s.database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var t Transfer
	err = tx.QueryRow(`
		SELECT transaction_id, from_account, to_account, start_amount, end_amount, status
		FROM transfers WHERE transaction_id = $1
	`, transferID).Scan(&t.Transaction_id, &t.From_account, &t.To_account, &t.Start_amount, &t.End_amount, &t.Status)
	if err != nil {
		return err
	}

	if t.Status != pending {
		return errors.New("transfer already processed")
	}

	// Fetch account currencies to determine if we need the bank intermediary
	fromAcc, _ := s.GetAccountByNumberRecord(t.From_account)
	toAcc, _ := s.GetAccountByNumberRecord(t.To_account)

	if fromAcc.Currency != toAcc.Currency {
		// Multi-currency => Involve Bank Accounts
		// We use the system email from seed.sql to find the bank's accounts
		systemEmail := "system@banka3.rs"

		// 1. Debit Client (Source Currency)
		res, _ := tx.Exec(`UPDATE accounts SET balance = balance - $1 WHERE number = $2 AND balance >= $1`, t.Start_amount, t.From_account)
		if aff, _ := res.RowsAffected(); aff == 0 {
			return errors.New("insufficient funds")
		}

		// 2. Credit Bank (Source Currency)
		_, err = tx.Exec(`UPDATE accounts SET balance = balance + $1 WHERE currency = $2 AND owner = (SELECT id FROM clients WHERE email = $3)`,
			t.Start_amount, fromAcc.Currency, systemEmail)
		if err != nil {
			return err
		}

		// 3. Debit Bank (Target Currency)
		_, err = tx.Exec(`UPDATE accounts SET balance = balance - $1 WHERE currency = $2 AND owner = (SELECT id FROM clients WHERE email = $3)`,
			t.End_amount, toAcc.Currency, systemEmail)
		if err != nil {
			return err
		}

		// 4. Credit Client (Target Currency)
		_, err = tx.Exec(`UPDATE accounts SET balance = balance + $1 WHERE number = $2`, t.End_amount, t.To_account)
		if err != nil {
			return err
		}

	} else {
		// Same currency: Standard direct transfer
		res, _ := tx.Exec(`UPDATE accounts SET balance = balance - $1 WHERE number = $2 AND balance >= $1`, t.Start_amount, t.From_account)
		if aff, _ := res.RowsAffected(); aff == 0 {
			return errors.New("insufficient funds")
		}
		_, err = tx.Exec(`UPDATE accounts SET balance = balance + $1 WHERE number = $2`, t.Start_amount, t.To_account)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE transfers SET status = $1 WHERE transaction_id = $2`, realized, t.Transaction_id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
