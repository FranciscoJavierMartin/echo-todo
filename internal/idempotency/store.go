// Package idempotency stores Idempotency-Key outcomes in PostgreSQL.
package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"echo-todo-2/internal/db"
	mw "echo-todo-2/internal/httpx/middleware"
	"echo-todo-2/internal/platform/postgres"
)

type Store struct {
	pool *pgxpool.Pool
}

var _ mw.IdempotencyStore = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Claim(ctx context.Context, userID uuid.UUID, key string, hash []byte, ttl time.Duration) (bool, *mw.IdempotencyRecord, error) {
	q := db.New(s.pool)
	for range 2 {
		n, err := q.ClaimIdempotencyKey(ctx, db.ClaimIdempotencyKeyParams{
			UserID: userID, Key: key, RequestHash: hash, ExpiresAt: time.Now().Add(ttl),
		})
		if err != nil {
			return false, nil, fmt.Errorf("claim idempotency key: %w", err)
		}
		if n == 1 {
			return true, nil, nil
		}
		// An expired record is garbage: drop it and claim again.
		gone, err := q.DeleteExpiredIdempotencyKey(ctx, db.DeleteExpiredIdempotencyKeyParams{UserID: userID, Key: key})
		if err != nil {
			return false, nil, fmt.Errorf("expire idempotency key: %w", err)
		}
		if gone == 1 {
			continue
		}
		row, err := q.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{UserID: userID, Key: key})
		if err != nil {
			if postgres.IsNoRows(err) {
				continue // released concurrently; try again
			}
			return false, nil, fmt.Errorf("get idempotency key: %w", err)
		}
		rec := &mw.IdempotencyRecord{RequestHash: row.RequestHash, Completed: row.State == "completed"}
		if rec.Completed && row.Status != nil {
			resp := &mw.StoredResponse{Status: int(*row.Status), Body: row.ResponseBody}
			if err := json.Unmarshal(row.ResponseHeaders, &resp.Headers); err != nil {
				return false, nil, fmt.Errorf("decode stored headers: %w", err)
			}
			rec.Response = resp
		}
		return false, rec, nil
	}
	return false, nil, fmt.Errorf("claim idempotency key: contention on %q", key)
}

func (s *Store) Complete(ctx context.Context, userID uuid.UUID, key string, resp mw.StoredResponse) error {
	headers, err := json.Marshal(resp.Headers)
	if err != nil {
		return err
	}
	status := int32(resp.Status) // #nosec G115 -- HTTP status
	return db.New(s.pool).CompleteIdempotencyKey(ctx, db.CompleteIdempotencyKeyParams{
		UserID: userID, Key: key, Status: &status, ResponseHeaders: headers, ResponseBody: resp.Body,
	})
}

func (s *Store) Release(ctx context.Context, userID uuid.UUID, key string) error {
	return db.New(s.pool).DeleteIdempotencyKey(ctx, db.DeleteIdempotencyKeyParams{UserID: userID, Key: key})
}

// Purge deletes expired keys; returns how many were removed.
func (s *Store) Purge(ctx context.Context) (int64, error) {
	return db.New(s.pool).PurgeExpiredIdempotencyKeys(ctx)
}
