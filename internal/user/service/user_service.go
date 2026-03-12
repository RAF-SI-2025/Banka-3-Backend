package service

import (
	"banka-raf/gen/user"
	"banka-raf/internal/user/models"
	"banka-raf/internal/user/repository"
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type UserService struct {
	user.UnimplementedUserServiceServer
	repo repository.IUserRepository
}

func NewUserService(repo repository.IUserRepository) *UserService {
	return &UserService{repo: repo}
}

// =================== Helpers ===================

func handleDBError(err error) error {
	if err == nil {
		return nil
	}
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "record not found") {
		return status.Error(codes.NotFound, "not found")
	}
	if strings.Contains(errStr, "unique") || strings.Contains(errStr, "23505") {
		return status.Error(codes.AlreadyExists, "duplicate record")
	}
	if strings.Contains(errStr, "foreign key") || strings.Contains(errStr, "23503") {
		return status.Error(codes.FailedPrecondition, "referenced data does not exist")
	}
	return status.Errorf(codes.Internal, "unexpected db error: %v", err)
}

// =================== Employee ===================

func (s *UserService) ListEmployees(ctx context.Context, req *user.ListEmployeesRequest) (*user.ListEmployeesResponse, error) {

	page := 1
	pageSize := 50

	emps, _, err := s.repo.ListEmployees(
		page,
		pageSize,
		req.Email,
		req.FirstName,
		req.LastName,
		req.Position,
	)

	if err != nil {
		return nil, handleDBError(err)
	}

	res := &user.ListEmployeesResponse{}

	for _, e := range emps {
		emp := e
		res.Employees = append(res.Employees, mapEmployeeToProto(&emp))
	}

	return res, nil
}

func (s *UserService) CreateEmployee(ctx context.Context, req *user.CreateEmployeeRequest) (*user.Employee, error) {
	if req.Email == "" || req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "email and username required")
	}
	dob, err := time.Parse("2006-01-02", req.DateOfBirth)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid date format")
	}

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
		IsActive:    req.Active,
	}

	var permIDs []uint
	for _, p := range req.Permissions {
		per, err := s.repo.GetPermissionByName(p)
		if err != nil || per == nil {
			return nil, status.Errorf(codes.InvalidArgument, "permission not found: %s", p)
		}
		permIDs = append(permIDs, per.ID)
	}

	if err := s.repo.CreateEmployee(emp, permIDs); err != nil {
		return nil, handleDBError(err)
	}
	return mapEmployeeToProto(emp), nil
}

func (s *UserService) GetEmployee(ctx context.Context, req *user.GetEmployeeRequest) (*user.Employee, error) {
	if req.EmployeeId == 0 {
		return nil, status.Error(codes.InvalidArgument, "employee_id required")
	}
	emp, err := s.repo.GetEmployeeByID(uint(req.EmployeeId))
	if err != nil {
		return nil, handleDBError(err)
	}
	return mapEmployeeToProto(emp), nil
}

func (s *UserService) UpdateEmployee(ctx context.Context, req *user.UpdateEmployeeRequest) (*user.Employee, error) {
	emp, err := s.repo.GetEmployeeByID(uint(req.EmployeeId))
	if err != nil {
		return nil, handleDBError(err)
	}

	dob, err := time.Parse("2006-01-02", req.DateOfBirth)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid date format")
	}

	emp.FirstName = req.FirstName
	emp.LastName = req.LastName
	emp.DateOfBirth, _ = time.Parse("2006-01-02", req.DateOfBirth)
	emp.DateOfBirth = dob
	emp.Gender = req.Gender
	emp.Email = req.Email
	emp.PhoneNumber = req.PhoneNumber
	emp.Address = req.Address
	emp.Position = req.Position
	emp.Department = req.Department
	emp.IsActive = req.Active

	var permIDs []uint
	for _, p := range req.Permissions {
		per, err := s.repo.GetPermissionByName(p)
		if err != nil || per == nil {
			return nil, status.Errorf(codes.InvalidArgument, "permission not found: %s", p)
		}
		permIDs = append(permIDs, per.ID)
	}

	if err := s.repo.UpdateEmployee(emp, permIDs); err != nil {
		return nil, handleDBError(err)
	}
	return mapEmployeeToProto(emp), nil
}

func (s *UserService) DeleteEmployee(ctx context.Context, req *user.DeleteEmployeeRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteEmployee(uint(req.EmployeeId)); err != nil {
		return nil, handleDBError(err)
	}
	return &emptypb.Empty{}, nil
}

// =================== Client ===================

func (s *UserService) ListClients(ctx context.Context, req *user.ListClientsRequest) (*user.ListClientsResponse, error) {

	page := 1
	pageSize := 50

	clients, _, err := s.repo.ListClients(
		page,
		pageSize,
		"", // first name filter
		"", // last name filter
		"", // email filter
	)

	if err != nil {
		return nil, handleDBError(err)
	}

	res := &user.ListClientsResponse{}

	for _, c := range clients {
		cli := c
		res.Clients = append(res.Clients, mapClientToProto(&cli))
	}

	return res, nil
}

func (s *UserService) CreateClient(ctx context.Context, req *user.CreateClientRequest) (*user.Client, error) {
	cli := &models.Client{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth, // int64 timestamp
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
	}
	if err := s.repo.CreateClient(cli); err != nil {
		return nil, handleDBError(err)
	}
	return mapClientToProto(cli), nil
}

func (s *UserService) GetClient(ctx context.Context, req *user.GetClientRequest) (*user.Client, error) {
	cli, err := s.repo.GetClientByID(uint(req.ClientId))
	if err != nil {
		return nil, handleDBError(err)
	}
	return mapClientToProto(cli), nil
}

func (s *UserService) UpdateClient(ctx context.Context, req *user.UpdateClientRequest) (*user.Client, error) {
	cli, err := s.repo.GetClientByID(uint(req.ClientId))
	if err != nil {
		return nil, handleDBError(err)
	}
	cli.FirstName = req.FirstName
	cli.LastName = req.LastName
	cli.DateOfBirth = req.DateOfBirth
	cli.Gender = req.Gender
	cli.Email = req.Email
	cli.PhoneNumber = req.PhoneNumber
	cli.Address = req.Address

	if err := s.repo.UpdateClient(cli); err != nil {
		return nil, handleDBError(err)
	}
	return mapClientToProto(cli), nil
}

func (s *UserService) DeleteClient(ctx context.Context, req *user.DeleteClientRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteClient(uint(req.ClientId)); err != nil {
		return nil, handleDBError(err)
	}
	return &emptypb.Empty{}, nil
}

// =================== Permissions ===================

func (s *UserService) ListPermissions(ctx context.Context, _ *emptypb.Empty) (*user.ListPermissionsResponse, error) {
	perms, err := s.repo.ListPermissions()
	if err != nil {
		return nil, handleDBError(err)
	}
	res := &user.ListPermissionsResponse{}
	for _, p := range perms {
		res.Permissions = append(res.Permissions, mapPermissionToProto(&p))
	}
	return res, nil
}

// =================== Mapping ===================

func mapEmployeeToProto(m *models.Employee) *user.Employee {
	if m == nil {
		return nil
	}

	perms := make([]string, len(m.Permissions))
	for i, p := range m.Permissions {
		perms[i] = p.Name
	}

	return &user.Employee{
		Id:          uint64(m.ID),
		FirstName:   m.FirstName,
		LastName:    m.LastName,
		DateOfBirth: m.DateOfBirth.Format("2006-01-02"), // time.Time -> string
		Gender:      m.Gender,
		Email:       m.Email,
		PhoneNumber: m.PhoneNumber,
		Address:     m.Address,
		Username:    m.Username,
		Position:    m.Position,
		Department:  m.Department,
		Active:      m.IsActive,
		Permissions: perms,
	}
}

func mapClientToProto(m *models.Client) *user.Client {
	if m == nil {
		return nil
	}

	var accounts []string
	if m.ConnectedAccounts != "" {
		accounts = strings.Split(m.ConnectedAccounts, ",")
	}

	return &user.Client{
		Id:                uint64(m.ID),
		FirstName:         m.FirstName,
		LastName:          m.LastName,
		DateOfBirth:       m.DateOfBirth, // int64 timestamp
		Gender:            m.Gender,
		Email:             m.Email,
		PhoneNumber:       m.PhoneNumber,
		Address:           m.Address,
		ConnectedAccounts: accounts,
	}
}

func mapPermissionToProto(m *models.Permission) *user.Permission {
	if m == nil {
		return nil
	}
	return &user.Permission{
		Id:          uint64(m.ID),
		Name:        m.Name,
		Description: m.Description,
	}
}
