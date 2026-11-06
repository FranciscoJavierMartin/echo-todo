package apptest

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	apispec "echo-todo-2/api"
)

var (
	specOnce   sync.Once
	specDoc    *openapi3.T
	specRouter routers.Router
	specErr    error
)

// Spec returns the parsed, validated OpenAPI document and a router over it.
func Spec(t testing.TB) (*openapi3.T, routers.Router) {
	t.Helper()
	specOnce.Do(func() {
		loader := openapi3.NewLoader()
		specDoc, specErr = loader.LoadFromData(apispec.Spec)
		if specErr != nil {
			return
		}
		if specErr = specDoc.Validate(context.Background()); specErr != nil {
			return
		}
		// httptest requests target example.com.
		specDoc.Servers = openapi3.Servers{{URL: "http://example.com"}}
		specRouter, specErr = gorillamux.NewRouter(specDoc)
	})
	if specErr != nil {
		t.Fatalf("openapi spec: %v", specErr)
	}
	return specDoc, specRouter
}

// validateResponse fails the test when a response for a documented operation
// uses an undocumented status code or violates the response schema. Requests are
// not validated: tests send malformed input on purpose.
func validateResponse(t testing.TB, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	_, router := Spec(t)
	route, pathParams, err := router.FindRoute(req)
	if err != nil {
		return // unknown path or method: not an operation, nothing to check
	}
	ct := rec.Header().Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	jsonBody := mt == "application/json" || strings.HasSuffix(mt, "+json")
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: pathParams, Route: route},
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		Options: &openapi3filter.Options{
			IncludeResponseStatus: true,
			ExcludeResponseBody:   !jsonBody,
			MultiError:            true,
		},
	}
	if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
		t.Errorf("response drifted from api/openapi.yaml: %s %s → %d (%s)\n%v\nbody: %s",
			req.Method, req.URL.Path, rec.Code, route.Operation.OperationID, err, truncate(rec.Body.String(), 500))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
