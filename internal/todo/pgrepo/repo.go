// Package pgrepo implements todo.Repo on PostgreSQL using sqlc-generated queries.
package pgrepo

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/todo"
)

type Repo struct {
	pool *pgxpool.Pool
}

var _ todo.Repo = (*Repo)(nil)

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) q(ctx context.Context) *db.Queries {
	return db.New(postgres.Conn(ctx, r.pool))
}

func (r *Repo) Create(ctx context.Context, t *todo.Todo) error {
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
	*t = fromRow(row)
	return nil
}

func (r *Repo) Get(ctx context.Context, ownerID, id uuid.UUID) (*todo.Todo, error) {
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

func (r *Repo) List(ctx context.Context, f todo.Filter) ([]todo.Todo, int, error) {
	where := []string{"owner_id = $1", "deleted_at IS NULL"}
	args := []any{f.OwnerID}
	if len(f.Statuses) > 0 {
		statuses := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		args = append(args, statuses)
		where = append(where, fmt.Sprintf("status = ANY($%d::todo_status[])", len(args)))
	}
	cond := strings.Join(where, " AND ")

	conn := postgres.Conn(ctx, r.pool)
	var total int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM todos WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count todos: %w", err)
	}

	args = append(args, f.Limit, f.Offset)
	sql := fmt.Sprintf("SELECT %s FROM todos WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d",
		columns, cond, len(args)-1, len(args))
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list todos: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanTodo)
	if err != nil {
		return nil, 0, fmt.Errorf("scan todos: %w", err)
	}
	return items, total, nil
}

func (r *Repo) Update(ctx context.Context, t *todo.Todo, expectedVersion int) (*todo.Todo, error) {
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
	out := fromRow(row)
	return &out, nil
}

func (r *Repo) SoftDelete(ctx context.Context, ownerID, id uuid.UUID) error {
	n, err := r.q(ctx).SoftDeleteTodo(ctx, db.SoftDeleteTodoParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("delete todo: %w", err)
	}
	if n == 0 {
		return todo.ErrNotFound
	}
	return nil
}

const columns = "id, owner_id, title, notes, status, due_at, priority, version, deleted_at, created_at, updated_at"

func scanTodo(row pgx.CollectableRow) (todo.Todo, error) {
	var d db.Todo
	err := row.Scan(&d.ID, &d.OwnerID, &d.Title, &d.Notes, &d.Status, &d.DueAt, &d.Priority, &d.Version, &d.DeletedAt, &d.CreatedAt, &d.UpdatedAt)
	return fromRow(d), err
}

func fromRow(d db.Todo) todo.Todo {
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
