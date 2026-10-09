//go:build !embed

package pdfformatter

import "embed"

// Embedded is false in regular builds: the UI and fonts are read from disk.
const Embedded = false

var (
	Web   embed.FS
	Fonts []embed.FS
)
