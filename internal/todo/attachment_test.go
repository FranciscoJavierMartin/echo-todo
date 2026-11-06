package todo_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/platform/storage"
	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/todotest"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

type attEnv struct {
	svc   *todo.AttachmentService
	repo  *todotest.Repo
	dir   string
	owner uuid.UUID
	todo  *todo.Todo
}

func newAttEnv(t *testing.T, max int64) *attEnv {
	t.Helper()
	svc, repo := todotest.NewService()
	dir := t.TempDir()
	blobs, err := storage.NewLocal(dir)
	require.NoError(t, err)
	owner := uuid.New()
	td, err := svc.Create(context.Background(), owner, todo.CreateInput{Title: "files"})
	require.NoError(t, err)
	return &attEnv{svc: todo.NewAttachmentService(svc, repo, blobs, max), repo: repo, dir: dir, owner: owner, todo: td}
}

// blobCount counts committed blobs (temp files start with a dot).
func (e *attEnv) blobCount(t *testing.T) int {
	entries, err := os.ReadDir(e.dir)
	require.NoError(t, err)
	n := 0
	for _, en := range entries {
		if !strings.HasPrefix(en.Name(), ".") {
			n++
		}
	}
	return n
}

func kindAndCode(t *testing.T, err error) (apperr.Kind, string) {
	t.Helper()
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	return ae.Kind, ae.MachineCode()
}

func TestUploadHappyPathListGetDelete(t *testing.T) {
	e := newAttEnv(t, 1<<20)
	ctx := context.Background()
	a, err := e.svc.Upload(ctx, e.owner, e.todo.ID, "dir/shot.PNG", bytes.NewReader(pngHeader))
	require.NoError(t, err)
	require.Equal(t, "shot.PNG", a.Filename)
	require.Equal(t, "image/png", a.ContentType)
	require.EqualValues(t, len(pngHeader), a.Size)
	require.Len(t, a.SHA256, 32)
	require.Equal(t, 1, e.blobCount(t))

	list, err := e.svc.List(ctx, e.owner, e.todo.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	got, err := e.svc.Get(ctx, e.owner, e.todo.ID, a.ID)
	require.NoError(t, err)
	require.Equal(t, a.StorageKey, got.StorageKey)

	require.NoError(t, e.svc.Delete(ctx, e.owner, e.todo.ID, a.ID))
	require.Equal(t, 0, e.blobCount(t))
	_, err = e.svc.Get(ctx, e.owner, e.todo.ID, a.ID)
	require.True(t, apperr.Is(err, apperr.KindNotFound))
	require.True(t, apperr.Is(e.svc.Delete(ctx, e.owner, e.todo.ID, a.ID), apperr.KindNotFound))
}

func TestUploadRejections(t *testing.T) {
	e := newAttEnv(t, 64)
	ctx := context.Background()

	_, err := e.svc.Upload(ctx, uuid.New(), e.todo.ID, "x.png", bytes.NewReader(pngHeader))
	require.True(t, apperr.Is(err, apperr.KindNotFound), "foreign todo")

	_, err = e.svc.Upload(ctx, e.owner, e.todo.ID, "", bytes.NewReader(pngHeader))
	require.True(t, apperr.Is(err, apperr.KindInvalid))

	_, err = e.svc.Upload(ctx, e.owner, e.todo.ID, "run.sh", strings.NewReader("#!/bin/sh"))
	k, code := kindAndCode(t, err)
	require.Equal(t, apperr.KindUnsupportedMedia, k)
	require.Equal(t, "file_type_not_allowed", code)

	_, err = e.svc.Upload(ctx, e.owner, e.todo.ID, "fake.pdf", strings.NewReader("plain text pretending"))
	_, code = kindAndCode(t, err)
	require.Equal(t, "content_mismatch", code)

	big := io.MultiReader(bytes.NewReader(pngHeader), bytes.NewReader(make([]byte, 100)))
	_, err = e.svc.Upload(ctx, e.owner, e.todo.ID, "big.png", big)
	k, code = kindAndCode(t, err)
	require.Equal(t, apperr.KindTooLarge, k)
	require.Equal(t, "file_too_large", code)
	require.Equal(t, 0, e.blobCount(t), "oversized blob deleted")

	e.repo.AttachErr = errors.New("db down")
	_, err = e.svc.Upload(ctx, e.owner, e.todo.ID, "ok.png", bytes.NewReader(pngHeader))
	require.True(t, apperr.Is(err, apperr.KindInternal))
	require.Equal(t, 0, e.blobCount(t), "blob removed when metadata fails")

	_, err = e.svc.List(ctx, uuid.New(), e.todo.ID)
	require.True(t, apperr.Is(err, apperr.KindNotFound))
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

type statusErr int

func (s statusErr) Error() string   { return "status" }
func (s statusErr) StatusCode() int { return int(s) }

func TestUploadReadErrors(t *testing.T) {
	e := newAttEnv(t, 1<<20)
	ctx := context.Background()
	_, err := e.svc.Upload(ctx, e.owner, e.todo.ID, "a.png", failingReader{statusErr(413)})
	k, _ := kindAndCode(t, err)
	require.Equal(t, apperr.KindTooLarge, k, "body limit while sniffing")

	_, err = e.svc.Upload(ctx, e.owner, e.todo.ID, "a.png", io.MultiReader(bytes.NewReader(pngHeader), failingReader{errors.New("conn reset")}))
	k, _ = kindAndCode(t, err)
	require.Equal(t, apperr.KindInternal, k)
}

func TestSanitizeFilenameAndInlineSafe(t *testing.T) {
	cases := map[string]string{
		"report.pdf":            "report.pdf",
		"../../etc/passwd":      "passwd",
		`C:\Users\me\photo.jpg`: "photo.jpg",
		"we\"ird\nname.txt":     "weirdname.txt",
		"/":                     "",
		".":                     "",
		"  spaced.md  ":         "spaced.md",
	}
	for in, want := range cases {
		require.Equal(t, want, todo.SanitizeFilename(in), in)
	}
	require.Len(t, []rune(todo.SanitizeFilename(strings.Repeat("é", 300)+".txt")), 255)

	require.True(t, todo.InlineSafe("image/png"))
	require.True(t, todo.InlineSafe("text/plain; charset=utf-8"))
	require.True(t, todo.InlineSafe("application/pdf"))
	require.False(t, todo.InlineSafe("text/html"))
	require.False(t, todo.InlineSafe("application/octet-stream"))
}

func TestServiceCacheAndOutbox(t *testing.T) {
	repo := todotest.NewRepo()
	cache, ob := todotest.NewCache(), &todotest.Outbox{}
	svc := todo.NewService(repo, todotest.NoTx{}, repo).WithCache(cache).WithOutbox(ob)
	ctx := context.Background()
	owner := uuid.New()

	td, err := svc.Create(ctx, owner, todo.CreateInput{Title: "c"})
	require.NoError(t, err)
	_, err = svc.Get(ctx, owner, td.ID) // miss → populate
	require.NoError(t, err)
	_, ok := cache.Get(ctx, td.ID)
	require.True(t, ok)

	_, err = svc.Get(ctx, uuid.New(), td.ID)
	require.True(t, apperr.Is(err, apperr.KindNotFound), "cached entries are still owner-checked")

	done := todo.StatusDone
	_, err = svc.Update(ctx, owner, td.ID, todo.UpdateInput{Status: &done}, 1)
	require.NoError(t, err)
	require.Contains(t, cache.Invalidated, td.ID)
	require.NoError(t, svc.Delete(ctx, owner, td.ID, 0))
	require.Equal(t, []string{todo.TopicTodoCreated, todo.TopicTodoUpdated, todo.TopicTodoDone, todo.TopicTodoDeleted}, ob.Topics)

	events, err := svc.AdminAudit(ctx, td.ID)
	require.NoError(t, err)
	require.Len(t, events, 3)
	_, err = svc.AdminAudit(ctx, uuid.New())
	require.True(t, apperr.Is(err, apperr.KindNotFound))

	var noop todo.NoopCache
	noop.Set(ctx, td)
	noop.Invalidate(ctx, td.ID)
	_, ok = noop.Get(ctx, td.ID)
	require.False(t, ok)
}

func TestListValidatesSortAndCursor(t *testing.T) {
	svc, _ := todotest.NewService()
	ctx := context.Background()
	owner := uuid.New()
	for range 3 {
		_, err := svc.Create(ctx, owner, todo.CreateInput{Title: "x"})
		require.NoError(t, err)
	}
	_, err := svc.List(ctx, todo.ListQuery{Filter: todo.Filter{OwnerID: owner}, SortSpec: "nope"})
	require.True(t, apperr.Is(err, apperr.KindInvalid))
	_, err = svc.List(ctx, todo.ListQuery{Filter: todo.Filter{OwnerID: owner}, Cursor: "bad"})
	require.True(t, apperr.Is(err, apperr.KindInvalid))

	page, err := svc.List(ctx, todo.ListQuery{Filter: todo.Filter{OwnerID: owner, Limit: 2}})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.NotEmpty(t, page.NextCursor)
	page, err = svc.List(ctx, todo.ListQuery{Filter: todo.Filter{OwnerID: owner, Limit: 500}, Cursor: page.NextCursor})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Empty(t, page.NextCursor)
}
