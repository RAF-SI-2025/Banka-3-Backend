package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Server struct {
	exchangepb.UnsafeExchangeServiceServer
	db_gorm *gorm.DB
}

func NewServer(gorm_db *gorm.DB) *Server {
	return &Server{
		db_gorm: gorm_db,
	}
}

func (s *Server) fetchAndStoreRates() error {
	logger.L().Info("fetching and storing exchange rates")

	apiKey := os.Getenv("EXCHANGE_RATE_API_KEY")
	if apiKey == "" || apiKey == "YOUR_KEY" {
		return fmt.Errorf("missing EXCHANGE_RATE_API_KEY")
	}

	url := fmt.Sprintf("https://v6.exchangerate-api.com/v6/%s/latest/RSD", apiKey)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}

	if apiResp.Result != "success" {
		return fmt.Errorf("api error: %s", apiResp.Result)
	}

	validUntil := time.Unix(apiResp.TimeNextUpdateUnix, 0)
	supported := []string{"EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}
	var ratesToUpdate []Rate

	for _, code := range supported {
		if val, ok := apiResp.ConversionRates[code]; ok {
			ratesToUpdate = append(ratesToUpdate, Rate{
				CurrencyCode: code,
				RateToRSD:    1.0 / val,
				ValidUntil:   validUntil,
			})
		}
	}

	return s.UpdateRatesRecord(ratesToUpdate)
}

func (s *Server) GetExchangeRates(_ context.Context, _ *exchangepb.ExchangeRateListRequest) (*exchangepb.ExchangeRateListResponse, error) {
	rates, err := s.GetRatesRecord()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	if len(rates) == 0 || time.Now().After(rates[0].ValidUntil) {
		logger.L().Info("rates expired or missing, refreshing")
		if err := s.fetchAndStoreRates(); err != nil {
			logger.L().Error("refresh failed", "err", err)
		}
		rates, _ = s.GetRatesRecord()
	}

	var pbRates []*exchangepb.CurrencyRate
	var latestUpdate time.Time
	for _, r := range rates {
		pbRates = append(pbRates, &exchangepb.CurrencyRate{
			Code: r.CurrencyCode,
			Rate: r.RateToRSD,
		})
		if r.UpdatedAt.After(latestUpdate) {
			latestUpdate = r.UpdatedAt
		}
	}
	pbRates = append(pbRates, &exchangepb.CurrencyRate{Code: "RSD", Rate: 1.0})

	return &exchangepb.ExchangeRateListResponse{
		Rates:       pbRates,
		LastUpdated: latestUpdate.Unix(),
	}, nil
}

func (s *Server) ConvertMoney(_ context.Context, req *exchangepb.ConversionRequest) (*exchangepb.ConversionResponse, error) {
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}

	from, err := s.GetRateByCodeRecord(req.FromCurrency)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "source %s not supported", req.FromCurrency)
	}

	to, err := s.GetRateByCodeRecord(req.ToCurrency)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "target %s not supported", req.ToCurrency)
	}

	effectiveRate := from.RateToRSD / to.RateToRSD
	converted := req.Amount * effectiveRate

	return &exchangepb.ConversionResponse{
		ConvertedAmount: converted,
		ExchangeRate:    effectiveRate,
	}, nil
}
