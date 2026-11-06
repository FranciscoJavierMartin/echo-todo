// Package todotest provides hand-written fakes for the todo domain.
package todotest

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/todo"
)

// Repo is an in-memory todo.Repo with the same ownership and versioning semantics as Postgres.
type Repo struct {
	mu     sync.Mutex
	items  map[uuid.UUID]todo.Todo
	audits []todo.AuditEvent
	Err    error // when set, every call fails with Err
}

var (
	_ todo.Repo     = (*Repo)(nil)
	_ todo.AuditLog = (*Repo)(nil)
)

func NewRepo() *Repo { return &Repo{items: map[uuid.UUID]todo.Todo{}} }

// NewService returns a service backed by an in-memory repo (handy for HTTP tests).
func NewService() (*todo.Service, *Repo) {
	r := NewRepo()
	return todo.NewService(r, NoTx{}, r), r
}

func (r *Repo) AppendAudit(_ context.Context, e todo.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	e.ID = int64(len(r.audits) + 1)
	e.CreatedAt = time.Now()
	r.audits = append(r.audits, e)
	return nil
}

func (r *Repo) ListAudit(_ context.Context, todoID uuid.UUID) ([]todo.AuditEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	if _, ok := r.items[todoID]; !ok {
		return nil, todo.ErrNotFound
	}
	var out []todo.AuditEvent
	for _, e := range r.audits {
		if e.TodoID == todoID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *Repo) Create(_ context.Context, t *todo.Todo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	now := time.Now()
	t.ID = uuid.New()
	t.Version = 1
	t.CreatedAt, t.UpdatedAt = now, now
	r.items[t.ID] = *t
	return nil
}

func (r *Repo) Get(_ context.Context, ownerID, id uuid.UUID) (*todo.Todo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	t, ok := r.items[id]
	if !ok || t.OwnerID != ownerID || t.DeletedAt != nil {
		return nil, todo.ErrNotFound
	}
	return &t, nil
}

func (r *Repo) List(_ context.Context, f todo.Filter) ([]todo.Todo, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, 0, r.Err
	}
	var out []todo.Todo
	for _, t := range r.items {
		if t.OwnerID != f.OwnerID || t.DeletedAt != nil {
			continue
		}
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, t.Status) {
			continue
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b todo.Todo) int { return b.CreatedAt.Compare(a.CreatedAt) })
	total := len(out)
	if f.Offset < len(out) {
		out = out[f.Offset:]
	} else {
		out = nil
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func (r *Repo) Update(_ context.Context, t *todo.Todo, expectedVersion int) (*todo.Todo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	cur, ok := r.items[t.ID]
	if !ok || cur.OwnerID != t.OwnerID || cur.DeletedAt != nil {
		return nil, todo.ErrNotFound
	}
	if cur.Version != expectedVersion {
		return nil, todo.ErrVersionConflict
	}
	next := *t
	next.Version = cur.Version + 1
	next.UpdatedAt = time.Now()
	r.items[t.ID] = next
	return &next, nil
}

func (r *Repo) SoftDelete(_ context.Context, ownerID, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	cur, ok := r.items[id]
	if !ok || cur.OwnerID != ownerID || cur.DeletedAt != nil {
		return todo.ErrNotFound
	}
	now := time.Now()
	cur.DeletedAt = &now
	cur.Version++
	r.items[id] = cur
	return nil
}

// NoTx runs fn directly; good enough for unit tests of service logic.
type NoTx struct{}

func (NoTx) WithTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }
