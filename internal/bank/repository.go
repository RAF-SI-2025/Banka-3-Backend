package bank

import (
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrCompanyNotFound = errors.New("company not found")
var ErrCompanyRegisteredIDExists = errors.New("company with registered id already exists")
var ErrCompanyOwnerNotFound = errors.New("company owner not found")
var ErrCompanyActivityCodeNotFound = errors.New("company activity code not found")

var ErrAccountOwnerNotFound = errors.New("account owner not found")
var ErrAccountCreatorNotFound = errors.New("account creator not found")
var ErrAccountCurrencyNotFound = errors.New("account currency not found")
var ErrAccountNumberGenerationFailed = errors.New("account number generation failed")

func scanCompany(scanner interface {
	Scan(dest ...any) error
}) (*Company, error) {
	var company Company
	var activityCodeID sql.NullInt64
	err := scanner.Scan(
		&company.Id,
		&company.Registered_id,
		&company.Name,
		&company.Tax_code,
		&activityCodeID,
		&company.Address,
		&company.Owner_id,
	)
	if err != nil {
		return nil, err
	}
	if activityCodeID.Valid {
		company.Activity_code_id = activityCodeID.Int64
	}
	return &company, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Server) CreateCompanyRecord(company Company) (*Company, error) {
	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var ownerExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`, company.Owner_id).Scan(&ownerExists); err != nil {
		return nil, fmt.Errorf("checking owner existence: %w", err)
	}
	if !ownerExists {
		return nil, ErrCompanyOwnerNotFound
	}

	if company.Activity_code_id != 0 {
		var activityCodeExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM activity_codes WHERE id = $1)`, company.Activity_code_id).Scan(&activityCodeExists); err != nil {
			return nil, fmt.Errorf("checking activity code existence: %w", err)
		}
		if !activityCodeExists {
			return nil, ErrCompanyActivityCodeNotFound
		}
	}

	var row *sql.Row
	if company.Activity_code_id == 0 {
		row = tx.QueryRow(`
			INSERT INTO companies (registered_id, name, tax_code, activity_code_id, address, owner_id)
			VALUES ($1, $2, $3, NULL, $4, $5)
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Registered_id, company.Name, company.Tax_code, company.Address, company.Owner_id)
	} else {
		row = tx.QueryRow(`
			INSERT INTO companies (registered_id, name, tax_code, activity_code_id, address, owner_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Registered_id, company.Name, company.Tax_code, company.Activity_code_id, company.Address, company.Owner_id)
	}

	created, err := scanCompany(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCompanyRegisteredIDExists
		}
		return nil, fmt.Errorf("creating company: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return created, nil
}

func (s *Server) GetCompanyByIDRecord(companyID int64) (*Company, error) {
	row := s.database.QueryRow(`
		SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id
		FROM companies
		WHERE id = $1
	`, companyID)

	company, err := scanCompany(row)
	if err == sql.ErrNoRows {
		return nil, ErrCompanyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting company by id: %w", err)
	}

	return company, nil
}

func (s *Server) GetCompaniesRecords() ([]*Company, error) {
	rows, err := s.database.Query(`
		SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id
		FROM companies
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var companies []*Company
	for rows.Next() {
		company, err := scanCompany(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning company: %w", err)
		}
		companies = append(companies, company)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating companies: %w", err)
	}

	return companies, nil
}

func (s *Server) UpdateCompanyRecord(company Company) (*Company, error) {
	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var companyExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`, company.Id).Scan(&companyExists); err != nil {
		return nil, fmt.Errorf("checking company existence: %w", err)
	}
	if !companyExists {
		return nil, ErrCompanyNotFound
	}

	var ownerExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`, company.Owner_id).Scan(&ownerExists); err != nil {
		return nil, fmt.Errorf("checking owner existence: %w", err)
	}
	if !ownerExists {
		return nil, ErrCompanyOwnerNotFound
	}

	if company.Activity_code_id != 0 {
		var activityCodeExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM activity_codes WHERE id = $1)`, company.Activity_code_id).Scan(&activityCodeExists); err != nil {
			return nil, fmt.Errorf("checking activity code existence: %w", err)
		}
		if !activityCodeExists {
			return nil, ErrCompanyActivityCodeNotFound
		}
	}

	var row *sql.Row
	if company.Activity_code_id == 0 {
		row = tx.QueryRow(`
			UPDATE companies
			SET name = $1, activity_code_id = NULL, address = $2, owner_id = $3
			WHERE id = $4
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Name, company.Address, company.Owner_id, company.Id)
	} else {
		row = tx.QueryRow(`
			UPDATE companies
			SET name = $1, activity_code_id = $2, address = $3, owner_id = $4
			WHERE id = $5
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Name, company.Activity_code_id, company.Address, company.Owner_id, company.Id)
	}

	updated, err := scanCompany(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCompanyNotFound
		}
		return nil, fmt.Errorf("updating company: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return updated, nil
}

func scanCard(scanner interface{ Scan(dest ...any) error }) (*Card, error) {
	var card Card
	err := scanner.Scan(
		&card.Id,
		&card.Number,
		&card.Type,
		&card.Brand,
		&card.Creation_date,
		&card.Valid_until,
		&card.Account_number,
		&card.Cvv,
		&card.Card_limit,
		&card.Status,
	)
	if err != nil {
		return nil, err
	}
	return &card, nil
}

func scanCardRequest(scanner interface{ Scan(dest ...any) error }) (*CardRequest, error) {
	var req CardRequest
	err := scanner.Scan(
		&req.Id,
		&req.Account_number,
		&req.Type,
		&req.Brand,
		&req.Token,
		&req.ExpirationDate,
		&req.Complete,
		&req.Email,
	)
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (s *Server) CreateCardRecord(card Card) (*Card, error) {
	row := s.database.QueryRow(`
		INSERT INTO cards (number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP, $4, $5, $6, $7, $8)
		RETURNING id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
	`, card.Number, card.Type, card.Brand, card.Valid_until, card.Account_number, card.Cvv, card.Card_limit, card.Status)
	return scanCard(row)
}

func (s *Server) GetCardsRecords() ([]*Card, error) {
	rows, err := s.database.Query(`
		SELECT id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
		FROM cards
	`)
	if err != nil {
		return nil, fmt.Errorf("listing cards: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Printf("[ERROR] closing rows: %v", err)
		}
	}(rows)

	var cards []*Card
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func (s *Server) BlockCardRecord(cardID int64) error {
	res, err := s.database.Exec(`UPDATE cards SET status = $1 WHERE id = $2`, Blocked, cardID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("card not found")
	}
	return nil
}

func (s *Server) CreateCardRequestRecord(req CardRequest) (*CardRequest, error) {
	row := s.database.QueryRow(`
		INSERT INTO card_requests (account_number, type, brand, token, expiration_date, complete, email)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, account_number, type, brand, token, expiration_date, complete, email
	`, req.Account_number, req.Type, req.Brand, req.Token, req.ExpirationDate, req.Complete, req.Email)
	return scanCardRequest(row)
}

func (s *Server) GetCardRequestByToken(token string) (*CardRequest, error) {
	row := s.database.QueryRow(`
		SELECT id, account_number, type, brand, token, expiration_date, complete, email
		FROM card_requests
		WHERE token = $1 AND complete = false
	`, token)
	return scanCardRequest(row)
}

func (s *Server) MarkCardRequestFulfilled(id int64) error {
	_, err := s.database.Exec(`UPDATE card_requests SET complete = true WHERE id = $1`, id)
	return err
}

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

func (s *Server) CountActiveCardsByAccountNumber(accountNumber string) (int, error) {
	var count int
	err := s.database.QueryRow(`
		SELECT COUNT(*) FROM cards
		WHERE account_number = $1 AND status != $2
	`, accountNumber, Deactivated).Scan(&count)
	return count, err
}

func (s *Server) IsAuthorizedParty(email string, accountNumber string) (bool, error) {
	var exists bool
	err := s.database.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM authorized_party ap
			WHERE ap.email = $1 AND EXISTS (
				SELECT 1 FROM accounts a WHERE a.number = $2
			)
		)
	`, email, accountNumber).Scan(&exists)
	return exists, err
}

func (s *Server) GetCardByNumberRecord(cardNumber string) (*Card, error) {
	row := s.database.QueryRow(`
		SELECT id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
		FROM cards WHERE number = $1
	`, cardNumber)
	return scanCard(row)
}

func (s *Server) GetCardByIDRecord(id int64) (*Card, error) {
	row := s.database.QueryRow(`
		SELECT id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
		FROM cards WHERE id = $1
	`, id)
	return scanCard(row)
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

type loanView struct {
	LoanNumber            string  `gorm:"column:loan_number"`
	LoanType              string  `gorm:"column:loan_type"`
	AccountNumber         string  `gorm:"column:account_number"`
	LoanAmount            float64 `gorm:"column:loan_amount"`
	RepaymentPeriod       int32   `gorm:"column:repayment_period"`
	NominalRate           float64 `gorm:"column:nominal_rate"`
	EffectiveRate         float64 `gorm:"column:effective_rate"`
	AgreementDate         string  `gorm:"column:agreement_date"`
	MaturityDate          string  `gorm:"column:maturity_date"`
	NextInstallmentAmount float64 `gorm:"column:next_installment_amount"`
	NextInstallmentDate   string  `gorm:"column:next_installment_date"`
	RemainingDebt         float64 `gorm:"column:remaining_debt"`
	Currency              string  `gorm:"column:currency"`
	Status                string  `gorm:"column:status"`
}

func (s *Server) getOwnedAccountByNumber(clientEmail string, accountNumber string) (*Account, error) {
	var account Account

	err := s.db_gorm.
		Model(&Account{}).
		Joins("JOIN clients ON clients.id = accounts.owner").
		Where("clients.email = ? AND accounts.number = ?", clientEmail, accountNumber).
		First(&account).Error
	if err != nil {
		return nil, err
	}

	return &account, nil
}

func (s *Server) getCurrencyByLabel(label string) (*Currency, error) {
	var currency Currency

	err := s.db_gorm.
		Model(&Currency{}).
		Where("label = ?", label).
		First(&currency).Error
	if err != nil {
		return nil, err
	}

	return &currency, nil
}

func (s *Server) getLoansForClient(clientEmail string, loanType string, accountNumber string, loanStatus string) ([]loanView, error) {
	var loans []loanView

	query := s.db_gorm.
		Model(&Loan{}).
		Joins("JOIN accounts ON accounts.id = loans.account_id").
		Joins("JOIN clients ON clients.id = accounts.owner").
		Joins("JOIN currencies ON currencies.id = loans.currency_id").
		Where("clients.email = ?", clientEmail).
		Select(`
			CAST(loans.id AS text) AS loan_number,
			loans.type::text AS loan_type,
			accounts.number AS account_number,
			loans.amount AS loan_amount,
			loans.installments AS repayment_period,
			loans.interest_rate AS nominal_rate,
			0 AS effective_rate,
			TO_CHAR(loans.date_signed, 'YYYY-MM-DD') AS agreement_date,
			TO_CHAR(loans.date_end, 'YYYY-MM-DD') AS maturity_date,
			loans.monthly_payment AS next_installment_amount,
			TO_CHAR(loans.next_payment_due, 'YYYY-MM-DD') AS next_installment_date,
			loans.remaining_debt AS remaining_debt,
			currencies.label AS currency,
			loans.loan_status::text AS status
		`)

	if loanType != "" {
		query = query.Where("loans.type = ?", loanType)
	}

	if accountNumber != "" {
		query = query.Where("accounts.number = ?", accountNumber)
	}

	if loanStatus != "" {
		query = query.Where("loans.loan_status = ?", loanStatus)
	}

	err := query.
		Order("loans.id DESC").
		Scan(&loans).Error
	if err != nil {
		return nil, err
	}

	return loans, nil
}

func (s *Server) getLoanByIDForClient(clientEmail string, loanID int64) (*loanView, error) {
	var loan loanView

	err := s.db_gorm.
		Model(&Loan{}).
		Joins("JOIN accounts ON accounts.id = loans.account_id").
		Joins("JOIN clients ON clients.id = accounts.owner").
		Joins("JOIN currencies ON currencies.id = loans.currency_id").
		Where("clients.email = ? AND loans.id = ?", clientEmail, loanID).
		Select(`
			CAST(loans.id AS text) AS loan_number,
			loans.type::text AS loan_type,
			accounts.number AS account_number,
			loans.amount AS loan_amount,
			loans.installments AS repayment_period,
			loans.interest_rate AS nominal_rate,
			0 AS effective_rate,
			TO_CHAR(loans.date_signed, 'YYYY-MM-DD') AS agreement_date,
			TO_CHAR(loans.date_end, 'YYYY-MM-DD') AS maturity_date,
			loans.monthly_payment AS next_installment_amount,
			TO_CHAR(loans.next_payment_due, 'YYYY-MM-DD') AS next_installment_date,
			loans.remaining_debt AS remaining_debt,
			currencies.label AS currency,
			loans.loan_status::text AS status
		`).
		Take(&loan).Error
	if err != nil {
		return nil, err
	}

	return &loan, nil
}

func (s *Server) createLoanRequest(req *LoanRequest) error {
	return s.db_gorm.Create(req).Error
}

type loanRequestView struct {
	Id               int64   `gorm:"column:id"`
	LoanType         string  `gorm:"column:loan_type"`
	Amount           float64 `gorm:"column:amount"`
	Currency         string  `gorm:"column:currency"`
	Purpose          string  `gorm:"column:purpose"`
	Salary           float64 `gorm:"column:salary"`
	EmploymentStatus string  `gorm:"column:employment_status"`
	EmploymentPeriod int64   `gorm:"column:employment_period"`
	PhoneNumber      string  `gorm:"column:phone_number"`
	RepaymentPeriod  int32   `gorm:"column:repayment_period"`
	AccountNumber    string  `gorm:"column:account_number"`
	Status           string  `gorm:"column:status"`
	InterestRateType string  `gorm:"column:interest_rate_type"`
	SubmissionDate   string  `gorm:"column:submission_date"`
}

func (s *Server) getLoanRequests(loanType, accountNumber string) ([]loanRequestView, error) {
	var requests []loanRequestView

	query := s.db_gorm.
		Model(&LoanRequest{}).
		Joins("JOIN accounts ON accounts.id = loan_request.account_id").
		Joins("JOIN currencies ON currencies.id = loan_request.currency_id").
		Select(`
			loan_request.id,
			loan_request.type::text AS loan_type,
			loan_request.amount,
			currencies.label AS currency,
			COALESCE(loan_request.purpose, '') AS purpose,
			COALESCE(loan_request.salary, 0) AS salary,
			COALESCE(loan_request.employment_status::text, '') AS employment_status,
			COALESCE(loan_request.employment_period, 0) AS employment_period,
			COALESCE(loan_request.phone_number, '') AS phone_number,
			loan_request.repayment_period,
			accounts.number AS account_number,
			loan_request.status::text AS status,
			loan_request.interest_rate_type::text AS interest_rate_type,
			TO_CHAR(loan_request.submission_date, 'YYYY-MM-DD"T"HH24:MI:SS') AS submission_date
		`)

	if loanType != "" {
		query = query.Where("loan_request.type = ?", loanType)
	}

	if accountNumber != "" {
		query = query.Where("accounts.number = ?", accountNumber)
	}

	err := query.
		Order("loan_request.submission_date DESC").
		Scan(&requests).Error
	if err != nil {
		return nil, err
	}

	return requests, nil
}

func (s *Server) getLoanRequestByID(id int64) (*LoanRequest, error) {
	var req LoanRequest
	err := s.db_gorm.First(&req, id).Error
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (s *Server) updateLoanRequestStatus(id int64, newStatus loan_request_status) error {
	return s.db_gorm.Model(&LoanRequest{}).Where("id = ?", id).Update("status", newStatus).Error
}

func (s *Server) getAllLoans(loanType, accountNumber, loanStatus string) ([]loanView, error) {
	var loans []loanView

	query := s.db_gorm.
		Model(&Loan{}).
		Joins("JOIN accounts ON accounts.id = loans.account_id").
		Joins("JOIN currencies ON currencies.id = loans.currency_id").
		Select(`
			CAST(loans.id AS text) AS loan_number,
			loans.type::text AS loan_type,
			accounts.number AS account_number,
			loans.amount AS loan_amount,
			loans.installments AS repayment_period,
			loans.interest_rate AS nominal_rate,
			0 AS effective_rate,
			TO_CHAR(loans.date_signed, 'YYYY-MM-DD') AS agreement_date,
			TO_CHAR(loans.date_end, 'YYYY-MM-DD') AS maturity_date,
			loans.monthly_payment AS next_installment_amount,
			TO_CHAR(loans.next_payment_due, 'YYYY-MM-DD') AS next_installment_date,
			loans.remaining_debt AS remaining_debt,
			currencies.label AS currency,
			loans.loan_status::text AS status
		`)

	if loanType != "" {
		query = query.Where("loans.type = ?", loanType)
	}

	if accountNumber != "" {
		query = query.Where("accounts.number = ?", accountNumber)
	}

	if loanStatus != "" {
		query = query.Where("loans.loan_status = ?", loanStatus)
	}

	err := query.
		Order("accounts.number").
		Scan(&loans).Error
	if err != nil {
		return nil, err
	}

	return loans, nil
}
