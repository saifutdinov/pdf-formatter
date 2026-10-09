//go:build embed

// Package pdfformatter carries the web UI and builtin fonts inside the
// binary for the standalone build: go build -tags embed ./cmd/server
package pdfformatter

import "embed"

const Embedded = true

//go:embed all:web/dist
var Web embed.FS

// Fonts are the bundled font folders, paths starting with fonts/: the free
// fonts always, the commercial ones only with the licensed tag
// (embed_licensed.go).
var Fonts = []embed.FS{freeFonts}

//go:embed fonts/free
var freeFonts embed.FS
