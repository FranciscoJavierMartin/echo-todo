// Package apispec embeds the OpenAPI document (served at /docs and used by the
// contract tests). Generate the server interface with `make openapi`.
package apispec

import _ "embed"

//go:generate go tool oapi-codegen -config oapi-codegen.yaml openapi.yaml

//go:embed openapi.yaml
var Spec []byte
