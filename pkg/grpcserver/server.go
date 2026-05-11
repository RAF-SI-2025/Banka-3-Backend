// Package grpcserver wraps grpc.Server with sensible defaults: panic
// recovery, slog request logging, buf-validate request validation,
// apperr → gRPC status mapping, and graceful shutdown on context
// cancel.
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	"buf.build/go/protovalidate"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	authmw "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Run starts a gRPC server bound to addr. register is called with the
// server before listen so the caller can register services and
// reflection. Run blocks until ctx is cancelled, then performs a graceful
// stop with a 30s timeout.
func Run(ctx context.Context, log *slog.Logger, addr string, register func(*grpc.Server)) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	validator, err := protovalidate.New()
	if err != nil {
		return fmt.Errorf("init protovalidate: %w", err)
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryInterceptor(log),
			loggingInterceptor(log),
			authmw.MetadataInterceptor(),
			// errorMap MUST sit outside validation: protovalidate
			// failures short-circuit before the handler runs, returning
			// apperr.Validation. If errorMap were inner (the previous
			// ordering), those returns bypassed apperr → grpc-status
			// translation and reached the gateway as code=Unknown(2) /
			// HTTP 500 instead of InvalidArgument / HTTP 400. Finding 3
			// from the 2026-05-11 soak audit.
			errorMapInterceptor(),
			validationInterceptor(validator),
		),
	)
	register(srv)

	errCh := make(chan error, 1)
	go func() {
		log.Info("grpc server listening", "addr", addr)
		if err := srv.Serve(lis); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Info("grpc server stopping")
		stopped := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(30 * time.Second):
			log.Warn("grpc graceful stop timed out, forcing")
			srv.Stop()
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic in grpc handler",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(debug.Stack()))
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func loggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		log.LogAttrs(ctx, levelFor(err), "grpc",
			slog.String("method", info.FullMethod),
			slog.Duration("dur", time.Since(start)),
			slog.String("code", status.Code(err).String()),
		)
		return resp, err
	}
}

// validationInterceptor runs buf-validate rules on every incoming
// request message. Failures are returned as apperr.Validation so the
// adjacent errorMapInterceptor maps them to InvalidArgument with the
// concrete reason in the message — that's what the FE surfaces to the
// user, so it must be specific (e.g. "from_account_id: must be a
// valid UUID") rather than generic "invalid input".
//
// Sits *between* auth-metadata and errorMap so the principal is on the
// context (useful for log correlation) and our error envelope is
// applied uniformly.
func validationInterceptor(v protovalidate.Validator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if pm, ok := req.(proto.Message); ok {
			if err := v.Validate(pm); err != nil {
				return nil, apperr.Validation(err.Error())
			}
		}
		return handler(ctx, req)
	}
}

// errorMapInterceptor sits closest to the handler so apperr values are
// translated to gRPC status before any outer interceptor (logging, etc.)
// observes the result.
func errorMapInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		return resp, apperr.ToGRPC(err)
	}
}

func levelFor(err error) slog.Level {
	if err == nil {
		return slog.LevelInfo
	}
	switch status.Code(err) {
	case codes.OK, codes.NotFound, codes.AlreadyExists, codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated:
		return slog.LevelInfo
	default:
		return slog.LevelError
	}
}
