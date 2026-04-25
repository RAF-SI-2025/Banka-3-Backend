package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	internalBank "github.com/RAF-SI-2025/Banka-3-Backend/internal/bank"
	internalTrading "github.com/RAF-SI-2025/Banka-3-Backend/internal/trading"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func connect_to_db_gorm() *gorm.DB {
	dsn := os.Getenv("DATABASE_URL")
	gorm_db, gorm_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if gorm_err != nil {
		log.Fatal("pgx", dsn)
	}
	return gorm_db
}

func connectToDB() *sql.DB {
	connStr := os.Getenv("DATABASE_URL")
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	return db
}

// buildPricingClient assembles the external-pricing client from env vars.
// Returns nil when no provider is configured — Refresher.Start treats that
// as a no-op, so dev/CI runs without API keys keep working off the static
// seed prices from #195. Order matters: Alpaca is tried first because its
// quote endpoint exposes ask/bid (which AV's free tier doesn't); AV serves
// as a fallback for tickers Alpaca refuses.
func buildPricingClient() pricing.Client {
	var clients []pricing.Client
	if id, secret := os.Getenv("ALPACA_KEY_ID"), os.Getenv("ALPACA_SECRET"); id != "" && secret != "" {
		clients = append(clients, pricing.NewAlpaca(id, secret))
	}
	if key := os.Getenv("ALPHAVANTAGE_KEY"); key != "" {
		clients = append(clients, pricing.NewAlphaVantage(key))
	}
	if len(clients) == 0 {
		return nil
	}
	return pricing.NewMulti(clients...)
}

// buildDailyHistoryClient is AV-only: TIME_SERIES_DAILY is the spec-named
// daily-history source (p.40) and Alpaca's bars endpoint is out of scope for
// #228. nil here disables the backfiller, same convention as the refresher.
func buildDailyHistoryClient() internalTrading.DailyHistoryClient {
	if key := os.Getenv("ALPHAVANTAGE_KEY"); key != "" {
		return pricing.NewAlphaVantage(key)
	}
	return nil
}

// buildCompanyOverviewClient is AV-only as well: OVERVIEW has no Alpaca
// equivalent and the spec table on p.40 names AV as the source for the
// metadata fields the syncer touches (#229).
func buildCompanyOverviewClient() internalTrading.CompanyOverviewClient {
	if key := os.Getenv("ALPHAVANTAGE_KEY"); key != "" {
		return pricing.NewAlphaVantage(key)
	}
	return nil
}

// buildOptionsChainClient wires Yahoo Finance's options endpoint (#230).
// Yahoo's public endpoint requires no API key, so we expose a single boolean
// gate (TRADING_OPTIONS_REFRESH=1) to opt in — defaulting off so dev/CI runs
// don't make outbound calls every hour.
func buildOptionsChainClient() internalTrading.OptionsChainClient {
	if os.Getenv("TRADING_OPTIONS_REFRESH") == "1" {
		return pricing.NewYahoo()
	}
	return nil
}

// buildForexRatesClient wires exchangerate-api (#230). Open endpoint is
// keyless; EXCHANGERATE_KEY routes to the paid host with the same response
// shape. nil disables the forex refresher.
func buildForexRatesClient() internalTrading.ForexRatesClient {
	if os.Getenv("TRADING_FOREX_REFRESH") == "1" || os.Getenv("EXCHANGERATE_KEY") != "" {
		return pricing.NewExchangeRate(os.Getenv("EXCHANGERATE_KEY"))
	}
	return nil
}

func main() {
	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	db := connectToDB()
	gorm_db := connect_to_db_gorm()
	//gorm_db.AutoMigrate(&internalUser.Clients{}, &internalUser.Employees{});
	log.Println("connected to database...")
	defer func() { _ = db.Close() }()

	bankService, err := internalBank.NewServer(db, gorm_db)
	if err != nil {
		log.Fatalf("failed to start bank service: %v", err)
	}
	stopScheduler := bankService.StartScheduler()
	defer stopScheduler()

	tradingService := internalTrading.NewServer(gorm_db, bankService)
	stopExecutor := tradingService.StartExecutor()
	defer stopExecutor()

	// External-pricing refresher (#184). No-op when no API keys are
	// configured, so dev/CI keep operating off the static seed data from
	// #195.
	stopRefresher := internalTrading.NewRefresher(gorm_db, buildPricingClient()).Start()
	defer stopRefresher()

	// Daily-history backfiller (#228). No-op without ALPHAVANTAGE_KEY.
	stopBackfiller := internalTrading.NewBackfiller(gorm_db, buildDailyHistoryClient()).Start()
	defer stopBackfiller()

	// Stock metadata syncer (#229). Weekly OVERVIEW pull. No-op without
	// ALPHAVANTAGE_KEY.
	stopMetadata := internalTrading.NewMetadataSyncer(gorm_db, buildCompanyOverviewClient()).Start()
	defer stopMetadata()

	// Options-chain refresher (#230). Yahoo Finance, opt-in via
	// TRADING_OPTIONS_REFRESH=1.
	stopOptions := internalTrading.NewOptionsRefresher(gorm_db, buildOptionsChainClient()).Start()
	defer stopOptions()

	// Forex-rates refresher (#230). exchangerate-api, opt-in via
	// TRADING_FOREX_REFRESH=1 or by setting EXCHANGERATE_KEY.
	stopForex := internalTrading.NewForexRefresher(gorm_db, buildForexRatesClient()).Start()
	defer stopForex()

	srv := grpc.NewServer()
	bank.RegisterBankServiceServer(srv, bankService)
	tradingpb.RegisterTradingServiceServer(srv, tradingService)
	reflection.Register(srv)

	log.Printf("bank service listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
