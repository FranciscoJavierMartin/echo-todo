// Package migrations embeds the goose SQL migrations so the binary, tests and
// the compose one-shot service all apply exactly the same files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
