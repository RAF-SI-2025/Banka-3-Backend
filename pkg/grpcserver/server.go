// Package grpcserver wraps grpc.Server with sensible defaults: panic
// recovery, slog request logging, apperr → gRPC status mapping, and
// graceful shutdown on context cancel.
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	authmw "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryInterceptor(log),
			loggingInterceptor(log),
			authmw.MetadataInterceptor(),
			errorMapInterceptor(),
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
