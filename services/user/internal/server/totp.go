package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/repo"
	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const totpAlreadyEnabledCode = 20

type TOTPServer struct {
	userpb.UnimplementedTOTPServiceServer
	notificationService notificationpb.NotificationServiceClient
	totpDisableUrl      string
	db                  *sql.DB
	repo                repo.Repository
}

const (
	totpDisableAction = "totp_disable"
)

func NewTotpServer(conn *Connections) *TOTPServer {
	baseURL := os.Getenv("TOTP_DISABLE_BASE_URL")
	if baseURL == "" {
		logger.L().Error("no url set for disabling TOTP")
		os.Exit(1)
	}

	return &TOTPServer{
		notificationService: conn.NotificationClient,
		totpDisableUrl:      baseURL,
		db:                  conn.Sql_db,
		repo: repo.Repository{
			Database: conn.Sql_db,
			Gorm:     conn.Gorm,
		},
	}
}

func (s *TOTPServer) VerifyCode(ctx context.Context, req *userpb.VerifyCodeRequest) (*userpb.VerifyCodeResponse, error) {
	client, err := s.repo.GetClientByAttribute("email", req.Email)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id
	secret, err := s.repo.GetSecret(userId)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
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
	if !valid {
		passed, err := s.repo.TryBurnBackupCode(userId, req.Code)
		if err != nil {
			return nil, err
		}
		if *passed {
			logger.FromContext(ctx).InfoContext(ctx, "audit: totp verified via backup code", "user_id", userId, "email", req.Email)
		} else {
			logger.FromContext(ctx).WarnContext(ctx, "audit: totp verify failed", "user_id", userId, "email", req.Email)
		}
		return &userpb.VerifyCodeResponse{
			Valid: *passed,
		}, nil
	}
	logger.FromContext(ctx).InfoContext(ctx, "audit: totp verified", "user_id", userId, "email", req.Email)
	return &userpb.VerifyCodeResponse{Valid: valid}, nil
}

func (s *TOTPServer) EnrollBegin(_ context.Context, req *userpb.EnrollBeginRequest) (*userpb.EnrollBeginResponse, error) {
	client, err := s.repo.GetClientByAttribute("email", req.Email)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	active, err := s.repo.Status(tx, userId)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, err
	}
	if *active {
		return nil, status.Error(totpAlreadyEnabledCode, "totp already enabled")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Banka3",
		AccountName: req.Email,
	})

	if err != nil {
		return nil, err
	}

	secret := key.Secret()

	err = s.repo.SetTempTOTPSecret(tx, userId, secret)
	if err != nil {
		return nil, err
	}
	err = tx.Commit()
	if err != nil {
		return nil, err
	}
	return &userpb.EnrollBeginResponse{
		Url: key.URL(),
	}, nil
}

func generateBackupCodes(num uint64) (*[]string, error) {
	var code_strings []string
	for range num {
		random, err := rand.Int(rand.Reader, big.NewInt(999999))
		if err != nil {
			return nil, err
		}
		code := fmt.Sprintf("%0*d", 6, random)
		code_strings = append(code_strings, code)
	}
	return &code_strings, nil
}

func (s *TOTPServer) EnrollConfirm(ctx context.Context, req *userpb.EnrollConfirmRequest) (*userpb.EnrollConfirmResponse, error) {
	client, err := s.repo.GetClientByAttribute("email", req.Email)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	tempSecret, err := s.repo.GetTempSecret(tx, userId)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
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

	err = s.repo.EnableTOTP(tx, userId, *tempSecret)
	if err != nil {
		return nil, err
	}

	backupCodes, err := generateBackupCodes(5)
	if err != nil {
		return nil, err
	}

	err = s.repo.InsertGeneratedCodes(tx, userId, *backupCodes)
	if err != nil {
		return nil, err
	}
	err = tx.Commit()
	if err != nil {
		return nil, err
	}
	logger.FromContext(ctx).InfoContext(ctx, "audit: totp enabled", "user_id", userId, "email", req.Email)
	return &userpb.EnrollConfirmResponse{
		Success:     true,
		BackupCodes: *backupCodes,
	}, nil
}

func (s *TOTPServer) Status(_ context.Context, req *userpb.StatusRequest) (*userpb.StatusResponse, error) {
	userId, err := s.repo.GetUserIdByEmail(req.Email)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	active, err := s.repo.Status(tx, *userId)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}
	return &userpb.StatusResponse{
		Active: *active,
	}, nil
}

func (s *TOTPServer) DisableBegin(ctx context.Context, req *userpb.DisableBeginRequest) (*userpb.DisableBeginResponse, error) {
	email := req.Email

	token, err := generateOpaqueToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "token generation failed")
	}

	validUntil := time.Now().Add(time.Hour)

	if err := s.repo.UpsertPasswordActionToken(email, totpDisableAction, HashValue(token), validUntil); err != nil {
		return nil, status.Error(codes.Internal, "storing token failed")
	}

	link, err := buildActionLink(s.totpDisableUrl, token)
	if err != nil {
		return nil, status.Error(codes.Internal, "building password link failed")
	}

	resp, err := s.notificationService.SendTOTPDisableEmail(ctx, &notificationpb.SendTOTPDisableEmailRequest{
		Email: email,
		Link:  link,
	})
	if err != nil {
		return nil, err
	}
	return &userpb.DisableBeginResponse{
		Success: resp.Successful,
	}, nil
}

func (s *TOTPServer) DisableConfirm(ctx context.Context, req *userpb.DisableConfirmRequest) (*userpb.DisableConfirmResponse, error) {
	client, err := s.repo.GetClientByAttribute("email", req.Email)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	userId := client.Id
	tx, err := s.db.Begin()
	if err != nil {
		return nil, status.Error(codes.Internal, "starting transaction failed")
	}
	defer func() { _ = tx.Rollback() }()

	token := req.Token

	_, _, err = s.repo.ConsumePasswordActionToken(tx, HashValue(token))
	if err != nil {
		if errors.Is(err, repo.ErrInvalidPasswordActionToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid or expired token")
		}
		return nil, status.Error(codes.Internal, "token validation failed")
	}

	err = s.repo.DeleteOldCodes(tx, userId)
	if err != nil {
		return nil, err
	}

	err = s.repo.DisableTOTP(tx, userId)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Error(codes.Internal, "committing transaction failed")
	}

	logger.FromContext(ctx).InfoContext(ctx, "audit: totp disabled", "user_id", userId, "email", req.Email)
	return &userpb.DisableConfirmResponse{
		Success: true,
	}, nil
}
