package bank

import (
	"errors"
	"fmt"
	"strings"

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

func (s *Server) UpdateAccountLimitsRecord(accountNumber string, dailyLimit *float64, monthlyLimit *float64) error {
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
	result := s.db_gorm.Where(&Account{Owner: int64(ownerID), Active: true}).
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

func (s *Server) GetFilteredTransactions(accNumbers []string, accountNumber string, date string, amount float64, status string) ([]*bankpb.ClientTransaction, error) {
	if len(accNumbers) == 0 {
		return []*bankpb.ClientTransaction{}, nil
	}

	makePlaceholders := func(start, count int) string {
		parts := make([]string, count)
		for i := 0; i < count; i++ {
			parts[i] = fmt.Sprintf("$%d", start+i)
		}
		return strings.Join(parts, ", ")
	}

	args := make([]any, 0, len(accNumbers)*2+8)
	for _, acc := range accNumbers {
		args = append(args, acc)
	}
	for _, acc := range accNumbers {
		args = append(args, acc)
	}

	nextArg := len(args) + 1
	statusFilter := strings.TrimSpace(strings.ToLower(status))

	paymentConditions := []string{
		fmt.Sprintf("(p.from_account IN (%s) OR p.to_account IN (%s))",
			makePlaceholders(1, len(accNumbers)),
			makePlaceholders(1+len(accNumbers), len(accNumbers))),
	}
	transferConditions := []string{
		fmt.Sprintf("(t.from_account IN (%s) OR t.to_account IN (%s))",
			makePlaceholders(1, len(accNumbers)),
			makePlaceholders(1+len(accNumbers), len(accNumbers))),
	}

	if accountNumber != "" {
		paymentConditions = append(paymentConditions, fmt.Sprintf("(p.from_account = $%d OR p.to_account = $%d)", nextArg, nextArg))
		transferConditions = append(transferConditions, fmt.Sprintf("(t.from_account = $%d OR t.to_account = $%d)", nextArg, nextArg))
		args = append(args, accountNumber)
		nextArg++
	}
	if date != "" {
		paymentConditions = append(paymentConditions, fmt.Sprintf("DATE(p.timestamp) = $%d", nextArg))
		transferConditions = append(transferConditions, fmt.Sprintf("DATE(t.timestamp) = $%d", nextArg))
		args = append(args, date)
		nextArg++
	}
	if amount > 0 {
		paymentConditions = append(paymentConditions, fmt.Sprintf("p.start_amount = $%d", nextArg))
		transferConditions = append(transferConditions, fmt.Sprintf("t.start_amount = $%d", nextArg))
		args = append(args, amount)
		nextArg++
	}
	if statusFilter != "" {
		switch statusFilter {
		case "realized", "completed":
			paymentConditions = append(paymentConditions, fmt.Sprintf("p.status = $%d", nextArg))
			transferConditions = append(transferConditions, fmt.Sprintf("t.status = $%d", nextArg))
			args = append(args, "realized")
			nextArg++
			args = append(args, "completed")
			nextArg++
		default:
			paymentConditions = append(paymentConditions, fmt.Sprintf("p.status = $%d", nextArg))
			transferConditions = append(transferConditions, fmt.Sprintf("t.status = $%d", nextArg))
			args = append(args, statusFilter)
			nextArg++
			args = append(args, statusFilter)
			nextArg++
		}
	}

	query := fmt.Sprintf(`
		SELECT
			tx.from_account,
			tx.to_account,
			tx.initial_amount,
			tx.final_amount,
			tx.fee,
			tx.payment_code,
			tx.reference_number,
			tx.purpose,
			tx.status,
			EXTRACT(EPOCH FROM tx.timestamp)::bigint AS timestamp
		FROM (
			SELECT
				p.from_account,
				p.to_account,
				p.start_amount::double precision AS initial_amount,
				p.end_amount::double precision AS final_amount,
				p.commission::double precision AS fee,
				COALESCE(p.transcaction_code::text, '') AS payment_code,
				COALESCE(p.call_number, '') AS reference_number,
				COALESCE(p.reason, '') AS purpose,
				p.status,
				p.timestamp
			FROM payments p
			WHERE %s

			UNION ALL

			SELECT
				t.from_account,
				t.to_account,
				t.start_amount::double precision AS initial_amount,
				t.end_amount::double precision AS final_amount,
				t.commission::double precision AS fee,
				''::text AS payment_code,
				''::text AS reference_number,
				''::text AS purpose,
				CASE WHEN t.status = 'completed' THEN 'realized' ELSE t.status END AS status,
				t.timestamp
			FROM transfers t
			WHERE %s
		) tx
		ORDER BY tx.timestamp DESC
	`, strings.Join(paymentConditions, " AND "), strings.Join(transferConditions, " AND "))

	rows, err := s.database.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	pbTransactions := make([]*bankpb.ClientTransaction, 0)
	for rows.Next() {
		tx := &bankpb.ClientTransaction{}
		if err := rows.Scan(
			&tx.FromAccount,
			&tx.ToAccount,
			&tx.InitialAmount,
			&tx.FinalAmount,
			&tx.Fee,
			&tx.PaymentCode,
			&tx.ReferenceNumber,
			&tx.Purpose,
			&tx.Status,
			&tx.Timestamp,
		); err != nil {
			return nil, err
		}
		pbTransactions = append(pbTransactions, tx)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pbTransactions, nil
}
