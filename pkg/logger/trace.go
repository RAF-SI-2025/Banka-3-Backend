package logger

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// traceHandler decorates an inner slog.Handler with trace_id / span_id
// attributes derived from the OpenTelemetry SpanContext on the record's
// context. The pair makes Grafana's Loki → Tempo derivedField regex
// (`trace_id=(\\w+)`) light up — click a log line, jump to the trace.
//
// The handler is a pass-through when the context carries no active
// span (e.g. boot-time logs, background goroutines without a parent
// span). Callers must use the *Context variants — slog.InfoContext,
// log.WarnContext, log.LogAttrs(ctx, ...) — or the trace ids are
// silently absent from that record.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
