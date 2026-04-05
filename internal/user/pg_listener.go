package user

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
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
			log.Printf("pg listener error: %v, reconnecting in 5s...", err)
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
	log.Println("pg listener: listening on permission_change")

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
				log.Printf("pg listener: failed to delete session for %s: %v", email, err)
			} else {
				log.Printf("pg listener: deleted session for deactivated employee %s", email)
			}
			continue
		}

		if err := srv.UpdateSessionPermissions(ctx, email, role, permissions); err != nil {
			log.Printf("pg listener: failed to update session for %s: %v", email, err)
		} else {
			log.Printf("pg listener: updated session for %s", email)
		}
	}
}
