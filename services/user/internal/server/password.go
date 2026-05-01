package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/repo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func (s *Server) RequestPasswordReset(ctx context.Context, req *userpb.PasswordActionRequest) (*userpb.PasswordActionResponse, error) {
	return s.requestPasswordAction(ctx, strings.TrimSpace(req.Email), PasswordActionReset)
}

func (s *Server) RequestInitialPasswordSet(ctx context.Context, req *userpb.PasswordActionRequest) (*userpb.PasswordActionResponse, error) {
	return s.requestPasswordAction(ctx, strings.TrimSpace(req.Email), PasswordActionInitialSet)
}

func (s *Server) SetPasswordWithToken(ctx context.Context, req *userpb.SetPasswordWithTokenRequest) (*userpb.SetPasswordWithTokenResponse, error) {
	token := strings.TrimSpace(req.Token)
	newPassword := strings.TrimSpace(req.NewPassword)

	if token == "" || newPassword == "" {
		return nil, status.Error(codes.InvalidArgument, "token and new password are required")
	}

	tx, err := s.repo.Database.Begin()
	if err != nil {
		return nil, status.Error(codes.Internal, "starting transaction failed")
	}
	defer func() { _ = tx.Rollback() }()

	email, actionType, err := s.repo.ConsumePasswordActionToken(tx, HashValue(token))
	if err != nil {
		if errors.Is(err, repo.ErrInvalidPasswordActionToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid or expired token")
		}
		return nil, status.Error(codes.Internal, "token validation failed")
	}

	user, err := s.repo.GetUserByEmail(email)
	if err != nil || user == nil {
		return nil, status.Error(codes.Internal, "user lookup failed")
	}

	hashedPassword := HashPassword(newPassword, user.Salt)

	if err := s.repo.UpdatePasswordByEmail(tx, email, hashedPassword); err != nil {
		return nil, status.Error(codes.Internal, "password update failed")
	}

	if actionType == PasswordActionInitialSet {
		if err := s.repo.ActivateEmployeeByEmail(tx, email); err != nil {
			return nil, status.Error(codes.Internal, "employee activation failed")
		}
	}

	if err := s.repo.RevokeRefreshTokensByEmail(tx, email); err != nil {
		return nil, status.Error(codes.Internal, "refresh token revocation failed")
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Error(codes.Internal, "committing transaction failed")
	}

	_ = s.DeleteSession(ctx, email)

	logger.FromContext(ctx).InfoContext(ctx, "audit: password set via token", "email", email, "action", actionType)
	return &userpb.SetPasswordWithTokenResponse{Successful: true}, nil
}

func (s *Server) requestPasswordAction(ctx context.Context, email string, actionType string) (*userpb.PasswordActionResponse, error) {
	if email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	user, err := s.repo.GetUserByEmail(email)

	if err != nil {
		return nil, status.Error(codes.Internal, "user lookup failed")
	}
	if user == nil {
		return &userpb.PasswordActionResponse{Accepted: true}, nil
	}

	token, err := generateOpaqueToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "token generation failed")
	}

	validUntil := time.Now().Add(resetPasswordTokenTTL)
	if actionType == PasswordActionInitialSet {
		validUntil = time.Now().Add(initialSetPasswordTTL)
	}

	if err := s.repo.UpsertPasswordActionToken(user.Email, actionType, HashValue(token), validUntil); err != nil {
		return nil, status.Error(codes.Internal, "storing token failed")
	}

	baseURL := os.Getenv("PASSWORD_RESET_BASE_URL")
	if actionType == PasswordActionInitialSet {
		baseURL = os.Getenv("PASSWORD_SET_BASE_URL")
	}
	link, err := buildActionLink(baseURL, token)
	if err != nil {
		return nil, status.Error(codes.Internal, "building password link failed")
	}

	if err := s.sendPasswordActionEmail(ctx, user.Email, link, actionType); err != nil {
		return nil, err
	}

	return &userpb.PasswordActionResponse{Accepted: true}, nil
}

func (s *Server) sendPasswordActionEmail(ctx context.Context, email string, link string, actionType string) error {
	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if strings.TrimSpace(notificationAddr) == "" {
		notificationAddr = defaultNotificationURL
	}

	conn, err := grpc.NewClient(
		notificationAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dialing notification service: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := notificationpb.NewNotificationServiceClient(conn)

	sendCtx, cancelSend := context.WithTimeout(ctx, 5*time.Second)
	defer cancelSend()

	req := &notificationpb.PasswordLinkMailRequest{
		ToAddr: email,
		Link:   link,
	}

	if actionType == PasswordActionInitialSet {
		resp, err := client.SendInitialPasswordSetEmail(sendCtx, req)
		if err != nil {
			return fmt.Errorf("calling SendInitialPasswordSetEmail: %w", err)
		}
		if !resp.Successful {
			return fmt.Errorf("notification service reported unsuccessful initial set send")
		}
		return nil
	}

	resp, err := client.SendPasswordResetEmail(sendCtx, req)
	if err != nil {
		return fmt.Errorf("calling SendPasswordResetEmail: %w", err)
	}
	if !resp.Successful {
		return fmt.Errorf("notification service reported unsuccessful reset send")
	}
	return nil
}
