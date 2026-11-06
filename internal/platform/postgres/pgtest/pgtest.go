// Package pgtest provides isolated, migrated Postgres databases for tests.
//
// One Postgres server is shared per test binary: either TEST_DATABASE_URL (CI services)
// or a testcontainers postgres:16 container. A migrated template database is built once
// and every test gets its own copy via CREATE DATABASE ... TEMPLATE, which keeps tests
// isolated and parallel-safe.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"echo-todo-2/internal/platform/postgres"
)

// templateDB is per test binary: `go test ./...` runs packages in parallel and,
// against a shared server (TEST_DATABASE_URL in CI), they must not race on one template.
var templateDB = "todo_template_" + randomHex(4)

var (
	once     sync.Once
	adminURL string
	setupErr error
)

// New returns a pool connected to a fresh, fully migrated database.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := NewWithURL(t)
	return pool
}

// NewWithURL is New but also returns the connection string of the database.
func NewWithURL(t testing.TB, opts ...postgres.Option) (*pgxpool.Pool, string) {
	t.Helper()
	once.Do(setup)
	if setupErr != nil {
		if os.Getenv("PGTEST_REQUIRED") != "" {
			t.Fatalf("pgtest: %v", setupErr)
		}
		t.Skipf("pgtest: postgres unavailable: %v", setupErr)
	}

	ctx := context.Background()
	name := "t_" + randomHex(8)
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("pgtest: connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, templateDB)); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}

	dbURL := withDatabase(adminURL, name)
	pool, err := postgres.New(ctx, dbURL, opts...)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), adminURL)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
	})
	return pool, dbURL
}

func setup() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		adminURL = u
	} else {
		c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("postgres"),
			tcpostgres.WithUsername("postgres"),
			tcpostgres.WithPassword("postgres"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			setupErr = fmt.Errorf("start container: %w", err)
			return
		}
		u, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			setupErr = err
			return
		}
		adminURL = u
	}

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		setupErr = fmt.Errorf("connect: %w", err)
		return
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
		setupErr = fmt.Errorf("create template: %w", err)
		return
	}

	pool, err := postgres.New(ctx, withDatabase(adminURL, templateDB))
	if err != nil {
		setupErr = err
		return
	}
	defer pool.Close()
	if err := postgres.MigrateUp(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		setupErr = fmt.Errorf("migrate template: %w", err)
	}
}

func withDatabase(raw, db string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + db
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
