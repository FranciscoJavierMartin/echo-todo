// Package outbox implements the transactional outbox: events are inserted in the
// same transaction as the business change and delivered by a worker.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/todo"
)

// Store enqueues events into the transaction found in ctx (see postgres.Conn).
type Store struct {
	pool *pgxpool.Pool
}

var _ todo.Outbox = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Enqueue(ctx context.Context, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	return db.New(postgres.Conn(ctx, s.pool)).EnqueueOutbox(ctx, db.EnqueueOutboxParams{
		Topic: topic, Payload: raw, AvailableAt: time.Now(),
	})
}

// Message is a claimed outbox row.
type Message struct {
	ID       int64
	Topic    string
	Payload  json.RawMessage
	Attempts int
}

// Handler delivers a message (send email, push, webhook...). Returning an error
// schedules a retry with backoff.
type Handler interface {
	Handle(ctx context.Context, m Message) error
}

type HandlerFunc func(ctx context.Context, m Message) error

func (f HandlerFunc) Handle(ctx context.Context, m Message) error { return f(ctx, m) }

// LogNotifier stands in for an email/push provider.
type LogNotifier struct{ Log *slog.Logger }

func (n LogNotifier) Handle(ctx context.Context, m Message) error {
	n.Log.InfoContext(ctx, "notification delivered", "topic", m.Topic, "outbox_id", m.ID, "payload", string(m.Payload))
	return nil
}

type Worker struct {
	pool       *pgxpool.Pool
	txm        *postgres.TxManager
	handler    Handler
	log        *slog.Logger
	batch      int32
	interval   time.Duration
	maxBackoff time.Duration
	// Maintenance hooks run every maintenanceEvery batches (purges).
	maintenance      []func(ctx context.Context) error
	maintenanceEvery time.Duration
}

func NewWorker(pool *pgxpool.Pool, h Handler, log *slog.Logger) *Worker {
	return &Worker{
		pool: pool, txm: postgres.NewTxManager(pool), handler: h, log: log,
		batch: 20, interval: time.Second, maxBackoff: 10 * time.Minute, maintenanceEvery: time.Hour,
	}
}

// WithInterval sets the polling interval.
func (w *Worker) WithInterval(d time.Duration) *Worker {
	w.interval = d
	return w
}

// WithMaintenance registers periodic cleanup jobs.
func (w *Worker) WithMaintenance(every time.Duration, jobs ...func(ctx context.Context) error) *Worker {
	w.maintenanceEvery, w.maintenance = every, append(w.maintenance, jobs...)
	return w
}

// Run drains the outbox until ctx is cancelled (the same signal context as the
// HTTP server). A batch in flight finishes or rolls back; nothing is lost.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("outbox worker started", "interval", w.interval)
	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	lastMaintenance := time.Now()
	for {
		for {
			n, err := w.ProcessBatch(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				w.log.Error("outbox batch failed", "err", err)
			}
			if n < int(w.batch) || err != nil {
				break
			}
		}
		if time.Since(lastMaintenance) >= w.maintenanceEvery {
			lastMaintenance = time.Now()
			for _, job := range w.maintenance {
				if err := job(ctx); err != nil {
					w.log.Error("maintenance job failed", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			w.log.Info("outbox worker stopped")
			return nil
		case <-tick.C:
		}
	}
}

// ProcessBatch claims up to batch messages with FOR UPDATE SKIP LOCKED and
// delivers them inside one transaction. If the process dies mid-batch the
// transaction rolls back and the rows become available again (at-least-once).
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	var processed int
	err := w.txm.WithTx(ctx, func(ctx context.Context) error {
		q := db.New(postgres.Conn(ctx, w.pool))
		rows, err := q.ClaimOutboxBatch(ctx, w.batch)
		if err != nil {
			return fmt.Errorf("claim outbox: %w", err)
		}
		for _, r := range rows {
			m := Message{ID: r.ID, Topic: r.Topic, Payload: r.Payload, Attempts: int(r.Attempts)}
			if herr := w.handler.Handle(ctx, m); herr != nil {
				if ctx.Err() != nil {
					return ctx.Err() // shutting down: roll back, retry later
				}
				next := time.Now().Add(w.backoff(m.Attempts + 1))
				w.log.Warn("outbox delivery failed; will retry", "outbox_id", m.ID, "topic", m.Topic, "attempt", m.Attempts+1, "err", herr)
				msg := herr.Error()
				if err := q.MarkOutboxFailed(ctx, db.MarkOutboxFailedParams{ID: m.ID, LastError: &msg, AvailableAt: next}); err != nil {
					return err
				}
				continue
			}
			if err := q.MarkOutboxProcessed(ctx, m.ID); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	return processed, err
}

func (w *Worker) backoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 20)
	return min(d, w.maxBackoff)
}

// PurgeProcessed deletes delivered messages older than age.
func PurgeProcessed(pool *pgxpool.Pool, age time.Duration) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		cutoff := time.Now().Add(-age)
		_, err := db.New(pool).PurgeProcessedOutbox(ctx, &cutoff)
		return err
	}
}

// Pending counts undelivered messages (metrics, tests).
func Pending(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return db.New(pool).CountPendingOutbox(ctx)
}
