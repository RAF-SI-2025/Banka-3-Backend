package service

import (
	"banka-raf/gen/user"
	"banka-raf/internal/user/models"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"gorm.io/gorm"
)

// ================= MOCK =================

type MockUserRepo struct {
	mock.Mock
}

func (m *MockUserRepo) CreateEmployee(emp *models.Employee, permissionIDs []uint) error {
	args := m.Called(emp, permissionIDs)
	return args.Error(0)
}
func (m *MockUserRepo) GetEmployeeByID(id uint) (*models.Employee, error) {
	args := m.Called(id)
	if e := args.Get(0); e != nil {
		return e.(*models.Employee), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockUserRepo) UpdateEmployee(emp *models.Employee, permissionIDs []uint) error {
	args := m.Called(emp, permissionIDs)
	return args.Error(0)
}
func (m *MockUserRepo) DeleteEmployee(id uint) error {
	args := m.Called(id)
	return args.Error(0)
}
func (m *MockUserRepo) CreateClient(cli *models.Client) error {
	args := m.Called(cli)
	return args.Error(0)
}
func (m *MockUserRepo) GetClientByID(id uint) (*models.Client, error) {
	args := m.Called(id)
	if c := args.Get(0); c != nil {
		return c.(*models.Client), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockUserRepo) UpdateClient(cli *models.Client) error {
	args := m.Called(cli)
	return args.Error(0)
}
func (m *MockUserRepo) DeleteClient(id uint) error {
	args := m.Called(id)
	return args.Error(0)
}
func (m *MockUserRepo) ListPermissions() ([]models.Permission, error) {
	args := m.Called()
	if perms := args.Get(0); perms != nil {
		return perms.([]models.Permission), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockUserRepo) GetPermissionByName(name string) (*models.Permission, error) {
	args := m.Called(name)
	if p := args.Get(0); p != nil {
		return p.(*models.Permission), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockUserRepo) ListEmployees(page, pageSize int, email, firstName, lastName, position string) ([]models.Employee, int64, error) {
	args := m.Called(page, pageSize, email, firstName, lastName, position)
	if e := args.Get(0); e != nil {
		return e.([]models.Employee), args.Get(1).(int64), args.Error(2)
	}
	return nil, 0, args.Error(2)
}
func (m *MockUserRepo) ListClients(page, pageSize int, firstName, lastName, email string) ([]models.Client, int64, error) {
	args := m.Called(page, pageSize, firstName, lastName, email)
	if c := args.Get(0); c != nil {
		return c.([]models.Client), args.Get(1).(int64), args.Error(2)
	}
	return nil, 0, args.Error(2)
}

// ================= TESTS =================

func TestUserService_Employees(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockUserRepo)
	svc := NewUserService(mockRepo)

	perm := &models.Permission{Model: gorm.Model{
		ID: 1,
	}, Name: "ADMIN"}

	// ---------------- CREATE EMPLOYEE ----------------
	mockRepo.On("GetPermissionByName", "ADMIN").Return(perm, nil)
	mockRepo.On("CreateEmployee", mock.Anything, []uint{1}).Return(nil)

	createReq := &user.CreateEmployeeRequest{
		FirstName:   "John",
		LastName:    "Doe",
		Email:       "john@example.com",
		Username:    "john123",
		DateOfBirth: "1990-01-01",
		Permissions: []string{"ADMIN"},
	}

	empRes, err := svc.CreateEmployee(ctx, createReq)
	assert.NoError(t, err)
	assert.Equal(t, "john@example.com", empRes.Email)

	// ---------------- GET EMPLOYEE ----------------
	employee := &models.Employee{
		Model:    gorm.Model{ID: 1},
		Email:    "john@example.com",
		Username: "john123",
		Permissions: []models.Permission{
			*perm,
		},
	}
	mockRepo.On("GetEmployeeByID", uint(1)).Return(employee, nil)

	assert.NoError(t, err)

	// ---------------- UPDATE EMPLOYEE ----------------
	mockRepo.On("GetPermissionByName", "ADMIN").Return(perm, nil)
	mockRepo.On("UpdateEmployee", employee, []uint{1}).Return(nil)

	updateReq := &user.UpdateEmployeeRequest{
		EmployeeId:  1,
		Email:       "john2@example.com",
		DateOfBirth: "1990-01-01",
		Permissions: []string{"ADMIN"},
	}
	empRes3, err := svc.UpdateEmployee(ctx, updateReq)
	assert.NoError(t, err)
	assert.Equal(t, "john2@example.com", empRes3.Email)

	// ---------------- DELETE EMPLOYEE ----------------
	mockRepo.On("DeleteEmployee", uint(1)).Return(nil)
	delReq := &user.DeleteEmployeeRequest{EmployeeId: 1}
	_, err = svc.DeleteEmployee(ctx, delReq)
	assert.NoError(t, err)
}

func TestUserService_Clients(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockUserRepo)
	svc := NewUserService(mockRepo)

	client := &models.Client{
		Model:     gorm.Model{ID: 1},
		FirstName: "Alice",
		LastName:  "Smith",
	}

	// CREATE CLIENT
	mockRepo.On("CreateClient", mock.Anything).Return(nil)
	createReq := &user.CreateClientRequest{
		FirstName: "Alice",
		LastName:  "Smith",
	}
	res, err := svc.CreateClient(ctx, createReq)
	assert.NoError(t, err)
	assert.Equal(t, "Alice", res.FirstName)

	// GET CLIENT
	mockRepo.On("GetClientByID", uint(1)).Return(client, nil)
	getReq := &user.GetClientRequest{ClientId: 1}
	res2, err := svc.GetClient(ctx, getReq)
	assert.NoError(t, err)
	assert.Equal(t, "Alice", res2.FirstName)

	// UPDATE CLIENT
	client.FirstName = "Alice2"
	mockRepo.On("UpdateClient", client).Return(nil)
	updateReq := &user.UpdateClientRequest{
		ClientId:  1,
		FirstName: "Alice2",
		LastName:  "Smith",
	}
	res3, err := svc.UpdateClient(ctx, updateReq)
	assert.NoError(t, err)
	assert.Equal(t, "Alice2", res3.FirstName)

	// DELETE CLIENT
	mockRepo.On("DeleteClient", uint(1)).Return(nil)
	delReq := &user.DeleteClientRequest{ClientId: 1}
	_, err = svc.DeleteClient(ctx, delReq)
	assert.NoError(t, err)
}

func TestUserService_ListPermissions(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockUserRepo)
	svc := NewUserService(mockRepo)

	perms := []models.Permission{
		{
			Model: gorm.Model{ID: 1},
			Name:  "ADMIN", Description: "admin perm"},
		{
			Model: gorm.Model{ID: 1},
			Name:  "USER", Description: "user perm"},
	}
	mockRepo.On("ListPermissions").Return(perms, nil)

	res, err := svc.ListPermissions(ctx, &emptypb.Empty{})
	assert.NoError(t, err)
	assert.Len(t, res.Permissions, 2)
	assert.Equal(t, "ADMIN", res.Permissions[0].Name)
	assert.Equal(t, "USER", res.Permissions[1].Name)
}

func TestUserService_ErrorCases(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockUserRepo)
	svc := NewUserService(mockRepo)

	// Invalid date
	req := &user.CreateEmployeeRequest{
		Email:       "a@b.com",
		Username:    "user1",
		DateOfBirth: "invalid",
	}
	_, err := svc.CreateEmployee(ctx, req)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Missing email/username
	req2 := &user.CreateEmployeeRequest{}
	_, err = svc.CreateEmployee(ctx, req2)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Permission not found
	mockRepo.On("GetPermissionByName", "ADMIN").Return(nil, nil)
	req3 := &user.CreateEmployeeRequest{
		Email:       "a@b.com",
		Username:    "user1",
		DateOfBirth: "1990-01-01",
		Permissions: []string{"ADMIN"},
	}
	_, err = svc.CreateEmployee(ctx, req3)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "permission not found")

	// Get employee not found
	mockRepo.On("GetEmployeeByID", uint(999)).Return(nil, errors.New("record not found"))
	_, err = svc.GetEmployee(ctx, &user.GetEmployeeRequest{EmployeeId: 999})
	assert.Equal(t, codes.NotFound, status.Code(err))
}
