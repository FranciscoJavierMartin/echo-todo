package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
)

// StrictJSONSerializer rejects unknown fields and trailing data in request bodies.
// Typos like "titel" become a 422 instead of being silently dropped.
type StrictJSONSerializer struct {
	echo.DefaultJSONSerializer
}

func (StrictJSONSerializer) Deserialize(c *echo.Context, target any) error {
	dec := json.NewDecoder(c.Request().Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON value")
	}
	return nil
}

// BindJSON binds the request body only and maps failures to the error taxonomy:
// 413/415 keep their status, malformed or mistyped JSON becomes a 422.
func BindJSON(c *echo.Context, target any) error {
	err := echo.BindBody(c, target)
	if err == nil {
		return nil
	}
	switch code := echo.StatusCode(err); code {
	case http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return err
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return echo.ErrStatusRequestEntityTooLarge.Wrap(err)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return apperr.Invalid(typeErr.Field, "must be of type "+jsonType(typeErr.Type.Kind().String())).Wrap(err)
	}
	var synErr *json.SyntaxError
	if errors.As(err, &synErr) {
		return apperr.Invalid("body", fmt.Sprintf("malformed JSON at offset %d", synErr.Offset)).Wrap(err)
	}
	inner := err
	for u := errors.Unwrap(inner); u != nil; u = errors.Unwrap(inner) {
		inner = u
	}
	msg := inner.Error()
	if field, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		return apperr.Invalid(strings.Trim(field, `"`), "unknown field").Wrap(err)
	}
	return apperr.Invalid("body", "malformed JSON").Wrap(err)
}

func jsonType(kind string) string {
	switch kind {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return "number"
	case "bool":
		return "boolean"
	case "slice", "array":
		return "array"
	case "struct", "map", "ptr":
		return "object"
	default:
		return kind
	}
}

// QueryParamOr is echo.QueryParamOr with parse failures mapped to 422 for that field.
func QueryParamOr[T any](c *echo.Context, key string, def T) (T, error) {
	v, err := echo.QueryParamOr[T](c, key, def)
	if err != nil {
		return v, apperr.Invalid(key, "has an invalid value").Wrap(err)
	}
	return v, nil
}

// QueryParams parses repeated and comma-separated values (?p=1,2&p=3) into []T.
func QueryParams[T any](c *echo.Context, key string) ([]T, error) {
	raw, ok := c.QueryParams()[key]
	if !ok {
		return nil, nil
	}
	var parts []string
	for _, r := range raw {
		for p := range strings.SplitSeq(r, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
	}
	vals, err := echo.ParseValues[T](parts)
	if err != nil {
		return nil, apperr.Invalid(key, "has an invalid value").Wrap(err)
	}
	return vals, nil
}

// PeekBody reads the full body and restores it so it can be read again.
func PeekBody(c *echo.Context) ([]byte, error) {
	req := c.Request()
	if req.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}
