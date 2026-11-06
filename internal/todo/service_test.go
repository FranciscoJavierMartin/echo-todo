package todo_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/todotest"
)

func newService() (*todo.Service, *todotest.Repo) {
	repo := todotest.NewRepo()
	return todo.NewService(repo, todotest.NoTx{}, repo), repo
}

func ptr[T any](v T) *T { return &v }

func TestCreate_EmptyTitleIsInvalid(t *testing.T) {
	svc, _ := newService()
	_, err := svc.Create(context.Background(), uuid.New(), todo.CreateInput{Title: "   "})

	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apperr.KindInvalid, ae.Kind)
	require.Contains(t, ae.Fields, "title")
	require.Equal(t, "todo.Service.Create", ae.Op)
}

func TestCreate_ValidationAggregatesFields(t *testing.T) {
	svc, _ := newService()
	_, err := svc.Create(context.Background(), uuid.New(), todo.CreateInput{
		Title:    strings.Repeat("x", 201),
		Notes:    strings.Repeat("n", 10001),
		Priority: 9,
		Status:   "nope",
	})
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	require.Len(t, ae.Fields, 4)
}

func TestCreate_Defaults(t *testing.T) {
	svc, _ := newService()
	owner := uuid.New()
	got, err := svc.Create(context.Background(), owner, todo.CreateInput{Title: "  write tests "})
	require.NoError(t, err)
	require.Equal(t, "write tests", got.Title)
	require.Equal(t, todo.StatusOpen, got.Status)
	require.Equal(t, 3, got.Priority)
	require.Equal(t, owner, got.OwnerID)
}

func TestGet_TranslatesNotFound(t *testing.T) {
	svc, _ := newService()
	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	require.True(t, apperr.Is(err, apperr.KindNotFound))
	require.ErrorIs(t, err, todo.ErrNotFound)
}

func TestUpdate_PartialAndVersioned(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	owner := uuid.New()
	created, err := svc.Create(ctx, owner, todo.CreateInput{Title: "a", DueAt: ptr(time.Now())})
	require.NoError(t, err)

	updated, err := svc.Update(ctx, owner, created.ID, todo.UpdateInput{
		Status: ptr(todo.StatusDone), ClearDueAt: true, Priority: ptr(1), Notes: ptr("n"), Title: ptr("b"),
	}, created.Version)
	require.NoError(t, err)
	require.Equal(t, todo.StatusDone, updated.Status)
	require.Nil(t, updated.DueAt)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, "b", updated.Title)

	_, err = svc.Update(ctx, owner, created.ID, todo.UpdateInput{Title: ptr("c")}, 1)
	require.True(t, apperr.Is(err, apperr.KindConflict))
	require.ErrorIs(t, err, todo.ErrVersionConflict)

	_, err = svc.Update(ctx, owner, created.ID, todo.UpdateInput{Priority: ptr(0)}, 0)
	require.True(t, apperr.Is(err, apperr.KindInvalid))

	_, err = svc.Update(ctx, uuid.New(), created.ID, todo.UpdateInput{}, 0)
	require.True(t, apperr.Is(err, apperr.KindNotFound))
}

func TestDeleteAndList(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	owner := uuid.New()
	for range 3 {
		_, err := svc.Create(ctx, owner, todo.CreateInput{Title: "x"})
		require.NoError(t, err)
	}
	page, err := svc.List(ctx, todo.ListQuery{Filter: todo.Filter{OwnerID: owner, Limit: 1000, WithCount: true}})
	require.NoError(t, err)
	require.Equal(t, 3, *page.Total)

	require.NoError(t, svc.Delete(ctx, owner, page.Items[0].ID, 0))
	require.True(t, apperr.Is(svc.Delete(ctx, owner, page.Items[0].ID, 0), apperr.KindNotFound))
	page, err = svc.List(ctx, todo.ListQuery{Filter: todo.Filter{OwnerID: owner}})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Nil(t, page.Total, "total only when requested")
}

func TestStorageFailureIsInternal(t *testing.T) {
	svc, repo := newService()
	repo.Err = errors.New("connection refused")
	_, err := svc.Create(context.Background(), uuid.New(), todo.CreateInput{Title: "x"})
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apperr.KindInternal, ae.Kind)
	require.Equal(t, "internal error", ae.Message)
	require.ErrorContains(t, ae.Err, "connection refused")

	_, err = svc.List(context.Background(), todo.ListQuery{})
	require.True(t, apperr.Is(err, apperr.KindInternal))
	repo.Err = context.DeadlineExceeded
	_, err = svc.Get(context.Background(), uuid.New(), uuid.New())
	require.ErrorAs(t, err, &ae)
	require.Equal(t, "timeout", ae.Code)
	require.Equal(t, 503, ae.StatusCode())
}
