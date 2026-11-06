// Package todo contains the TODO domain: model, repository contract and service.
// It must not depend on pgx, database/sql or Echo.
package todo

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusOpen     Status = "open"
	StatusDone     Status = "done"
	StatusArchived Status = "archived"
)

func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusDone, StatusArchived:
		return true
	}
	return false
}

type Todo struct {
	ID        uuid.UUID
	OwnerID   uuid.UUID
	Title     string
	Notes     string
	Status    Status
	DueAt     *time.Time
	Priority  int
	Version   int
	DeletedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Filter describes a list query. OwnerID is mandatory: every query is scoped to its owner.
type Filter struct {
	OwnerID  uuid.UUID
	Statuses []Status
	Limit    int
	Offset   int
}
