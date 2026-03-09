package service

import (
	"context"
	"errors"
	"testing"
	"user-service/models"
	"user-service/pb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/protobuf/types/known/emptypb"
)

// #region Mock Repository
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) CreateEmployee(e *models.Employee, ids []uint64) error {
	args := m.Called(e, ids)
	e.ID = 1
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
	args := m.Called(e, ids)
	return args.Error(0)
}
func (m *MockUserRepository) DeleteEmployee(id uint64) error {
	args := m.Called(id)
	return args.Error(0)
}
func (m *MockUserRepository) CreateClient(c *models.Client) error {
	args := m.Called(c)
	c.ID = 1
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
	args := m.Called(c)
	return args.Error(0)
}
func (m *MockUserRepository) DeleteClient(id uint64) error {
	args := m.Called(id)
	return args.Error(0)
}
func (m *MockUserRepository) CreatePermission(p *models.Permission) error {
	args := m.Called(p)
	p.ID = 1
	return args.Error(0)
}
func (m *MockUserRepository) ListPermissions() ([]models.Permission, error) {
	args := m.Called()
	return args.Get(0).([]models.Permission), args.Error(1)
}

// #endregion

// #region Employee Tests
func TestEmployeeService(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	t.Run("CreateEmployee_Success", func(t *testing.T) {
		mockRepo.On("CreateEmployee", mock.Anything, mock.Anything).Return(nil).Once()
		res, err := svc.CreateEmployee(context.Background(), &pb.CreateEmployeeRequest{DateOfBirth: "1990-01-01"})
		assert.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("GetEmployee_Success", func(t *testing.T) {
		mockRepo.On("GetEmployeeByID", uint64(1)).Return(&models.Employee{FirstName: "Test"}, nil).Once()
		res, err := svc.GetEmployee(context.Background(), &pb.GetEmployeeRequest{Id: 1})
		assert.NoError(t, err)
		assert.Equal(t, "Test", res.Employee.FirstName)
	})

	t.Run("ListEmployees_Error", func(t *testing.T) {
		mockRepo.On("ListEmployees", 1, 10).Return([]models.Employee{}, int64(0), errors.New("err")).Once()
		_, err := svc.ListEmployees(context.Background(), &pb.ListEmployeesRequest{Page: 1, PageSize: 10})
		assert.Error(t, err)
	})

	t.Run("UpdateEmployee_NotFound", func(t *testing.T) {
		mockRepo.On("GetEmployeeByID", uint64(1)).Return(nil, errors.New("not found")).Once()
		_, err := svc.UpdateEmployee(context.Background(), &pb.UpdateEmployeeRequest{Id: 1})
		assert.Error(t, err)
	})

	t.Run("DeleteEmployee_Error", func(t *testing.T) {
		mockRepo.On("DeleteEmployee", uint64(1)).Return(errors.New("db error")).Once()
		_, err := svc.DeleteEmployee(context.Background(), &pb.DeleteEmployeeRequest{Id: 1})
		assert.Error(t, err)
	})
}

// #endregion

// #region Client Tests
func TestClientService(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	t.Run("CreateClient_Success", func(t *testing.T) {
		mockRepo.On("CreateClient", mock.Anything).Return(nil).Once()
		res, err := svc.CreateClient(context.Background(), &pb.CreateClientRequest{FirstName: "Client"})
		assert.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("GetClient_NotFound", func(t *testing.T) {
		mockRepo.On("GetClientByID", uint64(1)).Return(nil, errors.New("err")).Once()
		_, err := svc.GetClient(context.Background(), &pb.GetClientRequest{Id: 1})
		assert.Error(t, err)
	})

	t.Run("ListClients_Success", func(t *testing.T) {
		mockRepo.On("ListClients", 1, 10).Return([]models.Client{{FirstName: "C"}}, int64(1), nil).Once()
		res, err := svc.ListClients(context.Background(), &pb.ListClientsRequest{Page: 1, PageSize: 10})
		assert.NoError(t, err)
		assert.Len(t, res.Clients, 1)
	})

	t.Run("UpdateClient_Error", func(t *testing.T) {
		mockRepo.On("GetClientByID", uint64(1)).Return(&models.Client{}, nil).Once()
		mockRepo.On("UpdateClient", mock.Anything).Return(errors.New("err")).Once()
		_, err := svc.UpdateClient(context.Background(), &pb.UpdateClientRequest{Id: 1})
		assert.Error(t, err)
	})

	t.Run("DeleteClient_Success", func(t *testing.T) {
		mockRepo.On("DeleteClient", uint64(1)).Return(nil).Once()
		_, err := svc.DeleteClient(context.Background(), &pb.DeleteClientRequest{Id: 1})
		assert.NoError(t, err)
	})
}

// #endregion

// #region Permission Tests
func TestPermissionService(t *testing.T) {
	mockRepo := new(MockUserRepository)
	svc := NewUserService(mockRepo)

	t.Run("CreatePermission_Success", func(t *testing.T) {
		mockRepo.On("CreatePermission", mock.Anything).Return(nil).Once()
		res, err := svc.CreatePermission(context.Background(), &pb.CreatePermissionRequest{Name: "Admin"})
		assert.NoError(t, err)
		assert.Equal(t, "Admin", res.Permission.Name)
	})

	t.Run("ListPermissions_Success", func(t *testing.T) {
		mockRepo.On("ListPermissions").Return([]models.Permission{{Name: "P1"}}, nil).Once()
		res, err := svc.ListPermissions(context.Background(), &emptypb.Empty{})
		assert.NoError(t, err)
		assert.NotEmpty(t, res.Permissions)
	})
}

// #endregion
