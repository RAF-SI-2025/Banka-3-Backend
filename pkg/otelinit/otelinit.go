// Package otelinit wires the OpenTelemetry SDK for every banka
// service: traces to OTLP/gRPC (sink: Alloy → Tempo), metrics to a
// Prometheus exporter that the probe server scrapes via /metrics.
//
// Usage from a service main:
//
//	prov, err := otelinit.Init(ctx, "user")
//	if err != nil { return err }
//	defer prov.Shutdown(context.Background())
//
//	probeSrv := probes.New(":8081")
//	probeSrv.MountMetrics(prov.MetricsHandler())
//	// gRPC server: grpcserver.Run(ctx, log, addr, register, grpcserver.WithStatsHandler(prov.GRPCServerHandler()))
//	// gRPC client: grpc.NewClient(addr, grpc.WithStatsHandler(prov.GRPCClientHandler()), ...)
//	// gateway HTTP: handler = prov.WrapHTTP(handler, "gateway")
//
// Environment:
//
//	OTEL_SERVICE_NAME             — overrides the service-name arg (k8s downward API sets this).
//	OTEL_EXPORTER_OTLP_ENDPOINT   — OTLP collector (default http://alloy.alloy:4317).
//	OTEL_TRACES_SAMPLER_ARG       — head-sampling ratio for ParentBasedTraceIDRatio. Default 1.0 (sample all).
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is empty, trace export is disabled
// (NeverSample) — useful for local docker-compose dev without an
// in-reach collector. Metrics still work; they're pull-mode.
package otelinit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc/stats"
)

// Provider bundles the lifetime objects produced by Init.
type Provider struct {
	tp           *sdktrace.TracerProvider
	mp           *sdkmetric.MeterProvider
	promReg      *prometheus.Registry
	serviceName  string
}

// Init builds and registers global TracerProvider + MeterProvider.
// The returned Provider exposes the integration points (handlers,
// middleware) used by the rest of the codebase.
//
// Service name precedence:
//  1. OTEL_SERVICE_NAME env (if set)
//  2. serviceName argument
//
// The k8s manifests stamp OTEL_SERVICE_NAME from the
// `app.kubernetes.io/name` pod label so every replica reports the
// same logical service.
func Init(ctx context.Context, serviceName string) (*Provider, error) {
	if env := os.Getenv("OTEL_SERVICE_NAME"); env != "" {
		serviceName = env
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceNamespace("banka-3"),
		),
		resource.WithFromEnv(),    // OTEL_RESOURCE_ATTRIBUTES
		resource.WithHost(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	// === Tracing ===
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://alloy.alloy:4317"
	}
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(headSampler()),
	}
	// Only attach an exporter when the endpoint is reachable in
	// principle. Empty OTEL_EXPORTER_OTLP_ENDPOINT (explicit unset)
	// means the operator wants traces disabled; we still build a
	// TracerProvider so otelhttp/otelgrpc no-op gracefully.
	if os.Getenv("OTEL_SDK_DISABLED") != "true" {
		exp, err := newTraceExporter(ctx, endpoint)
		if err != nil {
			return nil, fmt.Errorf("otel trace exporter: %w", err)
		}
		tpOpts = append(tpOpts,
			sdktrace.WithBatcher(exp,
				sdktrace.WithMaxQueueSize(2048),
				sdktrace.WithMaxExportBatchSize(512),
				sdktrace.WithBatchTimeout(5*time.Second),
			),
		)
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// === Metrics ===
	// A dedicated Prometheus registry — keeps the OTel metrics namespace
	// clean and avoids polluting prometheus.DefaultRegisterer with
	// process-collector cruft we don't want exported here.
	reg := prometheus.NewRegistry()
	promExp, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		return nil, fmt.Errorf("otel prom exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExp),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return &Provider{
		tp:          tp,
		mp:          mp,
		promReg:     reg,
		serviceName: serviceName,
	}, nil
}

// MetricsHandler returns the Prometheus scrape handler bound to the
// Provider's registry. Mount it on the probe server at /metrics so
// kube-prometheus-stack's PodMonitor can scrape without a second port.
func (p *Provider) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(p.promReg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// WrapHTTP wraps an http.Handler with otelhttp instrumentation. spanName
// is the root span's name when no other naming hint applies; otelhttp
// will use the matched route from the mux when possible.
func (p *Provider) WrapHTTP(h http.Handler, spanName string) http.Handler {
	return otelhttp.NewHandler(h, spanName,
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)
}

// GRPCServerHandler returns the gRPC stats handler that emits server-side
// spans + RED metrics. Pass it to grpc.NewServer via grpc.StatsHandler.
func (p *Provider) GRPCServerHandler() stats.Handler {
	return otelgrpc.NewServerHandler()
}

// GRPCClientHandler returns the gRPC client-side stats handler. Pass
// it to grpc.NewClient via grpc.WithStatsHandler so outbound calls
// continue the trace from the parent context.
func (p *Provider) GRPCClientHandler() stats.Handler {
	return otelgrpc.NewClientHandler()
}

// RunMetricsServer exposes /metrics on addr (e.g. ":9090") on its
// own listener — separate from the probe server so a failing probe
// doesn't black out the scrape. Blocks until ctx is cancelled. Use
// from an errgroup goroutine.
func (p *Provider) RunMetricsServer(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", p.MetricsHandler())
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// Shutdown flushes batched spans and closes exporters. Block on this
// during graceful shutdown so in-flight spans aren't lost.
func (p *Provider) Shutdown(ctx context.Context) error {
	// 5s budget split between providers.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.tp.Shutdown(ctx); err != nil {
		return err
	}
	return p.mp.Shutdown(ctx)
}

// headSampler reads OTEL_TRACES_SAMPLER_ARG (a float between 0 and 1)
// and returns a parent-based ratio sampler. Default 1.0 (sample
// everything) — fine until traffic scales; flip to 0.1 or lower from
// the env then.
func headSampler() sdktrace.Sampler {
	arg := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if arg == "" {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	ratio, err := strconv.ParseFloat(arg, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

func newTraceExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	// Strip http:// prefix — otlptracegrpc takes a plain host:port and
	// the TLS choice is via WithInsecure.
	addr := endpoint
	insecure := false
	switch {
	case len(addr) > 7 && addr[:7] == "http://":
		addr = addr[7:]
		insecure = true
	case len(addr) > 8 && addr[:8] == "https://":
		addr = addr[8:]
	}
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(addr),
	}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, opts...)
}
