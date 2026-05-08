// Package apperr defines typed application errors and their mapping to
// gRPC status codes. The service layer returns these; an interceptor in
// pkg/grpcserver translates them at the gRPC boundary.
package apperr

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Kind classifies application errors. Each Kind has a stable mapping to
// a gRPC status code.
type Kind int

const (
	KindInternal Kind = iota
	KindNotFound
	KindConflict
	KindValidation
	KindUnauthenticated
	KindPermissionDenied
	KindFailedPrecondition
	KindUnavailable
)

// Error is the application's unified error type. Cause is wrapped (use
// errors.Is / errors.As to traverse).
type Error struct {
	Kind    Kind
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// String returns a stable identifier suitable for logging/metrics.
func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindValidation:
		return "validation"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindPermissionDenied:
		return "permission_denied"
	case KindFailedPrecondition:
		return "failed_precondition"
	case KindUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// Constructors. Pick the one that fits, wrap the underlying cause if
// any.

func NotFound(msg string) error { return &Error{Kind: KindNotFound, Message: msg} }
func Conflict(msg string) error { return &Error{Kind: KindConflict, Message: msg} }
func Validation(msg string) error {
	return &Error{Kind: KindValidation, Message: msg}
}
func Unauthenticated(msg string) error {
	return &Error{Kind: KindUnauthenticated, Message: msg}
}
func PermissionDenied(msg string) error {
	return &Error{Kind: KindPermissionDenied, Message: msg}
}
func FailedPrecondition(msg string) error {
	return &Error{Kind: KindFailedPrecondition, Message: msg}
}
func Internal(msg string, cause error) error {
	return &Error{Kind: KindInternal, Message: msg, Cause: cause}
}

// ToGRPC maps an application error to a gRPC status. Non-application
// errors are returned as Internal to avoid leaking implementation
// details.
func ToGRPC(err error) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if !errors.As(err, &ae) {
		return status.Error(codes.Internal, "internal error")
	}
	return status.Error(toCode(ae.Kind), ae.Message)
}

func toCode(k Kind) codes.Code {
	switch k {
	case KindNotFound:
		return codes.NotFound
	case KindConflict:
		return codes.AlreadyExists
	case KindValidation:
		return codes.InvalidArgument
	case KindUnauthenticated:
		return codes.Unauthenticated
	case KindPermissionDenied:
		return codes.PermissionDenied
	case KindFailedPrecondition:
		return codes.FailedPrecondition
	case KindUnavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}
