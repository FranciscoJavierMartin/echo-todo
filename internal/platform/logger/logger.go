// Package logger builds the process-wide slog logger and request-scoped attributes.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"

	"echo-todo-2/internal/config"
)

// New returns the root logger: JSON in prod, text elsewhere.
func New(cfg config.Config) *slog.Logger {
	return NewWithWriter(cfg.Env, os.Stdout)
}

func NewWithWriter(env string, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var h slog.Handler
	if env == "prod" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		opts.Level = slog.LevelDebug
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(&contextHandler{Handler: h}).With("service", "todo", "env", env)
}

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyUserID
)

// WithRequestID stores the request ID in ctx so every log record emitted with
// that context (e.g. from services) carries it.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyUserID, id)
}

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(keyRequestID).(string)
	return v
}

func UserID(ctx context.Context) string {
	v, _ := ctx.Value(keyUserID).(string)
	return v
}

// contextHandler enriches records with request-scoped values found in the context.
type contextHandler struct {
	slog.Handler
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		if id := RequestID(ctx); id != "" {
			r.AddAttrs(slog.String("request_id", id))
		}
		if id := UserID(ctx); id != "" {
			r.AddAttrs(slog.String("user_id", id))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}
