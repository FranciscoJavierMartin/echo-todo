package httpx

import (
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
)

// ETag renders a strong entity tag for a resource version: "3".
func ETag(version int) string {
	return `"` + strconv.Itoa(version) + `"`
}

// SetETag writes the ETag header.
func SetETag(c *echo.Context, version int) {
	c.Response().Header().Set("ETag", ETag(version))
}

// RequireIfMatch returns the version the client expects. "*" yields 0 ("any
// version"). A missing header is 428 Precondition Required, so clients cannot
// silently overwrite concurrent changes; an unparsable one is 422.
func RequireIfMatch(c *echo.Context) (int, error) {
	raw := strings.TrimSpace(c.Request().Header.Get("If-Match"))
	if raw == "" {
		return 0, apperr.New(apperr.KindPreconditionRequired, "If-Match header with the current ETag is required").
			WithCode("precondition_required")
	}
	if raw == "*" {
		return 0, nil
	}
	v, ok := parseETag(raw)
	if !ok {
		return 0, apperr.Invalid("If-Match", "must be an ETag such as \"3\"")
	}
	return v, nil
}

// MatchesIfNoneMatch reports whether If-None-Match matches the given version.
func MatchesIfNoneMatch(c *echo.Context, version int) bool {
	raw := c.Request().Header.Get("If-None-Match")
	if raw == "" {
		return false
	}
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			return true
		}
		// Weak comparison is the rule for If-None-Match.
		if v, ok := parseETag(strings.TrimPrefix(part, "W/")); ok && v == version {
			return true
		}
	}
	return false
}

func parseETag(s string) (int, bool) {
	if len(s) < 3 || s[0] != '"' || s[len(s)-1] != '"' {
		return 0, false
	}
	v, err := strconv.Atoi(s[1 : len(s)-1])
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}
