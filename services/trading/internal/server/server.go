// Package server adapts the proto-generated TradingService surface to
// the service layer. RPCs are implemented across multiple files in
// this package, grouped by aggregate.
package server

import (
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// Server is the gRPC implementation of TradingService + the celina-5
// ExternalOTCService. Both surfaces share one *service.Service so
// cross-aggregate calls (e.g. external OTC reading the local holding
// catalog) don't need a side channel.
type Server struct {
	tradingpb.UnimplementedTradingServiceServer
	tradingpb.UnimplementedExternalOTCServiceServer
	Svc *service.Service
}

// New constructs the gRPC handler.
func New(svc *service.Service) *Server { return &Server{Svc: svc} }
