package server

import (
	"context"
	"fmt"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

func validateJWTToken(tokenString, secret string) (*userpb.ValidateTokenResponse, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})

	if err != nil || !token.Valid {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return nil, err
	}
	exp, err := claims.GetExpirationTime()
	if err != nil {
		return nil, err
	}
	iat, err := claims.GetIssuedAt()
	if err != nil {
		return nil, err
	}

	return &userpb.ValidateTokenResponse{
		Sub: sub,
		Exp: exp.Unix(),
		Iat: iat.Unix(),
	}, nil
}

func (s *Server) ValidateRefreshToken(_ context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	return validateJWTToken(req.Token, s.refreshJwtSecret)
}

func (s *Server) ValidateAccessToken(ctx context.Context, req *userpb.ValidateTokenRequest) (*userpb.ValidateTokenResponse, error) {
	resp, err := validateJWTToken(req.Token, s.accessJwtSecret)
	if err != nil {
		return nil, err
	}

	session, err := s.GetSession(ctx, resp.Sub)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "session store unavailable")
	}
	if session == nil {
		return nil, status.Error(codes.Unauthenticated, "no active session")
	}
	if !session.Active {
		return nil, status.Error(codes.Unauthenticated, "account deactivated")
	}

	return resp, nil
}

func (s *Server) Refresh(ctx context.Context, req *userpb.RefreshRequest) (*userpb.RefreshResponse, error) {
	token, err := validateJWTToken(req.RefreshToken, s.refreshJwtSecret)
	if err != nil {
		return nil, err
	}
	email := token.Sub

	newSignedToken, err := s.GenerateRefreshToken(email)
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	role, permissions, active := s.getRoleAndPermissions(email)

	if !active {
		return nil, status.Error(codes.Unauthenticated, "account deactivated")
	}

	newAccessToken, err := s.GenerateAccessToken(email)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	newParsed, _, err := jwt.NewParser().ParseUnverified(newSignedToken, &jwt.RegisteredClaims{})
	if err != nil {
		return nil, fmt.Errorf("parsing new token: %w", err)
	}
	newExpiry, err := newParsed.Claims.GetExpirationTime()
	if err != nil {
		return nil, fmt.Errorf("getting expiry: %w", err)
	}

	tx, err := s.repo.Database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = s.repo.RotateRefreshToken(tx, email, HashValue(req.RefreshToken), HashValue(newSignedToken), newExpiry.Time)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "wrong token")
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	if err := s.CreateSession(ctx, email, SessionData{
		Role:        role,
		Permissions: permissions,
		Active:      true,
	}); err != nil {
		return nil, status.Error(codes.Unavailable, "session store unavailable")
	}

	return &userpb.RefreshResponse{AccessToken: newAccessToken, RefreshToken: newSignedToken, Permissions: permissions, Role: role}, nil
}
