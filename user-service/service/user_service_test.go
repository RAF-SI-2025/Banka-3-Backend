package service

import (
	"context"
	"errors"
	"testing"
	"user-service/models"
	"user-service/pb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// #region Mock Repository
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) CreateEmployee(e *models.Employee, ids []uint64) error {
	args := m.Called(e, ids)
	if args.Get(0) == nil {
		e.ID = 1
		return nil
	}
	return args.Error(0)
}
func (m *MockUserRepository) GetEmployeeByID(id uint64) (*models.Employee, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Employee), args.Error(1)
}
func (m *MockUserRepository) ListEmployees(p, s int) ([]models.Employee, int64, error) {
	args := m.Called(p, s)
	return args.Get(0).([]models.Employee), args.Get(1).(int64), args.Error(2)
}
func (m *MockUserRepository) UpdateEmployee(e *models.Employee, ids []uint64) error {
	return m.Called(e, ids).Error(0)
}
func (m *MockUserRepository) DeleteEmployee(id uint64) error {
	return m.Called(id).Error(0)
}
func (m *MockUserRepository) CreateClient(c *models.Client) error {
	args := m.Called(c)
	if args.Get(0) == nil {
		c.ID = 1
		return nil
	}
	return args.Error(0)
}
func (m *MockUserRepository) GetClientByID(id uint64) (*models.Client, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}
func (m *MockUserRepository) ListClients(p, s int) ([]models.Client, int64, error) {
	args := m.Called(p, s)
	return args.Get(0).([]models.Client), args.Get(1).(int64), args.Error(2)
}
func (m *MockUserRepository) UpdateClient(c *models.Client) error {
	return m.Called(c).Error(0)
}
func (m *MockUserRepository) DeleteClient(id uint64) error {
	return m.Called(id).Error(0)
}
func (m *MockUserRepository) CreatePermission(p *models.Permission) error {
	args := m.Called(p)
	if args.Get(0) == nil {
		p.ID = 1
		return nil
	}
	return args.Error(0)
}
func (m *MockUserRepository) ListPermissions() ([]models.Permission, error) {
	args := m.Called()
	return args.Get(0).([]models.Permission), args.Error(1)
}

// #endregion

// #region Error Handler Tests
func TestHandleDBError(t *testing.T) {
	tests := []struct {
		name     string
		input    error
		expected codes.Code
	}{
		{"NilError", nil, codes.OK},
		{"NotFound", errors.New("record not found"), codes.NotFound},
		{"DuplicatePostgres", errors.New("duplicate key value violates unique constraint"), codes.AlreadyExists},
		{"DuplicateSQLite", errors.New("UNIQUE constraint failed"), codes.AlreadyExists},
		{"Internal", errors.New("some random db crash"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleDBError(tt.input)
			assert.Equal(t, tt.expected, status.Code(err))
		})
	}
}

// #endregion

// #region Employee Tests
func TestEmployeeService_Comprehensive(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	t.Run("Create_FullSuccess", func(t *testing.T) {
		req := &pb.CreateEmployeeRequest{
			Email: "test@test.com", Username: "testuser", DateOfBirth: "1990-01-01",
		}
		mockRepo.On("CreateEmployee", mock.Anything, mock.Anything).Return(nil).Once()
		res, err := svc.CreateEmployee(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("Create_ValidationFails", func(t *testing.T) {
		_, err := svc.CreateEmployee(context.Background(), &pb.CreateEmployeeRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("List_PaginationAndMapping", func(t *testing.T) {
		mockEmps := []models.Employee{{FirstName: "Alice", Permissions: []models.Permission{{Name: "P1"}}}}
		mockRepo.On("ListEmployees", 1, 10).Return(mockEmps, int64(1), nil).Once()

		res, err := svc.ListEmployees(context.Background(), &pb.ListEmployeesRequest{Page: -1, PageSize: 0})
		assert.NoError(t, err)
		assert.Len(t, res.Employees, 1)
		assert.Equal(t, "Alice", res.Employees[0].FirstName)
	})

	t.Run("Update_Success", func(t *testing.T) {
		mockRepo.On("GetEmployeeByID", uint64(1)).Return(&models.Employee{FirstName: "Old"}, nil).Once()
		mockRepo.On("UpdateEmployee", mock.Anything, mock.Anything).Return(nil).Once()

		res, err := svc.UpdateEmployee(context.Background(), &pb.UpdateEmployeeRequest{Id: 1, FirstName: "New"})
		assert.NoError(t, err)
		assert.Equal(t, "New", res.Employee.FirstName)
	})

	t.Run("Delete_ErrorMapping", func(t *testing.T) {
		mockRepo.On("DeleteEmployee", uint64(99)).Return(errors.New("record not found")).Once()
		_, err := svc.DeleteEmployee(context.Background(), &pb.DeleteEmployeeRequest{Id: 99})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

// #endregion

// #region Client Tests
func TestClientService_Comprehensive(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	t.Run("Create_NoEmail", func(t *testing.T) {
		_, err := svc.CreateClient(context.Background(), &pb.CreateClientRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Update_Success", func(t *testing.T) {
		mockRepo.On("GetClientByID", uint64(1)).Return(&models.Client{FirstName: "C"}, nil).Once()
		mockRepo.On("UpdateClient", mock.Anything).Return(nil).Once()
		res, err := svc.UpdateClient(context.Background(), &pb.UpdateClientRequest{Id: 1, FirstName: "U"})
		assert.NoError(t, err)
		assert.Equal(t, "U", res.Client.FirstName)
	})

	t.Run("List_Error", func(t *testing.T) {
		mockRepo.On("ListClients", 1, 10).Return([]models.Client{}, int64(0), errors.New("db fail")).Once()
		_, err := svc.ListClients(context.Background(), &pb.ListClientsRequest{Page: 1, PageSize: 10})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

// #endregion

// #region Mapper and Permissions
func TestMisc_Comprehensive(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	t.Run("Mapper_NilSafety", func(t *testing.T) {
		assert.Nil(t, mapEmployeeToProto(nil))
		assert.Nil(t, mapClientToProto(nil))
		assert.Nil(t, mapPermissionToProto(nil))
	})

	t.Run("CreatePermission_Empty", func(t *testing.T) {
		_, err := svc.CreatePermission(context.Background(), &pb.CreatePermissionRequest{Name: ""})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ListPermissions_DBError", func(t *testing.T) {
		mockRepo.On("ListPermissions").Return([]models.Permission{}, errors.New("fail")).Once()
		_, err := svc.ListPermissions(context.Background(), &emptypb.Empty{})
		assert.Error(t, err)
	})
}

// #endregion

func TestComprehensiveCoverage(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	// --- 1. Test handleDBError Branches ---
	t.Run("HandleDBError_AllBranches", func(t *testing.T) {
		assert.Equal(t, codes.NotFound, status.Code(handleDBError(errors.New("record not found"))))
		assert.Equal(t, codes.AlreadyExists, status.Code(handleDBError(errors.New("23505 duplicate key"))))
		assert.Equal(t, codes.AlreadyExists, status.Code(handleDBError(errors.New("UNIQUE constraint failed"))))
		assert.Equal(t, codes.FailedPrecondition, status.Code(handleDBError(errors.New("23503 foreign key violation"))))
		assert.Equal(t, codes.Internal, status.Code(handleDBError(errors.New("unknown crash"))))
		assert.NoError(t, handleDBError(nil))
	})

	// --- 2. Test CreateEmployee Validation & Date Parsing ---
	t.Run("CreateEmployee_Validation", func(t *testing.T) {
		// Missing fields
		_, err := svc.CreateEmployee(context.Background(), &pb.CreateEmployeeRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))

		// Bad Date
		req := &pb.CreateEmployeeRequest{Email: "a@b.com", Username: "u", DateOfBirth: "01-01-1990"}
		_, err = svc.CreateEmployee(context.Background(), req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	// --- 3. Test Mappers (Exercise mapEmployeeToProto loop) ---
	t.Run("Mappers_FullExercise", func(t *testing.T) {
		emp := &models.Employee{
			FirstName:   "Test",
			Permissions: []models.Permission{{Name: "Admin"}},
		}
		res := mapEmployeeToProto(emp)
		assert.Equal(t, "Test", res.FirstName)
		assert.Equal(t, "Admin", res.Permissions[0].Name)

		assert.Nil(t, mapEmployeeToProto(nil))
		assert.Nil(t, mapClientToProto(nil))
	})

	// --- 4. Test List Pagination Logic ---
	t.Run("ListEmployees_Pagination", func(t *testing.T) {
		mockRepo.On("ListEmployees", 1, 10).Return([]models.Employee{}, int64(0), nil).Once()
		_, err := svc.ListEmployees(context.Background(), &pb.ListEmployeesRequest{Page: 0, PageSize: 0})
		assert.NoError(t, err)
	})
}
