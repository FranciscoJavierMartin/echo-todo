// Package pgrepo implements todo.Repo on PostgreSQL using sqlc-generated queries.
package pgrepo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/todo"
)

type Repo struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

// DefaultQueryTimeout bounds every repository call unless overridden.
const DefaultQueryTimeout = 3 * time.Second

var _ todo.Repo = (*Repo)(nil)

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool, queryTimeout: DefaultQueryTimeout}
}

// WithQueryTimeout returns a copy whose calls are bounded by d.
func (r *Repo) WithQueryTimeout(d time.Duration) *Repo {
	c := *r
	c.queryTimeout = d
	return &c
}

// timeout bounds a single repository call; the request deadline still applies if sooner.
func (r *Repo) timeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.queryTimeout)
}

func (r *Repo) q(ctx context.Context) *db.Queries {
	return db.New(postgres.Conn(ctx, r.pool))
}

func (r *Repo) Create(ctx context.Context, t *todo.Todo) error {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	row, err := r.q(ctx).CreateTodo(ctx, db.CreateTodoParams{
		OwnerID:  t.OwnerID,
		Title:    t.Title,
		Notes:    t.Notes,
		Status:   db.TodoStatus(t.Status),
		DueAt:    t.DueAt,
		Priority: int16(t.Priority), // #nosec G115 -- validated to 1..5
	})
	if err != nil {
		return fmt.Errorf("create todo: %w", err)
	}
	*t = fromRow(db.GetTodoRow(row))
	return nil
}

func (r *Repo) Get(ctx context.Context, ownerID, id uuid.UUID) (*todo.Todo, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	row, err := r.q(ctx).GetTodo(ctx, db.GetTodoParams{ID: id, OwnerID: ownerID})
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, todo.ErrNotFound
		}
		return nil, fmt.Errorf("get todo: %w", err)
	}
	t := fromRow(row)
	return &t, nil
}

// sortExpr maps allowlisted sort fields to fixed SQL expressions. NULL due dates
// sort as +infinity and titles compare bytewise (COLLATE "C"), matching todo.Compare.
var sortExpr = map[todo.SortField]string{
	todo.SortPriority:  "priority",
	todo.SortDueAt:     "coalesce(due_at, 'infinity'::timestamptz)",
	todo.SortCreatedAt: "created_at",
	todo.SortUpdatedAt: "updated_at",
	todo.SortTitle:     `title COLLATE "C"`,
}

func (r *Repo) List(ctx context.Context, f todo.Filter) ([]todo.Todo, int, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()

	b := &builder{}
	b.where("owner_id = " + b.arg(f.OwnerID))
	b.where("deleted_at IS NULL")
	if len(f.Statuses) > 0 {
		statuses := make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			statuses[i] = string(st)
		}
		b.where("status = ANY(" + b.arg(statuses) + "::todo_status[])")
	}
	if len(f.Priorities) > 0 {
		b.where("priority = ANY(" + b.arg(f.Priorities) + "::smallint[])")
	}
	if f.DueBefore != nil {
		b.where("due_at < " + b.arg(*f.DueBefore))
	}
	if f.DueAfter != nil {
		b.where("due_at >= " + b.arg(*f.DueAfter))
	}
	if f.Query != "" {
		b.where("search @@ websearch_to_tsquery('english', " + b.arg(f.Query) + ")")
	}
	filterCond, filterArgs := b.cond(), append([]any(nil), b.args...)

	keys := f.Sort
	if len(keys) == 0 {
		keys = todo.DefaultSort
	}
	if f.After != nil {
		b.where(b.keyset(keys, *f.After))
	}
	order := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		order = append(order, sortExpr[k.Field]+dir(k.Desc))
	}
	order = append(order, "id"+dir(keys[len(keys)-1].Desc))

	sql := fmt.Sprintf("SELECT %s FROM todos WHERE %s ORDER BY %s LIMIT %s",
		columns, b.cond(), strings.Join(order, ", "), b.arg(f.Limit+1))

	conn := postgres.Conn(ctx, r.pool)
	rows, err := conn.Query(ctx, sql, b.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list todos: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanTodo)
	if err != nil {
		return nil, 0, fmt.Errorf("scan todos: %w", err)
	}

	var total int
	if f.WithCount {
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM todos WHERE "+filterCond, filterArgs...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count todos: %w", err)
		}
	}
	return items, total, nil
}

func dir(desc bool) string {
	if desc {
		return " DESC"
	}
	return " ASC"
}

// builder accumulates WHERE clauses with positional arguments.
type builder struct {
	conds []string
	args  []any
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

func (b *builder) where(c string) { b.conds = append(b.conds, c) }

func (b *builder) cond() string { return strings.Join(b.conds, " AND ") }

// keyset renders "row is after the cursor" for possibly mixed directions:
// (k1 > v1) OR (k1 = v1 AND k2 > v2) OR ... OR (all equal AND id > cursor.id),
// with < for descending keys.
func (b *builder) keyset(keys []todo.SortKey, c todo.Cursor) string {
	exprs := make([]string, 0, len(keys)+1)
	vals := make([]string, 0, len(keys)+1)
	ops := make([]string, 0, len(keys)+1)
	for i, k := range keys {
		exprs = append(exprs, sortExpr[k.Field])
		vals = append(vals, b.arg(cursorParam(k.Field, c.Values[i])))
		ops = append(ops, cmpOp(k.Desc))
	}
	exprs = append(exprs, "id")
	vals = append(vals, b.arg(c.ID))
	ops = append(ops, cmpOp(keys[len(keys)-1].Desc))

	var ors []string
	for i := range exprs {
		var ands []string
		for j := range i {
			ands = append(ands, exprs[j]+" = "+vals[j])
		}
		ands = append(ands, exprs[i]+" "+ops[i]+" "+vals[i])
		ors = append(ors, "("+strings.Join(ands, " AND ")+")")
	}
	return "(" + strings.Join(ors, " OR ") + ")"
}

func cmpOp(desc bool) string {
	if desc {
		return "<"
	}
	return ">"
}

// cursorParam converts a decoded cursor value into a query parameter, mapping a
// NULL due date to 'infinity' to match the coalesce in sortExpr.
func cursorParam(f todo.SortField, v any) any {
	if f == todo.SortDueAt {
		if t, _ := v.(*time.Time); t != nil {
			return *t
		}
		return pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}
	}
	return v
}

func (r *Repo) Update(ctx context.Context, t *todo.Todo, expectedVersion int) (*todo.Todo, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	q := r.q(ctx)
	row, err := q.UpdateTodo(ctx, db.UpdateTodoParams{
		ID:       t.ID,
		OwnerID:  t.OwnerID,
		Version:  int32(expectedVersion), // #nosec G115
		Title:    t.Title,
		Notes:    t.Notes,
		Status:   db.TodoStatus(t.Status),
		DueAt:    t.DueAt,
		Priority: int16(t.Priority), // #nosec G115
	})
	if err != nil {
		if postgres.IsNoRows(err) {
			// Zero rows: either it does not exist (for this owner) or the version moved on.
			if _, verr := q.GetTodoVersion(ctx, db.GetTodoVersionParams{ID: t.ID, OwnerID: t.OwnerID}); verr != nil {
				if postgres.IsNoRows(verr) {
					return nil, todo.ErrNotFound
				}
				return nil, fmt.Errorf("check todo version: %w", verr)
			}
			return nil, todo.ErrVersionConflict
		}
		return nil, fmt.Errorf("update todo: %w", err)
	}
	out := fromRow(db.GetTodoRow(row))
	return &out, nil
}

func (r *Repo) SoftDelete(ctx context.Context, ownerID, id uuid.UUID, expectedVersion int) error {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	q := r.q(ctx)
	n, err := q.SoftDeleteTodo(ctx, db.SoftDeleteTodoParams{ID: id, OwnerID: ownerID, ExpectedVersion: int32(expectedVersion)}) // #nosec G115
	if err != nil {
		return fmt.Errorf("delete todo: %w", err)
	}
	if n == 0 {
		if _, verr := q.GetTodoVersion(ctx, db.GetTodoVersionParams{ID: id, OwnerID: ownerID}); verr != nil {
			if postgres.IsNoRows(verr) {
				return todo.ErrNotFound
			}
			return fmt.Errorf("check todo version: %w", verr)
		}
		return todo.ErrVersionConflict
	}
	return nil
}

const columns = "id, owner_id, title, notes, status, due_at, priority, version, deleted_at, created_at, updated_at"

func scanTodo(row pgx.CollectableRow) (todo.Todo, error) {
	var d db.GetTodoRow
	err := row.Scan(&d.ID, &d.OwnerID, &d.Title, &d.Notes, &d.Status, &d.DueAt, &d.Priority, &d.Version, &d.DeletedAt, &d.CreatedAt, &d.UpdatedAt)
	return fromRow(d), err
}

// fromRow converts a todo row. Create/Get/Update rows share one shape (the
// generated search column is never selected), so they convert to db.GetTodoRow.
func fromRow(d db.GetTodoRow) todo.Todo {
	return todo.Todo{
		ID:        d.ID,
		OwnerID:   d.OwnerID,
		Title:     d.Title,
		Notes:     d.Notes,
		Status:    todo.Status(d.Status),
		DueAt:     d.DueAt,
		Priority:  int(d.Priority),
		Version:   int(d.Version),
		DeletedAt: d.DeletedAt,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}
