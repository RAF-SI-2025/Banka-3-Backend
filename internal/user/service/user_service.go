package service

import (
	"banka-raf/gen/user"
	"banka-raf/internal/user/models"
	"banka-raf/internal/user/repository"
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type UserService struct {
	user.UnimplementedUserServiceServer
	repo             repository.IUserRepository
	accessJwtSecret  string
	refreshJwtSecret string
}

func NewUserService(repo repository.IUserRepository, accessSecret, refreshSecret string) *UserService {
	return &UserService{
		repo:             repo,
		accessJwtSecret:  accessSecret,
		refreshJwtSecret: refreshSecret,
	}
}

// =================== Auth Logic ===================

func (s *UserService) Login(_ context.Context, req *user.LoginRequest) (*user.LoginResponse, error) {
	u, err := s.repo.FindUserByEmail(req.Email)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	var storedHash []byte
	var storedSalt []byte
	var userID uint64
	isEmployee := false

	switch v := u.(type) {
	case *models.Employee:
		userID = v.Id
		storedHash = v.Password
		storedSalt = v.SaltPassword
		isEmployee = true
	case *models.Client:
		userID = v.Id
		storedHash = v.Password
		storedSalt = v.SaltPassword
	}

	hasher := sha256.New()
	hasher.Write(append([]byte(req.Password), storedSalt...))
	if !bytes.Equal(hasher.Sum(nil), storedHash) {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	accessToken, _ := s.generateToken(req.Email, s.accessJwtSecret, 15*time.Minute)
	refreshToken, _ := s.generateToken(req.Email, s.refreshJwtSecret, 7*24*time.Hour)

	if isEmployee {
		expiresAt := time.Now().Add(7 * 24 * time.Hour)
		if err := s.repo.UpsertRefreshToken(userID, refreshToken, expiresAt); err != nil {
			return nil, handleDBError(err)
		}
	}

	return &user.LoginResponse{AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

func (s *UserService) Refresh(_ context.Context, req *user.RefreshRequest) (*user.RefreshResponse, error) {
	parsed, err := jwt.Parse(req.RefreshToken, func(t *jwt.Token) (any, error) {
		return []byte(s.refreshJwtSecret), nil
	})
	if err != nil || !parsed.Valid {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}

	email, _ := parsed.Claims.GetSubject()
	u, err := s.repo.FindUserByEmail(email)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "user not found")
	}

	emp, ok := u.(*models.Employee)
	if !ok {
		return nil, status.Error(codes.PermissionDenied, "clients cannot refresh tokens")
	}

	newRefresh, _ := s.generateToken(email, s.refreshJwtSecret, 7*24*time.Hour)
	newAccess, _ := s.generateToken(email, s.accessJwtSecret, 15*time.Minute)

	if err := s.repo.RotateRefreshToken(emp.Id, req.RefreshToken, newRefresh, time.Now().Add(7*24*time.Hour)); err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	return &user.RefreshResponse{AccessToken: newAccess, RefreshToken: newRefresh}, nil
}

func (s *UserService) ValidateAccessToken(_ context.Context, req *user.ValidateTokenRequest) (*user.ValidateTokenResponse, error) {
	_, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		return []byte(s.accessJwtSecret), nil
	})
	return &user.ValidateTokenResponse{Valid: err == nil}, nil
}

func (s *UserService) ValidateRefreshToken(_ context.Context, req *user.ValidateTokenRequest) (*user.ValidateTokenResponse, error) {
	_, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		return []byte(s.refreshJwtSecret), nil
	})
	return &user.ValidateTokenResponse{Valid: err == nil}, nil
}

// =================== Employee CRUD ===================

func (s *UserService) ListEmployees(_ context.Context, req *user.ListEmployeesRequest) (*user.ListEmployeesResponse, error) {
	page, pageSize := s.getPagination(req.Page, req.PageSize)
	emps, total, err := s.repo.ListEmployees(page, pageSize, req.Email, req.FirstName, req.LastName, req.Position)
	if err != nil {
		return nil, handleDBError(err)
	}

	res := &user.ListEmployeesResponse{Total: total}
	for i := range emps {
		res.Employees = append(res.Employees, mapEmployeeToProto(&emps[i]))
	}
	return res, nil
}

func (s *UserService) CreateEmployee(_ context.Context, req *user.CreateEmployeeRequest) (*user.Employee, error) {
	dob, _ := time.Parse("2006-01-02", req.DateOfBirth)
	emp := &models.Employee{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: dob,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		Active:      req.Active,
	}

	permIDs := s.resolvePermissionNames(req.Permissions)
	if err := s.repo.CreateEmployee(emp, permIDs); err != nil {
		return nil, handleDBError(err)
	}
	return mapEmployeeToProto(emp), nil
}

func (s *UserService) GetEmployee(_ context.Context, req *user.GetEmployeeRequest) (*user.Employee, error) {
	emp, err := s.repo.GetEmployeeByID(req.EmployeeId)
	if err != nil {
		return nil, handleDBError(err)
	}
	return mapEmployeeToProto(emp), nil
}

func (s *UserService) UpdateEmployee(_ context.Context, req *user.UpdateEmployeeRequest) (*user.Employee, error) {
	emp, err := s.repo.GetEmployeeByID(req.EmployeeId)
	if err != nil {
		return nil, handleDBError(err)
	}

	dob, _ := time.Parse("2006-01-02", req.DateOfBirth)
	emp.FirstName = req.FirstName
	emp.LastName = req.LastName
	emp.DateOfBirth = dob
	emp.Position = req.Position
	emp.Department = req.Department
	emp.Active = req.Active
	emp.Gender = req.Gender
	emp.Email = req.Email
	emp.PhoneNumber = req.PhoneNumber
	emp.Address = req.Address

	permIDs := s.resolvePermissionNames(req.Permissions)
	if err := s.repo.UpdateEmployee(emp, permIDs); err != nil {
		return nil, handleDBError(err)
	}
	return mapEmployeeToProto(emp), nil
}

func (s *UserService) DeleteEmployee(_ context.Context, req *user.DeleteEmployeeRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteEmployee(req.EmployeeId); err != nil {
		return nil, handleDBError(err)
	}
	return &emptypb.Empty{}, nil
}

// =================== Client CRUD ===================

func (s *UserService) ListClients(_ context.Context, req *user.ListClientsRequest) (*user.ListClientsResponse, error) {
	page, pageSize := s.getPagination(req.Page, req.PageSize)
	clients, total, err := s.repo.ListClients(page, pageSize, "", "", "") // Filtering can be added here
	if err != nil {
		return nil, handleDBError(err)
	}

	res := &user.ListClientsResponse{Total: total}
	for i := range clients {
		res.Clients = append(res.Clients, mapClientToProto(&clients[i]))
	}
	return res, nil
}

func (s *UserService) CreateClient(_ context.Context, req *user.CreateClientRequest) (*user.Client, error) {
	dob, _ := time.Parse("yyyy-mm-dd", req.DateOfBirth)
	cl := &models.Client{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: dob,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
	}
	if err := s.repo.CreateClient(cl); err != nil {
		return nil, handleDBError(err)
	}
	return mapClientToProto(cl), nil
}

func (s *UserService) GetClient(_ context.Context, req *user.GetClientRequest) (*user.Client, error) {
	cl, err := s.repo.GetClientByID(req.ClientId)
	if err != nil {
		return nil, handleDBError(err)
	}
	return mapClientToProto(cl), nil
}

func (s *UserService) UpdateClient(_ context.Context, req *user.UpdateClientRequest) (*user.Client, error) {
	cl, err := s.repo.GetClientByID(req.ClientId)
	if err != nil {
		return nil, handleDBError(err)
	}

	dob, _ := time.Parse("yyyy-mm-dd", req.DateOfBirth)
	cl.FirstName = req.FirstName
	cl.LastName = req.LastName
	cl.DateOfBirth = dob
	cl.Gender = req.Gender
	cl.Email = req.Email
	cl.PhoneNumber = req.PhoneNumber
	cl.Address = req.Address

	if err := s.repo.UpdateClient(cl); err != nil {
		return nil, handleDBError(err)
	}
	return mapClientToProto(cl), nil
}

func (s *UserService) DeleteClient(_ context.Context, req *user.DeleteClientRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteClient(req.ClientId); err != nil {
		return nil, handleDBError(err)
	}
	return &emptypb.Empty{}, nil
}

// =================== Permissions ===================

func (s *UserService) ListPermissions(_ context.Context, _ *emptypb.Empty) (*user.ListPermissionsResponse, error) {
	perms, err := s.repo.ListPermissions()
	if err != nil {
		return nil, handleDBError(err)
	}
	res := &user.ListPermissionsResponse{}
	for _, p := range perms {
		res.Permissions = append(res.Permissions, &user.Permission{Id: p.Id, Name: p.Name})
	}
	return res, nil
}

// =================== Helpers & Internals ===================

func (s *UserService) generateToken(email, secret string, duration time.Duration) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   email,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func (s *UserService) resolvePermissionNames(names []string) []uint64 {
	var ids []uint64
	for _, n := range names {
		p, _ := s.repo.GetPermissionByName(n)
		if p != nil {
			ids = append(ids, p.Id)
		}
	}
	return ids
}

func (s *UserService) getPagination(p, ps int32) (int, int) {
	page, pageSize := int(p), int(ps)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	return page, pageSize
}

func mapEmployeeToProto(m *models.Employee) *user.Employee {
	if m == nil {
		return nil
	}
	var perms []string
	for _, p := range m.Permissions {
		perms = append(perms, p.Name)
	}
	return &user.Employee{
		Id:          m.Id,
		FirstName:   m.FirstName,
		LastName:    m.LastName,
		DateOfBirth: m.DateOfBirth.Format("2006-01-02"),
		Gender:      m.Gender,
		Email:       m.Email,
		PhoneNumber: m.PhoneNumber,
		Address:     m.Address,
		Username:    m.Username,
		Position:    m.Position,
		Department:  m.Department,
		Active:      m.Active,
		Permissions: perms,
	}
}

func mapClientToProto(m *models.Client) *user.Client {
	if m == nil {
		return nil
	}
	return &user.Client{
		Id:          m.Id,
		FirstName:   m.FirstName,
		LastName:    m.LastName,
		DateOfBirth: m.DateOfBirth.Format("2006-01-02"),
		Gender:      m.Gender,
		Email:       m.Email,
		PhoneNumber: m.PhoneNumber,
		Address:     m.Address,
	}
}

func handleDBError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "record not found") {
		return status.Error(codes.NotFound, "record not found")
	}
	return status.Errorf(codes.Internal, "database error: %v", err)
}
