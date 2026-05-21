package trading

import (
	"time"

	"gorm.io/gorm"
)

// External OTC mirror state used by the Celina 5 inter-bank flow. The fresh
// backend keeps trading inside the bank service, so these records and helpers
// are the natural persistence landing zone for the Banka 2 compatibility work.

type ExternalOTCThreadRecord struct {
	ID                 string    `gorm:"column:id;primaryKey"`
	Direction          string    `gorm:"column:direction;type:varchar(16);not null"`
	RemoteBankCode     string    `gorm:"column:remote_bank_code;type:varchar(8);not null"`
	RemoteThreadID     string    `gorm:"column:remote_thread_id;type:text;not null;default:''"`
	RemoteUserRef      string    `gorm:"column:remote_user_ref;type:text;not null"`
	RemoteDisplayName  string    `gorm:"column:remote_display_name;type:text;not null"`
	RemoteAccountRef   string    `gorm:"column:remote_account_ref;type:text;not null;default:''"`
	LocalUserID        string    `gorm:"column:local_user_id;type:text;not null"`
	LocalUserKind      string    `gorm:"column:local_user_kind;type:varchar(16);not null"`
	LocalAccountID     string    `gorm:"column:local_account_id;type:text;not null"`
	LocalAccountNumber string    `gorm:"column:local_account_number;type:varchar(20);not null"`
	LocalRole          string    `gorm:"column:local_role;type:varchar(16);not null"`
	SecurityID         string    `gorm:"column:security_id;type:text;not null"`
	SecurityTicker     string    `gorm:"column:security_ticker;type:varchar(32);not null"`
	SellerHoldingID    string    `gorm:"column:seller_holding_id;type:text;not null;default:''"`
	Quantity           int64     `gorm:"column:quantity;not null"`
	PricePerUnit       string    `gorm:"column:price_per_unit;type:numeric(20,4);not null"`
	Premium            string    `gorm:"column:premium;type:numeric(20,4);not null"`
	Currency           string    `gorm:"column:currency;type:varchar(8);not null"`
	SettlementDate     time.Time `gorm:"column:settlement_date;type:date;not null"`
	ModifiedBySide     string    `gorm:"column:modified_by_side;type:varchar(16);not null"`
	Status             string    `gorm:"column:status;type:varchar(16);not null"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
}

func (ExternalOTCThreadRecord) TableName() string { return "external_otc_threads" }

type ExternalOTCIterationRecord struct {
	ID             string    `gorm:"column:id;primaryKey"`
	ThreadID       string    `gorm:"column:thread_id;type:text;not null"`
	ProposedBySide string    `gorm:"column:proposed_by_side;type:varchar(16);not null"`
	Quantity       int64     `gorm:"column:quantity;not null"`
	PricePerUnit   string    `gorm:"column:price_per_unit;type:numeric(20,4);not null"`
	Premium        string    `gorm:"column:premium;type:numeric(20,4);not null"`
	SettlementDate time.Time `gorm:"column:settlement_date;type:date;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;autoCreateTime"`
}

func (ExternalOTCIterationRecord) TableName() string { return "external_otc_iterations" }

type ExternalOTCContractRecord struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	ThreadID           string     `gorm:"column:thread_id;type:text;not null"`
	Direction          string     `gorm:"column:direction;type:varchar(16);not null"`
	RemoteBankCode     string     `gorm:"column:remote_bank_code;type:varchar(8);not null"`
	RemoteThreadID     string     `gorm:"column:remote_thread_id;type:text;not null;default:''"`
	RemoteUserRef      string     `gorm:"column:remote_user_ref;type:text;not null"`
	RemoteDisplayName  string     `gorm:"column:remote_display_name;type:text;not null"`
	RemoteAccountRef   string     `gorm:"column:remote_account_ref;type:text;not null;default:''"`
	LocalUserID        string     `gorm:"column:local_user_id;type:text;not null"`
	LocalUserKind      string     `gorm:"column:local_user_kind;type:varchar(16);not null"`
	LocalAccountID     string     `gorm:"column:local_account_id;type:text;not null"`
	LocalAccountNumber string     `gorm:"column:local_account_number;type:varchar(20);not null"`
	LocalRole          string     `gorm:"column:local_role;type:varchar(16);not null"`
	SecurityID         string     `gorm:"column:security_id;type:text;not null"`
	SecurityTicker     string     `gorm:"column:security_ticker;type:varchar(32);not null"`
	SellerHoldingID    string     `gorm:"column:seller_holding_id;type:text;not null;default:''"`
	Quantity           int64      `gorm:"column:quantity;not null"`
	StrikePrice        string     `gorm:"column:strike_price;type:numeric(20,4);not null"`
	PremiumPaid        string     `gorm:"column:premium_paid;type:numeric(20,4);not null"`
	Currency           string     `gorm:"column:currency;type:varchar(8);not null"`
	SettlementDate     time.Time  `gorm:"column:settlement_date;type:date;not null"`
	AcceptedBySide     string     `gorm:"column:accepted_by_side;type:varchar(16);not null"`
	Status             string     `gorm:"column:status;type:varchar(16);not null"`
	PremiumOpID        string     `gorm:"column:premium_op_id;type:text;not null;default:''"`
	ExerciseOpID       string     `gorm:"column:exercise_op_id;type:text;not null;default:''"`
	ExercisedAt        *time.Time `gorm:"column:exercised_at"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null;autoUpdateTime"`
}

func (ExternalOTCContractRecord) TableName() string { return "external_otc_contracts" }

func (s *Server) CreateExternalOTCThreadRecord(rec *ExternalOTCThreadRecord) error {
	return s.db.Create(rec).Error
}

func (s *Server) CreateExternalOTCIterationRecord(rec *ExternalOTCIterationRecord) error {
	return s.db.Create(rec).Error
}

func (s *Server) CreateExternalOTCContractRecord(rec *ExternalOTCContractRecord) error {
	return s.db.Create(rec).Error
}

func (s *Server) GetExternalOTCThreadRecord(id string) (*ExternalOTCThreadRecord, error) {
	var rec ExternalOTCThreadRecord
	if err := s.db.Where("id = ?", id).Take(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Server) GetExternalOTCContractRecord(id string) (*ExternalOTCContractRecord, error) {
	var rec ExternalOTCContractRecord
	if err := s.db.Where("id = ?", id).Take(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Server) GetExternalOTCContractByThreadRecord(threadID string) (*ExternalOTCContractRecord, error) {
	var rec ExternalOTCContractRecord
	if err := s.db.Where("thread_id = ?", threadID).Take(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Server) ListExternalOTCThreadsRecords(localUserID, status string) ([]ExternalOTCThreadRecord, error) {
	var out []ExternalOTCThreadRecord
	tx := s.db.Where("local_user_id = ?", localUserID)
	if status != "" && status != "any" {
		tx = tx.Where("status = ?", status)
	}
	if err := tx.Order("updated_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) ListExternalOTCThreadIterationsRecord(threadID string) ([]ExternalOTCIterationRecord, error) {
	var out []ExternalOTCIterationRecord
	if err := s.db.Where("thread_id = ?", threadID).Order("created_at ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) ListExternalOTCContractsRecords(localUserID, status string) ([]ExternalOTCContractRecord, error) {
	var out []ExternalOTCContractRecord
	tx := s.db.Where("local_user_id = ?", localUserID)
	if status != "" && status != "any" {
		tx = tx.Where("status = ?", status)
	}
	if err := tx.Order("updated_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) UpdateExternalOTCThreadTermsRecord(id string, quantity int64, pricePerUnit, premium string, settlementDate time.Time, modifiedBySide string) error {
	tx := s.db.Model(&ExternalOTCThreadRecord{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"quantity":         quantity,
			"price_per_unit":   pricePerUnit,
			"premium":          premium,
			"settlement_date":  settlementDate,
			"modified_by_side": modifiedBySide,
			"updated_at":       gorm.Expr("NOW()"),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Server) MarkExternalOTCThreadStatusRecord(id, nextStatus string) error {
	tx := s.db.Model(&ExternalOTCThreadRecord{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     nextStatus,
			"updated_at": gorm.Expr("NOW()"),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Server) MarkExternalOTCContractExercisedRecord(id, exerciseOpID string, exercisedAt time.Time) error {
	tx := s.db.Model(&ExternalOTCContractRecord{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":         "exercised",
			"exercise_op_id": exerciseOpID,
			"exercised_at":   exercisedAt,
			"updated_at":     gorm.Expr("NOW()"),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Server) MarkExternalOTCContractStatusRecord(id, status string) error {
	tx := s.db.Model(&ExternalOTCContractRecord{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     status,
			"updated_at": gorm.Expr("NOW()"),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
