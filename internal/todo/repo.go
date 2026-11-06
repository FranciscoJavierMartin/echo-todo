package todo

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Storage sentinels. Repositories translate driver errors into these; the service
// translates these into the apperr taxonomy.
var (
	ErrNotFound        = errors.New("todo not found")
	ErrVersionConflict = errors.New("version conflict")
)

type Repo interface {
	Create(ctx context.Context, t *Todo) error
	Get(ctx context.Context, ownerID, id uuid.UUID) (*Todo, error)
	List(ctx context.Context, f Filter) ([]Todo, int, error)
	Update(ctx context.Context, t *Todo, expectedVersion int) (*Todo, error)
	SoftDelete(ctx context.Context, ownerID, id uuid.UUID) error
}
