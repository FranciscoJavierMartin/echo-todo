package app_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	todohandler "echo-todo-2/internal/todo/handler"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return buf.Bytes()
}

func upload(t *testing.T, env *apptest.Env, token, todoID, filename string, content io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, content)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req := httptest.NewRequest(http.MethodPost, "/v1/todos/"+todoID+"/attachments", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.Echo.ServeHTTP(rec, req)
	_, _ = io.Copy(io.Discard, pr)
	return rec
}

func TestAttachmentLifecycle(t *testing.T) {
	env := apptest.New(t)
	u, other := env.CreateUser("user"), env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"with files"}`)
	img := pngBytes(t)

	rec := upload(t, env, u.Token, td.ID, `..\..\etc/"pic".png`, bytes.NewReader(img))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	att := apptest.JSON[todohandler.AttachmentResponse](t, rec)
	require.Equal(t, "pic.png", att.Filename, "path and quote characters stripped")
	require.Equal(t, "image/png", att.ContentType)
	require.Equal(t, int64(len(img)), att.Size)
	require.Len(t, att.SHA256, 64)

	base := "/v1/todos/" + td.ID + "/attachments"
	list := env.Do(apptest.Request{Method: http.MethodGet, Path: base, Token: u.Token})
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), att.ID)

	dl := env.Do(apptest.Request{Method: http.MethodGet, Path: base + "/" + att.ID, Token: u.Token})
	require.Equal(t, http.StatusOK, dl.Code)
	require.Equal(t, `attachment; filename="pic.png"`, dl.Header().Get("Content-Disposition"))
	require.Equal(t, "image/png", dl.Header().Get("Content-Type"))
	require.Equal(t, img, dl.Body.Bytes())

	inline := env.Do(apptest.Request{Method: http.MethodGet, Path: base + "/" + att.ID + "?disposition=inline", Token: u.Token})
	require.Equal(t, `inline; filename="pic.png"`, inline.Header().Get("Content-Disposition"))

	// Ownership: another user sees nothing.
	require.Equal(t, http.StatusNotFound, env.Do(apptest.Request{Method: http.MethodGet, Path: base + "/" + att.ID, Token: other.Token}).Code)
	require.Equal(t, http.StatusNotFound, upload(t, env, other.Token, td.ID, "x.png", bytes.NewReader(img)).Code)

	require.Equal(t, http.StatusNoContent, env.Do(apptest.Request{Method: http.MethodDelete, Path: base + "/" + att.ID, Token: u.Token}).Code)
	require.Equal(t, http.StatusNotFound, env.Do(apptest.Request{Method: http.MethodGet, Path: base + "/" + att.ID, Token: u.Token}).Code)
}

func TestUploadRejectsDisguisedExecutable(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"x"}`)
	exe := append([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff"), bytes.Repeat([]byte{0}, 600)...)

	rec := upload(t, env, u.Token, td.ID, "holiday.png", bytes.NewReader(exe))
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"content_mismatch"`)

	rec = upload(t, env, u.Token, td.ID, "tool.exe", bytes.NewReader(exe))
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"file_type_not_allowed"`)
}

func TestUploadRejectsOversizedFiles(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"x"}`)
	twentyMB := io.MultiReader(bytes.NewReader(pngBytes(t)), io.LimitReader(zeroReader{}, 20<<20))

	// Chunked (no Content-Length): the streaming limit trips mid-body.
	rec := upload(t, env, u.Token, td.ID, "big.png", twentyMB)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())

	// With Content-Length: rejected before the body is read.
	req := httptest.NewRequest(http.MethodPost, "/v1/todos/"+td.ID+"/attachments", bufio.NewReader(io.LimitReader(zeroReader{}, 20<<20)))
	req.ContentLength = 20 << 20
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.Header.Set("Authorization", "Bearer "+u.Token)
	rec = httptest.NewRecorder()
	env.Echo.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	list := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/" + td.ID + "/attachments", Token: u.Token})
	require.JSONEq(t, `{"items":[]}`, list.Body.String(), "nothing persisted")
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestExportStreams(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	for _, title := range []string{"alpha", "=cmd|' /C calc'!A0", "gamma"} {
		createTodo(t, env, u.Token, `{"title":`+jsonString(title)+`}`)
	}
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/export/todos.csv", Token: u.Token})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="todos.csv"`, rec.Header().Get("Content-Disposition"))
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	require.Len(t, lines, 4)
	require.Contains(t, rec.Body.String(), `'=cmd`, "formula injection defused")

	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/export/todos.ndjson", Token: u.Token})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, strings.Split(strings.TrimSpace(rec.Body.String()), "\n"), 3)

	require.Equal(t, http.StatusNotFound, env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/export/todos.xml", Token: u.Token}).Code)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSPA(t *testing.T) {
	env := apptest.New(t)
	for _, p := range []string{"/app", "/app/todos/123", "/app/settings/profile"} {
		rec := env.Do(apptest.Request{Method: http.MethodGet, Path: p})
		require.Equal(t, http.StatusOK, rec.Code, p)
		require.Contains(t, rec.Body.String(), `<main id="app">`, p)
		require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), p)
	}
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/app/assets/app.3f9c1e.js"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Header().Get("Content-Type"), "javascript")

	require.Equal(t, http.StatusNotFound, env.Do(apptest.Request{Method: http.MethodGet, Path: "/app/assets/missing.js"}).Code)
	require.Equal(t, http.StatusFound, env.Do(apptest.Request{Method: http.MethodGet, Path: "/"}).Code)

	// API paths keep JSON semantics.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/nope"})
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
}
