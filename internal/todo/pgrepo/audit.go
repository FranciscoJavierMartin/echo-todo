package pgrepo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"echo-todo-2/internal/db"
	"echo-todo-2/internal/todo"
)

var _ todo.AuditLog = (*Repo)(nil)

func (r *Repo) AppendAudit(ctx context.Context, e todo.AuditEvent) error {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	changes, err := json.Marshal(e.Changes)
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}
	if e.Changes == nil {
		changes = []byte("{}")
	}
	return r.q(ctx).InsertAuditEvent(ctx, db.InsertAuditEventParams{
		TodoID: e.TodoID, ActorID: e.ActorID, Action: e.Action, Changes: changes,
	})
}

func (r *Repo) ListAudit(ctx context.Context, todoID uuid.UUID) ([]todo.AuditEvent, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	q := r.q(ctx)
	exists, err := q.TodoExistsAnyOwner(ctx, todoID)
	if err != nil {
		return nil, fmt.Errorf("check todo: %w", err)
	}
	if !exists {
		return nil, todo.ErrNotFound
	}
	rows, err := q.ListAuditEvents(ctx, todoID)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	out := make([]todo.AuditEvent, len(rows))
	for i, row := range rows {
		out[i] = todo.AuditEvent{ID: row.ID, TodoID: row.TodoID, ActorID: row.ActorID, Action: row.Action, CreatedAt: row.CreatedAt}
		if err := json.Unmarshal(row.Changes, &out[i].Changes); err != nil {
			return nil, fmt.Errorf("unmarshal audit changes: %w", err)
		}
	}
	return out, nil
}
