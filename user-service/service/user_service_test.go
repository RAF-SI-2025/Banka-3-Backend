package service

import (
	"context"
	"errors"
	"testing"
	"time"

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
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
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
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
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
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Permission), args.Error(1)
}

// #endregion

func TestUserService_Complete(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)
	ctx := context.Background()

	// #region Error Handling Coverage
	t.Run("DatabaseErrorMappings", func(t *testing.T) {
		assert.Equal(t, codes.NotFound, status.Code(handleDBError(errors.New("record not found"))))
		assert.Equal(t, codes.AlreadyExists, status.Code(handleDBError(errors.New("23505: unique"))))
		assert.Equal(t, codes.FailedPrecondition, status.Code(handleDBError(errors.New("23503: fkey"))))
		assert.Equal(t, codes.Internal, status.Code(handleDBError(errors.New("boom"))))
		assert.Nil(t, handleDBError(nil))
	})
	// #endregion

	// #region Employee Detailed Tests
	t.Run("Employee_Create_AllPaths", func(t *testing.T) {
		_, err := svc.CreateEmployee(ctx, &pb.CreateEmployeeRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))

		_, err = svc.CreateEmployee(ctx, &pb.CreateEmployeeRequest{Email: "a@b.com", Username: "u", DateOfBirth: "invalid"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))

		mockRepo.On("CreateEmployee", mock.Anything, []uint64{1}).Return(errors.New("23505")).Once()
		_, err = svc.CreateEmployee(ctx, &pb.CreateEmployeeRequest{Email: "a@b.com", Username: "u", DateOfBirth: "1990-01-01", PermissionIds: []uint64{1}})
		assert.Equal(t, codes.AlreadyExists, status.Code(err))

		mockRepo.On("CreateEmployee", mock.Anything, []uint64{1}).Return(nil).Once()
		res, err := svc.CreateEmployee(ctx, &pb.CreateEmployeeRequest{Email: "a@b.com", Username: "u", DateOfBirth: "1990-01-01", PermissionIds: []uint64{1}})
		assert.NoError(t, err)
		assert.Equal(t, uint64(1), res.Employee.Id)
	})

	t.Run("Employee_Update_AllPaths", func(t *testing.T) {
		_, err := svc.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{Id: 0})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))

		mockRepo.On("GetEmployeeByID", uint64(1)).Return(nil, errors.New("record not found")).Once()
		_, err = svc.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{Id: 1})
		assert.Equal(t, codes.NotFound, status.Code(err))

		existing := &models.Employee{FirstName: "Old"}
		mockRepo.On("GetEmployeeByID", uint64(1)).Return(existing, nil).Once()
		mockRepo.On("UpdateEmployee", mock.Anything, []uint64{2}).Return(nil).Once()
		res, err := svc.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{Id: 1, FirstName: "New", PermissionIds: []uint64{2}})
		assert.NoError(t, err)
		assert.Equal(t, "New", res.Employee.FirstName)
	})

	t.Run("Employee_List_Pagination", func(t *testing.T) {
		mockRepo.On("ListEmployees", 1, 10).Return([]models.Employee{{FirstName: "E1"}}, int64(1), nil).Once()
		res, err := svc.ListEmployees(ctx, &pb.ListEmployeesRequest{Page: 0, PageSize: 0})
		assert.NoError(t, err)
		assert.Len(t, res.Employees, 1)

		mockRepo.On("ListEmployees", 2, 5).Return(nil, int64(0), errors.New("db error")).Once()
		_, err = svc.ListEmployees(ctx, &pb.ListEmployeesRequest{Page: 2, PageSize: 5})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
	// #endregion

	// #region Client Detailed Tests
	t.Run("Client_CRUD_Complex", func(t *testing.T) {
		_, err := svc.CreateClient(ctx, &pb.CreateClientRequest{Email: ""})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))

		mockRepo.On("CreateClient", mock.Anything).Return(nil).Once()
		res, err := svc.CreateClient(ctx, &pb.CreateClientRequest{Email: "c@c.com"})
		assert.NoError(t, err)
		assert.Equal(t, uint64(1), res.Client.Id)

		mockRepo.On("GetClientByID", uint64(1)).Return(&models.Client{Email: "c@c.com"}, nil).Once()
		mockRepo.On("UpdateClient", mock.Anything).Return(nil).Once()
		_, err = svc.UpdateClient(ctx, &pb.UpdateClientRequest{Id: 1, Email: "new@c.com"})
		assert.NoError(t, err)

		mockRepo.On("DeleteClient", uint64(1)).Return(nil).Once()
		_, err = svc.DeleteClient(ctx, &pb.DeleteClientRequest{Id: 1})
		assert.NoError(t, err)
	})
	// #endregion

	// #region Permission Detailed Tests
	t.Run("Permission_Operations", func(t *testing.T) {
		mockRepo.On("CreatePermission", mock.Anything).Return(nil).Once()
		res, err := svc.CreatePermission(ctx, &pb.CreatePermissionRequest{Name: "Admin"})
		assert.NoError(t, err)
		assert.Equal(t, "Admin", res.Permission.Name)

		mockRepo.On("ListPermissions").Return([]models.Permission{{Name: "P1"}, {Name: "P2"}}, nil).Once()
		list, err := svc.ListPermissions(ctx, &emptypb.Empty{})
		assert.NoError(t, err)
		assert.Len(t, list.Permissions, 2)
	})
	// #endregion

	// #region Mapper Edge Cases
	t.Run("Mappers_NilAndNested", func(t *testing.T) {
		assert.Nil(t, mapEmployeeToProto(nil))
		assert.Nil(t, mapClientToProto(nil))
		assert.Nil(t, mapPermissionToProto(nil))

		fullEmp := &models.Employee{
			FirstName:   "Dan",
			DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
			Permissions: []models.Permission{{Name: "Read"}, {Name: "Write"}},
		}
		p := mapEmployeeToProto(fullEmp)
		assert.Equal(t, "1990-01-01", p.DateOfBirth)
		assert.Len(t, p.Permissions, 2)
	})
	// #endregion
}
