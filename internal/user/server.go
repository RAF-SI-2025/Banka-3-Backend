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

func hashPassword(password string, salt []byte) []byte {
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

func (s *Server) GetEmployeeById(ctx context.Context, req *userpb.GetEmployeeByIdRequest) (*userpb.GetEmployeeByIdResponse, error) {
	map_to_protobuff_resp := func(emp Employee_by_Id_response) *userpb.GetEmployeeByIdResponse {
		return &userpb.GetEmployeeByIdResponse{
			Id:          int64(emp.Id),
			FirstName:   emp.First_name,
			LastName:    emp.Last_name,
			DateOfBirth: emp.Date_of_birth.Unix(),
			Gender:      emp.Gender,
			Email:       emp.Email,
			PhoneNumber: emp.Phone_number,
			Address:     emp.Address,
			Username:    emp.Username,
			Position:    emp.Position,
			Department:  emp.Department,
			Active:      emp.Active,
			Perms:       &userpb.Permissions{Id: int64(emp.Permission_id), Permision: emp.Permission_name},
		}
	}

	// user, err := get_user_by_id_from_model(Employee_by_Id_response{}, int(req.Id), s)
	user, err := s.GetUserByID(req.Id)
	if err != nil {
		log.Printf("Error in employee retrieval%s", err.Error())
		return nil, status.Error(codes.Internal, "Employee creation failed")
	}

	resp := map_to_protobuff_resp(*user)
	log.Println("I managed to return without fail, with response", resp)
	return resp, nil
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
			Perms:       &userpb.Permissions{Id: emp.Permission_id, Permision: emp.Permission_name},
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
	emp := Employees{Last_name: req.LastName, Gender: req.Gender,
		Phone_number: req.PhoneNumber, Address: req.Address, Position: req.Position,
		Department: req.Department, Active: req.Active, Id: uint64(req.Id)}
	var map_from_pbs = func(perms *userpb.Permissions) Permissions {
		return Permissions{Id: uint64(perms.Id), Name: perms.Permision}
	}
	var permissions []Permissions
	for _, perm := range req.Perms {
		permissions = append(permissions, map_from_pbs(perm))
	}
	err := s.UpdateEmployee_(&emp, permissions)
	if err != nil {
		return nil, status.Error(codes.Internal, "Messed something up in UpdateEmployee_ in repo")
	}
	return &userpb.UpdateEmployeeResponse{Valid: true, Response: "You made it"}, nil

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

func (s *Server) ValidateRefreshToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	token, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		return []byte(s.refreshJwtSecret), nil
	})

	if err != nil {
		return nil, err
	}
	return &userpb.ValidateTokenResponse{
		Valid: token.Valid,
	}, nil
}

func (s *Server) ValidateAccessToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	token, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		return []byte(s.accessJwtSecret), nil
	})

	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &userpb.ValidateTokenResponse{
		Valid: token.Valid,
	}, nil
}

func (s *Server) Refresh(ctx context.Context, req *userpb.RefreshRequest) (*userpb.RefreshResponse, error) {
	refreshToken := req.RefreshToken
	parsed, err := jwt.Parse(refreshToken, func(t *jwt.Token) (any, error) {
		return []byte(s.refreshJwtSecret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}
	email, err := parsed.Claims.GetSubject()
	if err != nil {
		return nil, fmt.Errorf("getting subject: %w", err)
	}

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

	err = s.rotateRefreshToken(tx, email, hashValue(refreshToken), hashValue(newSignedToken), newExpiry.Time)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "wrong token")
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &userpb.RefreshResponse{AccessToken: newAccessToken, RefreshToken: newSignedToken}, nil
}

func (s *Server) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	hashedPassword := hashValue(req.Password)
	user, err := s.GetUserByEmail(req.Email)
	if err != nil {
		return nil, err
	}

	if user != nil && bytes.Equal(hashedPassword, user.hashedPassword) {
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

	client := Clients{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.DateOfBirth, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Password: hashPassword(req.Password, salt),
		Salt_password: salt}

	err := create_user_from_model(client, s)
	if err != nil {
		log.Printf("Error in user creation%s", err.Error())
		return nil, status.Error(codes.Internal, "Employee creation failed")
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

	employee := Employees{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.DateOfBirth, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Username: req.Username, Position: req.Position,
		Department: req.Department, Salt_password: salt,
		Password: hashPassword(req.Password, salt)}

	err := create_user_from_model(employee, s)

	if err != nil {
		log.Printf("Error in user creation%s", err.Error())
		return nil, status.Error(codes.Internal, "Employee creation failed")
	}
	return &userpb.CreateEmployeeResponse{Valid: true}, nil

}
