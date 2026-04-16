package bank

import (
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
)

func (s *Server) AccountNameExistsForOwner(ownerID int64, name string, excludeAccountNumber string) (bool, error) {
	var count int64

	err := s.db_gorm.
		Model(&Account{}).
		Where("owner = ? AND name = ? AND number <> ?", ownerID, name, excludeAccountNumber).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Server) UpdateAccountNameRecord(accountNumber string, name string) error {
	result := s.db_gorm.
		Model(&Account{}).
		Where("number = ?", accountNumber).
		Update("name", name)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("account not found")
	}

	return nil
}

func (s *Server) UpdateAccountLimitsRecord(accountNumber string, dailyLimit *int64, monthlyLimit *int64) error {
	updates := map[string]any{}

	if dailyLimit != nil {
		updates["daily_limit"] = *dailyLimit
	}
	if monthlyLimit != nil {
		updates["monthly_limit"] = *monthlyLimit
	}

	if len(updates) == 0 {
		return errors.New("no limits provided")
	}

	result := s.db_gorm.
		Model(&Account{}).
		Where("number = ?", accountNumber).
		Updates(updates)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("account not found")
	}

	return nil
}

func (s *Server) GetActiveAccountsByOwnerID(ownerID int64) ([]Account, error) {
	var accounts []Account
	result := s.db_gorm.Where(&Account{Owner: ownerID, Active: true}).
		Order("balance DESC").
		Find(&accounts)
	return accounts, result.Error
}

func (s *Server) GetAccountsForEmployee(firstName, lastName, accountNumber string) ([]Account, error) {
	var accounts []Account
	query := s.db_gorm.Model(&Account{})

	if accountNumber != "" {
		query = query.Where("number = ?", accountNumber)
	}

	if firstName != "" || lastName != "" {
		query = query.Joins("JOIN clients ON clients.id = accounts.owner")
		if firstName != "" {
			query = query.Where("clients.first_name ILIKE ?", firstName+"%")
		}
		if lastName != "" {
			query = query.Where("clients.last_name ILIKE ?", lastName+"%")
		}
	}

	result := query.Find(&accounts)
	return accounts, result.Error
}

func (s *Server) GetAccountByNumber(accNumber string) (*Account, error) {
	var acc Account
	result := s.db_gorm.Where(&Account{Number: accNumber}).First(&acc)
	if result.Error != nil {
		return nil, result.Error
	}
	return &acc, nil
}

func (s *Server) GetCompanyByOwnerID(ownerID int64) (*Company, error) {
	var company Company
	result := s.db_gorm.Where(&Company{Owner_id: ownerID}).First(&company)
	if result.Error != nil {
		return nil, result.Error
	}
	return &company, nil
}

func (s *Server) GetFilteredTransactions(accNumbers []string, accountNumber string, date string, amount int64, status string) ([]*bankpb.ClientTransaction, error) {
	var pbTransactions []*bankpb.ClientTransaction

	var payments []Payment
	payQuery := s.db_gorm.Model(&Payment{}).Where("from_account IN ? OR to_account IN ?", accNumbers, accNumbers)
	if accountNumber != "" {
		payQuery = payQuery.Where("from_account = ? OR to_account = ?", accountNumber, accountNumber)
	}
	if date != "" {
		payQuery = payQuery.Where("DATE(timestamp) = ?", date)
	}
	if amount > 0 {
		payQuery = payQuery.Where("start_amount = ?", amount)
	}
	if status != "" {
		payQuery = payQuery.Where("status = ?", status)
	}
	payQuery.Order("timestamp DESC").Find(&payments)

	for _, p := range payments {
		pbTransactions = append(pbTransactions, &bankpb.ClientTransaction{
			FromAccount:     p.From_account,
			ToAccount:       p.To_account,
			InitialAmount:   float64(p.Start_amount),
			FinalAmount:     float64(p.End_amount),
			Fee:             float64(p.Commission),
			PaymentCode:     fmt.Sprintf("%d", p.Transaction_code),
			ReferenceNumber: p.Call_number,
			Purpose:         p.Reason,
			Status:          p.Status,
			Timestamp:       p.Timestamp.Unix(),
		})
	}

	var transfers []Transfer
	transQuery := s.db_gorm.Model(&Transfer{}).Where("from_account IN ? OR to_account IN ?", accNumbers, accNumbers)
	if accountNumber != "" {
		transQuery = transQuery.Where("from_account = ? OR to_account = ?", accountNumber, accountNumber)
	}
	if date != "" {
		transQuery = transQuery.Where("DATE(timestamp) = ?", date)
	}
	if amount > 0 {
		transQuery = transQuery.Where("start_amount = ?", amount)
	}
	transQuery.Order("timestamp DESC").Find(&transfers)

	for _, t := range transfers {
		pbTransactions = append(pbTransactions, &bankpb.ClientTransaction{
			FromAccount:   t.From_account,
			ToAccount:     t.To_account,
			InitialAmount: float64(t.Start_amount),
			FinalAmount:   float64(t.End_amount),
			Fee:           float64(t.Commission),
			Status:        "realized",
			Timestamp:     t.Timestamp.Unix(),
		})
	}

	return pbTransactions, nil
}

// CreateAccountRecord handles the database transaction to insert a new account.
func (s *Server) CreateAccountRecord(account Account) (*Account, error) {
	if account.Valid_until.IsZero() {
		account.Valid_until = time.Now().AddDate(5, 0, 0)
	}
	account.Active = true
	account.Daily_expenditure = 0
	account.Monthly_expenditure = 0

	var dailyLimit, monthlyLimit sql.NullInt64
	if account.Daily_limit != 0 {
		dailyLimit = sql.NullInt64{Int64: account.Daily_limit, Valid: true}
	}
	if account.Monthly_limit != 0 {
		monthlyLimit = sql.NullInt64{Int64: account.Monthly_limit, Valid: true}
	}

	for range 5 {
		tx, err := s.database.Begin()
		if err != nil {
			return nil, fmt.Errorf("starting transaction: %w", err)
		}

		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`, account.Owner).Scan(&exists); err != nil || !exists {
			_ = tx.Rollback()
			return nil, ErrAccountOwnerNotFound
		}
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`, account.Created_by).Scan(&exists); err != nil || !exists {
			_ = tx.Rollback()
			return nil, ErrAccountCreatorNotFound
		}
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`, account.Currency).Scan(&exists); err != nil || !exists {
			_ = tx.Rollback()
			return nil, ErrAccountCurrencyNotFound
		}

		number, err := s.generateAccountNumber(tx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		account.Number = number

		row := tx.QueryRow(`
    INSERT INTO accounts (
     number, name, owner, company_id, balance, created_by, valid_until, currency, active,
     owner_type, account_type, maintainance_cost, daily_limit, monthly_limit,
     daily_expenditure, monthly_expenditure
    )
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
    RETURNING
     id, number, name, owner, balance, created_by, created_at, valid_until,
     currency, active, owner_type, account_type, maintainance_cost, daily_limit,
     monthly_limit, daily_expenditure, monthly_expenditure
   `,
			account.Number, account.Name, account.Owner, account.CompanyID, account.Balance, account.Created_by,
			account.Valid_until, account.Currency, account.Active, string(account.Owner_type),
			string(account.Account_type), account.Maintainance_cost, dailyLimit, monthlyLimit,
			account.Daily_expenditure, account.Monthly_expenditure,
		)

		created, err := s.scanCreateAccount(row)
		if err != nil {
			_ = tx.Rollback()
			if isUniqueViolation(err) {
				continue
			}
			return nil, fmt.Errorf("creating account: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("committing transaction: %w", err)
		}
		return created, nil
	}
	return nil, ErrAccountNumberGenerationFailed
}

func (s *Server) scanCreateAccount(row *sql.Row) (*Account, error) {
	var a Account
	var dailyLimit, monthlyLimit, dailyExp, monthlyExp sql.NullInt64

	err := row.Scan(
		&a.Id, &a.Number, &a.Name, &a.Owner, &a.Balance, &a.Created_by, &a.Created_at, &a.Valid_until,
		&a.Currency, &a.Active, &a.Owner_type, &a.Account_type, &a.Maintainance_cost, &dailyLimit,
		&monthlyLimit, &dailyExp, &monthlyExp,
	)
	if err != nil {
		return nil, err
	}

	a.Daily_limit = dailyLimit.Int64
	a.Monthly_limit = monthlyLimit.Int64
	a.Daily_expenditure = dailyExp.Int64
	a.Monthly_expenditure = monthlyExp.Int64

	return &a, nil
}

func (s *Server) generateAccountNumber(tx *sql.Tx) (string, error) {
	for range 5 {
		number, _ := randomDigits(20)
		var exists bool
		err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts WHERE number = $1)`, number).Scan(&exists)
		if err == nil && !exists {
			return number, nil
		}
	}
	return "", ErrAccountNumberGenerationFailed
}

func randomDigits(length int) (string, error) {
	var builder strings.Builder
	builder.Grow(length)
	for i := 0; i < length; i++ {
		digit, err := cryptorand.Int(cryptorand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		builder.WriteByte(byte('0' + digit.Int64()))
	}
	return builder.String(), nil
}
