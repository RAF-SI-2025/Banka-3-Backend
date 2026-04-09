package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"path"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const RequestIDKey = "x-request-id"

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func requestIDFromIncoming(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(RequestIDKey); len(v) > 0 && v[0] != "" {
			return v[0]
		}
	}
	return newRequestID()
}

// UnaryServerInterceptor logs every unary RPC and stashes a request-scoped
// logger in ctx so handlers can pull it via FromContext.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		reqID := requestIDFromIncoming(ctx)

		l := L().With(
			"request_id", reqID,
			"rpc_method", path.Base(info.FullMethod),
			"rpc_service", path.Dir(info.FullMethod)[1:],
		)
		ctx = WithContext(ctx, l)
		ctx = WithRequestID(ctx, reqID)

		l.DebugContext(ctx, "rpc start")
		resp, err := handler(ctx, req)
		dur := time.Since(start)

		code := status.Code(err).String()
		attrs := []any{"code", code, "duration_ms", dur.Milliseconds()}
		if err != nil {
			attrs = append(attrs, "err", err.Error())
			l.LogAttrs(ctx, slog.LevelError, "rpc end", toAttrs(attrs)...)
		} else {
			l.LogAttrs(ctx, slog.LevelInfo, "rpc end", toAttrs(attrs)...)
		}
		return resp, err
	}
}

// StreamServerInterceptor is the streaming counterpart.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		ctx := ss.Context()
		reqID := requestIDFromIncoming(ctx)

		l := L().With(
			"request_id", reqID,
			"rpc_method", path.Base(info.FullMethod),
			"rpc_service", path.Dir(info.FullMethod)[1:],
			"stream", true,
		)

		streamCtx := WithContext(ctx, l)
		streamCtx = WithRequestID(streamCtx, reqID)
		wrapped := &wrappedStream{ServerStream: ss, ctx: streamCtx}
		l.DebugContext(ctx, "stream start")
		err := handler(srv, wrapped)
		dur := time.Since(start)

		code := status.Code(err).String()
		if err != nil {
			l.ErrorContext(ctx, "stream end", "code", code, "duration_ms", dur.Milliseconds(), "err", err.Error())
		} else {
			l.InfoContext(ctx, "stream end", "code", code, "duration_ms", dur.Milliseconds())
		}
		return err
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// UnaryClientInterceptor forwards the request_id from ctx into outgoing metadata.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = injectRequestID(ctx)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptor is the streaming counterpart.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = injectRequestID(ctx)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func injectRequestID(ctx context.Context) context.Context {
	reqID, _ := ctx.Value(reqIDCtxKey{}).(string)
	if reqID == "" {
		reqID = newRequestID()
	}
	return metadata.AppendToOutgoingContext(ctx, RequestIDKey, reqID)
}

type reqIDCtxKey struct{}

// WithRequestID stores a request_id in ctx so client interceptors forward it.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, reqIDCtxKey{}, id)
}

// RequestIDFromContext returns the stored request_id, or "".
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(reqIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}

func toAttrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		out = append(out, slog.Any(k, kv[i+1]))
	}
	return out
}
