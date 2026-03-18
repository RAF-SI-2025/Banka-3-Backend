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

type getEmployeesURI struct {
	Email     string `form:"email"`
	FirstName string `form:"first_name"`
	LastName  string `form:"last_name"`
	Position  string `form:"position"`
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
	LastName    *string   `json:"last_name"`
	Gender      *string   `json:"gender"`
	PhoneNumber *string   `json:"phone_number"`
	Address     *string   `json:"address"`
	Position    *string   `json:"position"`
	Department  *string   `json:"department"`
	Active      *bool     `json:"active"`
	Permissions *[]string `json:"permissions"`
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

type createEmployeeAccountRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	BirthDate   int64  `json:"date_of_birth"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required"`
	PhoneNumber string `json:"phone_number"`
	Address     string `json:"address"`
	Username    string `json:"username" binding:"required"`
	Position    string `json:"position"`
	Department  string `json:"department"`
	Password    string `json:"password"`
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
