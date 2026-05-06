package bank

import (
	"context"
	"net/http"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
)

// StartDebugHTTP exposes the cron entrypoints over plain HTTP so cypress can
// trigger them on demand. Only listens when BANK_DEBUG_HTTP_PORT is set —
// production deployments leave it unset and the listener never binds.
//
// Security note: routes are unauthenticated by design. The gating mechanism
// is "the port isn't exposed", not "the handler checks a token". Setting the
// env var in any environment that's reachable from outside the docker
// network is a misconfiguration.
func (s *Server) StartDebugHTTP(port string) func() {
	if port == "" {
		return func() {}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/cron/used-limit-reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		s.RunDailyUsedLimitReset()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/debug/cron/capital-gains", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		// `?period=YYYY-MM` lets cypress target a specific month; the cron
		// entrypoint itself uses time.Now() but the seeded unpaid rows live
		// in a fixed past period. Empty falls back to the cron entrypoint
		// so callers that want the natural current-month behavior still get
		// it.
		if period := r.URL.Query().Get("period"); period != "" {
			if _, err := s.CollectCapitalGains(period); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			s.RunMonthlyCapitalGainsCollection()
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.L().Info("debug HTTP listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L().Error("debug HTTP failed", "err", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
