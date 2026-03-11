package user

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
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

func (s *Server) GenerateRefreshToken(email string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   email,
		ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour * 7)),
		IssuedAt:  jwt.NewNumericDate(now),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.refreshJwtSecret))
}

func (s *Server) GenerateAccessToken(email string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   email,
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.accessJwtSecret))
}

func (s *Server) ValidateRefreshToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	token, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		return []byte(s.refreshJwtSecret), nil
	})

	if err != nil {
		return nil, err
	}
	return &user.ValidateTokenResponse{
		Valid: token.Valid,
	}, nil
}

func (s *Server) ValidateAccessToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	token, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		return []byte(s.accessJwtSecret), nil
	})

	if err != nil {
		return nil, err
	}
	return &user.ValidateTokenResponse{
		Valid: token.Valid,
	}, nil
}

func (s *Server) Refresh(ctx context.Context, req *userpb.RefreshRequest) (*userpb.RefreshResponse, error) {
	refreshToken := req.RefreshToken
	parsed, err := jwt.Parse(refreshToken, func(t *jwt.Token) (any, error) {
		return []byte(s.refreshJwtSecret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}
	email, err := parsed.Claims.GetSubject()
	if err != nil {
		return nil, fmt.Errorf("getting subject: %w", err)
	}

	newSignedToken, err := s.GenerateRefreshToken(email)
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	newAccessToken, err := s.GenerateAccessToken(email)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	hash := func(t string) []byte {
		h := sha256.New()
		h.Write([]byte(t))
		return h.Sum(nil)
	}

	newParsed, _, err := jwt.NewParser().ParseUnverified(newSignedToken, &jwt.RegisteredClaims{})
	if err != nil {
		return nil, fmt.Errorf("parsing new token: %w", err)
	}
	newExpiry, err := newParsed.Claims.GetExpirationTime()
	if err != nil {
		return nil, fmt.Errorf("getting expiry: %w", err)
	}

	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	err = s.rotateRefreshToken(tx, email, hash(refreshToken), hash(newSignedToken), newExpiry.Time)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &userpb.RefreshResponse{AccessToken: newAccessToken, RefreshToken: newSignedToken}, nil
}

func (s *Server) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	hasher := sha256.New()
	hasher.Write([]byte(req.Password))
	hashedPassword := hasher.Sum(nil)
	user, err := s.GetUserByEmail(req.Email)
	if err != nil {
		return nil, err
	}

	if user != nil && bytes.Equal(hashedPassword, user.hashedPassword) {
		accessToken, err := s.GenerateAccessToken(user.email)
		if err != nil {
			return nil, err
		}
		refreshToken, err := s.GenerateRefreshToken(user.email)
		if err != nil {
			return nil, err
		}
		err = s.InsertRefreshToken(refreshToken)
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
