// Package postgres owns the connection pool, transaction propagation and migrations.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option customizes the pool configuration before connecting (e.g. to add a tracer).
type Option func(*pgxpool.Config)

// New creates a pool and verifies connectivity.
func New(ctx context.Context, url string, opts ...Option) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 5 * time.Minute
	for _, o := range opts {
		o(cfg)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// WithStatementTimeout sets a server-side statement_timeout as a backstop for
// queries whose context deadline is not honoured (e.g. a stuck network).
func WithStatementTimeout(d time.Duration) Option {
	return func(cfg *pgxpool.Config) {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = fmt.Sprintf("%d", d.Milliseconds())
	}
}

// DBTX is satisfied by *pgxpool.Pool, pgx.Tx and *pgx.Conn (same as the sqlc interface).
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txKey struct{}

// TxManager runs functions inside a transaction. The pgx.Tx travels in the context,
// never on a struct, so repositories transparently join the caller's transaction.
type TxManager struct {
	pool *pgxpool.Pool
}

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// WithTx runs fn inside a transaction. Nested calls reuse the outer transaction.
func (m *TxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	return pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// Conn returns the transaction stored in ctx, or the pool when there is none.
func Conn(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// Postgres error codes we translate into domain errors.
const (
	CodeUniqueViolation     = "23505"
	CodeCheckViolation      = "23514"
	CodeForeignKeyViolation = "23503"
)

// ErrCode returns the SQLSTATE of err, or "" when err is not a Postgres error.
func ErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// IsNoRows reports whether err means "no rows".
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
