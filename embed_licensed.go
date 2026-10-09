//go:build embed && licensed

package pdfformatter

import "embed"

// Commercial fonts from fonts/licensed, which is not in the repository: a
// build with them is only for the holder of their licenses.
//
//go:embed fonts/licensed
var licensedFonts embed.FS

func init() { Fonts = append(Fonts, licensedFonts) }
