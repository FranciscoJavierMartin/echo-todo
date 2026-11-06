package server

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
)

// Under sustained concurrent load, SIGTERM must not break a single in-flight
// request: every request either completes with 200 or is refused at connect
// time because the listener is already closed.
func TestGracefulShutdownUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	url, logs, cancel, done := startServer(t, 5*time.Second, func(c *echo.Context) error {
		time.Sleep(time.Duration(20+time.Now().UnixNano()%80) * time.Millisecond)
		return c.String(http.StatusOK, "done")
	})

	var completed, refused atomic.Int64
	var mu sync.Mutex
	var failures []string
	stop := make(chan struct{})
	var wg sync.WaitGroup
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}
	for range 32 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := client.Get(url)
				if err != nil {
					if errors.Is(err, syscall.ECONNREFUSED) {
						refused.Add(1)
						return
					}
					mu.Lock()
					failures = append(failures, err.Error())
					mu.Unlock()
					return
				}
				body, rerr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if rerr != nil || resp.StatusCode != http.StatusOK || string(body) != "done" {
					mu.Lock()
					failures = append(failures, resp.Status+" "+string(body))
					mu.Unlock()
					return
				}
				completed.Add(1)
			}
		})
	}

	time.Sleep(400 * time.Millisecond) // steady state
	cancel()                           // SIGTERM
	require.NoError(t, <-done, "server exits cleanly")
	close(stop)
	wg.Wait()

	require.Empty(t, failures, "requests broken by shutdown")
	require.Greater(t, completed.Load(), int64(100))
	require.NotContains(t, logs.String(), "graceful shutdown did not complete")
	t.Logf("completed=%d refused-after-close=%d", completed.Load(), refused.Load())
}
