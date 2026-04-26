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

// UnifiedTransaction represents a combined view of payments and transfers.
// This is a DTO (Data Transfer Object) used for unified processing of different transaction types.
type UnifiedTransaction struct {
	ID              int64     `gorm:"column:id"`
	Type            string    `gorm:"column:type"`
	FromAccount     string    `gorm:"column:from_account"`
	ToAccount       string    `gorm:"column:to_account"`
	InitialAmount   int64     `gorm:"column:initial_amount"`
	FinalAmount     int64     `gorm:"column:final_amount"`
	Fee             int64     `gorm:"column:fee"`
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

// GetFilteredTransactionsRepo fetches a paginated list of both payments and transfers
// for a given set of account numbers, applying optional filters.
func (s *Server) GetFilteredTransactionsRepo(accNumbers []string, req *bankpb.GetTransactionsRequest) ([]UnifiedTransaction, int64, error) {
	var results []UnifiedTransaction
	var total int64

	// Prevent empty account list from causing unexpected query behavior
	if len(accNumbers) == 0 {
		return results, 0, nil
	}

	// 1. Sub-query for Payments: Mapping payment-specific fields and casting status to text for UNION compatibility
	paymentSub := s.db_gorm.Table("payments p").
		Select(`p.transaction_id AS id, 'payment' AS type, p.from_account, p.to_account, 
                p.start_amount AS initial_amount, p.end_amount AS final_amount, p.commission AS fee, 
                a.currency AS currency, p.transaction_code::text AS payment_code, 
                p.call_number AS reference_number, p.reason AS purpose, 
                p.status::text AS status, p.timestamp, p.recipient_id, 0 AS start_currency_id, 0 AS exchange_rate`).
		Joins("JOIN accounts a ON a.number = p.from_account").
		Where("p.from_account IN ? OR p.to_account IN ?", accNumbers, accNumbers)

	// 2. Sub-query for Transfers: Mapping transfer-specific fields and casting status to text
	transferSub := s.db_gorm.Table("transfers t").
		Select(`t.transaction_id AS id, 'transfer' AS type, t.from_account, t.to_account, 
                t.start_amount AS initial_amount, t.end_amount AS final_amount, t.commission AS fee, 
                a.currency AS currency, '' AS payment_code, '' AS reference_number, '' AS purpose, 
                t.status::text AS status, t.timestamp, 0 AS recipient_id, t.start_currency_id, t.exchange_rate`).
		Joins("JOIN accounts a ON a.number = t.from_account").
		Where("t.from_account IN ? OR t.to_account IN ?", accNumbers, accNumbers)

	// 3. Combine both tables using UNION ALL
	unionQuery := s.db_gorm.Raw("? UNION ALL ?", paymentSub, transferSub)
	query := s.db_gorm.Table("(?) AS tx", unionQuery)

	// 4. Applying Request Filters
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

	// 5. Total Count for pagination metadata
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 6. Sorting Logic (SQL Injection Protection: Whitelisting)
	// GORM's .Order() is vulnerable to string injection. We must validate against allowed columns.
	sortBy := "timestamp"
	allowedSortFields := map[string]bool{
		"timestamp": true,
		"type":      true,
		"currency":  true,
		"status":    true,
	}
	if req.SortBy != "" && allowedSortFields[req.SortBy] {
		sortBy = req.SortBy
	}

	sortOrder := "DESC"
	if req.SortOrder == "ASC" || req.SortOrder == "DESC" {
		sortOrder = req.SortOrder
	}

	// 7. Pagination and final scanning
	// Use Limit and Offset methods which are safe from injection
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 10
	}
	offset := (int(req.Page) - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	// Combine Order with validated strings
	err := query.Order(fmt.Sprintf("tx.%s %s", sortBy, sortOrder)).
		Limit(pageSize).
		Offset(offset).
		Scan(&results).Error

	return results, total, err
}

// GetSingleTransactionRepo retrieves a specific transaction by ID and type, ensuring
// compatibility with the UnifiedTransaction structure.
func (s *Server) GetSingleTransactionRepo(id int64, txType string) (UnifiedTransaction, error) {
	var row UnifiedTransaction

	if txType == "payment" {
		err := s.db_gorm.Table("payments p").
			Select(`p.transaction_id AS id, 'payment' AS type, p.from_account, p.to_account, 
                    p.start_amount AS initial_amount, p.end_amount AS final_amount, p.commission AS fee, 
                    a.currency AS currency, p.transaction_code::text AS payment_code, 
                    p.call_number AS reference_number, p.reason AS purpose, 
                    p.status::text AS status, p.timestamp, p.recipient_id, 0 AS start_currency_id, 0 AS exchange_rate`).
			Joins("JOIN accounts a ON a.number = p.from_account").
			Where("p.transaction_id = ?", id).
			First(&row).Error
		return row, err
	}

	// Fetching from transfers table if type is not payment
	err := s.db_gorm.Table("transfers t").
		Select(`t.transaction_id AS id, 'transfer' AS type, t.from_account, t.to_account, 
                t.start_amount AS initial_amount, t.end_amount AS final_amount, t.commission AS fee, 
                a.currency AS currency, '' AS payment_code, '' AS reference_number, '' AS purpose, 
                t.status::text AS status, t.timestamp, 0 AS recipient_id, 
                t.start_currency_id, t.exchange_rate`).
		Joins("JOIN accounts a ON a.number = t.from_account").
		Where("t.transaction_id = ?", id).
		First(&row).Error

	return row, err
}

// GetClientAccountNumbers returns a slice of all account numbers owned by a specific client.
func (s *Server) GetClientAccountNumbers(clientID int64) ([]string, error) {
	var accountNumbers []string
	err := s.db_gorm.Table("accounts").
		Where("owner = ?", clientID).
		Pluck("number", &accountNumbers).Error

	return accountNumbers, err
}

// CheckTransactionOwnership verifies if a client is either the sender or receiver
// in a transaction to enforce security policies.
func (s *Server) CheckTransactionOwnership(clientID int64, fromAccount string, toAccount string) (bool, error) {
	var count int64
	err := s.db_gorm.Table("accounts").
		Where("owner = ? AND (number = ? OR number = ?)", clientID, fromAccount, toAccount).
		Count(&count).Error

	return count > 0, err
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
