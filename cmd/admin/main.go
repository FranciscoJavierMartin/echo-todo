// Command admin performs operational tasks from the runbook (docs/runbook.md):
//
//	admin revoke-sessions [-user <uuid>]   revoke refresh tokens (all users or one)
//	admin promote -email <addr> [-role admin|user]
//	admin purge                            delete expired idempotency keys and old outbox rows
//	admin outbox-pending                   print the number of undelivered events
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/config"
	"echo-todo-2/internal/db"
	"echo-todo-2/internal/idempotency"
	"echo-todo-2/internal/outbox"
	"echo-todo-2/internal/platform/postgres"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, os.Args[1:]); err != nil {
		log.Error("admin failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: admin <revoke-sessions|promote|purge|outbox-pending> [flags]")
	}
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := postgres.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)

	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "revoke-sessions":
		user := fs.String("user", "", "only this user id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var n int64
		if *user != "" {
			id, err := uuid.Parse(*user)
			if err != nil {
				return fmt.Errorf("invalid -user: %w", err)
			}
			n, err = q.RevokeAllRefreshTokensForUser(ctx, id)
			if err != nil {
				return err
			}
		} else if n, err = q.RevokeAllRefreshTokens(ctx); err != nil {
			return err
		}
		log.Info("refresh tokens revoked", "count", n,
			"note", "access tokens stay valid until they expire (ACCESS_TTL) unless JWT_SECRET is rotated without a grace window")
	case "promote":
		email := fs.String("email", "", "user email")
		role := fs.String("role", "admin", "admin or user")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *role != "admin" && *role != "user" {
			return errors.New("-role must be admin or user")
		}
		n, err := q.SetUserRole(ctx, db.SetUserRoleParams{Email: *email, Role: *role})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no user with email %q", *email)
		}
		log.Info("role updated", "role", *role, "note", "takes effect on the next access token (≤ ACCESS_TTL)")
	case "purge":
		keys, err := idempotency.NewStore(pool).Purge(ctx)
		if err != nil {
			return err
		}
		if err := outbox.PurgeProcessed(pool, 7*24*time.Hour)(ctx); err != nil {
			return err
		}
		log.Info("purged", "idempotency_keys", keys)
	case "outbox-pending":
		n, err := outbox.Pending(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Println(n)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}
