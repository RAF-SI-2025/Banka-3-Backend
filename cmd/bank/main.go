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

	srv := grpc.NewServer()
	bank.RegisterBankServiceServer(srv, bankService)
	tradingpb.RegisterTradingServiceServer(srv, tradingService)
	reflection.Register(srv)

	log.Printf("bank service listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
