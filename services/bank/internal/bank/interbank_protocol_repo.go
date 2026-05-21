package bank

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InterbankProtocolMessageRecord struct {
	SenderRoutingNumber int       `gorm:"column:sender_routing_number;primaryKey"`
	IdempotenceKey      string    `gorm:"column:idempotence_key;primaryKey"`
	MessageType         string    `gorm:"column:message_type;type:text;not null"`
	TransactionID       string    `gorm:"column:transaction_id;type:text;not null;default:''"`
	ResponseStatus      int       `gorm:"column:response_status;not null"`
	ResponseBody        string    `gorm:"column:response_body;type:text;not null;default:''"`
	CreatedAt           time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
}

func (InterbankProtocolMessageRecord) TableName() string { return "interbank_protocol_messages" }

type InterbankProtocolTransactionRecord struct {
	SenderRoutingNumber int       `gorm:"column:sender_routing_number;primaryKey"`
	TransactionID       string    `gorm:"column:transaction_id;primaryKey"`
	TransactionBody     string    `gorm:"column:transaction_body;type:text;not null"`
	Status              string    `gorm:"column:status;type:text;not null"`
	CreatedAt           time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
}

func (InterbankProtocolTransactionRecord) TableName() string { return "interbank_protocol_transactions" }

func (s *Server) GetInterbankProtocolMessageRecord(senderRouting int, key string) (*InterbankProtocolMessageRecord, error) {
	var rec InterbankProtocolMessageRecord
	if err := s.db_gorm.
		Where("sender_routing_number = ? AND idempotence_key = ?", senderRouting, key).
		Take(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Server) GetInterbankProtocolTransactionRecord(senderRouting int, transactionID string) (*InterbankProtocolTransactionRecord, error) {
	var rec InterbankProtocolTransactionRecord
	if err := s.db_gorm.
		Where("sender_routing_number = ? AND transaction_id = ?", senderRouting, transactionID).
		Take(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Server) UpsertInterbankProtocolMessageRecord(rec *InterbankProtocolMessageRecord) error {
	return s.db_gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "sender_routing_number"},
			{Name: "idempotence_key"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"message_type":    rec.MessageType,
			"transaction_id":  rec.TransactionID,
			"response_status": rec.ResponseStatus,
			"response_body":   rec.ResponseBody,
			"updated_at":      gorm.Expr("NOW()"),
		}),
	}).Create(rec).Error
}

func (s *Server) UpsertInterbankProtocolTransactionRecord(rec *InterbankProtocolTransactionRecord) error {
	return s.db_gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "sender_routing_number"},
			{Name: "transaction_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"transaction_body": rec.TransactionBody,
			"status":           rec.Status,
			"updated_at":       gorm.Expr("NOW()"),
		}),
	}).Create(rec).Error
}

func (s *Server) MarkInterbankProtocolTransactionStatusRecord(senderRouting int, transactionID, nextStatus string) error {
	tx := s.db_gorm.Model(&InterbankProtocolTransactionRecord{}).
		Where("sender_routing_number = ? AND transaction_id = ?", senderRouting, transactionID).
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

func isRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
