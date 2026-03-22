package bank

import (
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var ErrAccountOwnerNotFound = errors.New("account owner not found")
var ErrAccountCreatorNotFound = errors.New("account creator not found")
var ErrAccountCurrencyNotFound = errors.New("account currency not found")
var ErrAccountNumberGenerationFailed = errors.New("account number generation failed")

func (s *Server) GetAccountByNumberRecord(number string) (*Account, error) {
	var acc Account
	err := s.database.QueryRow(`
        SELECT id, number, name, owner, balance, currency, active, owner_type, account_type,
               maintainance_cost, daily_limit, monthly_limit, daily_expenditure, monthly_expenditure,
               created_by, created_at, valid_until
        FROM accounts WHERE number = $1
    `, number).Scan(
		&acc.Id, &acc.Number, &acc.Name, &acc.Owner, &acc.Balance, &acc.Currency, &acc.Active, &acc.Owner_type, &acc.Account_type,
		&acc.Maintainance_cost, &acc.Daily_limit, &acc.Monthly_limit, &acc.Daily_expenditure, &acc.Monthly_expenditure,
		&acc.Created_by, &acc.Created_at, &acc.Valid_until,
	)
	if err == sql.ErrNoRows {
		return nil, errors.New("account not found")
	}
	return &acc, err
}

func scanAccount(scanner interface {
	Scan(dest ...any) error
}) (*Account, error) {
	var account Account
	var ownerType string
	var accountType string
	var dailyLimit sql.NullInt64
	var monthlyLimit sql.NullInt64
	var dailyExpenditure sql.NullInt64
	var monthlyExpenditure sql.NullInt64

	err := scanner.Scan(
		&account.Id,
		&account.Number,
		&account.Name,
		&account.Owner,
		&account.Balance,
		&account.Created_by,
		&account.Created_at,
		&account.Valid_until,
		&account.Currency,
		&account.Active,
		&ownerType,
		&accountType,
		&account.Maintainance_cost,
		&dailyLimit,
		&monthlyLimit,
		&dailyExpenditure,
		&monthlyExpenditure,
	)
	if err != nil {
		return nil, err
	}

	account.Owner_type = owner_type(ownerType)
	account.Account_type = account_type(accountType)
	if dailyLimit.Valid {
		account.Daily_limit = dailyLimit.Int64
	}
	if monthlyLimit.Valid {
		account.Monthly_limit = monthlyLimit.Int64
	}
	if dailyExpenditure.Valid {
		account.Daily_expenditure = dailyExpenditure.Int64
	}
	if monthlyExpenditure.Valid {
		account.Monthly_expenditure = monthlyExpenditure.Int64
	}

	return &account, nil
}

func (s *Server) CreateAccountRecord(account Account) (*Account, error) {
	if account.Valid_until.IsZero() {
		account.Valid_until = time.Now().AddDate(3, 0, 0)
	}
	account.Balance = 0
	account.Active = false
	account.Daily_expenditure = 0
	account.Monthly_expenditure = 0

	var dailyLimit any
	if account.Daily_limit != 0 {
		dailyLimit = account.Daily_limit
	}

	var monthlyLimit any
	if account.Monthly_limit != 0 {
		monthlyLimit = account.Monthly_limit
	}

	for range 5 {
		tx, err := s.database.Begin()
		if err != nil {
			return nil, fmt.Errorf("starting transaction: %w", err)
		}

		var ownerExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`, account.Owner).Scan(&ownerExists); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("checking account owner existence: %w", err)
		}
		if !ownerExists {
			_ = tx.Rollback()
			return nil, ErrAccountOwnerNotFound
		}

		var creatorExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`, account.Created_by).Scan(&creatorExists); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("checking account creator existence: %w", err)
		}
		if !creatorExists {
			_ = tx.Rollback()
			return nil, ErrAccountCreatorNotFound
		}

		var currencyExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`, account.Currency).Scan(&currencyExists); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("checking currency existence: %w", err)
		}
		if !currencyExists {
			_ = tx.Rollback()
			return nil, ErrAccountCurrencyNotFound
		}

		number, err := s.generateAccountNumber(tx)
		if err != nil {
			_ = tx.Rollback()
			if errors.Is(err, ErrAccountNumberGenerationFailed) {
				return nil, err
			}
			return nil, fmt.Errorf("generating account number: %w", err)
		}
		account.Number = number

		row := tx.QueryRow(`
            INSERT INTO accounts (
                number, name, owner, balance, created_by, valid_until, currency, active,
                owner_type, account_type, maintainance_cost, daily_limit, monthly_limit,
                daily_expenditure, monthly_expenditure
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
            RETURNING id, number, name, owner, balance, created_by, created_at, valid_until,
                currency, active, owner_type, account_type, maintainance_cost, daily_limit,
                monthly_limit, daily_expenditure, monthly_expenditure
        `, account.Number, account.Name, account.Owner, account.Balance, account.Created_by,
			account.Valid_until, account.Currency, account.Active, string(account.Owner_type),
			string(account.Account_type), account.Maintainance_cost, dailyLimit, monthlyLimit,
			account.Daily_expenditure, account.Monthly_expenditure)

		created, err := scanAccount(row)
		if err != nil {
			if isUniqueViolation(err) {
				_ = tx.Rollback()
				continue
			}
			_ = tx.Rollback()
			return nil, fmt.Errorf("creating account: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("committing transaction: %w", err)
		}

		return created, nil
	}

	return nil, ErrAccountNumberGenerationFailed
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

func (s *Server) accountNumberExists(tx *sql.Tx, number string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts WHERE number = $1)`, number).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking account number existence: %w", err)
	}
	return exists, nil
}

func (s *Server) generateAccountNumber(tx *sql.Tx) (string, error) {
	for range 5 {
		number, err := randomDigits(20)
		if err != nil {
			return "", fmt.Errorf("generating account number digits: %w", err)
		}

		exists, err := s.accountNumberExists(tx, number)
		if err != nil {
			return "", err
		}
		if !exists {
			return number, nil
		}
	}

	return "", ErrAccountNumberGenerationFailed
}
