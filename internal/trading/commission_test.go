package trading

import "testing"

func TestPlanCommissionCharge(t *testing.T) {
	// Rates: 1 EUR = 117.69 RSD, 1 USD = 100 RSD.
	const (
		rateEUR = 117.69
		rateUSD = 100.0
		rateRSD = 1.0
	)

	cases := []struct {
		name           string
		debitCur       string
		instrCur       string
		feeInstrument  int64
		rateAccRSD     float64
		rateInstrRSD   float64
		isClient       bool
		wantDebit      int64
		wantFeeInstr   int64
		wantMenjacnica int64
	}{
		{
			name:     "same currency — no conversion, no menjacnica",
			debitCur: "USD", instrCur: "USD",
			feeInstrument: 700, rateAccRSD: rateUSD, rateInstrRSD: rateUSD,
			isClient:  true,
			wantDebit: 700, wantFeeInstr: 700, wantMenjacnica: 0,
		},
		{
			name:     "RSD debit for USD instrument, client pays 1%",
			debitCur: "RSD", instrCur: "USD",
			feeInstrument: 700, rateAccRSD: rateRSD, rateInstrRSD: rateUSD,
			isClient: true,
			// 700 USD-minor * 100 / 1 = 70000 RSD-minor; 1% = 700.
			wantDebit: 70700, wantFeeInstr: 700, wantMenjacnica: 700,
		},
		{
			name:     "RSD debit for USD instrument, employee skips menjacnica",
			debitCur: "RSD", instrCur: "USD",
			feeInstrument: 700, rateAccRSD: rateRSD, rateInstrRSD: rateUSD,
			isClient:  false,
			wantDebit: 70000, wantFeeInstr: 700, wantMenjacnica: 0,
		},
		{
			name:     "EUR debit for USD instrument, client",
			debitCur: "EUR", instrCur: "USD",
			feeInstrument: 1200, rateAccRSD: rateEUR, rateInstrRSD: rateUSD,
			isClient: true,
			// 1200 * 100 / 117.69 = 1019.63 → ceil 1020; 1% of 1020 = 10.2 → ceil 11.
			wantDebit: 1031, wantFeeInstr: 1200, wantMenjacnica: 11,
		},
		{
			name:     "zero commission — no-op",
			debitCur: "EUR", instrCur: "USD",
			feeInstrument: 0, rateAccRSD: rateEUR, rateInstrRSD: rateUSD,
			isClient:  true,
			wantDebit: 0, wantFeeInstr: 0, wantMenjacnica: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planCommissionCharge(
				"acc-123", c.debitCur, c.instrCur,
				c.feeInstrument, c.rateAccRSD, c.rateInstrRSD, c.isClient,
			)
			if got.DebitAmount != c.wantDebit {
				t.Errorf("DebitAmount = %d, want %d", got.DebitAmount, c.wantDebit)
			}
			if got.FeeInstrument != c.wantFeeInstr {
				t.Errorf("FeeInstrument = %d, want %d", got.FeeInstrument, c.wantFeeInstr)
			}
			if got.MenjacnicaFee != c.wantMenjacnica {
				t.Errorf("MenjacnicaFee = %d, want %d", got.MenjacnicaFee, c.wantMenjacnica)
			}
			if got.DebitCurrency != c.debitCur || got.InstrumentCurrency != c.instrCur {
				t.Errorf("currencies not preserved: %+v", got)
			}
		})
	}
}

func TestComputeCommission(t *testing.T) {
	cases := []struct {
		name   string
		ot     OrderType
		approx int64
		want   int64
	}{
		// Market/stop: 14% of approx, capped at 700.
		{"market tiny → pct", OrderMarket, 1000, 140},
		{"market at cap", OrderMarket, 5000, 700},
		{"market above cap", OrderMarket, 100000, 700},
		{"stop follows market", OrderStop, 1000, 140},
		{"stop above cap", OrderStop, 100000, 700},

		// Limit/stop_limit: 24% of approx, capped at 1200.
		{"limit tiny → pct", OrderLimit, 1000, 240},
		{"limit at cap", OrderLimit, 5000, 1200},
		{"limit above cap", OrderLimit, 100000, 1200},
		{"stop_limit follows limit", OrderStopLimit, 1000, 240},
		{"stop_limit above cap", OrderStopLimit, 100000, 1200},

		{"zero approx → zero", OrderMarket, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeCommission(c.ot, c.approx); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
