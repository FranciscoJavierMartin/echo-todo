package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/config"
)

func TestMetricsExposeRequestCounters(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	id := uuid.NewString()
	env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/" + id, Token: u.Token})
	env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Token: u.Token})
	env.Do(apptest.Request{Method: http.MethodGet, Path: "/random/" + id})

	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/metrics"})
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `todo_http_requests_total{code="404",host="",method="GET",url="/v1/todos/:id"} 1`)
	require.Contains(t, body, `todo_http_requests_total{code="200",host="",method="GET",url="/v1/todos"} 1`)
	require.Contains(t, body, "todo_http_request_duration_seconds_bucket")
	require.NotContains(t, body, id, "raw paths must never become labels")
	require.Contains(t, body, "go_goroutines")
}

func TestMetricsToken(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.MetricsToken = "s3cret" }))
	require.Equal(t, http.StatusUnauthorized, env.Do(apptest.Request{Method: http.MethodGet, Path: "/metrics"}).Code)
	require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: "/metrics", Token: "s3cret"}).Code)
}

func TestTracingHandlerAndQuerySpans(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	env := apptest.New(t, apptest.WithTracerProvider(tp))
	u := env.CreateUser("user")
	exp.Reset()

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	rec := env.Do(apptest.Request{
		Method: http.MethodPost, Path: "/v1/todos", Token: u.Token, Body: `{"title":"traced"}`,
		Headers: map[string]string{"traceparent": "00-" + traceID + "-00f067aa0ba902b7-01"},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	spans := exp.GetSpans()
	var server *tracetest.SpanStub
	var queries []tracetest.SpanStub
	for i, s := range spans {
		switch {
		case s.SpanKind == oteltrace.SpanKindServer:
			server = &spans[i]
		case s.SpanKind == oteltrace.SpanKindClient:
			queries = append(queries, s)
		}
	}
	require.NotNil(t, server, "server span")
	require.Equal(t, "POST /v1/todos", server.Name)
	require.Equal(t, traceID, server.SpanContext.TraceID().String(), "incoming traceparent is continued")
	require.NotEmpty(t, queries, "query spans")
	for _, q := range queries {
		require.Equal(t, server.SpanContext.TraceID(), q.SpanContext.TraceID())
	}
	require.True(t, hasAncestor(spans, queries[0], server.SpanContext.SpanID()), "query span is a descendant of the server span")

	var access map[string]any
	for _, l := range env.Logs.Lines() {
		if l["msg"] == "access" {
			access = l
		}
	}
	require.Equal(t, traceID, access["trace_id"])
}

func hasAncestor(spans tracetest.SpanStubs, s tracetest.SpanStub, ancestor oteltrace.SpanID) bool {
	for range 10 {
		if s.Parent.SpanID() == ancestor {
			return true
		}
		found := false
		for _, cand := range spans {
			if cand.SpanContext.SpanID() == s.Parent.SpanID() {
				s, found = cand, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return false
}

func TestRateLimitKeysOnRealClientIPBehindProxy(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) {
		c.RequestsPerSec = 1 // burst 2
		c.TrustedProxies = []string{"10.0.0.1"}
	}))
	viaProxy := func(client string) int {
		return env.Do(apptest.Request{
			Method: http.MethodGet, Path: "/v1/nothing",
			Remote: "10.0.0.1:5555", Headers: map[string]string{"X-Forwarded-For": client},
		}).Code
	}
	// Client A exhausts its bucket; client B behind the same proxy is unaffected.
	require.NotEqual(t, http.StatusTooManyRequests, viaProxy("203.0.113.7"))
	require.NotEqual(t, http.StatusTooManyRequests, viaProxy("203.0.113.7"))
	require.Equal(t, http.StatusTooManyRequests, viaProxy("203.0.113.7"))
	require.NotEqual(t, http.StatusTooManyRequests, viaProxy("198.51.100.9"))

	// An untrusted peer cannot spoof X-Forwarded-For to escape its own bucket.
	spoof := func(fake string) int {
		return env.Do(apptest.Request{
			Method: http.MethodGet, Path: "/v1/nothing",
			Remote: "192.0.2.50:4444", Headers: map[string]string{"X-Forwarded-For": fake},
		}).Code
	}
	spoof("1.1.1.1")
	spoof("2.2.2.2")
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/nothing", Remote: "192.0.2.50:4444",
		Headers: map[string]string{"X-Forwarded-For": "3.3.3.3"}})
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
	require.Contains(t, rec.Body.String(), `"code":"rate_limited"`)

	// Health checks are never rate limited.
	for range 5 {
		require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: "/healthz", Remote: "192.0.2.50:4444"}).Code)
	}
}

func TestPprofRequiresAdmin(t *testing.T) {
	env := apptest.New(t)
	require.Equal(t, http.StatusUnauthorized, env.Do(apptest.Request{Method: http.MethodGet, Path: "/debug/pprof/"}).Code)
	user := env.CreateUser("user")
	require.Equal(t, http.StatusForbidden, env.Do(apptest.Request{Method: http.MethodGet, Path: "/debug/pprof/", Token: user.Token}).Code)
	admin := env.CreateAdmin()
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/debug/pprof/cmdline", Token: admin.Token})
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.Contains(rec.Body.String(), "test") || rec.Body.Len() > 0)
}
