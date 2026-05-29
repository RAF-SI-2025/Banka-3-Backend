// Adapter that bridges service.MarketDataMirror to the influxmarket
// HTTP client. Lets the service-layer code stay free of HTTP details.
package app

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/external/influxmarket"
)

type influxMirror struct{ s influxmarket.Store }

func (a *influxMirror) Enabled() bool { return a.s != nil && a.s.Enabled() }

func (a *influxMirror) WriteDaily(ctx context.Context, listingID string, date time.Time, price, ask, bid, changeAmt string, volume int64) error {
	if a.s == nil {
		return nil
	}
	return a.s.WriteDaily(ctx, influxmarket.Row{
		ListingID: listingID,
		Date:      date,
		Price:     price,
		Ask:       ask,
		Bid:       bid,
		ChangeAmt: changeAmt,
		Volume:    volume,
	})
}
