package todo_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/todo"
)

func TestParseSort(t *testing.T) {
	keys, err := todo.ParseSort("-priority, due_at,+title")
	require.NoError(t, err)
	require.Equal(t, []todo.SortKey{{todo.SortPriority, true}, {todo.SortDueAt, false}, {todo.SortTitle, false}}, keys)

	keys, err = todo.ParseSort("")
	require.NoError(t, err)
	require.Equal(t, todo.DefaultSort, keys)

	for _, bad := range []string{"password", "priority,priority", "-", "id", "priority;drop table", "a,b,c,d", "priority,due_at,title,created_at"} {
		_, err := todo.ParseSort(bad)
		require.ErrorIs(t, err, todo.ErrInvalidSort, bad)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	keys, _ := todo.ParseSort("-priority,due_at,title")
	due := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)
	for _, td := range []todo.Todo{
		{ID: uuid.New(), Priority: 2, DueAt: &due, Title: "a"},
		{ID: uuid.New(), Priority: 5, DueAt: nil, Title: "zz"},
	} {
		c := todo.CursorAfter(keys, &td)
		enc := todo.EncodeCursor(keys, c)
		dec, err := todo.DecodeCursor(keys, enc)
		require.NoError(t, err)
		require.Equal(t, c.ID, dec.ID)
		require.Equal(t, 0, todo.Compare(keys, &td, &td))
		require.False(t, todo.IsAfter(keys, &td, *dec), "a row is not after its own cursor")
	}

	td := todo.Todo{ID: uuid.New(), CreatedAt: time.Now()}
	enc := todo.EncodeCursor(todo.DefaultSort, todo.CursorAfter(todo.DefaultSort, &td))
	_, err := todo.DecodeCursor(keys, enc)
	require.ErrorIs(t, err, todo.ErrInvalidCursor, "cursor bound to another sort")

	for _, bad := range []string{"!!!", base64.RawURLEncoding.EncodeToString([]byte("{")), base64.RawURLEncoding.EncodeToString([]byte(`{"s":"-created_at","v":["x"],"id":"` + uuid.NewString() + `"}`))} {
		_, err := todo.DecodeCursor(todo.DefaultSort, bad)
		require.ErrorIs(t, err, todo.ErrInvalidCursor, bad)
	}
}

func TestCompareNullDueSortsLast(t *testing.T) {
	keys := []todo.SortKey{{Field: todo.SortDueAt}}
	now := time.Now()
	withDue := &todo.Todo{ID: uuid.New(), DueAt: &now}
	noDue := &todo.Todo{ID: uuid.New()}
	require.Negative(t, todo.Compare(keys, withDue, noDue))
	desc := []todo.SortKey{{Field: todo.SortDueAt, Desc: true}}
	require.Positive(t, todo.Compare(desc, withDue, noDue))
	later := now.Add(time.Hour)
	require.Negative(t, todo.Compare(keys, withDue, &todo.Todo{ID: uuid.New(), DueAt: &later}))
	require.Equal(t, 0, todo.Compare(keys, noDue, &todo.Todo{ID: noDue.ID}))
}
