package gateway

type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type getEmployeeByIDURI struct {
	EmployeeID int64 `uri:"id" binding:"required"`
}

type clientByIDURI struct {
	ClientID int64 `uri:"id" binding:"required"`
}

type companyByIDURI struct {
	CompanyID int64 `uri:"id" binding:"required"`
}

type passwordResetRequestRequest struct {
	Email string `json:"email" binding:"required"`
}

type passwordResetConfirmationRequest struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"password" binding:"required"`
}

type getEmployeesQuery struct {
	Email     string `form:"email"`
	FirstName string `form:"first_name"`
	LastName  string `form:"last_name"`
	Position  string `form:"position"`
}

type updateEmployeeURI struct {
	EmployeeID int64 `uri:"id" binding:"required"`
}

type updateEmployeeRequest struct {
	FirstName   string   `json:"first_name"`
	LastName    string   `json:"last_name"`
	Gender      string   `json:"gender"`
	PhoneNumber string   `json:"phone_number"`
	Address     string   `json:"address"`
	Position    string   `json:"position"`
	Department  string   `json:"department"`
	Active      bool     `json:"active"`
	Permissions []string `json:"permissions"`
}

type createClientAccountRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	DateOfBirth int64  `json:"date_of_birth"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required"`
	PhoneNumber string `json:"phone_number"`
	Address     string `json:"address"`
	Password    string `json:"password"`
}

type updateClientRequest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	DateOfBirth int64  `json:"date_of_birth"`
	Gender      string `json:"gender"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
	Address     string `json:"address"`
}

type createEmployeeAccountRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	BirthDate   int64  `json:"birth_date"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required"`
	PhoneNumber string `json:"phone_number"`
	Address     string `json:"address"`
	Username    string `json:"username" binding:"required"`
	Position    string `json:"position"`
	Department  string `json:"department"`
	Password    string `json:"password"`
}

type createLoanRequestRequest struct {
	AccountNumber    string  `json:"account_number" binding:"required"`
	LoanType         string  `json:"loan_type" binding:"required"`
	Amount           float64 `json:"amount" binding:"required"`
	RepaymentPeriod  int32   `json:"repayment_period" binding:"required"`
	Currency         string  `json:"currency" binding:"required"`
	Purpose          string  `json:"purpose"`
	Salary           float64 `json:"salary"`
	EmploymentStatus string  `json:"employment_status"`
	EmploymentPeriod int64   `json:"employment_period"`
	PhoneNumber      string  `json:"phone_number"`
	InterestRateType string  `json:"interest_rate_type"`
}

type getLoanRequestsQuery struct {
	LoanType      string `form:"loan_type"`
	AccountNumber string `form:"account_number"`
}

type loanRequestIDURI struct {
	ID int64 `uri:"id" binding:"required"`
}

type getLoansQuery struct {
	LoanType      string `form:"loan_type"`
	AccountNumber string `form:"account_number"`
	Status        string `form:"status"`
}

type getLoanByNumberURI struct {
	LoanNumber string `uri:"loanNumber" binding:"required"`
}
type createCompanyRequest struct {
	RegisteredID   int64  `json:"registered_id" binding:"required"`
	Name           string `json:"name" binding:"required"`
	TaxCode        int64  `json:"tax_code" binding:"required"`
	ActivityCodeID int64  `json:"activity_code_id"`
	Address        string `json:"address" binding:"required"`
	OwnerID        int64  `json:"owner_id" binding:"required"`
}

type updateCompanyRequest struct {
	Name           string `json:"name" binding:"required"`
	ActivityCodeID int64  `json:"activity_code_id"`
	Address        string `json:"address" binding:"required"`
	OwnerID        int64  `json:"owner_id" binding:"required"`
}

type createAccountRequest struct {
	Name             string `json:"name" binding:"required"`
	Owner            int64  `json:"owner" binding:"required"`
	Currency         string `json:"currency" binding:"required"`
	OwnerType        string `json:"owner_type" binding:"required"`
	AccountType      string `json:"account_type" binding:"required"`
	MaintainanceCost int64  `json:"maintainance_cost" binding:"required"`
	DailyLimit       int64  `json:"daily_limit"`
	MonthlyLimit     int64  `json:"monthly_limit"`
	CreatedBy        int64  `json:"created_by" binding:"required"`
	ValidUntil       int64  `json:"valid_until"`
}

type getAccountsQuery struct {
	FirstName     string `form:"first_name"`
	LastName      string `form:"last_name"`
	AccountNumber string `form:"account_number"`
}

type accountNumberURI struct {
	AccountNumber string `uri:"accountNumber" binding:"required"`
}

type updateAccountNameRequest struct {
	Name string `json:"name" binding:"required"`
}

type updateAccountLimitsRequest struct {
	DailyLimit   *int64 `json:"daily_limit"`
	MonthlyLimit *int64 `json:"monthly_limit"`
}

type createPaymentRecipientRequest struct {
	Name          string `json:"name" binding:"required"`
	AccountNumber string `json:"account_number" binding:"required"`
}
type updatePaymentRecipientRequest struct {
	Name          string `json:"name" binding:"required"`
	AccountNumber string `json:"account_number" binding:"required"`
}

type paymentRecipientByIDURI struct {
	ID int64 `uri:"id" binding:"required"`
}

type getTransactionsQuery struct {
	DateFrom   string  `form:"date_from"`
	DateTo     string  `form:"date_to"`
	AmountFrom float64 `form:"amount_from"`
	AmountTo   float64 `form:"amount_to"`
	Status     string  `form:"status"`

	Page     int32 `form:"page"`
	PageSize int32 `form:"page_size"`

	SortBy    string `form:"sort_by"`
	SortOrder string `form:"sort_order"`
}
type transactionByIDURI struct {
	ID int64 `uri:"id" binding:"required"`
}

type transactionTypeQuery struct {
	Type string `form:"type" binding:"required"`
}

type requestCardRequest struct {
	AccountNumber string `json:"account_number" binding:"required"`
	CardType      string `json:"card_type" binding:"required"`
	CardBrand     string `json:"card_brand" binding:"required"`
}

type confirmCardQuery struct {
	Token string `form:"token" binding:"required"`
}

type blockCardURI struct {
	CardID int64 `uri:"id" binding:"required"`
}

type conversionRequest struct {
	FromCurrency string  `json:"from_currency" binding:"required"`
	ToCurrency   string  `json:"to_currency" binding:"required"`
	Amount       float64 `json:"amount" binding:"required,gt=0"`
}

type TOTPSetupConfirmRequest struct {
	Code string `json:"code" binding:"required"`
}

type paymentRequest struct {
	SenderAccount    string `json:"sender_account" binding:"required"`
	RecipientAccount string `json:"recipient_account" binding:"required"`
	RecipientName    string `json:"recipient_name" binding:"required"`
	Amount           int64  `json:"amount" binding:"required"`
	PaymentCode      string `json:"payment_code" binding:"required"`
	ReferenceNumber  string `json:"reference_number"`
	Purpose          string `json:"purpose"`
}

type transferRequest struct {
	FromAccount string `json:"from_account" binding:"required"`
	ToAccount   string `json:"to_account" binding:"required"`
	Amount      int64  `json:"amount" binding:"required"`
	Description string `json:"description"`
}
