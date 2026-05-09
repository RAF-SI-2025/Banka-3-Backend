// Package server adapts the proto-generated TradingService surface to
// the service layer. RPCs are implemented across multiple files in
// this package, grouped by aggregate.
package server

import (
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// Server is the gRPC implementation of TradingService.
type Server struct {
	tradingpb.UnimplementedTradingServiceServer
	Svc *service.Service
}

// New constructs the gRPC handler.
func New(svc *service.Service) *Server { return &Server{Svc: svc} }
