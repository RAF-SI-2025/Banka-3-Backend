package grpcserver

import (
	"context"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"google.golang.org/grpc"
)

// TestValidationInterceptor_AcceptsValid confirms a well-formed proto
// passes through to the handler unchanged.
func TestValidationInterceptor_AcceptsValid(t *testing.T) {
	v, err := protovalidate.New()
	if err != nil {
		t.Fatalf("validator init: %v", err)
	}
	called := false
	handler := grpc.UnaryHandler(func(_ context.Context, _ any) (any, error) {
		called = true
		return &bankpb.PaymentResult{}, nil
	})
	intc := validationInterceptor(v)

	req := &bankpb.CreatePaymentRequest{
		FromAccountId:   "550e8400-e29b-41d4-a716-446655440000",
		ToAccountNumber: "333000100000000011",
		Amount:          "1000.00",
		RecipientName:   "Petar Petrović",
		PaymentCode:     "289",
		Purpose:         "Račun",
	}
	if _, err := intc(context.Background(), req, &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if !called {
		t.Error("handler not called on valid input")
	}
}

// TestValidationInterceptor_RejectsBadAccountNumber: spec p.16 18-digit
// shape rule must catch a typo'd account number before it reaches the
// service layer.
func TestValidationInterceptor_RejectsBadAccountNumber(t *testing.T) {
	v, _ := protovalidate.New()
	called := false
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	intc := validationInterceptor(v)

	req := &bankpb.CreatePaymentRequest{
		FromAccountId:   "550e8400-e29b-41d4-a716-446655440000",
		ToAccountNumber: "123", // too short
		Amount:          "100",
		RecipientName:   "X",
	}
	_, err := intc(context.Background(), req, &grpc.UnaryServerInfo{}, handler)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if called {
		t.Error("handler invoked despite invalid input")
	}
	var ae *apperr.Error
	if !errorAs(err, &ae) || ae.Kind != apperr.KindValidation {
		t.Errorf("error kind: %T %v, want apperr.Validation", err, err)
	}
	if !strings.Contains(err.Error(), "to_account_number") {
		t.Errorf("error message should name the field; got: %v", err)
	}
}

// TestValidationInterceptor_RejectsNonUUID: every id field is annotated
// as uuid; a stringy value must trip the rule.
func TestValidationInterceptor_RejectsNonUUID(t *testing.T) {
	v, _ := protovalidate.New()
	intc := validationInterceptor(v)
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) { return nil, nil })

	req := &bankpb.GetAccountRequest{Id: "not-a-uuid"}
	_, err := intc(context.Background(), req, &grpc.UnaryServerInfo{}, handler)
	if err == nil {
		t.Fatal("non-UUID id accepted")
	}
}

// TestValidationInterceptor_PaginationBounds: page_size > 200 must be
// rejected (spec-side: "default 50, max 200").
func TestValidationInterceptor_PaginationBounds(t *testing.T) {
	v, _ := protovalidate.New()
	intc := validationInterceptor(v)
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) { return nil, nil })

	req := &bankpb.ListAccountsRequest{Page: 1, PageSize: 999}
	_, err := intc(context.Background(), req, &grpc.UnaryServerInfo{}, handler)
	if err == nil {
		t.Error("page_size=999 accepted (max 200)")
	}
}

// TestValidationInterceptor_NonProtoUnaffected: handlers that take
// non-proto requests (rare; mostly streaming-internals) must not be
// blocked.
func TestValidationInterceptor_NonProtoUnaffected(t *testing.T) {
	v, _ := protovalidate.New()
	intc := validationInterceptor(v)
	called := false
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	if _, err := intc(context.Background(), "raw string", &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Errorf("non-proto blocked: %v", err)
	}
	if !called {
		t.Error("handler skipped for non-proto")
	}
}

// errorAs is a tiny wrapper because errors.As needs **T, and testing
// readability suffers when expanded inline. Local to this file.
func errorAs(err error, target **apperr.Error) bool {
	for err != nil {
		if e, ok := err.(*apperr.Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
