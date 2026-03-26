package bank

import "math"

// base annual rate brackets in RSD paras, straight from the Celina 2 spec
func BaseAnnualRate(amountRSD int64) float64 {
	switch {
	case amountRSD <= 500_000_00:
		return 6.25
	case amountRSD <= 1_000_000_00:
		return 6.00
	case amountRSD <= 2_000_000_00:
		return 5.75
	case amountRSD <= 5_000_000_00:
		return 5.50
	case amountRSD <= 10_000_000_00:
		return 5.25
	case amountRSD <= 20_000_000_00:
		return 5.00
	default:
		return 4.75
	}
}

// margin on top of the base rate, depends on how risky the loan type is
func MarginForLoanType(lt loan_type) float64 {
	switch lt {
	case Cash:
		return 1.75
	case Mortgage:
		return 1.50
	case Car:
		return 1.25
	case Refinancing:
		return 1.00
	case Student:
		return 0.75
	default:
		return 1.75
	}
}

// the classic annuity formula: A = P * r * (1+r)^n / ((1+r)^n - 1)
// r = annualRate/100/12, nothing fancy
// principalParas is in smallest currency unit (paras), returns paras
func CalculateAnnuity(principalParas int64, annualRatePercent float64, months int64) int64 {
	if months <= 0 {
		return 0
	}
	p := float64(principalParas)
	if annualRatePercent == 0 {
		return int64(math.Round(p / float64(months)))
	}
	r := annualRatePercent / 100.0 / 12.0
	n := float64(months)
	pow := math.Pow(1+r, n)
	return int64(math.Round(p * r * pow / (pow - 1)))
}
