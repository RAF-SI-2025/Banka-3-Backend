// Package app wires the gateway: config → upstream gRPC clients →
// HTTP server with REST mux + probes.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	"golang.org/x/sync/errgroup"
)

// Run blocks until shutdown.
func Run() error {
	log := logger.New("gateway")

	ctx, cancel := shutdown.Context()
	defer cancel()

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))

	mux := http.NewServeMux()
	// TODO: register grpc-gateway runtime mux at /api/ once protos are
	// generated. For now, expose a placeholder.
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	httpAddr := fmt.Sprintf(":%d", config.Int("HTTP_PORT", 8080))
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		log.Info("http listening", "addr", httpAddr)
		errCh := make(chan error, 1)
		go func() {
			err := httpSrv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
		select {
		case <-gctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return httpSrv.Shutdown(shutdownCtx)
		case err := <-errCh:
			return err
		}
	})

	probeSrv.MarkReady()
	log.Info("gateway ready")

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
