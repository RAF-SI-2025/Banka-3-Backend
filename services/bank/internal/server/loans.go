package server

import (
	"context"
	"log/slog"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) SubmitLoanRequest(ctx context.Context, in *bankpb.SubmitLoanRequestRequest) (*bankpb.LoanRequest, error) {
	r, err := s.Svc.SubmitLoanRequest(ctx, service.SubmitLoanRequestInput{
		AccountID:                in.GetAccountId(),
		LoanType:                 loanTypeFromProto(in.GetLoanType()),
		InterestType:             interestTypeFromProto(in.GetInterestType()),
		Amount:                   in.GetAmount(),
		Currency:                 currencyFromProto(in.GetCurrency()),
		Purpose:                  in.GetPurpose(),
		MonthlySalary:            in.GetMonthlySalary(),
		EmploymentStatus:         employmentStatusFromProto(in.GetEmploymentStatus()),
		EmploymentDurationMonths: int(in.GetEmploymentDurationMonths()),
		InstallmentsTotal:        int(in.GetInstallmentsTotal()),
		ContactPhone:             in.GetContactPhone(),
	})
	if err != nil {
		return nil, err
	}
	return loanRequestToProto(r), nil
}

func (s *Server) ListLoanRequests(ctx context.Context, in *bankpb.ListLoanRequestsRequest) (*bankpb.ListLoanRequestsResponse, error) {
	rs, total, err := s.Svc.ListLoanRequests(ctx, domain.LoanRequestFilter{
		Status:    loanRequestStatusFromProto(in.GetStatus()),
		LoanType:  loanTypeFromProto(in.GetLoanType()),
		AccountID: in.GetAccountId(),
	}, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.LoanRequest, 0, len(rs))
	for _, r := range rs {
		out = append(out, loanRequestToProto(r))
	}
	page, ps := int(in.GetPage()), int(in.GetPageSize())
	if page < 1 {
		page = 1
	}
	if ps < 1 {
		ps = 50
	}
	return &bankpb.ListLoanRequestsResponse{Requests: out, Page: int32(page), PageSize: int32(ps), Total: total}, nil
}

func (s *Server) DecideLoanRequest(ctx context.Context, in *bankpb.DecideLoanRequestRequest) (*bankpb.LoanRequest, error) {
	r, err := s.Svc.DecideLoanRequest(ctx, in.GetId(), in.GetApprove(), in.GetReason())
	if err != nil {
		return nil, err
	}
	return loanRequestToProto(r), nil
}

func (s *Server) ListLoans(ctx context.Context, in *bankpb.ListLoansRequest) (*bankpb.ListLoansResponse, error) {
	ls, total, err := s.Svc.ListLoans(ctx, domain.LoanFilter{
		ClientID:  in.GetClientId(),
		AccountID: in.GetAccountId(),
		LoanType:  loanTypeFromProto(in.GetLoanType()),
		Status:    loanStatusFromProto(in.GetStatus()),
	}, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.Loan, 0, len(ls))
	for _, l := range ls {
		out = append(out, loanToProto(l))
	}
	page, ps := int(in.GetPage()), int(in.GetPageSize())
	if page < 1 {
		page = 1
	}
	if ps < 1 {
		ps = 50
	}
	return &bankpb.ListLoansResponse{Loans: out, Page: int32(page), PageSize: int32(ps), Total: total}, nil
}

func (s *Server) GetLoan(ctx context.Context, in *bankpb.GetLoanRequest) (*bankpb.LoanWithInstallments, error) {
	l, ins, err := s.Svc.GetLoan(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	out := &bankpb.LoanWithInstallments{Loan: loanToProto(l)}
	for _, i := range ins {
		out.Installments = append(out.Installments, installmentToProto(i))
	}
	return out, nil
}

func (s *Server) RunInstallmentJob(ctx context.Context, in *bankpb.RunInstallmentJobRequest) (*bankpb.RunInstallmentJobResponse, error) {
	dueOn := time.Time{}
	if s := in.GetDueOn(); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, apperr.Validation("due_on must be YYYY-MM-DD")
		}
		dueOn = t
	}
	r, err := s.Svc.RunInstallmentJob(ctx, dueOn)
	if err != nil {
		return nil, err
	}
	return &bankpb.RunInstallmentJobResponse{
		Processed: int32(r.Processed),
		Paid:      int32(r.Paid),
		Overdue:   int32(r.Overdue),
	}, nil
}

func (s *Server) RunVariableRateJob(ctx context.Context, _ *bankpb.RunVariableRateJobRequest) (*bankpb.RunVariableRateJobResponse, error) {
	r, err := s.Svc.RunVariableRateJob(ctx)
	if err != nil {
		return nil, err
	}
	return &bankpb.RunVariableRateJobResponse{Updated: int32(r.Updated)}, nil
}

// =====================================================================
// Conversions
// =====================================================================

func loanRequestToProto(r *domain.LoanRequest) *bankpb.LoanRequest {
	out := &bankpb.LoanRequest{
		Id:                       r.ID,
		ClientId:                 r.ClientID,
		AccountId:                r.AccountID,
		LoanType:                 loanTypeToProto(r.LoanType),
		InterestType:             interestTypeToProto(r.InterestType),
		Amount:                   r.Amount,
		Currency:                 currencyToProto(r.Currency),
		Purpose:                  r.Purpose,
		MonthlySalary:            r.MonthlySalary,
		EmploymentStatus:         employmentStatusToProto(r.EmploymentStatus),
		EmploymentDurationMonths: int32(r.EmploymentDurationMonths),
		InstallmentsTotal:        int32(r.InstallmentsTotal),
		ContactPhone:             r.ContactPhone,
		Status:                   loanRequestStatusToProto(r.Status),
		RejectionReason:          r.RejectionReason,
		DecidedByEmployeeId:      r.DecidedByEmployeeID,
		CreatedAt:                timestamppb.New(r.CreatedAt),
	}
	if r.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*r.DecidedAt)
	}
	return out
}

func loanToProto(l *domain.Loan) *bankpb.Loan {
	// effective_rate = base + offset + margin (annual %).
	effective := ""
	if l.BaseRate != "" {
		base, err := money.Parse(l.BaseRate)
		if err != nil {
			slog.Warn("parse loan base rate failed", "err", err, "loan_id", l.ID)
		}
		offset, err := money.Parse(l.CurrentOffset)
		if err != nil {
			slog.Warn("parse loan rate offset failed", "err", err, "loan_id", l.ID)
		}
		margin, err := money.Parse(l.Margin)
		if err != nil {
			slog.Warn("parse loan margin failed", "err", err, "loan_id", l.ID)
		}
		effective = money.Format(money.Add(money.Add(base, offset), margin), 4)
	}
	out := &bankpb.Loan{
		Id:                    l.ID,
		LoanNumber:            l.LoanNumber,
		RequestId:             l.RequestID,
		ClientId:              l.ClientID,
		AccountId:             l.AccountID,
		LoanType:              loanTypeToProto(l.LoanType),
		InterestType:          interestTypeToProto(l.InterestType),
		Principal:             l.Principal,
		Currency:              currencyToProto(l.Currency),
		BaseRate:              l.BaseRate,
		Margin:                l.Margin,
		CurrentOffset:         l.CurrentOffset,
		EffectiveRate:         effective,
		InstallmentsTotal:     int32(l.InstallmentsTotal),
		InstallmentAmount:     l.InstallmentAmount,
		RemainingPrincipal:    l.RemainingPrincipal,
		NextInstallmentAmount: l.NextInstallmentAmount,
		Status:                loanStatusToProto(l.Status),
		ContractedAt:          timestamppb.New(l.ContractedAt),
	}
	if l.NextInstallmentDate != nil {
		out.NextInstallmentDate = l.NextInstallmentDate.Format("2006-01-02")
	}
	if l.MaturesAt != nil {
		out.MaturesAt = l.MaturesAt.Format("2006-01-02")
	}
	return out
}

func installmentToProto(i *domain.LoanInstallment) *bankpb.LoanInstallment {
	out := &bankpb.LoanInstallment{
		Id:                i.ID,
		LoanId:            i.LoanID,
		SequenceNumber:    int32(i.SequenceNumber),
		Amount:            i.Amount,
		InterestRateAtDue: i.InterestRateAtDue,
		Currency:          currencyToProto(i.Currency),
		ExpectedDueDate:   i.ExpectedDueDate.Format("2006-01-02"),
		Status:            installmentStatusToProto(i.Status),
	}
	if i.ActualPaidAt != nil {
		out.ActualPaidAt = timestamppb.New(*i.ActualPaidAt)
	}
	return out
}

// =====================================================================
// Enum conversions
// =====================================================================

func loanTypeToProto(t domain.LoanType) bankpb.LoanType {
	switch t {
	case domain.LoanTypeCash:
		return bankpb.LoanType_LOAN_TYPE_CASH
	case domain.LoanTypeHousing:
		return bankpb.LoanType_LOAN_TYPE_HOUSING
	case domain.LoanTypeAuto:
		return bankpb.LoanType_LOAN_TYPE_AUTO
	case domain.LoanTypeRefinance:
		return bankpb.LoanType_LOAN_TYPE_REFINANCE
	case domain.LoanTypeStudent:
		return bankpb.LoanType_LOAN_TYPE_STUDENT
	}
	return bankpb.LoanType_LOAN_TYPE_UNSPECIFIED
}

func loanTypeFromProto(t bankpb.LoanType) domain.LoanType {
	switch t {
	case bankpb.LoanType_LOAN_TYPE_CASH:
		return domain.LoanTypeCash
	case bankpb.LoanType_LOAN_TYPE_HOUSING:
		return domain.LoanTypeHousing
	case bankpb.LoanType_LOAN_TYPE_AUTO:
		return domain.LoanTypeAuto
	case bankpb.LoanType_LOAN_TYPE_REFINANCE:
		return domain.LoanTypeRefinance
	case bankpb.LoanType_LOAN_TYPE_STUDENT:
		return domain.LoanTypeStudent
	}
	return ""
}

func interestTypeToProto(t domain.InterestType) bankpb.InterestType {
	switch t {
	case domain.InterestFixed:
		return bankpb.InterestType_INTEREST_TYPE_FIXED
	case domain.InterestVariable:
		return bankpb.InterestType_INTEREST_TYPE_VARIABLE
	}
	return bankpb.InterestType_INTEREST_TYPE_UNSPECIFIED
}

func interestTypeFromProto(t bankpb.InterestType) domain.InterestType {
	switch t {
	case bankpb.InterestType_INTEREST_TYPE_FIXED:
		return domain.InterestFixed
	case bankpb.InterestType_INTEREST_TYPE_VARIABLE:
		return domain.InterestVariable
	}
	return ""
}

func employmentStatusToProto(s domain.EmploymentStatus) bankpb.EmploymentStatus {
	switch s {
	case domain.EmploymentPermanent:
		return bankpb.EmploymentStatus_EMPLOYMENT_STATUS_PERMANENT
	case domain.EmploymentTemporary:
		return bankpb.EmploymentStatus_EMPLOYMENT_STATUS_TEMPORARY
	case domain.EmploymentUnemployed:
		return bankpb.EmploymentStatus_EMPLOYMENT_STATUS_UNEMPLOYED
	}
	return bankpb.EmploymentStatus_EMPLOYMENT_STATUS_UNSPECIFIED
}

func employmentStatusFromProto(s bankpb.EmploymentStatus) domain.EmploymentStatus {
	switch s {
	case bankpb.EmploymentStatus_EMPLOYMENT_STATUS_PERMANENT:
		return domain.EmploymentPermanent
	case bankpb.EmploymentStatus_EMPLOYMENT_STATUS_TEMPORARY:
		return domain.EmploymentTemporary
	case bankpb.EmploymentStatus_EMPLOYMENT_STATUS_UNEMPLOYED:
		return domain.EmploymentUnemployed
	}
	return ""
}

func loanRequestStatusToProto(s domain.LoanRequestStatus) bankpb.LoanRequestStatus {
	switch s {
	case domain.RequestPending:
		return bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_PENDING
	case domain.RequestApproved:
		return bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_APPROVED
	case domain.RequestRejected:
		return bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_REJECTED
	}
	return bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_UNSPECIFIED
}

func loanRequestStatusFromProto(s bankpb.LoanRequestStatus) domain.LoanRequestStatus {
	switch s {
	case bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_PENDING:
		return domain.RequestPending
	case bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_APPROVED:
		return domain.RequestApproved
	case bankpb.LoanRequestStatus_LOAN_REQUEST_STATUS_REJECTED:
		return domain.RequestRejected
	}
	return ""
}

func loanStatusToProto(s domain.LoanStatus) bankpb.LoanStatus {
	switch s {
	case domain.LoanApproved:
		return bankpb.LoanStatus_LOAN_STATUS_APPROVED
	case domain.LoanRejected:
		return bankpb.LoanStatus_LOAN_STATUS_REJECTED
	case domain.LoanPaidOff:
		return bankpb.LoanStatus_LOAN_STATUS_PAID_OFF
	case domain.LoanOverdue:
		return bankpb.LoanStatus_LOAN_STATUS_OVERDUE
	}
	return bankpb.LoanStatus_LOAN_STATUS_UNSPECIFIED
}

func loanStatusFromProto(s bankpb.LoanStatus) domain.LoanStatus {
	switch s {
	case bankpb.LoanStatus_LOAN_STATUS_APPROVED:
		return domain.LoanApproved
	case bankpb.LoanStatus_LOAN_STATUS_REJECTED:
		return domain.LoanRejected
	case bankpb.LoanStatus_LOAN_STATUS_PAID_OFF:
		return domain.LoanPaidOff
	case bankpb.LoanStatus_LOAN_STATUS_OVERDUE:
		return domain.LoanOverdue
	}
	return ""
}

func installmentStatusToProto(s domain.InstallmentStatus) bankpb.InstallmentStatus {
	switch s {
	case domain.InstallmentPaid:
		return bankpb.InstallmentStatus_INSTALLMENT_STATUS_PAID
	case domain.InstallmentUnpaid:
		return bankpb.InstallmentStatus_INSTALLMENT_STATUS_UNPAID
	case domain.InstallmentOverdue:
		return bankpb.InstallmentStatus_INSTALLMENT_STATUS_OVERDUE
	}
	return bankpb.InstallmentStatus_INSTALLMENT_STATUS_UNSPECIFIED
}
