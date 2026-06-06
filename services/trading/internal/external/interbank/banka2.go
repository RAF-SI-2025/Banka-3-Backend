package interbank

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// =====================================================================
// Banka 2 dialect — REST shape used by coursemates whose Spring Boot
// banks were written before the native protocol was finalized. See
// Banka-2-Backend/banka2_bek/src/main/java/rs/raf/banka2_bek/interbank.
//
// Differences from our native shape:
//   * every endpoint is under /interbank on the partner's root base URL
//     (/interbank/public-stock, /interbank/negotiations/…), matching
//     Banka-4's mount; the §2 2PC envelope is POST {base}/interbank
//   * counter uses PUT, withdraw uses DELETE, accept uses GET
//   * money is { currency: "USD", amount: 150 } (BigDecimal, not string)
//   * settlement_date is OffsetDateTime / RFC3339
//   * ids are tuples { routingNumber: int, id: string }
//
// The 2PC envelope itself lives in payments.go; this file is the OTC
// subset (discover / create-offer / counter / withdraw / accept).
// =====================================================================

// banka2PublicStock mirrors rs.raf.banka2_bek.interbank.protocol.PublicStock.
type banka2PublicStock struct {
	Stock   banka2Stock    `json:"stock"`
	Sellers []banka2Seller `json:"sellers"`
}

type banka2Stock struct {
	Ticker string `json:"ticker"`
}

type banka2Seller struct {
	Seller banka2ForeignID `json:"seller"`
	Amount json.Number     `json:"amount"`
}

type banka2ForeignID struct {
	RoutingNumber int    `json:"routingNumber"`
	ID            string `json:"id"`
}

type banka2Monetary struct {
	Currency string      `json:"currency"`
	Amount   json.Number `json:"amount"`
}

type banka2OtcOffer struct {
	Stock          banka2Stock     `json:"stock"`
	SettlementDate string          `json:"settlementDate"` // RFC3339
	PricePerUnit   banka2Monetary  `json:"pricePerUnit"`
	Premium        banka2Monetary  `json:"premium"`
	BuyerID        banka2ForeignID `json:"buyerId"`
	SellerID       banka2ForeignID `json:"sellerId"`
	Amount         json.Number     `json:"amount"`
	LastModifiedBy banka2ForeignID `json:"lastModifiedBy"`
}

// =====================================================================
// Outbound implementations — Banka 2 dialect.
// =====================================================================

func (c *Client) discoverBanka2(ctx context.Context, bankCode, tickerFilter string) ([]*service.PartnerHolding, error) {
	url := c.baseURL(bankCode) + "/interbank/public-stock"
	status, body, err := c.doJSON(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("banka2 %s public-stock: HTTP %d", bankCode, status)
	}
	var parsed []banka2PublicStock
	if err := jsonDecode(body, &parsed); err != nil {
		return nil, fmt.Errorf("banka2 %s public-stock decode: %w", bankCode, err)
	}
	// Banka 2 returns one element per ticker carrying a list of sellers.
	// Flatten into the trading-service domain shape (one row per seller).
	out := make([]*service.PartnerHolding, 0)
	for _, ps := range parsed {
		for _, s := range ps.Sellers {
			qty, _ := strconv.ParseInt(s.Amount.String(), 10, 32)
			rowBankCode := strconv.Itoa(s.Seller.RoutingNumber)
			if rowBankCode == "" {
				rowBankCode = bankCode
			}
			out = append(out, &service.PartnerHolding{
				BankCode:         rowBankCode,
				SellerUserRef:    s.Seller.ID,
				SellerDisplay:    "",          // Banka2 doesn't expose this on /public-stock; resolve via /user lookup later.
				SellerHoldingRef: s.Seller.ID, // Banka2 keys by seller-id, not by holding row.
				SecurityTicker:   ps.Stock.Ticker,
				SecurityType:     domain.SecurityStock,
				Currency:         domain.CurrencyUSD, // Banka2 /public-stock doesn't carry currency; default to USD (course convention).
				Quantity:         int32(qty),
				AskPrice:         "",
				Premium:          "",
			})
		}
	}
	return out, nil
}

func (c *Client) createOfferBanka2(ctx context.Context, in service.PartnerCreateOfferInput) (*service.PartnerCreateOfferOutput, error) {
	settle, err := time.Parse(time.DateOnly, in.SettlementDate.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("banka2 create offer: parse settlement: %w", err)
	}
	ownRouting, _ := strconv.Atoi(c.cfg.OwnRoutingNumber)
	partnerRouting, _ := strconv.Atoi(in.RemoteBankCode)

	body := banka2OtcOffer{
		Stock:          banka2Stock{Ticker: in.SecurityTicker},
		SettlementDate: settle.Format(time.RFC3339),
		PricePerUnit:   banka2Monetary{Currency: string(in.Currency), Amount: json.Number(in.PricePerUnit)},
		Premium:        banka2Monetary{Currency: string(in.Currency), Amount: json.Number(in.Premium)},
		BuyerID:        banka2ForeignID{RoutingNumber: ownRouting, ID: in.LocalUserRef},
		SellerID:       banka2ForeignID{RoutingNumber: partnerRouting, ID: in.RemoteUserRef},
		Amount:         json.Number(strconv.Itoa(int(in.Quantity))),
		LastModifiedBy: banka2ForeignID{RoutingNumber: ownRouting, ID: in.LocalUserRef},
	}

	url := c.baseURL(in.RemoteBankCode) + "/interbank/negotiations"
	status, respBody, err := c.doJSON(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	// Banka 2 returns the ForeignBankId it minted — { routingNumber, id }.
	var partnerID banka2ForeignID
	if err := jsonDecode(respBody, &partnerID); err != nil {
		return nil, fmt.Errorf("banka2 create offer decode: %w", err)
	}
	return &service.PartnerCreateOfferOutput{
		RemoteThreadID:    partnerID.ID,
		RemoteUserDisplay: "",
		RemoteAccountRef:  "",
	}, nil
}

func (c *Client) actionBanka2(ctx context.Context, in service.PartnerActionInput, verb string) error {
	ownRouting, _ := strconv.Atoi(c.cfg.OwnRoutingNumber)
	partnerRouting, _ := strconv.Atoi(in.RemoteBankCode)
	base := c.baseURL(in.RemoteBankCode)
	path := fmt.Sprintf("/interbank/negotiations/%d/%s", partnerRouting, in.RemoteThreadID)

	switch verb {
	case "counter":
		// PUT with the new terms — full OtcOffer body, lastModifiedBy = us.
		settle := in.SettlementDate.UTC().Format(time.RFC3339)
		body := banka2OtcOffer{
			Stock:          banka2Stock{Ticker: ""}, // Banka 2 keeps the original stock from the prior offer
			SettlementDate: settle,
			PricePerUnit:   banka2Monetary{Currency: "USD", Amount: json.Number(in.PricePerUnit)},
			Premium:        banka2Monetary{Currency: "USD", Amount: json.Number(in.Premium)},
			Amount:         json.Number(strconv.Itoa(int(in.Quantity))),
			LastModifiedBy: banka2ForeignID{RoutingNumber: ownRouting, ID: ""},
		}
		status, respBody, err := c.doJSON(ctx, "PUT", base+path, body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
		}
		return nil

	case "withdraw":
		status, respBody, err := c.doJSON(ctx, "DELETE", base+path, nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
		}
		return nil

	case "accept":
		// Banka 2 quirk: accept is a SYNC GET that returns 204 only after
		// 2PC commit. Use the same JSON helper (we send no body and they
		// don't return one).
		status, respBody, err := c.doJSON(ctx, "GET", base+path+"/accept", nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
		}
		return nil
	}
	return fmt.Errorf("banka2: unknown action verb %q", verb)
}
