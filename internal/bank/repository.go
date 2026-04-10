package bank

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
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

var ErrAccountNotFound = errors.New("account not found")
var ErrInsufficientFunds = errors.New("insufficient funds")
var ErrLimitExceeded = errors.New("limit exceeded")

const commission_rate = 0.01

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

func scanPayment(scanner interface {
	Scan(dest ...any) error
}) (*Payment, error) {
	var payment Payment
	err := scanner.Scan(
		&payment.Transaction_id,
		&payment.From_account,
		&payment.To_account,
		&payment.Start_amount,
		&payment.End_amount,
		&payment.Commission,
		&payment.Status,
		&payment.Recipient_id,
		&payment.Transaction_code,
		&payment.Call_number,
		&payment.Reason,
		&payment.Timestamp,
	)
	if err != nil {
		return nil, err
	}
	return &payment, nil
}
func scanTransfer(scanner interface {
	Scan(dest ...any) error
}) (*Transfer, error) {
	var transfer Transfer
	var exchangeRate sql.NullFloat64
	err := scanner.Scan(
		&transfer.Transaction_id,
		&transfer.From_account,
		&transfer.To_account,
		&transfer.Start_amount,
		&transfer.End_amount,
		&transfer.Start_currency_id,
		&exchangeRate,
		&transfer.Commission,
		&transfer.Status,
		&transfer.Timestamp,
	)
	if err != nil {
		log.Println("error when scanning transfer: ", err)
		return nil, err
	}
	if exchangeRate.Valid {
		transfer.Exchange_rate = exchangeRate.Float64
	}
	return &transfer, nil
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

func (s *Server) GetCompanyByTaxCode(taxCode int64) (*Company, error) {
	row := s.database.QueryRow(`
        SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id
        FROM companies
        WHERE tax_code = $1
    `, taxCode)

	company, err := scanCompany(row)
	if err == sql.ErrNoRows {
		return nil, ErrCompanyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting company by tax code: %w", err)
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

func (s *Server) GetAccountIDByCardID(cardID int64) (int64, error) {
	var accountID int64

	err := s.db_gorm.
		Model(&Card{}).
		Select("accounts.id").
		Joins("JOIN accounts ON accounts.number = cards.account_number").
		Where("cards.id = ?", cardID).
		Scan(&accountID).Error

	if err != nil {
		return 0, err
	}

	if accountID == 0 {
		return 0, errors.New("account not found")
	}

	return accountID, nil
}

func (s *Server) GetCardStatus(cardID int64) (Card_status, error) {
	var status string

	err := s.database.QueryRow(`SELECT status FROM cards WHERE id = $1`, cardID).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", errors.New("card not found")
		}
		return "", err
	}

	return Card_status(status), nil
}

func (s *Server) UpdateCardStatus(cardID int64, status Card_status) error {
	res, err := s.database.Exec(`UPDATE cards SET status = $1 WHERE id = $2`, status, cardID)
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
		WHERE account_number = $1
	`, accountNumber).Scan(&count)
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

func (s *Server) GetEmployeeIDByEmail(email string) (int64, error) {
	type empRow struct {
		Id int64 `gorm:"column:id"`
	}
	var emp empRow

	err := s.db_gorm.
		Table("employees").
		Select("id").
		Where("email = ?", email).
		Take(&emp).Error
	if err != nil {
		return 0, err
	}
	return emp.Id, nil
}

func (s *Server) IsEmployeeByEmail(email string) (bool, error) {
	var count int64

	err := s.db_gorm.
		Table("employees").
		Where("email = ?", email).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Server) GetClientIDByEmail(email string) (int64, error) {
	type clientRow struct {
		Id int64 `gorm:"column:id"`
	}

	var client clientRow

	err := s.db_gorm.
		Table("clients").
		Select("id").
		Where("email = ?", email).
		Take(&client).Error
	if err != nil {
		return 0, err
	}

	return client.Id, nil
}

func (s *Server) GetCardsByOwnerID(ownerID int64) ([]Card, error) {
	var cards []Card

	err := s.db_gorm.
		Model(&Card{}).
		Joins("JOIN accounts ON accounts.number = cards.account_number").
		Where("accounts.owner = ?", ownerID).
		Order("cards.id DESC").
		Find(&cards).Error
	if err != nil {
		return nil, err
	}

	return cards, nil
}

func (s *Server) GetCardsForEmployee() ([]Card, error) {
	var cards []Card

	err := s.db_gorm.
		Model(&Card{}).
		Order("id DESC").
		Find(&cards).Error
	if err != nil {
		return nil, err
	}

	return cards, nil
}

type loanView struct {
	LoanNumber            string  `gorm:"column:loan_number"`
	LoanType              string  `gorm:"column:loan_type"`
	AccountNumber         string  `gorm:"column:account_number"`
	LoanAmount            int64   `gorm:"column:loan_amount"`
	RepaymentPeriod       int64   `gorm:"column:repayment_period"`
	NominalRate           float64 `gorm:"column:nominal_rate"`
	EffectiveRate         float64 `gorm:"column:effective_rate"`
	AgreementDate         string  `gorm:"column:agreement_date"`
	MaturityDate          string  `gorm:"column:maturity_date"`
	NextInstallmentAmount int64   `gorm:"column:next_installment_amount"`
	NextInstallmentDate   string  `gorm:"column:next_installment_date"`
	RemainingDebt         int64   `gorm:"column:remaining_debt"`
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
			loans.nominal_rate AS nominal_rate,
			(POWER(1 + loans.interest_rate / 100.0 / 12.0, 12) - 1) * 100 AS effective_rate,
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
		Order("loans.amount DESC").
		Scan(&loans).Error
	if err != nil {
		log.Printf("[getLoansForClient] ERROR executing query for client %s: %v", clientEmail, err)
		return nil, err
	}

	log.Printf("[getLoansForClient] SUCCESS: Retrieved %d loans for client %s", len(loans), clientEmail)
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
			loans.nominal_rate AS nominal_rate,
			(POWER(1 + loans.interest_rate / 100.0 / 12.0, 12) - 1) * 100 AS effective_rate,
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

func (s *Server) getLoanByID(loanID int64) (*loanView, error) {
	var loan loanView

	err := s.db_gorm.
		Model(&Loan{}).
		Joins("JOIN accounts ON accounts.id = loans.account_id").
		Joins("JOIN currencies ON currencies.id = loans.currency_id").
		Where("loans.id = ?", loanID).
		Select(`
			CAST(loans.id AS text) AS loan_number,
			loans.type::text AS loan_type,
			accounts.number AS account_number,
			loans.amount AS loan_amount,
			loans.installments AS repayment_period,
			loans.nominal_rate AS nominal_rate,
			(POWER(1 + loans.interest_rate / 100.0 / 12.0, 12) - 1) * 100 AS effective_rate,
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

func (s *Server) IncreaseAccountBalance(tx *sql.Tx, number string, amount int64) (*Account, error) {
	res, err := tx.Exec(
		"UPDATE accounts SET balance = balance + $1 WHERE number = $2",
		amount, number,
	)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, fmt.Errorf("account not found")
	}
	return s.GetAccountByNumberRecord(number)
}

func (s *Server) DecreaseAccountBalance(tx *sql.Tx, number string, amount int64) (*Account, error) {
	//everything is in one query to make sure
	//COALESCE in case expenditures are null, if so, use 0
	res := tx.QueryRow(`
		UPDATE accounts
		SET
			balance = balance - $2,
			daily_expenditure = COALESCE(daily_expenditure, 0) + $2,
			monthly_expenditure = COALESCE(monthly_expenditure, 0) + $2
		WHERE
			number = $1
			AND balance >= $2
			AND (COALESCE(daily_expenditure, 0) + $2) <= daily_limit
			AND (COALESCE(monthly_expenditure, 0) + $2) <= monthly_limit
		RETURNING number
	`, number, amount)
	//Did we get account from this query?
	//If so, return it
	var account string
	err := res.Scan(&account)
	if err == nil {
		return s.GetAccountByNumberRecord(number)
	}

	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("update failed: %w", err)
	}

	//If error occurred, we need to diagnose it
	//Running a query that will return data so we can check what conditions
	//weren't met
	var balance, dailyExp, monthlyExp, dailyLimit, monthlyLimit int64
	err = tx.QueryRow(`
		SELECT balance,
		       COALESCE(daily_expenditure, 0),
		       COALESCE(monthly_expenditure, 0),
		       daily_limit,
		       monthly_limit
		FROM accounts
		WHERE number = $1
	`, number).Scan(&balance, &dailyExp, &monthlyExp, &dailyLimit, &monthlyLimit)

	if err == sql.ErrNoRows {
		return nil, ErrAccountNotFound
	}

	if balance < amount {
		return nil, ErrInsufficientFunds
	}

	if dailyExp+amount > dailyLimit || monthlyExp+amount > monthlyLimit {
		return nil, ErrLimitExceeded
	}

	return nil, fmt.Errorf("unknown failure")
}
func (s *Server) CreatePayment(tx *sql.Tx, from_account string, to_account string, start_amount int64,
	end_amount int64, commission int64, transaction_code int64, call_number string,
	reason string) (*Payment, error) {
	recipient_id, err := s.getOwnerFromAccount(tx, to_account)
	if err != nil {
		return nil, fmt.Errorf("get owner from account failed: %w", err)
	}
	row := tx.QueryRow(`
		INSERT INTO payments (
			from_account, to_account, start_amount, end_amount,
			commission,status, recipient_id, transaction_code,
			call_number, reason, timestamp
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,CURRENT_TIMESTAMP)
		RETURNING transaction_id, from_account, to_account,
		          start_amount, end_amount, commission,status,
		          recipient_id, transaction_code,
		          call_number, reason, timestamp
	`,
		from_account,
		to_account,
		start_amount,
		end_amount,
		commission,
		"realized",
		recipient_id,
		transaction_code,
		call_number,
		reason,
	)

	payment, err := scanPayment(row)
	if err != nil {
		return nil, fmt.Errorf("scan payment: %w", err)
	}

	return payment, nil
}

func (s *Server) getOwnerFromAccount(tx *sql.Tx, account string) (int64, error) {
	var ownerID int64

	err := tx.QueryRow(
		`SELECT owner FROM accounts WHERE number = $1`,
		account,
	).Scan(&ownerID)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("account not found")
		}
		return 0, fmt.Errorf("query owner: %w", err)
	}

	return ownerID, nil
}

func (s *Server) GetTransferHistory(clientEmail string, page, pageSize int32) (*bankpb.TransferHistoryResponse, error) {

	offset := (page - 1) * pageSize

	rows, err := s.database.Query(`
		SELECT t.transaction_id, t.from_account, t.to_account,
		       t.start_amount, t.end_amount,
		       t.start_currency_id, t.exchange_rate,
		       t.commission, t.status, t.timestamp
		FROM transfers t
		JOIN accounts a ON t.from_account = a.number OR t.to_account = a.number
		JOIN clients c ON a.owner = c.id
		WHERE c.email = $1
		ORDER BY t.timestamp DESC
		LIMIT $2 OFFSET $3
	`, clientEmail, pageSize, offset)

	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Println("bank/server.go row close failed (GetTransferHistory)")
			log.Println("rows close failed:", err)
		}
	}()

	var history []*bankpb.TransferResponse

	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}

		history = append(history, &bankpb.TransferResponse{
			FromAccount:     t.From_account,
			ToAccount:       t.To_account,
			InitialAmount:   t.Start_amount,
			FinalAmount:     t.End_amount,
			Fee:             t.Commission,
			Currency:        "",
			PaymentCode:     "",
			ReferenceNumber: "",
			Purpose:         "",
			Status:          string(t.Status),
			Timestamp:       t.Timestamp.Format(time.RFC3339),
		})
	}

	if err := rows.Err(); err != nil {
		log.Println("bank/server.go got some error in rows (GetTransferHistory)")
		return nil, err
	}

	return &bankpb.TransferHistoryResponse{
		History: history,
	}, nil
}

type loanRequestView struct {
	Id               int64  `gorm:"column:id"`
	LoanType         string `gorm:"column:loan_type"`
	Amount           int64  `gorm:"column:amount"`
	Currency         string `gorm:"column:currency"`
	Purpose          string `gorm:"column:purpose"`
	Salary           int64  `gorm:"column:salary"`
	EmploymentStatus string `gorm:"column:employment_status"`
	EmploymentPeriod int64  `gorm:"column:employment_period"`
	PhoneNumber      string `gorm:"column:phone_number"`
	RepaymentPeriod  int64  `gorm:"column:repayment_period"`
	AccountNumber    string `gorm:"column:account_number"`
	Status           string `gorm:"column:status"`
	InterestRateType string `gorm:"column:interest_rate_type"`
	SubmissionDate   string `gorm:"column:submission_date"`
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

func (s *Server) getExchangeRateToRSD(currencyLabel string) (float64, error) {
	if currencyLabel == "RSD" {
		return 1.0, nil
	}
	var rate ExchangeRate
	if err := s.db_gorm.Where("currency_code = ?", currencyLabel).First(&rate).Error; err != nil {
		return 0, err
	}
	return rate.Rate_to_rsd, nil
}

func (s *Server) getApprovedVariableLoans() ([]Loan, error) {
	var loans []Loan
	err := s.db_gorm.
		Where("interest_rate_type = ? AND loan_status = ?", Variable, Approved).
		Find(&loans).Error
	return loans, err
}

func (s *Server) getLoansDueForCollection(today time.Time) ([]Loan, error) {
	var loans []Loan
	err := s.db_gorm.
		Where("next_payment_due <= ? AND loan_status IN ?", today, []loan_status{Approved, Late}).
		Find(&loans).Error
	return loans, err
}

func (s *Server) countPaidInstallments(loanID int64) int {
	var count int64
	s.db_gorm.Model(&LoanInstallment{}).
		Where("loan_id = ? AND status = ?", loanID, Installment_Paid).
		Count(&count)
	return int(count)
}

func (s *Server) getCurrencyLabelByID(id int64) (string, error) {
	var currency Currency
	if err := s.db_gorm.First(&currency, id).Error; err != nil {
		return "", err
	}
	return currency.Label, nil
}

// TODO: Mozda bi bilo bolje da se poziva user servis za ovo?
// Svakako ostavljam ovde za sada.
func (s *Server) getClientEmailByAccountID(accountID int64) (string, error) {
	var email string
	err := s.db_gorm.
		Model(&Account{}).
		Joins("JOIN clients ON clients.id = accounts.owner").
		Where("accounts.id = ?", accountID).
		Select("clients.email").
		Scan(&email).Error
	return email, err
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
			loans.nominal_rate AS nominal_rate,
			(POWER(1 + loans.interest_rate / 100.0 / 12.0, 12) - 1) * 100 AS effective_rate,
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
		log.Printf("[getAllLoans] ERROR executing query: %v", err)
		return nil, err
	}

	log.Printf("[getAllLoans] SUCCESS: Retrieved %d loans", len(loans))
	return loans, nil
}
