package todo

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SortField is an allowlisted sort column. Storage maps each value to a fixed SQL
// expression; client input is never interpolated into SQL.
type SortField string

const (
	SortPriority  SortField = "priority"
	SortDueAt     SortField = "due_at"
	SortCreatedAt SortField = "created_at"
	SortUpdatedAt SortField = "updated_at"
	SortTitle     SortField = "title"
)

var sortFields = map[SortField]bool{SortPriority: true, SortDueAt: true, SortCreatedAt: true, SortUpdatedAt: true, SortTitle: true}

type SortKey struct {
	Field SortField
	Desc  bool
}

// DefaultSort is newest first.
var DefaultSort = []SortKey{{Field: SortCreatedAt, Desc: true}}

var ErrInvalidSort = errors.New("invalid sort")
var ErrInvalidCursor = errors.New("invalid cursor")

// ParseSort parses "-priority,due_at". The id tiebreaker is implicit.
func ParseSort(s string) ([]SortKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultSort, nil
	}
	var keys []SortKey
	seen := map[SortField]bool{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		k := SortKey{}
		if rest, ok := strings.CutPrefix(part, "-"); ok {
			k.Desc, part = true, rest
		} else {
			part = strings.TrimPrefix(part, "+")
		}
		k.Field = SortField(part)
		if !sortFields[k.Field] || seen[k.Field] {
			return nil, fmt.Errorf("%w: %q", ErrInvalidSort, part)
		}
		seen[k.Field] = true
		keys = append(keys, k)
	}
	if len(keys) > 3 {
		return nil, fmt.Errorf("%w: at most 3 sort keys", ErrInvalidSort)
	}
	return keys, nil
}

func sortSignature(keys []SortKey) string {
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		if k.Desc {
			b.WriteByte('-')
		}
		b.WriteString(string(k.Field))
	}
	return b.String()
}

// Cursor is the keyset position: the sort values and id of the last row returned.
// A nil time value means NULL due_at, which sorts as +infinity (last ascending).
type Cursor struct {
	Values []any
	ID     uuid.UUID
}

type wireCursor struct {
	Sort   string            `json:"s"`
	Values []json.RawMessage `json:"v"`
	ID     uuid.UUID         `json:"id"`
}

// CursorAfter builds the cursor positioned after t.
func CursorAfter(keys []SortKey, t *Todo) Cursor {
	c := Cursor{ID: t.ID}
	for _, k := range keys {
		c.Values = append(c.Values, sortValue(t, k.Field))
	}
	return c
}

// EncodeCursor serialises a cursor bound to its sort, as opaque base64url.
func EncodeCursor(keys []SortKey, c Cursor) string {
	w := wireCursor{Sort: sortSignature(keys), ID: c.ID}
	for _, v := range c.Values {
		raw, _ := json.Marshal(v)
		w.Values = append(w.Values, raw)
	}
	b, _ := json.Marshal(w)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses s and checks it was produced for the same sort.
func DecodeCursor(keys []SortKey, s string) (*Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var w wireCursor
	if err := json.Unmarshal(b, &w); err != nil || w.ID == uuid.Nil {
		return nil, ErrInvalidCursor
	}
	if w.Sort != sortSignature(keys) || len(w.Values) != len(keys) {
		return nil, fmt.Errorf("%w: cursor belongs to a different sort", ErrInvalidCursor)
	}
	c := &Cursor{ID: w.ID}
	for i, k := range keys {
		v, err := decodeValue(k.Field, w.Values[i])
		if err != nil {
			return nil, ErrInvalidCursor
		}
		c.Values = append(c.Values, v)
	}
	return c, nil
}

func decodeValue(f SortField, raw json.RawMessage) (any, error) {
	switch f {
	case SortPriority:
		var n int
		err := json.Unmarshal(raw, &n)
		return n, err
	case SortTitle:
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	case SortDueAt:
		var t *time.Time
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		if t == nil {
			return (*time.Time)(nil), nil
		}
		return t, nil
	default:
		var t time.Time
		err := json.Unmarshal(raw, &t)
		return t, err
	}
}

func sortValue(t *Todo, f SortField) any {
	switch f {
	case SortPriority:
		return t.Priority
	case SortDueAt:
		return t.DueAt
	case SortUpdatedAt:
		return t.UpdatedAt
	case SortTitle:
		return t.Title
	default:
		return t.CreatedAt
	}
}

// Compare orders a and b by keys then id (id follows the last key's direction).
// It defines the semantics storage implementations must reproduce.
func Compare(keys []SortKey, a, b *Todo) int {
	return compareTuple(keys, CursorAfter(keys, a), CursorAfter(keys, b))
}

// IsAfter reports whether t sorts strictly after the cursor position.
func IsAfter(keys []SortKey, t *Todo, c Cursor) bool {
	return compareTuple(keys, CursorAfter(keys, t), c) > 0
}

func compareTuple(keys []SortKey, a, b Cursor) int {
	for i, k := range keys {
		r := compareValue(a.Values[i], b.Values[i])
		if k.Desc {
			r = -r
		}
		if r != 0 {
			return r
		}
	}
	r := strings.Compare(a.ID.String(), b.ID.String())
	if len(keys) > 0 && keys[len(keys)-1].Desc {
		r = -r
	}
	return r
}

func compareValue(a, b any) int {
	switch av := a.(type) {
	case int:
		return cmp.Compare(av, b.(int))
	case string:
		return strings.Compare(av, b.(string))
	case time.Time:
		return av.Compare(b.(time.Time))
	case *time.Time:
		bv := b.(*time.Time)
		switch {
		case av == nil && bv == nil:
			return 0
		case av == nil:
			return 1 // NULL = +infinity
		case bv == nil:
			return -1
		}
		return av.Compare(*bv)
	}
	return 0
}
