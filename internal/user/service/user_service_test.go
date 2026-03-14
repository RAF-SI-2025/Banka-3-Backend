package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"banka-raf/gen/user"
	"banka-raf/internal/user/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// =================== MOCK REPOSITORY ===================

type MockUserRepository struct{ mock.Mock }

func (m *MockUserRepository) FindUserByEmail(e string) (any, error) {
	a := m.Called(e)
	return a.Get(0), a.Error(1)
}
func (m *MockUserRepository) UpsertRefreshToken(id uint64, t string, ex time.Time) error {
	return m.Called(id, t, ex).Error(0)
}
func (m *MockUserRepository) RotateRefreshToken(id uint64, o, n string, ex time.Time) error {
	return m.Called(id, o, n, ex).Error(0)
}
func (m *MockUserRepository) GetPermissionByName(n string) (*models.Permission, error) {
	a := m.Called(n)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).(*models.Permission), a.Error(1)
}
func (m *MockUserRepository) CreateEmployee(e *models.Employee, p []uint64) error {
	return m.Called(e, p).Error(0)
}
func (m *MockUserRepository) GetEmployeeByID(id uint64) (*models.Employee, error) {
	a := m.Called(id)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).(*models.Employee), a.Error(1)
}
func (m *MockUserRepository) ListEmployees(p, ps int, e, fn, ln, pos string) ([]models.Employee, int64, error) {
	a := m.Called(p, ps, e, fn, ln, pos)
	return a.Get(0).([]models.Employee), int64(a.Int(1)), a.Error(2)
}
func (m *MockUserRepository) UpdateEmployee(e *models.Employee, p []uint64) error {
	return m.Called(e, p).Error(0)
}
func (m *MockUserRepository) DeleteEmployee(id uint64) error      { return m.Called(id).Error(0) }
func (m *MockUserRepository) CreateClient(c *models.Client) error { return m.Called(c).Error(0) }
func (m *MockUserRepository) GetClientByID(id uint64) (*models.Client, error) {
	a := m.Called(id)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).(*models.Client), a.Error(1)
}
func (m *MockUserRepository) ListClients(p, ps int, fn, ln, e string) ([]models.Client, int64, error) {
	a := m.Called(p, ps, fn, ln, e)
	return a.Get(0).([]models.Client), int64(a.Int(1)), a.Error(2)
}
func (m *MockUserRepository) UpdateClient(c *models.Client) error { return m.Called(c).Error(0) }
func (m *MockUserRepository) DeleteClient(id uint64) error        { return m.Called(id).Error(0) }
func (m *MockUserRepository) ListPermissions() ([]models.Permission, error) {
	a := m.Called()
	return a.Get(0).([]models.Permission), a.Error(1)
}

// =================== TEST HELPERS ===================

func getHash(p string, s []byte) []byte {
	h := sha256.New()
	h.Write(append([]byte(p), s...))
	return h.Sum(nil)
}

// =================== TESTS ===================

func TestAuth_Login(t *testing.T) {
	repo := new(MockUserRepository)
	svc := NewUserService(repo, "access", "refresh")
	salt := []byte("3030")
	pass := "secret"
	h := getHash(pass, salt)

	t.Run("Login Success", func(t *testing.T) {
		repo.On("FindUserByEmail", "test@raf.rs").Return(&models.Employee{Id: 1, Email: "test@raf.rs", Password: h, SaltPassword: salt}, nil).Once()
		repo.On("UpsertRefreshToken", uint64(1), mock.Anything, mock.Anything).Return(nil).Once()

		resp, err := svc.Login(context.Background(), &user.LoginRequest{Email: "test@raf.rs", Password: pass})
		assert.NoError(t, err)
		assert.NotEmpty(t, resp.AccessToken)
	})

	t.Run("Login Wrong Password", func(t *testing.T) {
		repo.On("FindUserByEmail", "test@raf.rs").Return(&models.Employee{Password: h, SaltPassword: salt}, nil).Once()
		_, err := svc.Login(context.Background(), &user.LoginRequest{Email: "test@raf.rs", Password: "wrong"})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}

func TestEmployee_CRUD(t *testing.T) {
	repo := new(MockUserRepository)
	svc := NewUserService(repo, "a", "r")

	t.Run("Create Employee Success", func(t *testing.T) {
		repo.On("GetPermissionByName", "admin").Return(&models.Permission{Id: 10, Name: "admin"}, nil).Once()
		repo.On("CreateEmployee", mock.Anything, []uint64{10}).Return(nil).Once()

		req := &user.CreateEmployeeRequest{
			FirstName: "John", LastName: "Doe", Email: "john@raf.rs",
			DateOfBirth: "1990-01-01", Permissions: []string{"admin"},
		}
		resp, err := svc.CreateEmployee(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, "John", resp.FirstName)
	})

	t.Run("Get Employee Not Found", func(t *testing.T) {
		repo.On("GetEmployeeByID", uint64(99)).Return(nil, errors.New("record not found")).Once()
		_, err := svc.GetEmployee(context.Background(), &user.GetEmployeeRequest{EmployeeId: 99})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestClient_CRUD(t *testing.T) {
	repo := new(MockUserRepository)
	svc := NewUserService(repo, "a", "r")

	t.Run("Create Client Success", func(t *testing.T) {
		repo.On("CreateClient", mock.Anything).Return(nil).Once()
		req := &user.CreateClientRequest{
			FirstName: "Alice", Email: "alice@raf.rs", DateOfBirth: "1710345600",
		}
		resp, err := svc.CreateClient(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, "Alice", resp.FirstName)
	})

	t.Run("Delete Client Success", func(t *testing.T) {
		repo.On("DeleteClient", uint64(1)).Return(nil).Once()
		_, err := svc.DeleteClient(context.Background(), &user.DeleteClientRequest{ClientId: 1})
		assert.NoError(t, err)
	})
}

func TestPermissions_List(t *testing.T) {
	repo := new(MockUserRepository)
	svc := NewUserService(repo, "a", "r")

	t.Run("List Permissions", func(t *testing.T) {
		repo.On("ListPermissions").Return([]models.Permission{{Id: 1, Name: "read"}}, nil).Once()
		resp, err := svc.ListPermissions(context.Background(), nil)
		assert.NoError(t, err)
		assert.Len(t, resp.Permissions, 1)
		assert.Equal(t, "read", resp.Permissions[0].Name)
	})
}
