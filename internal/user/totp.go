package user

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

type TOTPServer struct {
	userpb.UnimplementedTOTPServiceServer
	db *sql.DB
}

func NewTotpServer(database *sql.DB) *TOTPServer {
	return &TOTPServer{db: database}
}

func (s *TOTPServer) VerifyCode(_ context.Context, req *userpb.VerifyCodeRequest) (*userpb.VerifyCodeResponse, error) {
	userId, err := s.getUserIdByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	secret, err := s.GetSecret(*userId)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.Unauthenticated, "user doesn't have TOTP set up")
		}
		return nil, err
	}
	valid, err := totp.ValidateCustom(req.Code, *secret, time.Now(), totp.ValidateOpts{
		Digits: 6,
		Period: 30,
		Skew:   1,
	})
	if err != nil {
		return nil, err
	}
	return &userpb.VerifyCodeResponse{Valid: valid}, nil
}
func (s *TOTPServer) EnrollBegin(_ context.Context, req *userpb.EnrollBeginRequest) (*userpb.EnrollBeginResponse, error) {
	userId, err := s.getUserIdByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Banka3",
		AccountName: req.Email,
	})

	if err != nil {
		return nil, err
	}

	secret := key.Secret()

	err = s.SetTempTOTPSecret(*userId, secret)
	if err != nil {
		return nil, err
	}

	return &userpb.EnrollBeginResponse{
		Url: key.URL(),
	}, nil
}
func (s *TOTPServer) EnrollConfirm(_ context.Context, req *userpb.EnrollConfirmRequest) (*userpb.EnrollConfirmResponse, error) {
	userId, err := s.getUserIdByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	tempSecret, err := s.GetTempSecret(tx, *userId)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}

	valid := totp.Validate(req.Code, *tempSecret)

	if !valid {
		return &userpb.EnrollConfirmResponse{
			Success: false,
		}, nil
	}

	err = s.EnableTOTP(tx, *userId, *tempSecret)
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}
	return &userpb.EnrollConfirmResponse{
		Success: true,
	}, nil
}
