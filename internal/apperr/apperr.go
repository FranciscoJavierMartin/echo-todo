// Package apperr is the domain error taxonomy shared by services and the HTTP layer.
//
// Services return *Error; the central HTTP error handler renders it as RFC 9457
// problem+json. Message, Code and Fields are safe to show to clients; Err never is.
package apperr

import (
	"context"
	"errors"
	"maps"
	"net/http"

	"github.com/labstack/echo/v5"
)

type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindUnauthenticated
	KindForbidden
	KindRateLimited
	KindPreconditionRequired
	KindTooLarge
	KindUnavailable
	KindUnsupportedMedia
)

func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindForbidden:
		return "forbidden"
	case KindRateLimited:
		return "rate_limited"
	case KindPreconditionRequired:
		return "precondition_required"
	case KindTooLarge:
		return "too_large"
	case KindUnavailable:
		return "unavailable"
	case KindUnsupportedMedia:
		return "unsupported_media_type"
	default:
		return "internal"
	}
}

type Error struct {
	Kind       Kind
	Op         string            // operation, e.g. "todo.Service.Create"; logged, not shown
	Message    string            // safe to show a user
	Code       string            // machine-readable code; defaults to Kind.String()
	Fields     map[string]string // per-field validation detail
	Extensions map[string]any    // extra problem members (e.g. "current" representation on 409)
	Err        error             // wrapped cause, never shown
}

func (e *Error) Error() string {
	msg := e.Message
	if e.Op != "" {
		msg = e.Op + ": " + msg
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) StatusCode() int {
	switch e.Kind {
	case KindInvalid:
		return http.StatusUnprocessableEntity
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindUnauthenticated:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindRateLimited:
		return http.StatusTooManyRequests
	case KindPreconditionRequired:
		return http.StatusPreconditionRequired
	case KindTooLarge:
		return http.StatusRequestEntityTooLarge
	case KindUnavailable:
		return http.StatusServiceUnavailable
	case KindUnsupportedMedia:
		return http.StatusUnsupportedMediaType
	default:
		return http.StatusInternalServerError
	}
}

// ProblemError implements echo.ProblemErrorer (RFC 9457).
func (e *Error) ProblemError() *echo.ProblemError {
	return &echo.ProblemError{
		Title:  http.StatusText(e.StatusCode()),
		Status: e.StatusCode(),
		Detail: e.Message,
	}
}

// MachineCode returns Code or the default code for the Kind.
func (e *Error) MachineCode() string {
	if e.Code != "" {
		return e.Code
	}
	return e.Kind.String()
}

func (e *Error) clone() *Error {
	c := *e
	c.Fields = maps.Clone(e.Fields)
	c.Extensions = maps.Clone(e.Extensions)
	return &c
}

// Wrap returns a copy carrying cause as the wrapped error.
func (e *Error) Wrap(cause error) *Error {
	c := e.clone()
	c.Err = cause
	return c
}

// WithOp returns a copy with the operation name set.
func (e *Error) WithOp(op string) *Error {
	c := e.clone()
	c.Op = op
	return c
}

// WithCode returns a copy with a machine-readable code.
func (e *Error) WithCode(code string) *Error {
	c := e.clone()
	c.Code = code
	return c
}

// WithExtension returns a copy with an extra problem member.
func (e *Error) WithExtension(key string, v any) *Error {
	c := e.clone()
	if c.Extensions == nil {
		c.Extensions = map[string]any{}
	}
	c.Extensions[key] = v
	return c
}

func New(kind Kind, msg string) *Error { return &Error{Kind: kind, Message: msg} }

// Invalid reports a single invalid field.
func Invalid(field, msg string) *Error {
	return &Error{Kind: KindInvalid, Message: msg, Fields: map[string]string{field: msg}}
}

// InvalidFields reports several invalid fields at once.
func InvalidFields(fields map[string]string) *Error {
	return &Error{Kind: KindInvalid, Message: "validation failed", Fields: fields}
}

func NotFound(msg string) *Error        { return New(KindNotFound, msg) }
func Conflict(msg string) *Error        { return New(KindConflict, msg) }
func Unauthenticated(msg string) *Error { return New(KindUnauthenticated, msg) }
func Forbidden(msg string) *Error       { return New(KindForbidden, msg) }
func RateLimited(msg string) *Error     { return New(KindRateLimited, msg) }

// Internal hides cause behind a generic message. Deadline overruns (request or
// query timeouts) are reported as 503 "timeout" instead, since retrying may work.
func Internal(op string, cause error) *Error {
	if errors.Is(cause, context.DeadlineExceeded) {
		return &Error{Kind: KindUnavailable, Op: op, Message: "request timed out", Code: "timeout", Err: cause}
	}
	return &Error{Kind: KindInternal, Op: op, Message: "internal error", Err: cause}
}

// KindOf returns the Kind of the first *Error in err's chain, or KindInternal.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// Is reports whether err carries an *Error of the given kind.
func Is(err error, kind Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == kind
}
