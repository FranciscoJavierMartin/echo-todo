package storage_test

import (
	"context"
	"crypto/sha256"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/platform/storage"
)

func TestLocalPutOpenDelete(t *testing.T) {
	l, err := storage.NewLocal(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	key := uuid.NewString()

	n, sum, err := l.Put(ctx, key, strings.NewReader("hello"))
	require.NoError(t, err)
	require.EqualValues(t, 5, n)
	want := sha256.Sum256([]byte("hello"))
	require.Equal(t, want[:], sum)

	rc, err := l.Open(ctx, key)
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.Equal(t, "hello", string(b))

	fb, err := fs.ReadFile(l.FS(), key)
	require.NoError(t, err)
	require.Equal(t, "hello", string(fb))

	require.NoError(t, l.Delete(ctx, key))
	require.NoError(t, l.Delete(ctx, key), "idempotent")
	_, err = l.Open(ctx, key)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestLocalRejectsNonUUIDKeys(t *testing.T) {
	l, err := storage.NewLocal(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	_, _, err = l.Put(ctx, "../escape", strings.NewReader("x"))
	require.Error(t, err)
	_, err = l.Open(ctx, "../../etc/passwd")
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, l.Delete(ctx, "../x"))
}

func TestLocalPutHonoursCancellation(t *testing.T) {
	l, err := storage.NewLocal(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key := uuid.NewString()
	_, _, err = l.Put(ctx, key, strings.NewReader("data"))
	require.ErrorIs(t, err, context.Canceled)
	_, err = l.Open(context.Background(), key)
	require.ErrorIs(t, err, storage.ErrNotFound, "no partial blob committed")
}
