package app

import (
	"context"
	"net/http"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// unavailableFriendly returns a grpc-gateway error handler that
// rewrites Unavailable to a generic Serbian 503 message. The default
// handler forwards whatever the gRPC client emitted — for an
// unreachable upstream that's "name resolver error: produced zero
// addresses", which is opaque to a frontend user. Other codes fall
// through to the default handler unchanged.
func unavailableFriendly() runtime.ErrorHandlerFunc {
	return func(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, req *http.Request, err error) {
		log := logger.From(ctx)
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			// Log the raw dialer/upstream error before it's rewritten —
			// the friendly 503 body intentionally hides the detail the
			// on-call person needs.
			log.ErrorContext(
				ctx, "upstream unavailable",
				"err", err,
				"method", req.Method,
				"path", req.URL.Path,
			)
			err = status.Error(codes.Unavailable, "Servis trenutno nije dostupan. Pokušajte ponovo za par trenutaka.")
		} else if st != nil {
			if httpCode := runtime.HTTPStatusFromCode(st.Code()); httpCode >= 500 {
				log.ErrorContext(
					ctx, "upstream grpc call failed",
					"err", err,
					"code", st.Code().String(),
					"status", httpCode,
					"method", req.Method,
					"path", req.URL.Path,
				)
			}
		}
		runtime.DefaultHTTPErrorHandler(ctx, mux, m, w, req, err)
	}
}
