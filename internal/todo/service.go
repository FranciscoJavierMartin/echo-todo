package todo

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"echo-todo-2/internal/apperr"
)

const (
	MaxTitleLen  = 200
	MaxNotesLen  = 10000
	DefaultLimit = 20
	MaxLimit     = 100
)

// TxRunner runs fn in a transaction; the transaction travels in ctx.
type TxRunner interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type CreateInput struct {
	Title    string
	Notes    string
	Status   Status // "" → open
	DueAt    *time.Time
	Priority int // 0 → 3
}

// UpdateInput is a partial update; nil fields are left unchanged.
type UpdateInput struct {
	Title      *string
	Notes      *string
	Status     *Status
	DueAt      *time.Time
	ClearDueAt bool
	Priority   *int
}

type Page struct {
	Items      []Todo
	NextCursor string // empty on the last page
	Total      *int   // only when Filter.WithCount
}

type Service struct {
	repo  Repo
	tx    TxRunner
	audit AuditLog
}

func NewService(repo Repo, tx TxRunner, audit AuditLog) *Service {
	return &Service{repo: repo, tx: tx, audit: audit}
}

func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, in CreateInput) (*Todo, error) {
	const op = "todo.Service.Create"
	t := &Todo{
		OwnerID:  ownerID,
		Title:    strings.TrimSpace(in.Title),
		Notes:    in.Notes,
		Status:   in.Status,
		DueAt:    in.DueAt,
		Priority: in.Priority,
	}
	if t.Status == "" {
		t.Status = StatusOpen
	}
	if t.Priority == 0 {
		t.Priority = 3
	}
	if err := validate(t); err != nil {
		return nil, err.WithOp(op)
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.Create(ctx, t); err != nil {
			return err
		}
		return s.audit.AppendAudit(ctx, AuditEvent{TodoID: t.ID, ActorID: ownerID, Action: "create", Changes: diff(&Todo{}, t)})
	})
	if err != nil {
		return nil, translate(op, err)
	}
	return t, nil
}

func (s *Service) Get(ctx context.Context, ownerID, id uuid.UUID) (*Todo, error) {
	t, err := s.repo.Get(ctx, ownerID, id)
	if err != nil {
		return nil, translate("todo.Service.Get", err)
	}
	return t, nil
}

// ListQuery is the raw list request; the service parses sort and cursor.
type ListQuery struct {
	Filter
	SortSpec string
	Cursor   string
}

func (s *Service) List(ctx context.Context, q ListQuery) (Page, error) {
	const op = "todo.Service.List"
	f := q.Filter
	if f.Limit <= 0 {
		f.Limit = DefaultLimit
	}
	f.Limit = min(f.Limit, MaxLimit)
	sort, err := ParseSort(q.SortSpec)
	if err != nil {
		return Page{}, apperr.Invalid("sort", "must be a comma-separated list of [-]priority, due_at, created_at, updated_at, title").WithOp(op).Wrap(err)
	}
	f.Sort = sort
	if q.Cursor != "" {
		c, err := DecodeCursor(sort, q.Cursor)
		if err != nil {
			return Page{}, apperr.Invalid("cursor", "is invalid or does not match the sort").WithOp(op).Wrap(err)
		}
		f.After = c
	}
	items, total, err := s.repo.List(ctx, f)
	if err != nil {
		return Page{}, translate(op, err)
	}
	page := Page{Items: items}
	if f.WithCount {
		page.Total = &total
	}
	if len(items) > f.Limit {
		page.Items = items[:f.Limit]
		page.NextCursor = EncodeCursor(sort, CursorAfter(sort, &page.Items[f.Limit-1]))
	}
	return page, nil
}

// Update applies a partial update. expectedVersion == 0 means "current version"
// (no client precondition); otherwise a mismatch yields a conflict.
func (s *Service) Update(ctx context.Context, ownerID, id uuid.UUID, in UpdateInput, expectedVersion int) (*Todo, error) {
	const op = "todo.Service.Update"
	var out *Todo
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		cur, err := s.repo.Get(ctx, ownerID, id)
		if err != nil {
			return err
		}
		if expectedVersion == 0 {
			expectedVersion = cur.Version
		}
		if cur.Version != expectedVersion {
			return ErrVersionConflict
		}
		next := *cur
		apply(&next, in)
		if verr := validate(&next); verr != nil {
			return verr
		}
		out, err = s.repo.Update(ctx, &next, expectedVersion)
		if err != nil {
			return err
		}
		return s.audit.AppendAudit(ctx, AuditEvent{TodoID: id, ActorID: ownerID, Action: "update", Changes: diff(cur, out)})
	})
	if err != nil {
		return nil, translate(op, err)
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id uuid.UUID) error {
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.SoftDelete(ctx, ownerID, id); err != nil {
			return err
		}
		return s.audit.AppendAudit(ctx, AuditEvent{TodoID: id, ActorID: ownerID, Action: "delete"})
	})
	if err != nil {
		return translate("todo.Service.Delete", err)
	}
	return nil
}

// AdminAudit returns the audit trail of any todo regardless of owner. Callers
// must have checked the admin role (RequireRole) before calling.
func (s *Service) AdminAudit(ctx context.Context, id uuid.UUID) ([]AuditEvent, error) {
	events, err := s.audit.ListAudit(ctx, id)
	if err != nil {
		return nil, translate("todo.Service.AdminAudit", err)
	}
	return events, nil
}

// diff lists the fields that differ between a and b.
func diff(a, b *Todo) map[string]Change {
	ch := map[string]Change{}
	if a.Title != b.Title {
		ch["title"] = Change{a.Title, b.Title}
	}
	if a.Notes != b.Notes {
		ch["notes"] = Change{a.Notes, b.Notes}
	}
	if a.Status != b.Status {
		ch["status"] = Change{a.Status, b.Status}
	}
	if a.Priority != b.Priority {
		ch["priority"] = Change{a.Priority, b.Priority}
	}
	if !equalTime(a.DueAt, b.DueAt) {
		ch["due_at"] = Change{a.DueAt, b.DueAt}
	}
	return ch
}

func equalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func apply(t *Todo, in UpdateInput) {
	if in.Title != nil {
		t.Title = strings.TrimSpace(*in.Title)
	}
	if in.Notes != nil {
		t.Notes = *in.Notes
	}
	if in.Status != nil {
		t.Status = *in.Status
	}
	if in.ClearDueAt {
		t.DueAt = nil
	} else if in.DueAt != nil {
		t.DueAt = in.DueAt
	}
	if in.Priority != nil {
		t.Priority = *in.Priority
	}
}

// validate enforces domain invariants. The HTTP validator catches most of these
// earlier; the service still checks because it is the last line before storage.
func validate(t *Todo) *apperr.Error {
	fields := map[string]string{}
	switch n := utf8.RuneCountInString(t.Title); {
	case n == 0:
		fields["title"] = "title is required"
	case n > MaxTitleLen:
		fields["title"] = "title must be at most 200 characters"
	}
	if utf8.RuneCountInString(t.Notes) > MaxNotesLen {
		fields["notes"] = "notes must be at most 10000 characters"
	}
	if t.Priority < 1 || t.Priority > 5 {
		fields["priority"] = "priority must be between 1 and 5"
	}
	if !t.Status.Valid() {
		fields["status"] = "status must be one of open, done, archived"
	}
	if len(fields) > 0 {
		return apperr.InvalidFields(fields)
	}
	return nil
}

// translate maps storage sentinels to the domain taxonomy.
func translate(op string, err error) error {
	var ae *apperr.Error
	switch {
	case errors.As(err, &ae):
		if ae.Op == "" {
			return ae.WithOp(op)
		}
		return ae
	case errors.Is(err, ErrNotFound):
		return apperr.NotFound("todo not found").WithOp(op).Wrap(err)
	case errors.Is(err, ErrVersionConflict):
		return apperr.Conflict("todo was modified").WithOp(op).Wrap(err)
	case errors.Is(err, context.DeadlineExceeded):
		return apperr.New(apperr.KindUnavailable, "request timed out").WithOp(op).WithCode("timeout").Wrap(err)
	default:
		return apperr.Internal(op, err)
	}
}
