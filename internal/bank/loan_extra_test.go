package bank

import (
	"context"
	"testing"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetLoanByNumber_MissingFields(t *testing.T) {
	server, _, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: ""})
	assert.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	_, err = server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "test", LoanNumber: ""})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "test", LoanNumber: "abc"})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateLoanRequest_InvalidArgs(t *testing.T) {
	server, _, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	tests := []struct {
		req  *bankpb.CreateLoanRequestRequest
		code codes.Code
	}{
		{&bankpb.CreateLoanRequestRequest{ClientEmail: ""}, codes.Unauthenticated},
		{&bankpb.CreateLoanRequestRequest{ClientEmail: "a", AccountNumber: ""}, codes.InvalidArgument},
		{&bankpb.CreateLoanRequestRequest{ClientEmail: "a", AccountNumber: "123", Currency: ""}, codes.InvalidArgument},
		{&bankpb.CreateLoanRequestRequest{ClientEmail: "a", AccountNumber: "123", Currency: "RSD", Amount: 0}, codes.InvalidArgument},
		{&bankpb.CreateLoanRequestRequest{ClientEmail: "a", AccountNumber: "123", Currency: "RSD", Amount: 10, RepaymentPeriod: 0}, codes.InvalidArgument},
	}

	for _, tt := range tests {
		_, err := server.CreateLoanRequest(context.Background(), tt.req)
		assert.Error(t, err)
		assert.Equal(t, tt.code, status.Code(err))
	}
}
