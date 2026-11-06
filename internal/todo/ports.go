package todo

import (
	"context"

	"github.com/google/uuid"
)

// Cache is a read-through cache of todos keyed by id and version. It never makes
// authorization decisions: the service checks ownership on every cached read.
type Cache interface {
	Get(ctx context.Context, id uuid.UUID) (*Todo, bool)
	Set(ctx context.Context, t *Todo)
	Invalidate(ctx context.Context, id uuid.UUID)
}

// NoopCache disables caching (no REDIS_URL).
type NoopCache struct{}

func (NoopCache) Get(context.Context, uuid.UUID) (*Todo, bool) { return nil, false }
func (NoopCache) Set(context.Context, *Todo)                   {}
func (NoopCache) Invalidate(context.Context, uuid.UUID)        {}

// Outbox enqueues integration events in the caller's transaction.
type Outbox interface {
	Enqueue(ctx context.Context, topic string, payload any) error
}

// NoopOutbox drops events (unit tests).
type NoopOutbox struct{}

func (NoopOutbox) Enqueue(context.Context, string, any) error { return nil }

// Event topics.
const (
	TopicTodoCreated = "todo.created"
	TopicTodoUpdated = "todo.updated"
	TopicTodoDeleted = "todo.deleted"
	TopicTodoDone    = "todo.completed"
)

// Event is the outbox payload for todo changes.
type Event struct {
	TodoID  uuid.UUID `json:"todo_id"`
	OwnerID uuid.UUID `json:"owner_id"`
	Title   string    `json:"title,omitempty"`
	Version int       `json:"version"`
}
