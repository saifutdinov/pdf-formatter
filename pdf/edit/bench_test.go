package edit

import (
	"testing"

	"pdf-formatter/pdf/content"
)

// go test ./pdf/edit -run X -bench BigDoc -benchtime 3x
func BenchmarkBigDoc(b *testing.B) {
	t := &testing.T{}
	d := open(t, "big.pdf")
	if d == nil {
		b.Skip()
	}
	env := testEnv(t)
	pages := d.Pages()
	ops := []Op{
		{Op: "hf", HF: []HFItem{{Text: "Стр. {page} из {pages}", Position: "bottom-center", Size: 9, Offset: 20, Side: 40}}},
		{Op: "transform", Page: pages[300].Ref.Num, Blocks: []string{"001058"}, Matrix: &[6]float64{1, 0, 0, 1, 0, -100}},
		{Op: "add", Page: pages[10].Ref.Num, Object: &Object{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 50, 100}, W: 300, H: 30, Text: "Заметка", Size: 12, Opacity: 1}},
	}
	env.Fonts = content.NewFontCache()
	for b.Loop() {
		u, err := Apply(d, ops, env)
		if err != nil {
			b.Fatal(err)
		}
		u.Tail()
	}
}
