package user

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
)

type User struct {
	email          string
	hashedPassword []byte
	salt           []byte
}

type (

	// I am indeed aware (unlike most)
	// That these are in fact the same type
	// But We should disambiguate what purpose
	// these are used for
	user_restrictions = map[string]string
)

var ErrInvalidPasswordActionToken = errors.New("invalid or expired password token")

var ErrClientNotFound = errors.New("client not found")
var ErrClientEmailExists = errors.New("client email already exists")
var ErrClientNoFieldsToUpdate = errors.New("no client fields to update")
var ErrEmployeeNotFound = errors.New("employee not found")

var ErrUnknownPermission = errors.New("unknown permissions")

func (s *Server) GetUserByEmail(email string) (*User, error) {
	query := `
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`

	var user User

	err := s.database.QueryRow(query, email).Scan(&user.email, &user.hashedPassword, &user.salt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Server) rotateRefreshToken(tx *sql.Tx, email string, oldHash, newHash []byte, newExpiry time.Time) error {
	var storedHash []byte
	err := tx.QueryRow(`
        SELECT hashed_token FROM refresh_tokens
        WHERE email = $1 AND revoked = FALSE AND valid_until > now()
        FOR UPDATE
    `, email).Scan(&storedHash)
	if err != nil {
		return fmt.Errorf("refresh token not found or expired: %w", err)
	}

	if !bytes.Equal(storedHash, oldHash) {
		_, err := tx.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`, email)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to revoke tokens: %w", err)
		}
		_ = tx.Commit()
		return fmt.Errorf("token mismatch: possible reuse attack")
	}

	_, err = tx.Exec(`
        UPDATE refresh_tokens
        SET hashed_token = $1, valid_until = $2, revoked = FALSE
        WHERE email = $3
    `, newHash, newExpiry, email)
	return err
}

func (s *Server) InsertRefreshToken(token string) error {
	parsed, _, err := jwt.NewParser().ParseUnverified(token, &jwt.RegisteredClaims{})
	if err != nil {
		return fmt.Errorf("parsing token: %w", err)
	}

	email, err := parsed.Claims.GetSubject()
	if err != nil {
		return fmt.Errorf("getting subject: %w", err)
	}

	expiry, err := parsed.Claims.GetExpirationTime()
	if err != nil {
		return fmt.Errorf("getting expiry: %w", err)
	}
	hasher := sha256.New()
	hasher.Write([]byte(token))
	hashed_token := hasher.Sum(nil)
	query := `
	INSERT INTO refresh_tokens VALUES ($1, $2, $3, FALSE)
	ON CONFLICT (email) DO UPDATE SET (hashed_token, valid_until, revoked) = (excluded.hashed_token, excluded.valid_until, excluded.revoked)
	`
	_, err = s.database.Exec(query, email, hashed_token, expiry.Time)
	if err != nil {
		return fmt.Errorf("inserting refresh token: %w", err)
	}

	return nil
}

func upsertPasswordActionToken(db *sql.DB, email, actionType string, hashedToken []byte, validUntil time.Time) error {
	query := `
	INSERT INTO password_action_tokens (email, action_type, hashed_token, valid_until, used)
	VALUES ($1, $2, $3, $4, FALSE)
	ON CONFLICT (email, action_type)
	DO UPDATE SET
		hashed_token = excluded.hashed_token,
		valid_until = excluded.valid_until,
		used = FALSE,
		used_at = NULL
	`

	_, err := db.Exec(query, email, actionType, hashedToken, validUntil)
	if err != nil {
		return fmt.Errorf("upserting password action token: %w", err)
	}
	return nil
}

func (s *Server) UpsertPasswordActionToken(email, actionType string, hashedToken []byte, validUntil time.Time) error {
	return upsertPasswordActionToken(s.database, email, actionType, hashedToken, validUntil)
}

func consumePasswordActionToken(tx *sql.Tx, hashedToken []byte) (string, string, error) {
	var email string
	var actionType string
	err := tx.QueryRow(`
		SELECT email, action_type
		FROM password_action_tokens
		WHERE hashed_token = $1 AND used = FALSE AND valid_until > NOW()
		FOR UPDATE
	`, hashedToken).Scan(&email, &actionType)
	if err == sql.ErrNoRows {
		return "", "", ErrInvalidPasswordActionToken
	}
	if err != nil {
		return "", "", fmt.Errorf("querying password action token: %w", err)
	}

	_, err = tx.Exec(`
		UPDATE password_action_tokens
		SET used = TRUE, used_at = NOW()
		WHERE email = $1 AND action_type = $2
	`, email, actionType)
	if err != nil {
		return "", "", fmt.Errorf("marking password action token used: %w", err)
	}

	return email, actionType, nil
}

func (s *Server) UpdatePasswordByEmail(tx *sql.Tx, email string, hashedPassword []byte) error {
	employeeRes, err := tx.Exec(`
		UPDATE employees
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`, hashedPassword, email)
	if err != nil {
		return fmt.Errorf("updating employee password: %w", err)
	}
	employeeRows, err := employeeRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading employee affected rows: %w", err)
	}
	if employeeRows > 0 {
		return nil
	}

	clientRes, err := tx.Exec(`
		UPDATE clients
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`, hashedPassword, email)
	if err != nil {
		return fmt.Errorf("updating client password: %w", err)
	}
	clientRows, err := clientRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading client affected rows: %w", err)
	}
	if clientRows == 0 {
		return fmt.Errorf("user not found for email")
	}

	return nil
}

func (s *Server) RevokeRefreshTokensByEmail(tx *sql.Tx, email string) error {
	_, err := tx.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("revoking refresh tokens: %w", err)
	}
	return nil
}

func GetAllUsersFromModel[T Client | Employee](user T, s *Server, constraints user_restrictions) ([]T, error) {
	add_constraints := func(query *gorm.DB, restrictions user_restrictions) *gorm.DB {
		keys := make([]string, 0, len(restrictions))
		for k := range restrictions {
			keys = append(keys, k)
		}

		sort.Strings(keys)
		for _, key := range keys {
			value := restrictions[key]
			if value == "" {
				continue
			}

			if key != "" {
				switch key {
				case "email", "position":
					query = query.Where(key+" = ?", value)
				default:
					query = query.Where(key+" ILIKE ?", value)
				}
			}
		}
		return query
	}
	switch any(user).(type) {
	case Client, Employee:
		var users []T
		var query *gorm.DB
		if reflect.TypeOf(any(user)) == reflect.TypeFor[Employee]() {
			query = s.db_gorm.Model(&user).Preload("Permissions")
		} else {
			query = s.db_gorm.Model(&user)
		}
		query = add_constraints(query, constraints)
		err := query.Find(&users).Error
		if err != nil {
			return nil, err
		}
		return users, nil

	default:
		return nil, fmt.Errorf("called with a type which is neither Client nor employee")
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func create_user_from_model[T Client | Employee](user T, s *Server) error {
	result := s.db_gorm.Create(&user)
	if result.Error != nil {
		logger.L().Error("create user failed", "err", result.Error)
		return result.Error
	}
	return nil
}

func getUserByAttribute[T Client | Employee](user T, gorm *gorm.DB, attribute_name string, attribute_value any) (*T, error) {
	var ret T
	var err error
	if reflect.TypeOf(any(user)) == reflect.TypeFor[Employee]() {
		err = gorm.Preload("Permissions").Where(attribute_name+" = ?", attribute_value).First(&ret).Error
	} else {
		err = gorm.Model(&user).Where(attribute_name+" = ?", attribute_value).First(&ret).Error
	}
	if err != nil {
		logger.L().Error("getUserByAttribute failed", "err", err)
		return nil, err
	}

	logger.L().Debug("getUserByAttribute result", "value", ret)
	return &ret, nil
}

func deleteUser[T Client | Employee](user T, s *Server) error {
	result := s.db_gorm.Delete(&user)
	if result.RowsAffected == 0 {
		return ErrEmployeeNotFound
	} else if result.Error != nil {
		logger.L().Error("deleteUser failed", "err", result.Error)
	}
	return nil

}

func userExists[T Client | Employee](user T, s *Server) bool {
	result := s.db_gorm.First(&user)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false
	} else if result.Error != nil {
		logger.L().Error("userExists failed", "err", result.Error)
		return false
	}
	return true
}

func updateUserRecord[T Client | Employee](user T, s *Server) (*T, error) {
	find_perm_by_name := func(perm_name string) uint64 {
		var perms Permission
		s.db_gorm.First(&perms, "name = ?", perm_name)
		return perms.Id
	}

	var result = s.db_gorm
	switch any(user).(type) {
	case Client:
		if userExists(user, s) {
			result = s.db_gorm.Model(&user).Updates(user)
		}

	case Employee:
		for index, val := range any(user).(Employee).Permissions {
			any(user).(Employee).Permissions[index].Id = find_perm_by_name(val.Name)
		}
		if userExists(user, s) {
			result = s.db_gorm.Model(&user).Updates(user)
		}
	}

	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return nil, ErrClientEmailExists
		}
		return nil, fmt.Errorf("updating user record: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, ErrClientNotFound
	}

	return &user, nil
}

var ErrUserNotFound = errors.New("user not found")
var ErrTOTPAlreadyActive = errors.New("totp already active")

func (s *TOTPServer) getUserIdByEmail(email string) (*uint64, error) {
	query := `
		SELECT id FROM employees WHERE email = $1
		UNION ALL
		SELECT id FROM clients WHERE email = $1
		LIMIT 1
	`

	var id uint64

	err := s.db.QueryRow(query, email).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *TOTPServer) SetTempTOTPSecret(tx *sql.Tx, id uint64, secret string) error {
	_, err := tx.Exec(`
		INSERT INTO verification_codes (client_id, temp_secret, temp_created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (client_id)
		DO UPDATE SET
			temp_secret = EXCLUDED.temp_secret,
			temp_created_at = NOW()
	`, id, secret)
	return err
}

func (s *TOTPServer) GetTempSecret(tx *sql.Tx, id uint64) (*string, error) {
	var temp_secret string
	row := tx.QueryRow(`
		SELECT temp_secret
		FROM verification_codes
		WHERE client_id = $1
		FOR UPDATE
	`, id)
	if row == nil {
		return nil, ErrUserNotFound
	}
	err := row.Scan(&temp_secret)
	if err != nil {
		return nil, err
	}
	return &temp_secret, nil
}

func (s *TOTPServer) GetSecret(id uint64) (*string, error) {
	var secret string
	row := s.db.QueryRow(`
		SELECT secret
		FROM verification_codes
		WHERE client_id = $1 AND enabled = TRUE
	`, id)
	err := row.Scan(&secret)
	if err != nil {
		return nil, ErrUserNotFound
	}
	return &secret, nil
}

func (s *TOTPServer) EnableTOTP(tx *sql.Tx, id uint64, tempSecret string) error {
	_, err := tx.Exec(`
		UPDATE verification_codes
		SET enabled = TRUE,
		    secret = $1,
		    temp_secret = NULL
		WHERE client_id = $2
	`, tempSecret, id)

	if err != nil {
		return err
	}
	return nil
}

func (s *TOTPServer) DisableTOTP(tx *sql.Tx, id uint64) error {
	_, err := tx.Exec(`
		UPDATE verification_codes
		SET enabled = FALSE
		WHERE client_id = $1
	`, id)

	if err != nil {
		return err
	}
	return nil
}

func (s *TOTPServer) deleteOldCodes(tx *sql.Tx, id uint64) error {
	_, err := tx.Exec(`
		DELETE FROM backup_codes
		WHERE client_id = $1
	`, id)
	if err != nil {
		return err
	}
	return nil
}

func (s *TOTPServer) InsertGeneratedCodes(tx *sql.Tx, id uint64, codes []string) error {
	err := s.deleteOldCodes(tx, id)
	if err != nil {
		return err
	}

	query := "INSERT INTO backup_codes (client_id, token) VALUES"
	values := []any{}
	paramIdx := 1
	for _, code := range codes {
		query += fmt.Sprintf("($%d, $%d),", paramIdx, paramIdx+1)
		paramIdx += 2
		values = append(values, id, code)
	}
	query = query[:len(query)-1]
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	_, err = stmt.Exec(values...)
	if err != nil {
		return err
	}
	return nil
}

func (s *TOTPServer) status(tx *sql.Tx, id uint64) (*bool, error) {
	var active bool
	query := `
		SELECT enabled
		FROM verification_codes
		WHERE client_id = $1
		FOR UPDATE
	`
	row := tx.QueryRow(query, id)
	err := row.Scan(&active)
	if err != nil {
		dummy := false
		if errors.Is(err, sql.ErrNoRows) {
			return &dummy, nil
		}
		return nil, err
	}
	return &active, nil
}

func (s *TOTPServer) tryBurnBackupCode(id uint64, code string) (*bool, error) {
	query := `
		UPDATE backup_codes
		SET used = TRUE
		WHERE client_id = $1 AND token = $2
	`
	res, err := s.db.Exec(query, id, code)
	if err != nil {
		return nil, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	ret := rows == 1
	return &ret, nil
}
