package todo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Storage sentinels. Repositories translate driver errors into these; the service
// translates these into the apperr taxonomy.
var (
	ErrNotFound        = errors.New("todo not found")
	ErrVersionConflict = errors.New("version conflict")
)

type Repo interface {
	Create(ctx context.Context, t *Todo) error
	Get(ctx context.Context, ownerID, id uuid.UUID) (*Todo, error)
	// List returns up to f.Limit+1 rows after f.After in f.Sort order (the extra row
	// signals another page) and, when f.WithCount, the total matching rows.
	List(ctx context.Context, f Filter) ([]Todo, int, error)
	Update(ctx context.Context, t *Todo, expectedVersion int) (*Todo, error)
	// SoftDelete deletes when the version matches (expectedVersion 0 = any version).
	SoftDelete(ctx context.Context, ownerID, id uuid.UUID, expectedVersion int) error
}

// AuditEvent records who changed what. Changes maps field → {from, to}.
type AuditEvent struct {
	ID        int64
	TodoID    uuid.UUID
	ActorID   uuid.UUID
	Action    string // create | update | delete
	Changes   map[string]Change
	CreatedAt time.Time
}

type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// AuditLog stores audit events. It joins the transaction in ctx, so an event is
// committed if and only if the change it describes is.
type AuditLog interface {
	AppendAudit(ctx context.Context, e AuditEvent) error
	// ListAudit returns ErrNotFound when the todo does not exist (for any owner).
	ListAudit(ctx context.Context, todoID uuid.UUID) ([]AuditEvent, error)
}
