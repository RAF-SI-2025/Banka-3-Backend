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

func (s *Server) VerifyCode(_ context.Context, req *userpb.VerifyCodeRequest) (*userpb.VerifyCodeResponse, error) {
	client, err := s.GetClientByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id

	secret, err := s.GetSecret(userId)
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
func (s *Server) EnrollBegin(_ context.Context, req *userpb.EnrollBeginRequest) (*userpb.EnrollBeginResponse, error) {
	client, err := s.GetClientByEmail(req.Email)
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
	userId := client.Id

	if err != nil {
		return nil, err
	}

	secret := key.Secret()

	err = s.SetTempTOTPSecret(userId, secret)
	if err != nil {
		return nil, err
	}

	return &userpb.EnrollBeginResponse{
		Url: key.URL(),
	}, nil
}
func (s *Server) EnrollConfirm(_ context.Context, req *userpb.EnrollConfirmRequest) (*userpb.EnrollConfirmResponse, error) {
	client, err := s.GetClientByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id

	tx, err := s.database.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	tempSecret, err := s.GetTempSecret(tx, userId)
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

	err = s.EnableTOTP(tx, userId, *tempSecret)
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

func (s *Server) TOTPStatus(_ context.Context, req *userpb.TOTPStatusRequest) (*userpb.TOTPStatusResponse, error) {
	client, err := s.GetClientByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id
	active, err := s.totpStatus(userId)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) || errors.Is(err, sql.ErrNoRows) {
			return &userpb.TOTPStatusResponse{
				Active: false,
			}, nil
		}
		return nil, err
	}
	return &userpb.TOTPStatusResponse{
		Active: *active,
	}, nil
}
