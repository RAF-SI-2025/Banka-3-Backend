package app

import (
	"context"
	"net/http"

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
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			err = status.Error(codes.Unavailable, "Servis trenutno nije dostupan. Pokušajte ponovo za par trenutaka.")
		}
		runtime.DefaultHTTPErrorHandler(ctx, mux, m, w, req, err)
	}
}
