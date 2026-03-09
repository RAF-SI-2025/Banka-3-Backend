package service

import (
	"context"
	"strings"
	"time"

	"user-service/models"
	"user-service/pb"
	"user-service/repository"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type UserService struct {
	pb.UnimplementedUserServiceServer
	repo repository.IUserRepository
}

func NewUserService(repo repository.IUserRepository) *UserService {
	return &UserService{repo: repo}
}

// #region Helper: Error Handling
func handleDBError(err error) error {
	if err == nil {
		return nil
	}

	errStr := strings.ToLower(err.Error())

	// 1. Check for Not Found
	if strings.Contains(errStr, "record not found") || strings.Contains(errStr, "not found") {
		return status.Error(codes.NotFound, "resource not found")
	}

	// 2. Check for Duplicates (Postgres/SQLite/MySQL)
	if strings.Contains(errStr, "duplicate") ||
		strings.Contains(errStr, "unique constraint") ||
		strings.Contains(errStr, "23505") { // SQLSTATE for Unique Violation
		return status.Error(codes.AlreadyExists, "this record already exists")
	}

	// 3. Check for Foreign Key Violations (Parent missing)
	if strings.Contains(errStr, "foreign key") || strings.Contains(errStr, "23503") {
		return status.Error(codes.FailedPrecondition, "referenced resource does not exist")
	}

	// Default to Internal if we really don't know what happened
	return status.Errorf(codes.Internal, "database error: %v", err)
}

// #endregion

// #region Employee Handlers

func (s *UserService) CreateEmployee(ctx context.Context, req *pb.CreateEmployeeRequest) (*pb.EmployeeResponse, error) {
	if req.Email == "" || req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "email and username are required")
	}

	dob, err := time.Parse("2006-01-02", req.DateOfBirth)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid date format: %v", err)
	}

	emp := &models.Employee{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Email:       req.Email,
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
		IsActive:    req.Active,
		Gender:      req.Gender,
		DateOfBirth: dob,
	}

	if err := s.repo.CreateEmployee(emp, req.PermissionIds); err != nil {
		return nil, handleDBError(err)
	}
	return &pb.EmployeeResponse{Employee: mapEmployeeToProto(emp)}, nil
}

func (s *UserService) GetEmployee(ctx context.Context, req *pb.GetEmployeeRequest) (*pb.EmployeeResponse, error) {
	if req.Id == 0 {
		return nil, status.Error(codes.InvalidArgument, "valid ID is required")
	}
	emp, err := s.repo.GetEmployeeByID(req.Id)
	if err != nil {
		return nil, handleDBError(err)
	}
	return &pb.EmployeeResponse{Employee: mapEmployeeToProto(emp)}, nil
}

func (s *UserService) ListEmployees(ctx context.Context, req *pb.ListEmployeesRequest) (*pb.ListEmployeesResponse, error) {
	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 10
	}

	emps, total, err := s.repo.ListEmployees(page, pageSize)
	if err != nil {
		return nil, handleDBError(err)
	}

	var protoEmps []*pb.Employee
	for _, e := range emps {
		protoEmps = append(protoEmps, mapEmployeeToProto(&e))
	}
	return &pb.ListEmployeesResponse{Employees: protoEmps, Total: int32(total)}, nil
}

func (s *UserService) UpdateEmployee(ctx context.Context, req *pb.UpdateEmployeeRequest) (*pb.EmployeeResponse, error) {
	emp, err := s.repo.GetEmployeeByID(req.Id)
	if err != nil {
		return nil, handleDBError(err)
	}

	emp.FirstName = req.FirstName
	emp.LastName = req.LastName
	emp.Email = req.Email
	emp.Position = req.Position
	emp.Department = req.Department
	emp.PhoneNumber = req.PhoneNumber
	emp.Address = req.Address
	emp.IsActive = req.Active

	if err := s.repo.UpdateEmployee(emp, req.PermissionIds); err != nil {
		return nil, handleDBError(err)
	}
	return &pb.EmployeeResponse{Employee: mapEmployeeToProto(emp)}, nil
}

func (s *UserService) DeleteEmployee(ctx context.Context, req *pb.DeleteEmployeeRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteEmployee(req.Id); err != nil {
		return nil, handleDBError(err)
	}
	return &emptypb.Empty{}, nil
}

// #endregion

// #region Client Handlers

func (s *UserService) CreateClient(ctx context.Context, req *pb.CreateClientRequest) (*pb.ClientResponse, error) {
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "client email is required")
	}

	cli := &models.Client{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
	}
	if err := s.repo.CreateClient(cli); err != nil {
		return nil, handleDBError(err)
	}
	return &pb.ClientResponse{Client: mapClientToProto(cli)}, nil
}

func (s *UserService) GetClient(ctx context.Context, req *pb.GetClientRequest) (*pb.ClientResponse, error) {
	cli, err := s.repo.GetClientByID(req.Id)
	if err != nil {
		return nil, handleDBError(err)
	}
	return &pb.ClientResponse{Client: mapClientToProto(cli)}, nil
}

func (s *UserService) ListClients(ctx context.Context, req *pb.ListClientsRequest) (*pb.ListClientsResponse, error) {
	clients, total, err := s.repo.ListClients(int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, handleDBError(err)
	}

	var protoClients []*pb.Client
	for _, c := range clients {
		protoClients = append(protoClients, mapClientToProto(&c))
	}
	return &pb.ListClientsResponse{Clients: protoClients, Total: int32(total)}, nil
}

func (s *UserService) UpdateClient(ctx context.Context, req *pb.UpdateClientRequest) (*pb.ClientResponse, error) {
	cli, err := s.repo.GetClientByID(req.Id)
	if err != nil {
		return nil, handleDBError(err)
	}

	cli.FirstName = req.FirstName
	cli.LastName = req.LastName
	cli.Email = req.Email
	cli.PhoneNumber = req.PhoneNumber
	cli.Address = req.Address
	cli.Gender = req.Gender

	if err := s.repo.UpdateClient(cli); err != nil {
		return nil, handleDBError(err)
	}
	return &pb.ClientResponse{Client: mapClientToProto(cli)}, nil
}

func (s *UserService) DeleteClient(ctx context.Context, req *pb.DeleteClientRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteClient(req.Id); err != nil {
		return nil, handleDBError(err)
	}
	return &emptypb.Empty{}, nil
}

// #endregion

// #region Permission Handlers

func (s *UserService) CreatePermission(ctx context.Context, req *pb.CreatePermissionRequest) (*pb.PermissionResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "permission name cannot be empty")
	}
	p := &models.Permission{Name: req.Name, Description: req.Description}
	if err := s.repo.CreatePermission(p); err != nil {
		return nil, handleDBError(err)
	}
	return &pb.PermissionResponse{Permission: mapPermissionToProto(p)}, nil
}

func (s *UserService) ListPermissions(ctx context.Context, _ *emptypb.Empty) (*pb.ListPermissionsResponse, error) {
	perms, err := s.repo.ListPermissions()
	if err != nil {
		return nil, handleDBError(err)
	}
	var protoPerms []*pb.Permission
	for _, p := range perms {
		protoPerms = append(protoPerms, mapPermissionToProto(&p))
	}
	return &pb.ListPermissionsResponse{Permissions: protoPerms}, nil
}

// #endregion

// #region Mappers

func mapEmployeeToProto(m *models.Employee) *pb.Employee {
	if m == nil {
		return nil
	}
	var perms []*pb.Permission
	for _, p := range m.Permissions {
		perms = append(perms, mapPermissionToProto(&p))
	}

	return &pb.Employee{
		Id:          uint64(m.ID),
		FirstName:   m.FirstName,
		LastName:    m.LastName,
		Email:       m.Email,
		Username:    m.Username,
		Position:    m.Position,
		Department:  m.Department,
		PhoneNumber: m.PhoneNumber,
		Address:     m.Address,
		Active:      m.IsActive,
		Gender:      m.Gender,
		DateOfBirth: m.DateOfBirth.Format("2006-01-02"),
		Permissions: perms,
	}
}

func mapClientToProto(m *models.Client) *pb.Client {
	if m == nil {
		return nil
	}
	return &pb.Client{
		Id:                uint64(m.ID),
		FirstName:         m.FirstName,
		LastName:          m.LastName,
		DateOfBirth:       m.DateOfBirth,
		Gender:            m.Gender,
		Email:             m.Email,
		PhoneNumber:       m.PhoneNumber,
		Address:           m.Address,
		ConnectedAccounts: m.ConnectedAccounts,
	}
}

func mapPermissionToProto(m *models.Permission) *pb.Permission {
	if m == nil {
		return nil
	}
	return &pb.Permission{
		Id:          uint64(m.ID),
		Name:        m.Name,
		Description: m.Description,
	}
}

// #endregion
