package exchange

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrRateNotFound = errors.New("exchange rate not found")

func (s *Server) GetRatesRecord() ([]Rate, error) {
	var rates []Rate
	result := s.readDB().Find(&rates)
	if result.Error != nil {
		return nil, result.Error
	}
	return rates, nil
}

func (s *Server) GetRateByCodeRecord(code string) (*Rate, error) {
	if code == "RSD" {
		return &Rate{CurrencyCode: "RSD", RateToRSD: 1.0, UpdatedAt: time.Now(), ValidUntil: time.Now().Add(24 * time.Hour)}, nil
	}

	var r Rate
	result := s.readDB().Where("currency_code = ?", code).First(&r)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrRateNotFound
		}
		return nil, result.Error
	}
	return &r, nil
}

func (s *Server) UpdateRatesRecord(rates []Rate) error {
	return s.db_gorm.Transaction(func(tx *gorm.DB) error {
		for _, r := range rates {
			err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "currency_code"}},
				DoUpdates: clause.AssignmentColumns([]string{"rate_to_rsd", "updated_at", "valid_until"}),
			}).Create(&r).Error

			if err != nil {
				return err
			}
		}
		return nil
	})
}
