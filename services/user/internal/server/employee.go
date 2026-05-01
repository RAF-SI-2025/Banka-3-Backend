package server

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/model"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

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
		logger.FromContext(ctx).ErrorContext(ctx, "error generating salt", "err", salt_err)
	}

	// Spec p.38: every admin is also a supervisor.
	reqPerms := EnsureAdminImpliesSupervisor(req.Permissions)

	permissions := make([]model.Permission, 0, len(reqPerms))
	for _, permName := range reqPerms {
		var perm model.Permission
		if err := s.repo.Gorm.First(&perm, "name = ?", permName).Error; err != nil {
			logger.FromContext(ctx).WarnContext(ctx, "permission not found, skipping", "name", permName)
			continue
		}
		permissions = append(permissions, perm)
	}

	employee := model.Employee{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.BirthDate, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Username: req.Username, Position: req.Position,
		Department: req.Department, Salt_password: salt,
		Password: []byte{}, Active: true, Permissions: permissions}

	err := s.repo.CreateEmployee(employee)

	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "employee creation failed", "err", err)
		return nil, status.Error(codes.Internal, "Employee creation failed")
	}

	// Re-fetch to get the auto-assigned ID and properly loaded permissions
	created, err := s.repo.GetEmployeeByAttribute("email", employee.Email)
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "employee created but failed to fetch", "err", err)
		return employee.ToProtobuf(), nil
	}

	// Send activation email so the employee can set their own password
	_, emailErr := s.RequestInitialPasswordSet(ctx, &userpb.PasswordActionRequest{
		Email: req.Email,
	})
	if emailErr != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "employee created but activation email failed", "err", emailErr)
	}

	return created.ToProtobuf(), nil
}

// GetActuaries returns employees that hold the `agent` permission, with the same
// filter set as GetEmployees. Spec page 39 — supervisor portal.
func (s *Server) GetActuaries(ctx context.Context, req *userpb.GetEmployeesRequest) (*userpb.GetEmployeesResponse, error) {
	restrictions := repo.UserRestrictions{
		"first_name": req.FirstName,
		"last_name":  req.LastName,
		"email":      req.Email,
		"position":   req.Position,
	}

	employees, err := s.repo.GetAllEmployees(restrictions)
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "error retrieving actuaries", "err", err)
		return nil, status.Error(codes.Internal, "Failed to retrieve actuaries")
	}

	out := make([]*userpb.GetEmployeesResponse_Employee, 0, len(employees))
	for _, emp := range employees {
		isAgent := false
		for _, p := range emp.Permissions {
			if p.Name == "agent" {
				isAgent = true
				break
			}
		}
		if !isAgent {
			continue
		}
		out = append(out, &userpb.GetEmployeesResponse_Employee{
			Id:           int64(emp.Id),
			FirstName:    emp.First_name,
			LastName:     emp.Last_name,
			Email:        emp.Email,
			Position:     emp.Position,
			PhoneNumber:  emp.Phone_number,
			Active:       emp.Active,
			Limit:        emp.Limit,
			UsedLimit:    emp.Used_limit,
			NeedApproval: emp.Need_approval,
		})
	}
	return &userpb.GetEmployeesResponse{Employees: out}, nil
}

// UpdateEmployeeTradingLimit sets an agent's daily trading limit and/or used_limit.
// Only admins and supervisors may call this. The caller is identified by CallerEmail
// (forwarded from the gateway).
func (s *Server) UpdateEmployeeTradingLimit(ctx context.Context, req *userpb.UpdateEmployeeTradingLimitRequest) (*userpb.GetEmployeeResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if req.Limit == nil && req.UsedLimit == nil {
		return nil, status.Error(codes.InvalidArgument, "limit or used_limit must be provided")
	}
	if req.Limit != nil && *req.Limit < 0 {
		return nil, status.Error(codes.InvalidArgument, "limit must be non-negative")
	}
	if req.UsedLimit != nil && *req.UsedLimit < 0 {
		return nil, status.Error(codes.InvalidArgument, "used_limit must be non-negative")
	}

	if !callerCanManageLimits(ctx, s, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "only admins and supervisors may change an agent's limit")
	}

	target, err := s.repo.GetEmployeeByAttribute("id", req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, status.Error(codes.Internal, "failed to load employee")
	}

	updates := map[string]any{"updated_at": time.Now()}
	if req.Limit != nil {
		updates["limit"] = *req.Limit
	}
	if req.UsedLimit != nil {
		updates["used_limit"] = *req.UsedLimit
	}

	if err := s.repo.Gorm.Model(&model.Employee{}).Where("id = ?", target.Id).Updates(updates).Error; err != nil {
		return nil, status.Error(codes.Internal, "failed to update trading limit")
	}

	reloaded, err := s.repo.GetEmployeeByAttribute("id", req.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to reload employee")
	}
	return reloaded.ToProtobuf(), nil
}

// UpdateEmployeeNeedApproval flips the need_approval flag on an employee. Admins
// and supervisors only — caller is identified by CallerEmail.
func (s *Server) UpdateEmployeeNeedApproval(ctx context.Context, req *userpb.UpdateEmployeeNeedApprovalRequest) (*userpb.GetEmployeeResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if !callerCanManageLimits(ctx, s, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "only admins and supervisors may change need_approval")
	}

	target, err := s.repo.GetEmployeeByAttribute("id", req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, status.Error(codes.Internal, "failed to load employee")
	}

	if err := s.repo.Gorm.Model(&model.Employee{}).Where("id = ?", target.Id).Updates(map[string]any{
		"need_approval": req.NeedApproval,
		"updated_at":    time.Now(),
	}).Error; err != nil {
		return nil, status.Error(codes.Internal, "failed to update need_approval")
	}

	reloaded, err := s.repo.GetEmployeeByAttribute("id", req.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to reload employee")
	}
	return reloaded.ToProtobuf(), nil
}

func (s *Server) DeleteEmployee(_ context.Context, req *userpb.DeleteEmployeeRequest) (*userpb.DeleteEmployeeResponse, error) {
	err := s.repo.DeleteEmployee(model.Employee{
		Id: uint64(req.Id),
	})

	if err != nil {
		if errors.Is(err, repo.ErrEmployeeNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, err
	}
	return &userpb.DeleteEmployeeResponse{Success: true}, nil
}

func (s *Server) GetEmployees(ctx context.Context, req *userpb.GetEmployeesRequest) (*userpb.GetEmployeesResponse, error) {
	map_func := func(emp model.Employee) *userpb.GetEmployeesResponse_Employee {
		return &userpb.GetEmployeesResponse_Employee{
			Id:           int64(emp.Id),
			FirstName:    emp.First_name,
			LastName:     emp.Last_name,
			Email:        emp.Email,
			Position:     emp.Position,
			PhoneNumber:  emp.Phone_number,
			Active:       emp.Active,
			Limit:        emp.Limit,
			UsedLimit:    emp.Used_limit,
			NeedApproval: emp.Need_approval,
		}
	}
	restrictions := repo.UserRestrictions{"first_name": req.FirstName, "last_name": req.LastName, "email": req.Email, "position": req.Position}

	employees, err := s.repo.GetAllEmployees(restrictions)
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "error retrieving employees", "err", err)
		return nil, status.Error(codes.Internal, "Failed to retrieve employees")
	}

	var employee_responses []*userpb.GetEmployeesResponse_Employee
	for _, emp := range employees {
		employee_responses = append(employee_responses, map_func(emp))
	}

	return &userpb.GetEmployeesResponse{Employees: employee_responses}, nil
}

func callerIsAdmin(_ context.Context, s *Server, callerEmail string) bool {
	if strings.TrimSpace(callerEmail) == "" {
		return false
	}
	caller, err := s.repo.GetEmployeeByAttribute("email", callerEmail)
	if err != nil || caller == nil {
		return false
	}
	for _, p := range caller.Permissions {
		if p.Name == "admin" {
			return true
		}
	}
	return false
}

func (s *Server) UpdateEmployee(ctx context.Context, req *userpb.UpdateEmployeeRequest) (*userpb.GetEmployeeResponse, error) {
	existing, existingErr := s.repo.GetEmployeeByAttribute("id", req.Id)

	// Spec p.38: an admin may not edit another admin. Self-edits are allowed.
	if existingErr == nil && existing != nil {
		existingPerms := permissionSet(existing.Permissions)
		if _, targetIsAdmin := existingPerms["admin"]; targetIsAdmin &&
			!strings.EqualFold(strings.TrimSpace(req.CallerEmail), existing.Email) {
			return nil, status.Error(codes.PermissionDenied, "admins cannot edit other admins")
		}
	}

	if !req.Active {
		if existingErr == nil && existing != nil {
			for _, p := range existing.Permissions {
				if p.Name == "admin" {
					return nil, status.Error(codes.PermissionDenied, "cannot deactivate an admin")
				}
			}
		}
	}

	// Spec p.38: every admin is also a supervisor. Granting admin implicitly
	// grants supervisor; revoking admin leaves supervisor untouched.
	if req.Permissions != nil {
		req.Permissions = EnsureAdminImpliesSupervisor(req.Permissions)
	}

	// Only admins may grant or revoke the `agent` / `supervisor` permissions.
	if req.Permissions != nil && existingErr == nil && existing != nil {
		oldSet := permissionSet(existing.Permissions)
		newSet := NamesToSet(req.Permissions)
		if TogglesTradingRole(oldSet, newSet) {
			if !callerIsAdmin(ctx, s, req.CallerEmail) {
				return nil, status.Error(codes.PermissionDenied, "only admins may grant or revoke agent/supervisor permissions")
			}
		}
	}

	var permissions []model.Permission
	for _, perm := range req.Permissions {
		// yes these are invalid. i don't care
		permissions = append(permissions, model.Permission{Id: 0, Name: perm})
	}

	emp := model.Employee{
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

	updated, err := s.repo.UpdateEmployee(emp)
	if err != nil {
		if errors.Is(err, repo.ErrEmployeeNotFound) {
			return nil, status.Error(codes.NotFound, "Employee not found")
		}
		if errors.Is(err, repo.ErrUnknownPermission) {
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

	return updated.ToProtobuf(), nil

}

func (s *Server) GetEmployeeByEmail(_ context.Context, req *userpb.GetUserByEmailRequest) (*userpb.GetEmployeeResponse, error) {
	resp, err := s.repo.GetEmployeeByAttribute("email", req.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, status.Error(codes.Internal, "failed to get employee")
	}
	return resp.ToProtobuf(), nil
}

func (s *Server) GetEmployeeById(_ context.Context, req *userpb.GetUserByIdRequest) (*userpb.GetEmployeeResponse, error) {
	resp, err := s.repo.GetEmployeeByAttribute("id", req.Id)

	if err != nil {
		return nil, err
	}
	return resp.ToProtobuf(), nil
}

func callerCanManageLimits(_ context.Context, s *Server, callerEmail string) bool {
	if strings.TrimSpace(callerEmail) == "" {
		return false
	}
	caller, err := s.repo.GetEmployeeByAttribute("email", callerEmail)
	if err != nil || caller == nil {
		return false
	}
	for _, p := range caller.Permissions {
		if p.Name == "admin" || p.Name == "supervisor" {
			return true
		}
	}
	return false
}
