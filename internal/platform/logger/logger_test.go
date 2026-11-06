package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONInProdWithContextValues(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter("prod", &buf)
	ctx := WithUserID(WithRequestID(context.Background(), "rid-1"), "user-1")
	log.InfoContext(ctx, "hello", "k", "v")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	require.Equal(t, "hello", rec["msg"])
	require.Equal(t, "rid-1", rec["request_id"])
	require.Equal(t, "user-1", rec["user_id"])
	require.Equal(t, "todo", rec["service"])
	require.Equal(t, "prod", rec["env"])
}

func TestNoDuplicateRequestID(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter("dev", &buf).With("request_id", "rid-1", "user_id", "u")
	log.InfoContext(WithUserID(WithRequestID(context.Background(), "rid-1"), "u"), "x")
	require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("request_id=")))
	require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("user_id=")))
}

func TestTextInDevAndDebugEnabled(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter("dev", &buf).With("a", 1).WithGroup("g")
	log.Debug("dbg", "x", 2)
	require.Contains(t, buf.String(), "msg=dbg")
	require.Contains(t, buf.String(), "g.x=2")
}
