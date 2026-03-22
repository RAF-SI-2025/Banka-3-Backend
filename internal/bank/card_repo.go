package bank

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
)

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
