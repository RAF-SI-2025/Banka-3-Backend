package user

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/gateway"
)

type TOTPServer struct {
	userpb.UnimplementedTOTPServiceServer
	db                  *sql.DB
	gorm                *gorm.DB
	notificationService notificationpb.NotificationServiceClient
	totpDisableUrl      string
}

const (
	totpDisableAction = "totp_disable"
)

func NewTotpServer(conn *Connections) *TOTPServer {
	baseURL := os.Getenv("TOTP_DISABLE_BASE_URL")
	if baseURL == "" {
		log.Fatalf("No url set for disabling TOTP!")
	}
	return &TOTPServer{
		db:                  conn.Sql_db,
		notificationService: conn.NotificationClient,
		gorm:                conn.Gorm,
		totpDisableUrl:      baseURL,
	}
}

func (s *TOTPServer) VerifyCode(_ context.Context, req *userpb.VerifyCodeRequest) (*userpb.VerifyCodeResponse, error) {
	client, err := getUserByAttribute(Client{}, s.gorm, "email", req.Email)
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
	if !valid {
		passed, err := s.tryBurnBackupCode(userId, req.Code)
		if err != nil {
			return nil, err
		}
		return &userpb.VerifyCodeResponse{
			Valid: *passed,
		}, nil
	}
	return &userpb.VerifyCodeResponse{Valid: valid}, nil
}
func (s *TOTPServer) EnrollBegin(_ context.Context, req *userpb.EnrollBeginRequest) (*userpb.EnrollBeginResponse, error) {
	client, err := getUserByAttribute(Client{}, s.gorm, "email", req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
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

	active, err := s.status(tx, userId)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, err
	}
	if *active {
		return nil, status.Error(gateway.TotpAleadyEnabledCode, "totp already enabled")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Banka3",
		AccountName: req.Email,
	})

	if err != nil {
		return nil, err
	}

	secret := key.Secret()

	err = s.SetTempTOTPSecret(tx, userId, secret)
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
	var codes []string
	for range num {
		random, err := rand.Int(rand.Reader, big.NewInt(999999))
		if err != nil {
			return nil, err
		}
		code := fmt.Sprintf("%0*d", 6, random)
		codes = append(codes, code)
	}
	return &codes, nil
}

func (s *TOTPServer) EnrollConfirm(_ context.Context, req *userpb.EnrollConfirmRequest) (*userpb.EnrollConfirmResponse, error) {
	client, err := getUserByAttribute(Client{}, s.gorm, "email", req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
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

	backupCodes, err := generateBackupCodes(5)
	if err != nil {
		return nil, err
	}

	err = s.InsertGeneratedCodes(tx, userId, *backupCodes)
	if err != nil {
		return nil, err
	}
	err = tx.Commit()
	if err != nil {
		return nil, err
	}
	return &userpb.EnrollConfirmResponse{
		Success:     true,
		BackupCodes: *backupCodes,
	}, nil
}

func (s *TOTPServer) Status(_ context.Context, req *userpb.StatusRequest) (*userpb.StatusResponse, error) {
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

	active, err := s.status(tx, *userId)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
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

	if err := upsertPasswordActionToken(s.db, email, totpDisableAction, hashValue(token), validUntil); err != nil {
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

func (s *TOTPServer) DisableConfirm(_ context.Context, req *userpb.DisableConfirmRequest) (*userpb.DisableConfirmResponse, error) {
	client, err := getUserByAttribute(Client{}, s.gorm, "email", req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
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

	_, _, err = consumePasswordActionToken(tx, hashValue(token))
	if err != nil {
		if errors.Is(err, ErrInvalidPasswordActionToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid or expired token")
		}
		return nil, status.Error(codes.Internal, "token validation failed")
	}

	err = s.deleteOldCodes(tx, userId)
	if err != nil {
		return nil, err
	}

	err = s.DisableTOTP(tx, userId)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Error(codes.Internal, "committing transaction failed")
	}

	return &userpb.DisableConfirmResponse{
		Success: true,
	}, nil
}
