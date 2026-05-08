// Command seed plants development fixtures into the user service's
// database. Today this means: a bootstrap admin so the system can be
// brought up from zero (the spec says only an admin can create
// employees, so without this nobody could log in).
//
// Idempotent: if any employee already has the `admin` permission, the
// program does nothing. Re-run after a `task migrate` and you'll either
// create the admin (first run) or no-op (subsequent runs).
//
// Configuration:
//
//	DATABASE_URL    — required; standard pgx DSN
//	SEED_ADMIN_EMAIL    (default admin@banka.local)
//	SEED_ADMIN_PASSWORD (default Admin123!) — must satisfy spec policy
//	SEED_ADMIN_USERNAME (default admin)
//
// The default password meets the spec's complexity rules (8–32 chars,
// ≥2 digits, ≥1 upper, ≥1 lower) but should be changed in any shared
// environment. Print the credentials on success so a fresh dev knows
// what to log in with.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/passwords"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	email := envOr("SEED_ADMIN_EMAIL", "admin@banka.local")
	username := envOr("SEED_ADMIN_USERNAME", "admin")
	password := envOr("SEED_ADMIN_PASSWORD", "Admin123!")

	if err := passwords.ValidateComplexity(password); err != nil {
		return fmt.Errorf("SEED_ADMIN_PASSWORD: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	var existingID string
	const checkQ = `select id from "user".employees where 'admin' = any(permissions) limit 1`
	switch err := pool.QueryRow(ctx, checkQ).Scan(&existingID); err {
	case nil:
		fmt.Printf("seed: admin already exists (id=%s); skipping\n", existingID)
		return nil
	default:
		// pgx.ErrNoRows is the expected "create me" path; any other error
		// is fatal. Comparing strings keeps this file dep-light.
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check existing admin: %w", err)
		}
	}

	hash, err := passwords.Hash(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	const insertQ = `
        insert into "user".employees (
            email, username, password_hash,
            first_name, last_name, date_of_birth, gender, phone, address,
            position, department, active, permissions
        ) values (
            $1, $2, $3,
            'Admin', 'Banka 3', '1990-01-01', 'other', '+381000000000', 'Beograd',
            'Administrator', 'IT', true,
            array['admin','employee.read','employee.write','client.read','client.write','permission.grant']
        )
        returning id`
	var id string
	if err := pool.QueryRow(ctx, insertQ, email, username, hash).Scan(&id); err != nil {
		return fmt.Errorf("insert admin: %w", err)
	}

	fmt.Printf("seed: admin created (id=%s)\n  email:    %s\n  username: %s\n  password: %s\n",
		id, email, username, password)
	fmt.Println("seed: change SEED_ADMIN_PASSWORD before any shared environment.")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
