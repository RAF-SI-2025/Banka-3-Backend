// Package server adapts the proto-generated UserService surface to the
// service layer. Each handler converts proto ↔ domain and delegates.
package server

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/service"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements userpb.UserServiceServer.
type Server struct {
	userpb.UnimplementedUserServiceServer
	Svc *service.Service
}

// New returns a server backed by svc.
func New(svc *service.Service) *Server { return &Server{Svc: svc} }

// =====================================================================
// Auth
// =====================================================================

func (s *Server) Login(ctx context.Context, in *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	r, err := s.Svc.Login(ctx, in.GetEmail(), in.GetPassword())
	if err != nil {
		return nil, err
	}
	return &userpb.LoginResponse{
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		AccessExpiresIn:  int64(r.AccessExpiresIn.Seconds()),
		RefreshExpiresIn: int64(r.RefreshExpiresIn.Seconds()),
		UserKind:         userKindToProto(r.UserKind),
		UserId:           r.UserID,
		Permissions:      r.Permissions,
	}, nil
}

func (s *Server) Refresh(ctx context.Context, in *userpb.RefreshRequest) (*userpb.RefreshResponse, error) {
	r, err := s.Svc.Refresh(ctx, in.GetRefreshToken())
	if err != nil {
		return nil, err
	}
	return &userpb.RefreshResponse{
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		AccessExpiresIn:  int64(r.AccessExpiresIn.Seconds()),
		RefreshExpiresIn: int64(r.RefreshExpiresIn.Seconds()),
	}, nil
}

func (s *Server) Logout(ctx context.Context, in *userpb.LogoutRequest) (*emptypb.Empty, error) {
	if err := s.Svc.Logout(ctx, in.GetRefreshToken()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) Me(ctx context.Context, _ *emptypb.Empty) (*userpb.MeResponse, error) {
	r, err := s.Svc.Me(ctx)
	if err != nil {
		return nil, err
	}
	out := &userpb.MeResponse{}
	switch {
	case r.Employee != nil:
		out.User = &userpb.MeResponse_Employee{Employee: employeeToProto(r.Employee)}
	case r.Client != nil:
		out.User = &userpb.MeResponse_Client{Client: clientToProto(r.Client)}
	}
	return out, nil
}

// =====================================================================
// Activation / reset
// =====================================================================

func (s *Server) ActivateAccount(ctx context.Context, in *userpb.ActivateAccountRequest) (*emptypb.Empty, error) {
	if err := s.Svc.ActivateAccount(ctx, in.GetToken(), in.GetNewPassword()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) ResendActivation(ctx context.Context, in *userpb.ResendActivationRequest) (*emptypb.Empty, error) {
	if err := s.Svc.ResendActivation(ctx, in.GetEmployeeId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) RequestPasswordReset(ctx context.Context, in *userpb.RequestPasswordResetRequest) (*emptypb.Empty, error) {
	if err := s.Svc.RequestPasswordReset(ctx, in.GetEmail()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) ConfirmPasswordReset(ctx context.Context, in *userpb.ConfirmPasswordResetRequest) (*emptypb.Empty, error) {
	if err := s.Svc.ConfirmPasswordReset(ctx, in.GetToken(), in.GetNewPassword()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// =====================================================================
// Employee management
// =====================================================================

func (s *Server) CreateEmployee(ctx context.Context, in *userpb.CreateEmployeeRequest) (*userpb.Employee, error) {
	dob, err := parseDate(in.GetDateOfBirth())
	if err != nil {
		return nil, err
	}
	emp, err := s.Svc.CreateEmployee(ctx, service.CreateEmployeeInput{
		Email:       in.GetEmail(),
		Username:    in.GetUsername(),
		FirstName:   in.GetFirstName(),
		LastName:    in.GetLastName(),
		DateOfBirth: dob,
		Gender:      genderFromProto(in.GetGender()),
		Phone:       in.GetPhone(),
		Address:     in.GetAddress(),
		Position:    in.GetPosition(),
		Department:  in.GetDepartment(),
		Active:      in.Active == nil || *in.Active, // spec default: active when unset
		Role:        in.GetRole(),
	})
	if err != nil {
		return nil, err
	}
	return employeeToProto(emp), nil
}

func (s *Server) ListEmployees(ctx context.Context, in *userpb.ListEmployeesRequest) (*userpb.ListEmployeesResponse, error) {
	page, pageSize := int(in.GetPage()), int(in.GetPageSize())
	emps, total, err := s.Svc.ListEmployees(ctx, domain.EmployeeFilter{
		Email:    in.GetEmailQuery(),
		Name:     in.GetNameQuery(),
		Position: in.GetPositionQuery(),
	}, page, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]*userpb.Employee, 0, len(emps))
	for _, e := range emps {
		out = append(out, employeeToProto(e))
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	return &userpb.ListEmployeesResponse{
		Employees: out,
		Page:      int32(page),
		PageSize:  int32(pageSize),
		Total:     total,
	}, nil
}

func (s *Server) GetEmployee(ctx context.Context, in *userpb.GetEmployeeRequest) (*userpb.Employee, error) {
	e, err := s.Svc.GetEmployee(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return employeeToProto(e), nil
}

func (s *Server) UpdateEmployee(ctx context.Context, in *userpb.UpdateEmployeeRequest) (*userpb.Employee, error) {
	dob, err := parseDateOptional(in.GetDateOfBirth())
	if err != nil {
		return nil, err
	}
	e, err := s.Svc.UpdateEmployee(ctx, service.UpdateEmployeeInput{
		ID:          in.GetId(),
		Email:       in.GetEmail(),
		Username:    in.GetUsername(),
		FirstName:   in.GetFirstName(),
		LastName:    in.GetLastName(),
		DateOfBirth: dob,
		Gender:      genderFromProto(in.GetGender()),
		Phone:       in.GetPhone(),
		Address:     in.GetAddress(),
		Position:    in.GetPosition(),
		Department:  in.GetDepartment(),
	})
	if err != nil {
		return nil, err
	}
	return employeeToProto(e), nil
}

func (s *Server) SetEmployeeActive(ctx context.Context, in *userpb.SetEmployeeActiveRequest) (*userpb.Employee, error) {
	e, err := s.Svc.SetEmployeeActive(ctx, in.GetId(), in.GetActive())
	if err != nil {
		return nil, err
	}
	return employeeToProto(e), nil
}

func (s *Server) SetEmployeePermissions(ctx context.Context, in *userpb.SetEmployeePermissionsRequest) (*userpb.Employee, error) {
	e, err := s.Svc.SetEmployeePermissions(ctx, in.GetId(), in.GetPermissions())
	if err != nil {
		return nil, err
	}
	return employeeToProto(e), nil
}

// =====================================================================
// Clients
// =====================================================================

func (s *Server) ListClients(ctx context.Context, in *userpb.ListClientsRequest) (*userpb.ListClientsResponse, error) {
	page, pageSize := int(in.GetPage()), int(in.GetPageSize())
	cs, total, err := s.Svc.ListClients(ctx, domain.ClientFilter{
		Email: in.GetEmailQuery(),
		Name:  in.GetNameQuery(),
	}, page, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]*userpb.Client, 0, len(cs))
	for _, c := range cs {
		out = append(out, clientToProto(c))
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	return &userpb.ListClientsResponse{
		Clients:  out,
		Page:     int32(page),
		PageSize: int32(pageSize),
		Total:    total,
	}, nil
}

func (s *Server) GetClient(ctx context.Context, in *userpb.GetClientRequest) (*userpb.Client, error) {
	c, err := s.Svc.GetClient(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return clientToProto(c), nil
}

func (s *Server) UpdateClient(ctx context.Context, in *userpb.UpdateClientRequest) (*userpb.Client, error) {
	dob, err := parseDateOptional(in.GetDateOfBirth())
	if err != nil {
		return nil, err
	}
	c, err := s.Svc.UpdateClient(ctx, service.UpdateClientInput{
		ID:          in.GetId(),
		Email:       in.GetEmail(),
		FirstName:   in.GetFirstName(),
		LastName:    in.GetLastName(),
		DateOfBirth: dob,
		Gender:      genderFromProto(in.GetGender()),
		Phone:       in.GetPhone(),
		Address:     in.GetAddress(),
	})
	if err != nil {
		return nil, err
	}
	return clientToProto(c), nil
}

// =====================================================================
// Internal RPCs
// =====================================================================

func (s *Server) GetSessionVersion(ctx context.Context, in *userpb.GetSessionVersionRequest) (*userpb.GetSessionVersionResponse, error) {
	v, err := s.Svc.GetSessionVersion(ctx, userKindFromProto(in.GetUserKind()), in.GetUserId())
	if err != nil {
		return nil, err
	}
	return &userpb.GetSessionVersionResponse{SessionVersion: v}, nil
}

// =====================================================================
// Conversions
// =====================================================================

func employeeToProto(e *domain.Employee) *userpb.Employee {
	return &userpb.Employee{
		Id:          e.ID,
		Email:       e.Email,
		Username:    e.Username,
		FirstName:   e.FirstName,
		LastName:    e.LastName,
		DateOfBirth: e.DateOfBirth.Format("2006-01-02"),
		Gender:      genderToProto(e.Gender),
		Phone:       e.Phone,
		Address:     e.Address,
		Position:    e.Position,
		Department:  e.Department,
		Active:      e.Active,
		Activated:   e.Activated(),
		Permissions: e.Permissions,
		CreatedAt:   timestamppb.New(e.CreatedAt),
		UpdatedAt:   timestamppb.New(e.UpdatedAt),
	}
}

func clientToProto(c *domain.Client) *userpb.Client {
	return &userpb.Client{
		Id:          c.ID,
		Email:       c.Email,
		FirstName:   c.FirstName,
		LastName:    c.LastName,
		DateOfBirth: c.DateOfBirth.Format("2006-01-02"),
		Gender:      genderToProto(c.Gender),
		Phone:       c.Phone,
		Address:     c.Address,
		Active:      c.Active,
		Permissions: c.Permissions,
		CreatedAt:   timestamppb.New(c.CreatedAt),
		UpdatedAt:   timestamppb.New(c.UpdatedAt),
	}
}

func genderToProto(g domain.Gender) userpb.Gender {
	switch g {
	case domain.GenderMale:
		return userpb.Gender_GENDER_MALE
	case domain.GenderFemale:
		return userpb.Gender_GENDER_FEMALE
	case domain.GenderOther:
		return userpb.Gender_GENDER_OTHER
	}
	return userpb.Gender_GENDER_UNSPECIFIED
}

func genderFromProto(g userpb.Gender) domain.Gender {
	switch g {
	case userpb.Gender_GENDER_MALE:
		return domain.GenderMale
	case userpb.Gender_GENDER_FEMALE:
		return domain.GenderFemale
	case userpb.Gender_GENDER_OTHER:
		return domain.GenderOther
	}
	return domain.GenderUnspecified
}

func userKindToProto(k domain.UserKind) userpb.UserKind {
	switch k {
	case domain.KindEmployee:
		return userpb.UserKind_USER_KIND_EMPLOYEE
	case domain.KindClient:
		return userpb.UserKind_USER_KIND_CLIENT
	}
	return userpb.UserKind_USER_KIND_UNSPECIFIED
}

func userKindFromProto(k userpb.UserKind) domain.UserKind {
	switch k {
	case userpb.UserKind_USER_KIND_EMPLOYEE:
		return domain.KindEmployee
	case userpb.UserKind_USER_KIND_CLIENT:
		return domain.KindClient
	}
	return ""
}

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, apperr.Validation("date_of_birth is required (YYYY-MM-DD)")
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, apperr.Validation("date_of_birth must be YYYY-MM-DD")
	}
	return t, nil
}

func parseDateOptional(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return parseDate(s)
}
