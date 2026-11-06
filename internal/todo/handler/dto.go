package handler

import (
	"bytes"
	"encoding/json"
	"time"

	"echo-todo-2/internal/todo"
)

type CreateTodoRequest struct {
	Title    string     `json:"title" validate:"notblank,max=200"`
	Notes    string     `json:"notes" validate:"max=10000"`
	Status   string     `json:"status" validate:"omitempty,oneof=open done archived"`
	DueAt    *time.Time `json:"due_at"`
	Priority *int       `json:"priority" validate:"omitnil,min=1,max=5"`
}

func (r CreateTodoRequest) toInput() todo.CreateInput {
	in := todo.CreateInput{Title: r.Title, Notes: r.Notes, Status: todo.Status(r.Status), DueAt: r.DueAt}
	if r.Priority != nil {
		in.Priority = *r.Priority
	}
	return in
}

// Nullable distinguishes "absent" from "explicit null" in a JSON PATCH body.
type Nullable[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if bytes.Equal(b, []byte("null")) {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

type UpdateTodoRequest struct {
	Title    *string             `json:"title" validate:"omitnil,notblank,max=200"`
	Notes    *string             `json:"notes" validate:"omitnil,max=10000"`
	Status   *string             `json:"status" validate:"omitnil,oneof=open done archived"`
	DueAt    Nullable[time.Time] `json:"due_at"`
	Priority *int                `json:"priority" validate:"omitnil,min=1,max=5"`
}

func (r UpdateTodoRequest) toInput() todo.UpdateInput {
	in := todo.UpdateInput{Title: r.Title, Notes: r.Notes, Priority: r.Priority}
	if r.Status != nil {
		s := todo.Status(*r.Status)
		in.Status = &s
	}
	if r.DueAt.Set {
		if r.DueAt.Null {
			in.ClearDueAt = true
		} else {
			v := r.DueAt.Value
			in.DueAt = &v
		}
	}
	return in
}

type TodoResponse struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Notes     string     `json:"notes"`
	Status    string     `json:"status"`
	DueAt     *time.Time `json:"due_at"`
	Priority  int        `json:"priority"`
	Version   int        `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func toDTO(t *todo.Todo) TodoResponse {
	return TodoResponse{
		ID:        t.ID.String(),
		Title:     t.Title,
		Notes:     t.Notes,
		Status:    string(t.Status),
		DueAt:     t.DueAt,
		Priority:  t.Priority,
		Version:   t.Version,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}

type ListResponse struct {
	Items  []TodoResponse `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}
