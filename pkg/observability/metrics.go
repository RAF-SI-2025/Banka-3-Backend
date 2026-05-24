package observability

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var (
	registerMetricsOnce sync.Once

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "banka_http_requests_total",
			Help: "Total number of handled HTTP requests grouped by service, route, method, and status.",
		},
		[]string{"service", "route", "method", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "banka_http_request_duration_seconds",
			Help:    "HTTP request duration grouped by service, route, method, and status.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "route", "method", "status"},
	)
	grpcRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "banka_grpc_requests_total",
			Help: "Total number of handled gRPC requests grouped by service, rpc service, method, type, and status code.",
		},
		[]string{"service", "rpc_service", "rpc_method", "rpc_type", "code"},
	)
	grpcRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "banka_grpc_request_duration_seconds",
			Help:    "gRPC request duration grouped by service, rpc service, method, type, and status code.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "rpc_service", "rpc_method", "rpc_type", "code"},
	)
	serviceInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "banka_service_info",
			Help: "Static service info metric exposed with value 1 for each running service.",
		},
		[]string{"service"},
	)
)

func registerMetrics() {
	registerMetricsOnce.Do(func() {
		prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, grpcRequestsTotal, grpcRequestDuration, serviceInfo)
	})
}

func StartMetricsServer(service, port string) func() {
	registerMetrics()
	serviceInfo.WithLabelValues(service).Set(1)

	if port == "" {
		logger.L().Warn("metrics port not configured, metrics server disabled", "service", service)
		return func() {}
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(w, "%s metrics available at /metrics\n", service)
	})

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.L().Info("metrics server listening", "service", service, "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L().Error("metrics server stopped", "service", service, "port", port, "err", err)
		}
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.L().Warn("metrics server shutdown failed", "service", service, "port", port, "err", err)
		}
	}
}

func GinMiddleware(service string) gin.HandlerFunc {
	registerMetrics()
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		statusCode := strconv.Itoa(c.Writer.Status())
		labels := []string{service, route, c.Request.Method, statusCode}

		httpRequestsTotal.WithLabelValues(labels...).Inc()
		httpRequestDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
	}
}

func UnaryServerInterceptor(service string) grpc.UnaryServerInterceptor {
	registerMetrics()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		rpcService := path.Dir(info.FullMethod)[1:]
		rpcMethod := path.Base(info.FullMethod)
		code := status.Code(err).String()
		labels := []string{service, rpcService, rpcMethod, "unary", code}

		grpcRequestsTotal.WithLabelValues(labels...).Inc()
		grpcRequestDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())

		return resp, err
	}
}

func StreamServerInterceptor(service string) grpc.StreamServerInterceptor {
	registerMetrics()
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)

		rpcService := path.Dir(info.FullMethod)[1:]
		rpcMethod := path.Base(info.FullMethod)
		code := status.Code(err).String()
		labels := []string{service, rpcService, rpcMethod, "stream", code}

		grpcRequestsTotal.WithLabelValues(labels...).Inc()
		grpcRequestDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())

		return err
	}
}
