package httpx_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/httpx"
)

type sample struct {
	Name  string   `json:"name" validate:"notblank,min=2,max=5"`
	Email string   `json:"email" validate:"omitempty,email"`
	Kind  string   `query:"kind" validate:"omitempty,oneof=a b"`
	Tags  []string `json:"tags" validate:"max=1"`
	N     int      `json:"n" validate:"gte=0,lte=3"`
	ID    string   `json:"id" validate:"omitempty,uuid"`
	Inner struct {
		X int `json:"x" validate:"min=1"`
	} `json:"inner"`
}

func TestValidatorMessages(t *testing.T) {
	v := httpx.NewValidator()
	err := v.Validate(&sample{Name: "abcdef", Email: "nope", Kind: "z", Tags: []string{"a", "b"}, N: 9, ID: "x"})
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apperr.KindInvalid, ae.Kind)
	require.Equal(t, map[string]string{
		"name":    "must be at most 5 characters",
		"email":   "must be a valid email address",
		"kind":    "must be one of: a, b",
		"tags":    "must contain at most 1 items",
		"n":       "must be less than or equal to 3",
		"id":      "must be a valid UUID",
		"inner.x": "must be at least 1",
	}, ae.Fields)

	require.NoError(t, v.Validate(&sample{Name: "ab", Inner: struct {
		X int `json:"x" validate:"min=1"`
	}{X: 1}}))

	err = v.Validate("not a struct")
	require.True(t, apperr.Is(err, apperr.KindInternal))
}
