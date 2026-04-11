package gateway

import (
	"errors"
	"math"
)

func normalizeMoneyAmount(amount float64) (float64, error) {
	if amount <= 0 {
		return 0, errors.New("amount must be greater than zero")
	}

	rounded := math.Round(amount*100) / 100
	if math.Abs(amount-rounded) > 0.000001 {
		return 0, errors.New("amount must have at most 2 decimal places")
	}

	return rounded, nil
}
