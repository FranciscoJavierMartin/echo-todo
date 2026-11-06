// Package web embeds the built single-page app.
package web

import "embed"

// FS holds dist/ (index.html and hashed assets). all: includes dotfiles.
//
//go:embed all:dist
var FS embed.FS
