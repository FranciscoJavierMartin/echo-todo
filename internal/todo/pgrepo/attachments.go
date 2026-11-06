package pgrepo

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/todo"
)

var _ todo.AttachmentRepo = (*Repo)(nil)

func attachmentFromRow(a db.Attachment) todo.Attachment {
	return todo.Attachment{
		ID: a.ID, TodoID: a.TodoID, OwnerID: a.OwnerID, Filename: a.Filename, ContentType: a.ContentType,
		Size: a.SizeBytes, SHA256: a.Sha256, StorageKey: a.StorageKey, CreatedAt: a.CreatedAt,
	}
}

func (r *Repo) InsertAttachment(ctx context.Context, a *todo.Attachment) error {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	row, err := r.q(ctx).InsertAttachment(ctx, db.InsertAttachmentParams{
		TodoID: a.TodoID, OwnerID: a.OwnerID, Filename: a.Filename, ContentType: a.ContentType,
		SizeBytes: a.Size, Sha256: a.SHA256, StorageKey: a.StorageKey,
	})
	if err != nil {
		return fmt.Errorf("insert attachment: %w", err)
	}
	*a = attachmentFromRow(row)
	return nil
}

func (r *Repo) GetAttachment(ctx context.Context, ownerID, todoID, id uuid.UUID) (*todo.Attachment, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	row, err := r.q(ctx).GetAttachment(ctx, db.GetAttachmentParams{ID: id, TodoID: todoID, OwnerID: ownerID})
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, todo.ErrNotFound
		}
		return nil, fmt.Errorf("get attachment: %w", err)
	}
	a := attachmentFromRow(db.Attachment(row))
	return &a, nil
}

func (r *Repo) ListAttachments(ctx context.Context, ownerID, todoID uuid.UUID) ([]todo.Attachment, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	rows, err := r.q(ctx).ListAttachments(ctx, db.ListAttachmentsParams{TodoID: todoID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	out := make([]todo.Attachment, len(rows))
	for i, row := range rows {
		out[i] = attachmentFromRow(db.Attachment(row))
	}
	return out, nil
}

func (r *Repo) DeleteAttachment(ctx context.Context, ownerID, todoID, id uuid.UUID) (string, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	key, err := r.q(ctx).DeleteAttachment(ctx, db.DeleteAttachmentParams{ID: id, TodoID: todoID, OwnerID: ownerID})
	if err != nil {
		if postgres.IsNoRows(err) {
			return "", todo.ErrNotFound
		}
		return "", fmt.Errorf("delete attachment: %w", err)
	}
	return key, nil
}
