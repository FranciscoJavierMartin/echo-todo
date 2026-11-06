package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
)

func TestStatusCodes(t *testing.T) {
	cases := map[Kind]int{
		KindInternal:             500,
		KindInvalid:              422,
		KindNotFound:             404,
		KindConflict:             409,
		KindUnauthenticated:      401,
		KindForbidden:            403,
		KindRateLimited:          429,
		KindPreconditionRequired: 428,
		KindTooLarge:             413,
	}
	for k, code := range cases {
		e := New(k, "m")
		require.Equal(t, code, e.StatusCode(), k.String())
		require.Equal(t, code, echo.StatusCode(fmt.Errorf("wrapped: %w", e)))
		require.Equal(t, code, e.ProblemError().Status)
		require.Equal(t, http.StatusText(code), e.ProblemError().Title)
		require.Equal(t, k.String(), e.MachineCode())
	}
}

func TestWrapAndCopySemantics(t *testing.T) {
	cause := errors.New("db down")
	base := NotFound("todo not found")
	wrapped := base.Wrap(cause).WithOp("op").WithCode("todo_missing").WithExtension("k", 1)

	require.Nil(t, base.Err, "Wrap must not mutate the receiver")
	require.Nil(t, base.Extensions)
	require.ErrorIs(t, wrapped, cause)
	require.Equal(t, "op: todo not found: db down", wrapped.Error())
	require.Equal(t, "todo_missing", wrapped.MachineCode())
	require.Equal(t, 1, wrapped.Extensions["k"])
	require.True(t, Is(wrapped, KindNotFound))
	require.Equal(t, KindNotFound, KindOf(fmt.Errorf("x: %w", wrapped)))
	require.Equal(t, KindInternal, KindOf(cause))
}

func TestConstructors(t *testing.T) {
	e := Invalid("title", "required")
	require.Equal(t, map[string]string{"title": "required"}, e.Fields)
	e = InvalidFields(map[string]string{"a": "b"})
	require.Equal(t, "validation failed", e.Message)
	in := Internal("op", errors.New("secret sql"))
	require.Equal(t, "internal error", in.Message)
	require.NotContains(t, in.ProblemError().Detail, "secret")
	for _, c := range []*Error{Conflict("c"), Unauthenticated("u"), Forbidden("f"), RateLimited("r")} {
		require.NotEmpty(t, c.Message)
	}
}
