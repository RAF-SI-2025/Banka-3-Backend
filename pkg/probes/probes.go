// Package probes runs the Kubernetes liveness and readiness HTTP server.
//
// /healthz returns 200 once Server is running.
// /readyz  returns 200 only after every registered Check returns nil.
package probes

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// Check returns nil when the dependency is healthy.
type Check func(ctx context.Context) error

// Server is a probe HTTP server. The zero value is not usable; use [New].
type Server struct {
	addr   string
	mux    *http.ServeMux
	srv    *http.Server
	ready  atomic.Bool
	checks []namedCheck
}

type namedCheck struct {
	name  string
	check Check
}

// New constructs a probe server bound to addr (e.g. ":8081").
func New(addr string) *Server {
	s := &Server{addr: addr, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	return s
}

// MountMetrics exposes /metrics on the probe server. Pass the handler
// from observability.Observer.MetricsHandler(). Prometheus scrapes
// the same port as readyz/healthz so the dev-stack only needs one
// per-service hostname:port mapping.
func (s *Server) MountMetrics(h http.Handler) {
	s.mux.Handle("GET /metrics", h)
}

// Register adds a readiness check. Checks run in parallel on each /readyz
// request with a 2s timeout each.
func (s *Server) Register(name string, c Check) {
	s.checks = append(s.checks, namedCheck{name, c})
}

// MarkReady flips /readyz to start running checks. Until called, /readyz
// returns 503.
func (s *Server) MarkReady() { s.ready.Store(true) }

// ListenAndServe blocks until ctx is cancelled or the server errors.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := s.srv.ListenAndServe()
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
		_ = s.srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	for _, c := range s.checks {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := c.check(ctx)
		cancel()
		if err != nil {
			http.Error(w, c.name+": "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}
