// Package observability wires Prometheus metrics into the rewrite's
// probe HTTP server + gRPC server interceptors. Ported from main
// branch's BonusMLAObservability (PR #292), trimmed of its
// gin/logger-package dependencies — the rewrite uses stdlib net/http
// and log/slog directly.
//
// Usage from a service main:
//
//	obs := observability.New("user")
//	probeSrv := probes.New(":8081")
//	probeSrv.MountMetrics(obs.MetricsHandler())
//	// gRPC: chain obs.UnaryServerInterceptor() into grpcserver.Run.
//
// HTTP services (the gateway) wrap each handler with obs.HTTPMiddleware.
package observability

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var (
	registerOnce sync.Once

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "banka_http_requests_total",
			Help: "Total HTTP requests by service, route, method, and status.",
		},
		[]string{"service", "route", "method", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "banka_http_request_duration_seconds",
			Help:    "HTTP request duration by service, route, method, and status.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "route", "method", "status"},
	)
	grpcRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "banka_grpc_requests_total",
			Help: "Total gRPC requests by service, rpc service, method, and status code.",
		},
		[]string{"service", "rpc_service", "rpc_method", "code"},
	)
	grpcRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "banka_grpc_request_duration_seconds",
			Help:    "gRPC request duration by service, rpc service, method, and status code.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "rpc_service", "rpc_method", "code"},
	)
	serviceInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "banka_service_info",
			Help: "1 for each running service.",
		},
		[]string{"service"},
	)
)

func register() {
	registerOnce.Do(func() {
		prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, grpcRequestsTotal, grpcRequestDuration, serviceInfo)
	})
}

// Observer is the per-service entry point. service is the value
// stamped into the `service` label on every emitted metric.
type Observer struct{ service string }

// New constructs an Observer for the named service. The
// banka_service_info gauge is stamped to 1 immediately so absence-on-
// scrape from Prometheus surfaces as the service being down.
func New(service string) *Observer {
	register()
	serviceInfo.WithLabelValues(service).Set(1)
	return &Observer{service: service}
}

// MetricsHandler returns the Prometheus scrape handler. Mount it on
// the probe HTTP server at /metrics so Prometheus can scrape without
// a second port.
func (o *Observer) MetricsHandler() http.Handler { return promhttp.Handler() }

// HTTPMiddleware records request count + latency under the
// banka_http_* metrics. Use it on the gateway's outermost handler.
func (o *Observer) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		route := r.URL.Path
		labels := prometheus.Labels{
			"service": o.service,
			"route":   route,
			"method":  r.Method,
			"status":  strconv.Itoa(rw.status),
		}
		httpRequestsTotal.With(labels).Inc()
		httpRequestDuration.With(labels).Observe(time.Since(start).Seconds())
	})
}

// UnaryServerInterceptor records gRPC method count + latency. Chain
// this into grpc.NewServer alongside the existing interceptors.
func (o *Observer) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		rpcService, rpcMethod := splitRPC(info.FullMethod)
		resp, err := handler(ctx, req)
		code := status.Code(err).String()
		labels := prometheus.Labels{
			"service":     o.service,
			"rpc_service": rpcService,
			"rpc_method":  rpcMethod,
			"code":        code,
		}
		grpcRequestsTotal.With(labels).Inc()
		grpcRequestDuration.With(labels).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func splitRPC(full string) (svc, method string) {
	// full = "/banka.user.v1.UserService/Login"
	if len(full) > 0 && full[0] == '/' {
		full = full[1:]
	}
	idx := -1
	for i := 0; i < len(full); i++ {
		if full[i] == '/' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "unknown", full
	}
	return full[:idx], full[idx+1:]
}
