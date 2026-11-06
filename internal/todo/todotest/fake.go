// Package todotest provides hand-written fakes for the todo domain.
package todotest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/todo"
)

// Repo is an in-memory todo.Repo with the same ownership and versioning semantics as Postgres.
type Repo struct {
	mu          sync.Mutex
	items       map[uuid.UUID]todo.Todo
	audits      []todo.AuditEvent
	attachments map[uuid.UUID]todo.Attachment
	Err         error // when set, every call fails with Err
	AttachErr   error // when set, attachment inserts fail
}

var (
	_ todo.Repo           = (*Repo)(nil)
	_ todo.AuditLog       = (*Repo)(nil)
	_ todo.AttachmentRepo = (*Repo)(nil)
)

func NewRepo() *Repo {
	return &Repo{items: map[uuid.UUID]todo.Todo{}, attachments: map[uuid.UUID]todo.Attachment{}}
}

// Put stores t as-is (fixed IDs and timestamps for golden tests).
func (r *Repo) Put(t todo.Todo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[t.ID] = t
}

func (r *Repo) InsertAttachment(_ context.Context, a *todo.Attachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.AttachErr != nil {
		return r.AttachErr
	}
	a.ID, a.CreatedAt = uuid.New(), time.Now()
	r.attachments[a.ID] = *a
	return nil
}

func (r *Repo) GetAttachment(_ context.Context, ownerID, todoID, id uuid.UUID) (*todo.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.attachments[id]
	if !ok || a.OwnerID != ownerID || a.TodoID != todoID {
		return nil, todo.ErrNotFound
	}
	return &a, nil
}

func (r *Repo) ListAttachments(_ context.Context, ownerID, todoID uuid.UUID) ([]todo.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []todo.Attachment
	for _, a := range r.attachments {
		if a.OwnerID == ownerID && a.TodoID == todoID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *Repo) DeleteAttachment(_ context.Context, ownerID, todoID, id uuid.UUID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.attachments[id]
	if !ok || a.OwnerID != ownerID || a.TodoID != todoID {
		return "", todo.ErrNotFound
	}
	delete(r.attachments, id)
	return a.StorageKey, nil
}

// Cache is an in-memory todo.Cache that records invalidations.
type Cache struct {
	mu          sync.Mutex
	items       map[uuid.UUID]todo.Todo
	Invalidated []uuid.UUID
}

func NewCache() *Cache { return &Cache{items: map[uuid.UUID]todo.Todo{}} }

func (c *Cache) Get(_ context.Context, id uuid.UUID) (*todo.Todo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.items[id]
	return &t, ok
}

func (c *Cache) Set(_ context.Context, t *todo.Todo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[t.ID] = *t
}

func (c *Cache) Invalidate(_ context.Context, id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, id)
	c.Invalidated = append(c.Invalidated, id)
}

// Outbox records enqueued topics.
type Outbox struct {
	mu     sync.Mutex
	Topics []string
}

func (o *Outbox) Enqueue(_ context.Context, topic string, _ any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Topics = append(o.Topics, topic)
	return nil
}

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

// List mirrors the Postgres semantics using todo.Compare for ordering.
func (r *Repo) List(_ context.Context, f todo.Filter) ([]todo.Todo, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, 0, r.Err
	}
	keys := f.Sort
	if len(keys) == 0 {
		keys = todo.DefaultSort
	}
	var matched []todo.Todo
	for _, t := range r.items {
		if t.OwnerID != f.OwnerID || t.DeletedAt != nil {
			continue
		}
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, t.Status) {
			continue
		}
		if len(f.Priorities) > 0 && !slices.Contains(f.Priorities, t.Priority) {
			continue
		}
		if f.DueBefore != nil && (t.DueAt == nil || !t.DueAt.Before(*f.DueBefore)) {
			continue
		}
		if f.DueAfter != nil && (t.DueAt == nil || t.DueAt.Before(*f.DueAfter)) {
			continue
		}
		if f.Query != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.Notes), strings.ToLower(f.Query)) {
			continue
		}
		matched = append(matched, t)
	}
	total := len(matched)
	slices.SortFunc(matched, func(a, b todo.Todo) int { return todo.Compare(keys, &a, &b) })
	var out []todo.Todo
	for _, t := range matched {
		if f.After != nil && !todo.IsAfter(keys, &t, *f.After) {
			continue
		}
		out = append(out, t)
		if f.Limit > 0 && len(out) == f.Limit+1 {
			break
		}
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

func (r *Repo) SoftDelete(_ context.Context, ownerID, id uuid.UUID, expectedVersion int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	cur, ok := r.items[id]
	if !ok || cur.OwnerID != ownerID || cur.DeletedAt != nil {
		return todo.ErrNotFound
	}
	if expectedVersion != 0 && cur.Version != expectedVersion {
		return todo.ErrVersionConflict
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
