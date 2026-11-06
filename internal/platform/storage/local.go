// Package storage stores binary blobs (attachments).
package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("blob not found")

// Local stores blobs as files named by opaque keys under a root directory.
// Keys are server-generated UUIDs, never user input, so there is no traversal.
type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

func validKey(key string) error {
	if _, err := uuid.Parse(key); err != nil {
		return fmt.Errorf("invalid storage key %q", key)
	}
	return nil
}

// Put streams r into a temp file and renames it into place, so readers never see
// partial blobs. It returns the byte count and SHA-256 of what was written.
func (l *Local) Put(ctx context.Context, key string, r io.Reader) (int64, []byte, error) {
	if err := validKey(key); err != nil {
		return 0, nil, err
	}
	tmp, err := os.CreateTemp(l.root, ".upload-*")
	if err != nil {
		return 0, nil, fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), ctxReader{ctx: ctx, r: r})
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, nil, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(l.root, key)); err != nil {
		return n, nil, fmt.Errorf("commit blob: %w", err)
	}
	return n, h.Sum(nil), nil
}

func (l *Local) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	if err := validKey(key); err != nil {
		return nil, ErrNotFound
	}
	f, err := os.Open(filepath.Join(l.root, key)) // #nosec G304 -- key validated as UUID
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l *Local) Delete(_ context.Context, key string) error {
	if err := validKey(key); err != nil {
		return nil
	}
	err := os.Remove(filepath.Join(l.root, key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// FS exposes the blobs as an fs.FS (used as echo's Filesystem for c.Attachment).
func (l *Local) FS() fs.FS { return os.DirFS(l.root) }

// ctxReader stops copying when the request context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
