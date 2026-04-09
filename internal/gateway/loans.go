package gateway

import (
	"context"
	"net/http"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) getAuthenticatedEmployeeID(c *gin.Context) (int64, bool) {
	email := c.GetString("email")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetEmployeeByEmail(ctx, &userpb.GetEmployeeByEmailRequest{
		Email: email,
	})
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "authenticated user is not an employee",
		})
		return 0, false
	}

	return resp.Id, true
}

func loanListResponse(resp *bankpb.GetLoansResponse) []gin.H {
	loans := make([]gin.H, 0, len(resp.Loans))
	for _, loan := range resp.Loans {
		loans = append(loans, gin.H{
			"loan_number":             loan.LoanNumber,
			"loan_type":               loan.LoanType,
			"account_number":          loan.AccountNumber,
			"loan_amount":             loan.LoanAmount,
			"repayment_period":        loan.RepaymentPeriod,
			"nominal_rate":            loan.NominalRate,
			"effective_rate":          loan.EffectiveRate,
			"agreement_date":          loan.AgreementDate,
			"maturity_date":           loan.MaturityDate,
			"next_installment_amount": loan.NextInstallmentAmount,
			"next_installment_date":   loan.NextInstallmentDate,
			"remaining_debt":          loan.RemainingDebt,
			"currency":                loan.Currency,
			"status":                  loan.Status,
		})
	}
	return loans
}

func (s *Server) GetLoans(c *gin.Context) {
	var query getLoansQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	email := c.GetString("email")
	l := logger.FromContext(ctx).With("email", email)

	// Try employee first; if not an employee, fall back to client view
	_, err := s.UserClient.GetEmployeeByEmail(ctx, &userpb.GetEmployeeByEmailRequest{Email: email})
	if err == nil {
		resp, err := s.BankClient.GetAllLoans(ctx, &bankpb.GetAllLoansRequest{
			LoanType:      query.LoanType,
			AccountNumber: query.AccountNumber,
			Status:        query.Status,
		})
		if err != nil {
			writeGRPCError(c, err)
			return
		}
		l.InfoContext(ctx, "loans listed (employee)", "count", len(resp.Loans))
		c.JSON(http.StatusOK, loanListResponse(resp))
		return
	}

	// If the user service returned something other than NotFound, it's a real error
	if code := status.Code(err); code != codes.NotFound {
		writeGRPCError(c, err)
		return
	}

	// Client view
	resp, err := s.BankClient.GetLoans(ctx, &bankpb.GetLoansRequest{
		ClientEmail:   email,
		LoanType:      query.LoanType,
		AccountNumber: query.AccountNumber,
		Status:        query.Status,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	l.InfoContext(ctx, "loans listed (client)", "count", len(resp.Loans))
	c.JSON(http.StatusOK, loanListResponse(resp))
}

func (s *Server) GetLoanByNumber(c *gin.Context) {
	var uri getLoanByNumberURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "loan number is required")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	email := c.GetString("email")

	resp, err := s.BankClient.GetLoanByNumber(ctx, &bankpb.GetLoanByNumberRequest{
		ClientEmail: email,
		LoanNumber:  uri.LoanNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"loan_number":             resp.LoanNumber,
		"loan_type":               resp.LoanType,
		"account_number":          resp.AccountNumber,
		"loan_amount":             resp.LoanAmount,
		"repayment_period":        resp.RepaymentPeriod,
		"nominal_rate":            resp.NominalRate,
		"effective_rate":          resp.EffectiveRate,
		"agreement_date":          resp.AgreementDate,
		"maturity_date":           resp.MaturityDate,
		"next_installment_amount": resp.NextInstallmentAmount,
		"next_installment_date":   resp.NextInstallmentDate,
		"remaining_debt":          resp.RemainingDebt,
		"currency":                resp.Currency,
		"status":                  resp.Status,
	})
}

func (s *Server) CreateLoanRequest(c *gin.Context) {
	var req createLoanRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.BankClient.CreateLoanRequest(ctx, &bankpb.CreateLoanRequestRequest{
		ClientEmail:      c.GetString("email"),
		AccountNumber:    req.AccountNumber,
		LoanType:         req.LoanType,
		Amount:           req.Amount,
		RepaymentPeriod:  req.RepaymentPeriod,
		Currency:         req.Currency,
		Purpose:          req.Purpose,
		Salary:           req.Salary,
		EmploymentStatus: req.EmploymentStatus,
		EmploymentPeriod: req.EmploymentPeriod,
		PhoneNumber:      req.PhoneNumber,
		InterestRateType: req.InterestRateType,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

func (s *Server) GetLoanRequests(c *gin.Context) {
	_, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	var query getLoanRequestsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetLoanRequests(ctx, &bankpb.GetLoanRequestsRequest{
		LoanType:      query.LoanType,
		AccountNumber: query.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	requests := make([]gin.H, 0, len(resp.LoanRequests))
	for _, r := range resp.LoanRequests {
		requests = append(requests, gin.H{
			"id":              r.Id,
			"status":          r.Status,
			"loan_type":       r.LoanType,
			"loan_amount":     r.Amount,
			"purpose":         r.Purpose,
			"account_number":  r.AccountNumber,
			"submission_date": r.SubmissionDate,
		})
	}

	c.JSON(http.StatusOK, requests)
}

func (s *Server) ApproveLoanRequest(c *gin.Context) {
	_, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	var uri loanRequestIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "loan request id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.BankClient.ApproveLoanRequest(ctx, &bankpb.ApproveLoanRequestRequest{
		Id: uri.ID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusOK)
}

func (s *Server) RejectLoanRequest(c *gin.Context) {
	_, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	var uri loanRequestIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "loan request id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.BankClient.RejectLoanRequest(ctx, &bankpb.RejectLoanRequestRequest{
		Id: uri.ID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusOK)
}
