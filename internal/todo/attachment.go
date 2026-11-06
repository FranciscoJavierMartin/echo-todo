package todo

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"echo-todo-2/internal/apperr"
)

type Attachment struct {
	ID          uuid.UUID
	TodoID      uuid.UUID
	OwnerID     uuid.UUID
	Filename    string
	ContentType string
	Size        int64
	SHA256      []byte
	StorageKey  string
	CreatedAt   time.Time
}

// AttachmentRepo stores attachment metadata; every query is owner-scoped.
type AttachmentRepo interface {
	InsertAttachment(ctx context.Context, a *Attachment) error
	GetAttachment(ctx context.Context, ownerID, todoID, id uuid.UUID) (*Attachment, error)
	ListAttachments(ctx context.Context, ownerID, todoID uuid.UUID) ([]Attachment, error)
	DeleteAttachment(ctx context.Context, ownerID, todoID, id uuid.UUID) (storageKey string, err error)
}

// BlobStore holds attachment bytes.
type BlobStore interface {
	Put(ctx context.Context, key string, r io.Reader) (int64, []byte, error)
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
}

// allowedTypes maps permitted extensions to the media type content sniffing must
// produce. The client's declared Content-Type is never trusted.
var allowedTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".md":   "text/plain",
	".csv":  "text/plain",
}

// InlineSafe reports whether a type may be rendered inline by browsers.
func InlineSafe(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return strings.HasPrefix(mt, "image/") || mt == "application/pdf" || mt == "text/plain"
}

type AttachmentService struct {
	todos   *Service
	repo    AttachmentRepo
	blobs   BlobStore
	maxSize int64
}

func NewAttachmentService(todos *Service, repo AttachmentRepo, blobs BlobStore, maxSize int64) *AttachmentService {
	return &AttachmentService{todos: todos, repo: repo, blobs: blobs, maxSize: maxSize}
}

func (s *AttachmentService) MaxSize() int64 { return s.maxSize }

// Upload validates the extension and sniffed content, streams the bytes to the
// blob store (never more than maxSize+1 bytes), then records metadata.
func (s *AttachmentService) Upload(ctx context.Context, ownerID, todoID uuid.UUID, filename string, r io.Reader) (*Attachment, error) {
	const op = "todo.AttachmentService.Upload"
	if _, err := s.todos.Get(ctx, ownerID, todoID); err != nil {
		return nil, err
	}
	name := SanitizeFilename(filename)
	if name == "" {
		return nil, apperr.Invalid("file", "a file name is required").WithOp(op)
	}
	want, ok := allowedTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return nil, apperr.New(apperr.KindUnsupportedMedia, "file type not allowed").WithOp(op).WithCode("file_type_not_allowed")
	}

	br := bufio.NewReaderSize(r, 512)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, uploadReadError(op, err)
	}
	sniffed := http.DetectContentType(head)
	if mt, _, _ := mime.ParseMediaType(sniffed); mt != want {
		return nil, apperr.New(apperr.KindUnsupportedMedia, "file content does not match its extension").
			WithOp(op).WithCode("content_mismatch")
	}

	key := uuid.NewString()
	n, sum, err := s.blobs.Put(ctx, key, io.LimitReader(br, s.maxSize+1))
	if err != nil {
		_ = s.blobs.Delete(context.WithoutCancel(ctx), key)
		return nil, uploadReadError(op, err)
	}
	if n > s.maxSize {
		_ = s.blobs.Delete(context.WithoutCancel(ctx), key)
		return nil, apperr.New(apperr.KindTooLarge, "file exceeds the upload limit").WithOp(op).WithCode("file_too_large")
	}
	a := &Attachment{
		TodoID: todoID, OwnerID: ownerID, Filename: name, ContentType: sniffed,
		Size: n, SHA256: sum, StorageKey: key,
	}
	if err := s.repo.InsertAttachment(ctx, a); err != nil {
		_ = s.blobs.Delete(context.WithoutCancel(ctx), key)
		return nil, translate(op, err)
	}
	return a, nil
}

func uploadReadError(op string, err error) error {
	var sc interface{ StatusCode() int }
	if errors.As(err, &sc) && sc.StatusCode() == http.StatusRequestEntityTooLarge {
		return apperr.New(apperr.KindTooLarge, "file exceeds the upload limit").WithOp(op).WithCode("file_too_large").Wrap(err)
	}
	return apperr.Internal(op, err)
}

func (s *AttachmentService) List(ctx context.Context, ownerID, todoID uuid.UUID) ([]Attachment, error) {
	if _, err := s.todos.Get(ctx, ownerID, todoID); err != nil {
		return nil, err
	}
	items, err := s.repo.ListAttachments(ctx, ownerID, todoID)
	if err != nil {
		return nil, translate("todo.AttachmentService.List", err)
	}
	return items, nil
}

// Open returns metadata and a reader for the blob. The caller closes the reader.
func (s *AttachmentService) Open(ctx context.Context, ownerID, todoID, id uuid.UUID) (*Attachment, io.ReadSeekCloser, error) {
	const op = "todo.AttachmentService.Open"
	a, err := s.repo.GetAttachment(ctx, ownerID, todoID, id)
	if err != nil {
		return nil, nil, translate(op, err)
	}
	rc, err := s.blobs.Open(ctx, a.StorageKey)
	if err != nil {
		return nil, nil, apperr.Internal(op, err)
	}
	return a, rc, nil
}

// Get returns attachment metadata (owner-scoped).
func (s *AttachmentService) Get(ctx context.Context, ownerID, todoID, id uuid.UUID) (*Attachment, error) {
	a, err := s.repo.GetAttachment(ctx, ownerID, todoID, id)
	if err != nil {
		return nil, translate("todo.AttachmentService.Get", err)
	}
	return a, nil
}

func (s *AttachmentService) Delete(ctx context.Context, ownerID, todoID, id uuid.UUID) error {
	const op = "todo.AttachmentService.Delete"
	key, err := s.repo.DeleteAttachment(ctx, ownerID, todoID, id)
	if err != nil {
		return translate(op, err)
	}
	if err := s.blobs.Delete(ctx, key); err != nil {
		return apperr.Internal(op, err)
	}
	return nil
}

// SanitizeFilename keeps only the base name, drops control and quote characters
// and caps the length, so it is safe in Content-Disposition and logs.
func SanitizeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 255 {
		name = string(r[len(r)-255:])
	}
	return name
}
