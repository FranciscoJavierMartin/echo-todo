package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/logger"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startServer(t *testing.T, timeout time.Duration, h echo.HandlerFunc) (url string, logs *syncBuffer, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	logs = &syncBuffer{}
	log := logger.NewWithWriter("test", logs)
	e := echo.NewWithConfig(echo.Config{Logger: log})
	e.GET("/work", h)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	sc := StartConfig(config.Config{Env: "test", ShutdownTimeout: timeout}, log)
	sc.Listener = ln
	sc.HideBanner = true

	ctx, cancelFn := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- sc.Start(ctx, e) }()
	return "http://" + ln.Addr().String() + "/work", logs, cancelFn, errCh
}

func TestGracefulShutdown_DrainsSlowRequest(t *testing.T) {
	started := make(chan struct{})
	url, logs, cancel, done := startServer(t, 2*time.Second, func(c *echo.Context) error {
		close(started)
		time.Sleep(300 * time.Millisecond)
		return c.String(http.StatusOK, "finished")
	})

	type result struct {
		code int
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{code: resp.StatusCode, body: string(b)}
	}()

	<-started
	cancel() // simulate SIGTERM while the request is in flight

	res := <-resCh
	require.NoError(t, res.err)
	require.Equal(t, http.StatusOK, res.code)
	require.Equal(t, "finished", res.body)
	require.NoError(t, <-done)
	require.Contains(t, logs.String(), "draining in-flight requests")
	require.NotContains(t, logs.String(), "graceful shutdown did not complete")
}

func TestGracefulShutdown_HungRequestIsReported(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	url, logs, cancel, done := startServer(t, 150*time.Millisecond, func(c *echo.Context) error {
		close(started)
		<-release
		return c.NoContent(http.StatusOK)
	})

	go func() {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after graceful timeout")
	}
	require.Contains(t, logs.String(), "graceful shutdown did not complete")
}
