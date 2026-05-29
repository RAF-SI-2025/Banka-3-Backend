package domain

import "time"

// =====================================================================
// Loan request
// =====================================================================

type LoanType string

const (
	LoanTypeCash      LoanType = "cash"
	LoanTypeHousing   LoanType = "housing"
	LoanTypeAuto      LoanType = "auto"
	LoanTypeRefinance LoanType = "refinance"
	LoanTypeStudent   LoanType = "student"
)

type InterestType string

const (
	InterestFixed    InterestType = "fixed"
	InterestVariable InterestType = "variable"
)

type EmploymentStatus string

const (
	EmploymentPermanent  EmploymentStatus = "permanent"
	EmploymentTemporary  EmploymentStatus = "temporary"
	EmploymentUnemployed EmploymentStatus = "unemployed"
)

type LoanRequestStatus string

const (
	RequestPending  LoanRequestStatus = "pending"
	RequestApproved LoanRequestStatus = "approved"
	RequestRejected LoanRequestStatus = "rejected"
)

type LoanRequest struct {
	ID                       string
	ClientID                 string
	AccountID                string
	LoanType                 LoanType
	InterestType             InterestType
	Amount                   string
	Currency                 Currency
	Purpose                  string
	MonthlySalary            string
	EmploymentStatus         EmploymentStatus
	EmploymentDurationMonths int
	InstallmentsTotal        int
	ContactPhone             string
	Status                   LoanRequestStatus
	RejectionReason          string
	DecidedAt                *time.Time
	DecidedByEmployeeID      string
	CreatedAt                time.Time
}

// =====================================================================
// Loan
// =====================================================================

type LoanStatus string

const (
	LoanApproved LoanStatus = "approved"
	LoanRejected LoanStatus = "rejected"
	LoanPaidOff  LoanStatus = "paid_off"
	LoanOverdue  LoanStatus = "overdue"
)

type Loan struct {
	ID                    string
	LoanNumber            string
	RequestID             string
	ClientID              string
	AccountID             string
	LoanType              LoanType
	InterestType          InterestType
	Principal             string
	Currency              Currency
	BaseRate              string // annual %, decimal string (e.g. "6.2500")
	Margin                string
	CurrentOffset         string
	InstallmentsTotal     int
	InstallmentAmount     string
	RemainingPrincipal    string
	NextInstallmentDate   *time.Time
	NextInstallmentAmount string
	Status                LoanStatus
	// LatePenaltyApplied is set true after the +0.05% bump has been
	// added to BaseRate following a 72h retry failure (spec p.35).
	// Idempotency flag — never re-applied for the same loan.
	LatePenaltyApplied bool
	ContractedAt       time.Time
	MaturesAt          *time.Time
}

// EffectiveRate returns base + offset + margin, summed in callers via
// pkg/money. We don't store it (avoids drift); the server-layer
// computes it for the proto response.
func (l *Loan) EffectiveRateInputs() (base, offset, margin string) {
	return l.BaseRate, l.CurrentOffset, l.Margin
}

// =====================================================================
// Installment
// =====================================================================

type InstallmentStatus string

const (
	InstallmentPaid    InstallmentStatus = "paid"
	InstallmentUnpaid  InstallmentStatus = "unpaid"
	InstallmentOverdue InstallmentStatus = "overdue"
)

type LoanInstallment struct {
	ID                string
	LoanID            string
	SequenceNumber    int
	Amount            string
	InterestRateAtDue string
	Currency          Currency
	ExpectedDueDate   time.Time
	ActualPaidAt      *time.Time
	// OverdueSince is the timestamp of the first failed debit attempt;
	// the daily cron uses it to schedule the spec p.35 72h retry. NULL
	// for installments that have either never been attempted or were
	// paid on the first try.
	OverdueSince *time.Time
	Status       InstallmentStatus
}

// =====================================================================
// Filters
// =====================================================================

type LoanRequestFilter struct {
	Status    LoanRequestStatus
	LoanType  LoanType
	AccountID string
	ClientID  string
}

type LoanFilter struct {
	ClientID  string
	AccountID string
	LoanType  LoanType
	Status    LoanStatus
}
