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
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

type Connections struct {
	NotificationClient notificationpb.NotificationServiceClient
	Sql_db             *sql.DB
	Gorm               *gorm.DB
	Rdb                *redis.Client
}

const (
	passwordActionReset      = "reset"
	passwordActionInitialSet = "initial_set"

	resetPasswordTokenTTL  = 30 * time.Minute
	initialSetPasswordTTL  = 24 * time.Hour
	defaultNotificationURL = "notification:50051"
)

type Server struct {
	userpb.UnimplementedUserServiceServer
	userpb.UnimplementedTOTPServiceServer
	accessJwtSecret  string
	refreshJwtSecret string
	database         *sql.DB
	db_gorm          *gorm.DB
	rdb              *redis.Client
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

func NewServer(accessJwtSecret string, refreshJwtSecret string, conn *Connections) *Server {
	return &Server{
		accessJwtSecret:  accessJwtSecret,
		refreshJwtSecret: refreshJwtSecret,
		database:         conn.Sql_db,
		db_gorm:          conn.Gorm,
		rdb:              conn.Rdb,
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

func (client Client) toProtobuff() *userpb.Client {
	return &userpb.Client{
		Id:          int64(client.Id),
		FirstName:   client.First_name,
		LastName:    client.Last_name,
		DateOfBirth: client.Date_of_birth.Unix(),
		Gender:      client.Gender,
		Email:       client.Email,
		PhoneNumber: client.Phone_number,
		Address:     client.Address,
	}
}

func (s *Server) GetEmployeeByEmail(_ context.Context, req *userpb.GetEmployeeByEmailRequest) (*userpb.GetEmployeeResponse, error) {
	resp, err := getUserByAttribute(Employee{}, s.db_gorm, "email", req.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, status.Error(codes.Internal, "failed to get employee")
	}
	return resp.toProtobuf(), nil
}

func (s *Server) GetEmployeeById(_ context.Context, req *userpb.GetEmployeeByIdRequest) (*userpb.GetEmployeeResponse, error) {
	resp, err := getUserByAttribute(Employee{}, s.db_gorm, "id", req.Id)
	if err != nil {
		return nil, err
	}
	return resp.toProtobuf(), nil
}

func (s *Server) DeleteEmployee(_ context.Context, req *userpb.DeleteEmployeeRequest) (*userpb.DeleteEmployeeResponse, error) {

	err := deleteUser(Employee{Id: uint64(req.Id)}, s)
	if err != nil {
		if errors.Is(err, ErrEmployeeNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, err
	}
	return &userpb.DeleteEmployeeResponse{Success: true}, nil
}

func (s *Server) GetEmployees(_ context.Context, req *userpb.GetEmployeesRequest) (*userpb.GetEmployeesResponse, error) {
	map_func := func(emp Employee) *userpb.GetEmployeesResponse_Employee {
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
	restrictions := user_restrictions{"first_name": req.FirstName, "last_name": req.LastName, "email": req.Email, "position": req.Position}

	employees, err := GetAllUsersFromModel(Employee{}, s, restrictions)
	if err != nil {
		log.Printf("Error in retrieving employees: %s", err.Error())
		return nil, status.Error(codes.Internal, "Failed to retrieve employees")
	}

	var employee_responses []*userpb.GetEmployeesResponse_Employee
	for _, emp := range employees {
		employee_responses = append(employee_responses, map_func(emp))
	}

	return &userpb.GetEmployeesResponse{Employees: employee_responses}, nil
}

func (s *Server) UpdateEmployee(ctx context.Context, req *userpb.UpdateEmployeeRequest) (*userpb.GetEmployeeResponse, error) {
	if !req.Active {
		existing, err := getUserByAttribute(Employee{}, s.db_gorm, "id", req.Id)
		if err == nil && existing != nil {
			for _, p := range existing.Permissions {
				if p.Name == "admin" {
					return nil, status.Error(codes.PermissionDenied, "cannot deactivate an admin")
				}
			}
		}
	}

	var permissions []Permission
	for _, perm := range req.Permissions {
		// yes these are invalid. i don't care
		permissions = append(permissions, Permission{Id: 0, Name: perm})
	}

	emp := Employee{
		Last_name:    req.LastName,
		Gender:       req.Gender,
		Phone_number: req.PhoneNumber,
		Address:      req.Address,
		Position:     req.Position,
		Department:   req.Department,
		Active:       req.Active,
		Id:           uint64(req.Id),
		Updated_at:   time.Now(),
		Permissions:  permissions,
	}

	updated, err := updateUserRecord(emp, s)
	if err != nil {
		if errors.Is(err, ErrEmployeeNotFound) {
			return nil, status.Error(codes.NotFound, "Employee not found")
		}
		if errors.Is(err, ErrUnknownPermission) {
			return nil, status.Error(codes.NotFound, "Uknown permissions")
		}
		return nil, status.Error(codes.Internal, "Messed something up in UpdateEmployee_ in repo")
	}

	// Sync session: deactivation deletes session, otherwise update permissions
	if !req.Active {
		_ = s.DeleteSession(ctx, updated.Email)
	} else {
		permNames := make([]string, len(updated.Permissions))
		for i, p := range updated.Permissions {
			permNames[i] = p.Name
		}
		_ = s.UpdateSessionPermissions(ctx, updated.Email, "employee", permNames)
	}

	return updated.toProtobuf(), nil

}

func (s *Server) GetClients(_ context.Context, req *userpb.GetClientsRequest) (*userpb.GetClientsResponse, error) {

	clients, err := GetAllUsersFromModel(Client{}, s, user_restrictions{"first_name": strings.TrimSpace(req.FirstName), "last_name": strings.TrimSpace(req.LastName), "email": strings.TrimSpace(req.Email)})

	if err != nil {
		log.Printf("Error in retrieving clients: %s", err.Error())
		return nil, status.Error(codes.Internal, "Failed to retrieve clients")
	}

	var clientResponses []*userpb.Client
	for _, client := range clients {
		clientResponses = append(clientResponses, client.toProtobuff())
	}

	return &userpb.GetClientsResponse{Clients: clientResponses}, nil
}

func (s *Server) UpdateClient(_ context.Context, req *userpb.UpdateClientRequest) (*userpb.UpdateClientResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if strings.TrimSpace(req.Gender) != "" && req.Gender != "M" && req.Gender != "F" {
		return nil, status.Error(codes.InvalidArgument, "Gender must be one of M or F")
	}
	client := Client{
		Id:           uint64(req.Id),
		First_name:   req.FirstName,
		Last_name:    req.LastName,
		Gender:       req.Gender,
		Email:        req.Email,
		Phone_number: req.PhoneNumber,
		Address:      req.Address,
	}

	// I hope any potential reader of this has as much fun reading it as I had Implementing it.
	ref := reflect.ValueOf(&client).Elem()
	for i := 0; i < ref.NumField(); i++ {
		field := ref.Field(i)
		if field.Type() == reflect.TypeFor[string]() {
			if !field.CanSet() {
				log.Println("cannot set the value of struct field")
				// This need not be an error, but it will also probably
				// never happen
				return nil, status.Error(codes.Internal, "client update failed")
			}
			field.SetString(strings.TrimSpace(field.String()))
		}

	}

	if req.DateOfBirth != 0 {
		client.Date_of_birth = time.Unix(req.DateOfBirth, 0)
	}

	_, err := updateUserRecord(client, s)
	if err != nil {
		switch {
		case errors.Is(err, ErrClientNotFound):
			return nil, status.Error(codes.NotFound, "client not found")
		case errors.Is(err, ErrClientEmailExists):
			return nil, status.Error(codes.AlreadyExists, "client with that email already exists")
		case errors.Is(err, ErrClientNoFieldsToUpdate):
			return nil, status.Error(codes.InvalidArgument, "no fields to update")
		default:
			return nil, status.Error(codes.Internal, "client update failed")
		}
	}

	return &userpb.UpdateClientResponse{Valid: true, Response: "Client updated"}, nil
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
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})

	if err != nil || !token.Valid {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return nil, err
	}
	exp, err := claims.GetExpirationTime()
	if err != nil {
		return nil, err
	}
	iat, err := claims.GetIssuedAt()
	if err != nil {
		return nil, err
	}

	return &userpb.ValidateTokenResponse{
		Sub: sub,
		Exp: exp.Unix(),
		Iat: iat.Unix(),
	}, nil
}

func (s *Server) ValidateRefreshToken(_ context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	return validateJWTToken(req.Token, s.refreshJwtSecret)
}

func (s *Server) ValidateAccessToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	resp, err := validateJWTToken(req.Token, s.accessJwtSecret)
	if err != nil {
		return nil, err
	}

	session, err := s.GetSession(ctx, resp.Sub)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "session store unavailable")
	}
	if session == nil {
		return nil, status.Error(codes.Unauthenticated, "no active session")
	}
	if !session.Active {
		return nil, status.Error(codes.Unauthenticated, "account deactivated")
	}

	return resp, nil
}

func (s *Server) GetUserPermissions(ctx context.Context, req *userpb.GetUserPermissionsRequest) (*userpb.GetUserPermissionsResponse, error) {
	session, err := s.GetSession(ctx, req.Email)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "session store unavailable")
	}
	if session == nil {
		return nil, status.Error(codes.Unauthenticated, "no active session")
	}

	return &userpb.GetUserPermissionsResponse{
		Role:        session.Role,
		Permissions: session.Permissions,
	}, nil
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

	role, permissions, active := s.getRoleAndPermissions(email)

	if !active {
		return nil, status.Error(codes.Unauthenticated, "account deactivated")
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

	if err := s.CreateSession(ctx, email, SessionData{
		Role:        role,
		Permissions: permissions,
		Active:      true,
	}); err != nil {
		return nil, status.Error(codes.Unavailable, "session store unavailable")
	}

	return &userpb.RefreshResponse{AccessToken: newAccessToken, RefreshToken: newSignedToken, Permissions: permissions, Role: role}, nil
}

// getRoleAndPermissions determines the role, permissions, and active status for a user by email.
// Employees get role "employee" with their DB permissions; clients get role "client" with empty permissions.
// The active flag is only meaningful for employees; clients always return true.
func (s *Server) getRoleAndPermissions(email string) (role string, permissions []string, active bool) {
	emp, err := getUserByAttribute(Employee{}, s.db_gorm, "email", email)
	if err == nil && emp != nil {
		permissions := make([]string, len(emp.Permissions))
		for i, v := range emp.Permissions {
			permissions[i] = v.Name
		}
		return "employee", permissions, emp.Active
	}
	return "client", []string{}, true
}

func (s *Server) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	user, err := s.GetUserByEmail(req.Email)
	if err != nil || user == nil {
		return nil, status.Error(codes.Unauthenticated, "wrong creds")
	}
	hashedPassword := HashPassword(req.Password, user.salt)

	if bytes.Equal(hashedPassword, user.hashedPassword) {
		role, permissions, active := s.getRoleAndPermissions(user.email)

		if !active {
			return nil, status.Error(codes.Unauthenticated, "account deactivated")
		}

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

		if err := s.CreateSession(ctx, user.email, SessionData{
			Role:        role,
			Permissions: permissions,
			Active:      true,
		}); err != nil {
			return nil, status.Error(codes.Unavailable, "session store unavailable")
		}

		return &userpb.LoginResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			Permissions:  permissions,
			Role:         role,
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

	_ = s.DeleteSession(ctx, email)

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

	email, actionType, err := consumePasswordActionToken(tx, hashValue(token))
	if err != nil {
		if errors.Is(err, ErrInvalidPasswordActionToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid or expired token")
		}
		return nil, status.Error(codes.Internal, "token validation failed")
	}

	user, err := s.GetUserByEmail(email)
	if err != nil || user == nil {
		return nil, status.Error(codes.Internal, "user lookup failed")
	}

	hashedPassword := HashPassword(newPassword, user.salt)

	if err := s.UpdatePasswordByEmail(tx, email, hashedPassword); err != nil {
		return nil, status.Error(codes.Internal, "password update failed")
	}

	if actionType == passwordActionInitialSet {
		if _, err := tx.Exec(`UPDATE employees SET active = true, updated_at = NOW() WHERE email = $1`, email); err != nil {
			return nil, status.Error(codes.Internal, "employee activation failed")
		}
	}

	if err := s.RevokeRefreshTokensByEmail(tx, email); err != nil {
		return nil, status.Error(codes.Internal, "refresh token revocation failed")
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Error(codes.Internal, "committing transaction failed")
	}

	_ = s.DeleteSession(ctx, email)

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
	link, err := buildActionLink(baseURL, token)
	if err != nil {
		return nil, status.Error(codes.Internal, "building password link failed")
	}

	if err := s.sendPasswordActionEmail(ctx, user.email, link, actionType); err != nil {
		return nil, err
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

func buildActionLink(baseURL string, token string) (string, error) {
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

func (s *Server) CreateClientAccount(_ context.Context, req *userpb.CreateClientRequest) (*userpb.CreateClientResponse, error) {
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

func (s *Server) CreateEmployeeAccount(ctx context.Context, req *userpb.CreateEmployeeRequest) (*userpb.GetEmployeeResponse, error) {
	is_null := func(str string) bool {
		return strings.TrimSpace(str) == ""
	}
	vals := []string{req.FirstName, req.LastName, req.Email,
		req.Username}
	if slices.ContainsFunc(vals, is_null) {
		return nil, status.Error(codes.InvalidArgument, "One of the required cols is null")
	}

	salt, salt_err := generateSalt()
	if salt_err != nil {
		log.Printf("Error generating salt %s", salt_err.Error())
	}

	permissions := make([]Permission, 0, len(req.Permissions))
	for _, permName := range req.Permissions {
		var perm Permission
		if err := s.db_gorm.First(&perm, "name = ?", permName).Error; err != nil {
			log.Printf("Permission %q not found, skipping", permName)
			continue
		}
		permissions = append(permissions, perm)
	}

	employee := Employee{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.BirthDate, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Username: req.Username, Position: req.Position,
		Department: req.Department, Salt_password: salt,
		Password: []byte{}, Permissions: permissions}

	err := create_user_from_model(employee, s)

	if err != nil {
		log.Printf("Error in user creation%s", err.Error())
		return nil, status.Error(codes.Internal, "Employee creation failed")
	}

	// Re-fetch to get the auto-assigned ID and properly loaded permissions
	created, err := getUserByAttribute(Employee{}, s.db_gorm, "email", employee.Email)
	if err != nil {
		log.Printf("Employee created but failed to fetch: %s", err.Error())
		return employee.toProtobuf(), nil
	}

	// Send activation email so the employee can set their own password
	_, emailErr := s.RequestInitialPasswordSet(ctx, &userpb.PasswordActionRequest{
		Email: req.Email,
	})
	if emailErr != nil {
		log.Printf("Employee created but activation email failed: %s", emailErr.Error())
	}

	return created.toProtobuf(), nil

}
