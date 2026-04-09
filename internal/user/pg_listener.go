package user

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
)

// StartPGListener connects to PostgreSQL and listens for permission_change
// notifications. When a notification arrives, it refreshes the employee's
// session in Redis (or deletes it if deactivated).
func StartPGListener(ctx context.Context, databaseURL string, srv *Server) {
	for {
		if err := listenLoop(ctx, databaseURL, srv); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.L().Error("pg listener error, reconnecting in 5s", "err", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func listenLoop(ctx context.Context, databaseURL string, srv *Server) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, "LISTEN permission_change"); err != nil {
		return err
	}
	logger.L().Info("pg listener: listening on permission_change")

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}

		email := notification.Payload
		if email == "" {
			continue
		}

		role, permissions, active := srv.getRoleAndPermissions(email)

		if !active {
			if err := srv.DeleteSession(ctx, email); err != nil {
				logger.FromContext(ctx).Error("pg listener: failed to delete session", "email", email, "err", err)
			} else {
				logger.FromContext(ctx).Info("pg listener: deleted session for deactivated employee", "email", email)
			}
			continue
		}

		if err := srv.UpdateSessionPermissions(ctx, email, role, permissions); err != nil {
			logger.FromContext(ctx).Error("pg listener: failed to update session", "email", email, "err", err)
		} else {
			logger.FromContext(ctx).Info("pg listener: updated session", "email", email)
		}
	}
}
