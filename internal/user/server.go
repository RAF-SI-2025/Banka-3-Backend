package user

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

const (
	passwordActionReset      = "reset"
	passwordActionInitialSet = "initial_set"

	resetPasswordTokenTTL  = 30 * time.Minute
	initialSetPasswordTTL  = 24 * time.Hour
	defaultNotificationURL = "notification:50051"
)

type Server struct {
	userpb.UnimplementedUserServiceServer
	accessJwtSecret  string
	refreshJwtSecret string
	database         *sql.DB
	db_gorm          *gorm.DB
}

func generateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	if err != nil {
		return nil, err
	}
	return salt, nil
}

func HashPassword(password string, salt []byte) []byte {
	hashed := sha256.New()
	hashed.Write(salt)
	hashed.Write([]byte(password))
	return hashed.Sum(nil)
}

func NewServer(accessJwtSecret string, refreshJwtSecret string, database *sql.DB, gorm_db *gorm.DB) *Server {
	return &Server{
		accessJwtSecret:  accessJwtSecret,
		refreshJwtSecret: refreshJwtSecret,
		database:         database,
		db_gorm:          gorm_db,
	}
}

func (emp Employee) toProtobuf() *userpb.GetEmployeeResponse {
	permissions := make([]string, len(emp.Permissions))
	for i, v := range emp.Permissions {
		permissions[i] = v.Name
	}
	return &userpb.GetEmployeeResponse{
		Id:          int64(emp.Id),
		FirstName:   emp.First_name,
		LastName:    emp.Last_name,
		BirthDate:   emp.Date_of_birth.Unix(),
		Gender:      emp.Gender,
		Email:       emp.Email,
		PhoneNumber: emp.Phone_number,
		Address:     emp.Address,
		Username:    emp.Username,
		Position:    emp.Position,
		Department:  emp.Department,
		Active:      emp.Active,
		Permissions: permissions,
	}
}

func (s *Server) GetEmployeeByEmail(ctx context.Context, req *userpb.GetEmployeeByEmailRequest) (*userpb.GetEmployeeResponse, error) {
	resp, err := s.getEmployeeByEmail(req.Email)
	if err != nil {
		return nil, err
	}
	return resp.toProtobuf(), nil
}

func (s *Server) GetEmployeeById(ctx context.Context, req *userpb.GetEmployeeByIdRequest) (*userpb.GetEmployeeResponse, error) {
	resp, err := s.getEmployeeById(req.Id)
	if err != nil {
		return nil, err
	}
	return resp.toProtobuf(), nil
}

func (s *Server) GetEmployees(ctx context.Context, req *userpb.GetEmployeesRequest) (*userpb.GetEmployeesResponse, error) {
	map_func := func(emp Get_employees) *userpb.GetEmployeesResponse_Employee {
		return &userpb.GetEmployeesResponse_Employee{
			Id:          int64(emp.Id),
			FirstName:   emp.First_name,
			LastName:    emp.Last_name,
			Email:       emp.Email,
			Position:    emp.Position,
			PhoneNumber: emp.Phone_number,
			Active:      emp.Active,
		}
	}
	employees, err := s.GetAllEmployees(req.Email, req.FirstName, req.LastName, req.Position)
	if err != nil {
		log.Printf("Error in retrieving employees: %s", err.Error())
		return nil, status.Error(codes.Internal, "Failed to retrieve employees")
	}
	var employee_responses []*userpb.GetEmployeesResponse_Employee
	for _, emp := range *employees {
		employee_responses = append(employee_responses, map_func(emp))
	}

	return &userpb.GetEmployeesResponse{Employees: employee_responses}, nil
}

func (s *Server) UpdateEmployee(ctx context.Context, req *userpb.UpdateEmployeeRequest) (*userpb.UpdateEmployeeResponse, error) {
	var permissions []Permission
	for _, perm := range req.Permissions {
		// yes these are invalid. i don't care
		permissions = append(permissions, Permission{Id: 0, Name: perm})
	}

	emp := Employee{
		Last_name:     req.LastName,
		Gender:        req.Gender,
		Phone_number:  req.PhoneNumber,
		Address:       req.Address,
		Position:      req.Position,
		Department:    req.Department,
		Active:        req.Active,
		Id:            uint64(req.Id),
		Date_of_birth: time.Time{},
		Updated_at:    time.Now(),
		Permissions:   permissions,
	}

	err := s.UpdateEmployee_(&emp)
	if err != nil {
		return nil, status.Error(codes.Internal, "Messed something up in UpdateEmployee_ in repo")
	}
	return &userpb.UpdateEmployeeResponse{Valid: true, Response: "You made it"}, nil

}

func mapCompanyToProto(company *Company) *userpb.Company {
	if company == nil {
		return nil
	}

	return &userpb.Company{
		Id:             company.Id,
		RegisteredId:   company.Registered_id,
		Name:           company.Name,
		TaxCode:        company.Tax_code,
		ActivityCodeId: company.Activity_code_id,
		Address:        company.Address,
		OwnerId:        company.Owner_id,
	}
}

func validateCreateCompanyInput(registeredID int64, name string, taxCode int64, address string, ownerID int64) error {
	if registeredID <= 0 {
		return status.Error(codes.InvalidArgument, "registered id must be greater than zero")
	}
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if taxCode <= 0 {
		return status.Error(codes.InvalidArgument, "tax code must be greater than zero")
	}
	if strings.TrimSpace(address) == "" {
		return status.Error(codes.InvalidArgument, "address is required")
	}
	if ownerID <= 0 {
		return status.Error(codes.InvalidArgument, "owner id must be greater than zero")
	}
	return nil
}

func validateUpdateCompanyInput(id int64, name string, address string, ownerID int64) error {
	if id <= 0 {
		return status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if strings.TrimSpace(address) == "" {
		return status.Error(codes.InvalidArgument, "address is required")
	}
	if ownerID <= 0 {
		return status.Error(codes.InvalidArgument, "owner id must be greater than zero")
	}
	return nil
}

func (s *Server) CreateCompany(ctx context.Context, req *userpb.CreateCompanyRequest) (*userpb.CreateCompanyResponse, error) {
	if err := validateCreateCompanyInput(req.RegisteredId, req.Name, req.TaxCode, req.Address, req.OwnerId); err != nil {
		return nil, err
	}

	company, err := s.CreateCompanyRecord(Company{
		Registered_id:    req.RegisteredId,
		Name:             strings.TrimSpace(req.Name),
		Tax_code:         req.TaxCode,
		Activity_code_id: req.ActivityCodeId,
		Address:          strings.TrimSpace(req.Address),
		Owner_id:         req.OwnerId,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyRegisteredIDExists):
			return nil, status.Error(codes.AlreadyExists, "company with that registered id already exists")
		case errors.Is(err, ErrCompanyOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrCompanyActivityCodeNotFound):
			return nil, status.Error(codes.InvalidArgument, "activity code does not exist")
		default:
			return nil, status.Error(codes.Internal, "company creation failed")
		}
	}

	return &userpb.CreateCompanyResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GetCompanyById(ctx context.Context, req *userpb.GetCompanyByIdRequest) (*userpb.GetCompanyByIdResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}

	company, err := s.GetCompanyByIDRecord(req.Id)
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyNotFound):
			return nil, status.Error(codes.NotFound, "company not found")
		default:
			return nil, status.Error(codes.Internal, "company lookup failed")
		}
	}

	return &userpb.GetCompanyByIdResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GetCompanies(ctx context.Context, req *userpb.GetCompaniesRequest) (*userpb.GetCompaniesResponse, error) {
	companies, err := s.GetCompaniesRecords()
	if err != nil {
		return nil, status.Error(codes.Internal, "company listing failed")
	}

	var responseCompanies []*userpb.Company
	for _, company := range companies {
		responseCompanies = append(responseCompanies, mapCompanyToProto(company))
	}

	return &userpb.GetCompaniesResponse{Companies: responseCompanies}, nil
}

func (s *Server) UpdateCompany(ctx context.Context, req *userpb.UpdateCompanyRequest) (*userpb.UpdateCompanyResponse, error) {
	if err := validateUpdateCompanyInput(req.Id, req.Name, req.Address, req.OwnerId); err != nil {
		return nil, err
	}

	company, err := s.UpdateCompanyRecord(Company{
		Id:               req.Id,
		Name:             strings.TrimSpace(req.Name),
		Activity_code_id: req.ActivityCodeId,
		Address:          strings.TrimSpace(req.Address),
		Owner_id:         req.OwnerId,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyNotFound):
			return nil, status.Error(codes.NotFound, "company not found")
		case errors.Is(err, ErrCompanyOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrCompanyActivityCodeNotFound):
			return nil, status.Error(codes.InvalidArgument, "activity code does not exist")
		default:
			return nil, status.Error(codes.Internal, "company update failed")
		}
	}

	return &userpb.UpdateCompanyResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GenerateRefreshToken(email string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   email,
		ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour * 7)),
		IssuedAt:  jwt.NewNumericDate(now),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.refreshJwtSecret))
}

func (s *Server) GenerateAccessToken(email string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   email,
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.accessJwtSecret))
}

func validateJWTToken(tokenString, secret string) (*userpb.ValidateTokenResponse, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})

	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	sub, err := token.Claims.GetSubject()
	if err != nil {
		return nil, err
	}
	exp, err := token.Claims.GetExpirationTime()
	if err != nil {
		return nil, err
	}
	iat, err := token.Claims.GetIssuedAt()
	if err != nil {
		return nil, err
	}

	return &userpb.ValidateTokenResponse{
		Sub: sub,
		Exp: exp.Unix(),
		Iat: iat.Unix(),
	}, nil
}

func (s *Server) ValidateRefreshToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	return validateJWTToken(req.Token, s.refreshJwtSecret)
}

func (s *Server) ValidateAccessToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	return validateJWTToken(req.Token, s.accessJwtSecret)
}

func (s *Server) Refresh(ctx context.Context, req *userpb.RefreshRequest) (*userpb.RefreshResponse, error) {
	token, err := validateJWTToken(req.RefreshToken, s.refreshJwtSecret)
	if err != nil {
		return nil, err
	}
	email := token.Sub

	newSignedToken, err := s.GenerateRefreshToken(email)
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	newAccessToken, err := s.GenerateAccessToken(email)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	newParsed, _, err := jwt.NewParser().ParseUnverified(newSignedToken, &jwt.RegisteredClaims{})
	if err != nil {
		return nil, fmt.Errorf("parsing new token: %w", err)
	}
	newExpiry, err := newParsed.Claims.GetExpirationTime()
	if err != nil {
		return nil, fmt.Errorf("getting expiry: %w", err)
	}

	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = s.rotateRefreshToken(tx, email, hashValue(req.RefreshToken), hashValue(newSignedToken), newExpiry.Time)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "wrong token")
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &userpb.RefreshResponse{AccessToken: newAccessToken, RefreshToken: newSignedToken}, nil
}

func (s *Server) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	user, err := s.GetUserByEmail(req.Email)
	if err != nil || user == nil {
		return nil, status.Error(codes.Unauthenticated, "wrong creds")
	}
	hashedPassword := HashPassword(req.Password, user.salt)

	if bytes.Equal(hashedPassword, user.hashedPassword) {
		accessToken, err := s.GenerateAccessToken(user.email)
		if err != nil {
			return nil, err
		}
		refreshToken, err := s.GenerateRefreshToken(user.email)
		if err != nil {
			return nil, err
		}
		err = s.InsertRefreshToken(refreshToken)
		if err != nil {
			return nil, err
		}

		return &userpb.LoginResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
		}, nil
	}

	return nil, status.Error(codes.Unauthenticated, "wrong creds")
}

func (s *Server) Logout(ctx context.Context, req *userpb.LogoutRequest) (*userpb.LogoutResponse, error) {
	email := req.Email
	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	err = s.RevokeRefreshTokensByEmail(tx, email)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &userpb.LogoutResponse{
		Success: true,
	}, nil
}

func (s *Server) RequestPasswordReset(ctx context.Context, req *userpb.PasswordActionRequest) (*userpb.PasswordActionResponse, error) {
	return s.requestPasswordAction(ctx, strings.TrimSpace(req.Email), passwordActionReset)
}

func (s *Server) RequestInitialPasswordSet(ctx context.Context, req *userpb.PasswordActionRequest) (*userpb.PasswordActionResponse, error) {
	return s.requestPasswordAction(ctx, strings.TrimSpace(req.Email), passwordActionInitialSet)
}

func (s *Server) SetPasswordWithToken(ctx context.Context, req *userpb.SetPasswordWithTokenRequest) (*userpb.SetPasswordWithTokenResponse, error) {
	token := strings.TrimSpace(req.Token)
	newPassword := strings.TrimSpace(req.NewPassword)

	if token == "" || newPassword == "" {
		return nil, status.Error(codes.InvalidArgument, "token and new password are required")
	}

	tx, err := s.database.Begin()
	if err != nil {
		return nil, status.Error(codes.Internal, "starting transaction failed")
	}
	defer func() { _ = tx.Rollback() }()

	email, _, err := s.ConsumePasswordActionToken(tx, hashValue(token))
	if err != nil {
		if errors.Is(err, ErrInvalidPasswordActionToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid or expired token")
		}
		return nil, status.Error(codes.Internal, "token validation failed")
	}

	if err := s.UpdatePasswordByEmail(tx, email, hashValue(newPassword)); err != nil {
		return nil, status.Error(codes.Internal, "password update failed")
	}

	if err := s.RevokeRefreshTokensByEmail(tx, email); err != nil {
		return nil, status.Error(codes.Internal, "refresh token revocation failed")
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Error(codes.Internal, "committing transaction failed")
	}

	return &userpb.SetPasswordWithTokenResponse{Successful: true}, nil
}

func (s *Server) requestPasswordAction(ctx context.Context, email string, actionType string) (*userpb.PasswordActionResponse, error) {
	if email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	user, err := s.GetUserByEmail(email)
	if err != nil {
		return nil, status.Error(codes.Internal, "user lookup failed")
	}
	if user == nil {
		return &userpb.PasswordActionResponse{Accepted: true}, nil
	}

	token, err := generateOpaqueToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "token generation failed")
	}

	validUntil := time.Now().Add(resetPasswordTokenTTL)
	if actionType == passwordActionInitialSet {
		validUntil = time.Now().Add(initialSetPasswordTTL)
	}

	if err := s.UpsertPasswordActionToken(user.email, actionType, hashValue(token), validUntil); err != nil {
		return nil, status.Error(codes.Internal, "storing token failed")
	}

	baseURL := os.Getenv("PASSWORD_RESET_BASE_URL")
	if actionType == passwordActionInitialSet {
		baseURL = os.Getenv("PASSWORD_SET_BASE_URL")
	}
	link, err := buildPasswordLink(baseURL, token)
	if err != nil {
		return nil, status.Error(codes.Internal, "building password link failed")
	}

	if err := s.sendPasswordActionEmail(ctx, user.email, link, actionType); err != nil {
		return nil, status.Error(codes.Internal, "sending password email failed")
	}

	return &userpb.PasswordActionResponse{Accepted: true}, nil
}

func (s *Server) sendPasswordActionEmail(ctx context.Context, email string, link string, actionType string) error {
	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if strings.TrimSpace(notificationAddr) == "" {
		notificationAddr = defaultNotificationURL
	}

	conn, err := grpc.NewClient(
		notificationAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dialing notification service: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := notificationpb.NewNotificationServiceClient(conn)

	sendCtx, cancelSend := context.WithTimeout(ctx, 5*time.Second)
	defer cancelSend()

	req := &notificationpb.PasswordLinkMailRequest{
		ToAddr: email,
		Link:   link,
	}

	if actionType == passwordActionInitialSet {
		resp, err := client.SendInitialPasswordSetEmail(sendCtx, req)
		if err != nil {
			return fmt.Errorf("calling SendInitialPasswordSetEmail: %w", err)
		}
		if !resp.Successful {
			return fmt.Errorf("notification service reported unsuccessful initial set send")
		}
		return nil
	}

	resp, err := client.SendPasswordResetEmail(sendCtx, req)
	if err != nil {
		return fmt.Errorf("calling SendPasswordResetEmail: %w", err)
	}
	if !resp.Successful {
		return fmt.Errorf("notification service reported unsuccessful reset send")
	}
	return nil
}

func generateOpaqueToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func hashValue(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func buildPasswordLink(baseURL string, token string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("base URL is empty")
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL: %w", err)
	}

	query := parsedURL.Query()
	query.Set("token", token)
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String(), nil
}

func (s *Server) CreateClientAccount(ctx context.Context, req *userpb.CreateClientRequest) (*userpb.CreateClientResponse, error) {
	is_null := func(str string) bool {
		return strings.TrimSpace(str) == ""
	}
	vals := []string{req.FirstName, req.LastName, req.Gender, req.Email, req.PhoneNumber,
		req.Address}

	if slices.ContainsFunc(vals, is_null) {
		return nil, status.Error(codes.InvalidArgument, "One of the required cols is null")
	}

	if req.Gender != "M" && req.Gender != "F" {
		return nil, status.Error(codes.InvalidArgument, "Gender must be one of M or F")
	}

	salt, salt_err := generateSalt()
	if salt_err != nil {
		log.Printf("Error generating salt %s", salt_err.Error())
		return nil, status.Error(codes.Internal, "Password salting failed")
	}

	client := Client{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.BirthDate, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Password: HashPassword(req.Password, salt),
		Salt_password: salt}

	err := create_user_from_model(client, s)
	if err != nil {
		log.Printf("Error in user creation%s", err.Error())
		return nil, status.Error(codes.Internal, "Client creation failed")
	}
	return &userpb.CreateClientResponse{Valid: true}, nil

}

func (s *Server) CreateEmployeeAccount(ctx context.Context, req *userpb.CreateEmployeeRequest) (*userpb.CreateEmployeeResponse, error) {
	is_null := func(str string) bool {
		return strings.TrimSpace(str) == ""
	}
	vals := []string{req.FirstName, req.LastName, req.Gender, req.Email, req.PhoneNumber,
		req.Address, req.Username}
	if slices.ContainsFunc(vals, is_null) {
		return nil, status.Error(codes.InvalidArgument, "One of the required cols is null")
	}

	if req.Gender != "M" && req.Gender != "F" {
		log.Print("create employee gender must be M or F")
		return nil, errors.New("gender must be M or F")
	}

	salt, salt_err := generateSalt()
	if salt_err != nil {
		log.Printf("Error generating salt %s", salt_err.Error())
	}

	employee := Employee{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.BirthDate, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Username: req.Username, Position: req.Position,
		Department: req.Department, Salt_password: salt,
		Password: HashPassword(req.Password, salt)}

	err := create_user_from_model(employee, s)

	if err != nil {
		log.Printf("Error in user creation%s", err.Error())
		return nil, status.Error(codes.Internal, "Employee creation failed")
	}
	return &userpb.CreateEmployeeResponse{Valid: true}, nil

}
