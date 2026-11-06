package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/todo"
)

type exportEncoder interface {
	contentType() string
	header(w io.Writer) error
	row(w io.Writer, t *todo.Todo) error
	flush(w io.Writer) error
}

// exportAll pages through every todo (keyset, oldest first) and encodes it.
func exportAll(ctx context.Context, svc *todo.Service, owner uuid.UUID, enc exportEncoder, w io.Writer) error {
	if err := enc.header(w); err != nil {
		return err
	}
	cursor := ""
	for {
		page, err := svc.List(ctx, todo.ListQuery{
			Filter:   todo.Filter{OwnerID: owner, Limit: todo.MaxLimit},
			SortSpec: "created_at",
			Cursor:   cursor,
		})
		if err != nil {
			return err
		}
		for i := range page.Items {
			if err := enc.row(w, &page.Items[i]); err != nil {
				return err
			}
		}
		if err := enc.flush(w); err != nil {
			return err
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

type csvEncoder struct{ cw *csv.Writer }

func newCSVEncoder() *csvEncoder { return &csvEncoder{} }

func (e *csvEncoder) contentType() string { return "text/csv; charset=utf-8" }

func (e *csvEncoder) header(w io.Writer) error {
	e.cw = csv.NewWriter(w)
	return e.cw.Write([]string{"id", "title", "notes", "status", "priority", "due_at", "version", "created_at", "updated_at"})
}

func (e *csvEncoder) row(_ io.Writer, t *todo.Todo) error {
	due := ""
	if t.DueAt != nil {
		due = t.DueAt.UTC().Format(time.RFC3339)
	}
	return e.cw.Write([]string{
		t.ID.String(), csvSafe(t.Title), csvSafe(t.Notes), string(t.Status), strconv.Itoa(t.Priority), due,
		strconv.Itoa(t.Version), t.CreatedAt.UTC().Format(time.RFC3339), t.UpdatedAt.UTC().Format(time.RFC3339),
	})
}

func (e *csvEncoder) flush(io.Writer) error {
	e.cw.Flush()
	return e.cw.Error()
}

// csvSafe defuses spreadsheet formula injection (cells starting with = + - @).
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}

type ndjsonEncoder struct{ je *json.Encoder }

func newNDJSONEncoder() *ndjsonEncoder { return &ndjsonEncoder{} }

func (e *ndjsonEncoder) contentType() string { return "application/x-ndjson" }

func (e *ndjsonEncoder) header(w io.Writer) error {
	e.je = json.NewEncoder(w)
	return nil
}

func (e *ndjsonEncoder) row(_ io.Writer, t *todo.Todo) error { return e.je.Encode(toDTO(t)) }

func (e *ndjsonEncoder) flush(io.Writer) error { return nil }
