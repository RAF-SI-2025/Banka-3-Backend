package app

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// accessLog wraps an http.Handler so every request emits one slog line
// with method, path, status, duration. Without this, the gateway pod
// only emits startup messages — when an alert fires there's nothing in
// Loki to show what request was in flight. otelhttp gives us traces
// and metrics but not log lines, and the FE folks queue questions in
// "what was happening on the gateway at 03:14" form, not "show me the
// trace for span X". Probe paths are skipped to keep Loki signal-rich.
//
// Status codes are captured via a wrapped ResponseWriter — Go's net/http
// doesn't expose the written status otherwise.
func accessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSilentPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		dur := time.Since(start)

		level := slog.LevelInfo
		switch {
		case sr.status >= 500:
			level = slog.LevelWarn
		case sr.status >= 400:
			level = slog.LevelInfo
		}
		log.LogAttrs(r.Context(), level, "http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sr.status),
			slog.Duration("dur", dur),
			slog.String("remote", clientIP(r)),
		)
	})
}

func isSilentPath(p string) bool {
	return p == "/healthz" || p == "/readyz" || strings.HasPrefix(p, "/_probe")
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return xff
	}
	return r.RemoteAddr
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}
