package layout

import (
	"testing"

	"pdf-formatter/pdf/content"
)

// go test ./pdf/layout -bench Big -benchtime 1x
func BenchmarkBig(b *testing.B) {
	t := &testing.T{}
	d := open(t, "big.pdf")
	if d == nil {
		b.Skip()
	}
	for b.Loop() {
		fc := content.NewFontCache()
		for _, p := range d.Pages() {
			Analyze(d, p, fc)
		}
	}
}
