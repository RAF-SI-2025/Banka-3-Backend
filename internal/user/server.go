package user

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"banka-raf/gen/user"
	userpb "banka-raf/gen/user"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	userpb.UnimplementedUserServiceServer
	accessJwtSecret  string
	refreshJwtSecret string
	database         *sql.DB
}

func NewServer(accessJwtSecret string, refreshJwtSecret string, database *sql.DB) *Server {
	return &Server{
		accessJwtSecret:  accessJwtSecret,
		refreshJwtSecret: refreshJwtSecret,
		database:         database,
	}
}

func (s *Server) GetEmployeeById(ctx context.Context, req *userpb.GetEmployeeByIdRequest) (*user.EmployeeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func generateRefreshToken(email string, secret string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": email,
		"exp":     time.Now().Add(time.Hour * 27 * 7).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func generateAccessToken(email string, secret string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": email,
		"exp":     time.Now().Add(time.Minute * 15).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func (s *Server) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	hasher := sha256.New()
	hasher.Write([]byte(req.Password))
	hashedPassword := hasher.Sum(nil)
	println("encoded hash " + string(hashedPassword))
	user, err := s.GetUserByEmail(req.Email)
	if err != nil {
		return nil, err
	}

	if user != nil && bytes.Equal(hashedPassword, user.hashedPassword) {
		accessToken, err := generateAccessToken(user.email, s.accessJwtSecret)
		if err != nil {
			return nil, err
		}

		refreshToken, err := generateAccessToken(user.email, s.refreshJwtSecret)
		if err != nil {
			return nil, err
		}

		return &userpb.LoginResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
		}, nil
	}

	return &userpb.LoginResponse{
		AccessToken:  "",
		RefreshToken: "",
	}, errors.New("wrong creds")
}
