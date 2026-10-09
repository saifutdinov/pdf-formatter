package font

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"pdf-formatter/pdf/cos"
)

// A CFF font is subset like a TrueType one: the used glyphs keep their
// outlines and ids, the others are empty, the file is much smaller and
// still reads back as text (macOS ships STIX as OpenType/CFF).
func TestSubsetCFF(t *testing.T) {
	data, err := os.ReadFile("/System/Library/Fonts/Supplemental/STIXGeneral.otf")
	if err != nil {
		t.Skip(err)
	}
	faces, err := ParseFaces(data)
	if err != nil || !faces[0].CFF {
		t.Fatalf("not a CFF font: %v", err)
	}
	face := faces[0]
	text := "Привет, world"
	used := map[uint16]bool{}
	for _, r := range text {
		used[face.Glyph(r)] = true
	}
	sub, err := face.Subset(used)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub)*3 > len(face.Data) {
		t.Errorf("subset %d bytes of %d", len(sub), len(face.Data))
	}
	orig, _ := sfnt.Parse(face.Data)
	f, err := sfnt.Parse(sub)
	if err != nil {
		t.Fatal(err)
	}
	if f.NumGlyphs() != orig.NumGlyphs() {
		t.Errorf("glyphs %d, was %d", f.NumGlyphs(), orig.NumGlyphs())
	}
	var buf sfnt.Buffer
	outline := func(g uint16) int {
		segs, err := f.LoadGlyph(&buf, sfnt.GlyphIndex(g), fixed.I(1000), nil)
		if err != nil {
			t.Fatalf("glyph %d: %v", g, err)
		}
		return len(segs)
	}
	if outline(face.Glyph('П')) == 0 || outline(face.Glyph('w')) == 0 {
		t.Error("used glyphs lost their outlines")
	}
	if outline(face.Glyph('Z')) != 0 {
		t.Error("an unused glyph kept its outline")
	}

	// Embedded in a page, the text reads back.
	d, _ := cos.Open([]byte(blank))
	u := cos.NewUpdate(d)
	e := NewEmbedding(face)
	shown := e.Encode(text)
	fontRef := e.Write(u)
	page := cos.Copy(d.Get(cos.Ref{Num: 3})).(cos.Dict)
	page["Resources"] = cos.Dict{"Font": cos.Dict{"F1": fontRef}}
	page["Contents"] = u.Add(&cos.Stream{Dict: cos.Dict{}, Data: append(append([]byte("BT /F1 24 Tf 72 700 Td "), cos.AppendObject(nil, shown)...), " Tj ET"...)})
	u.Set(cos.Ref{Num: 3}, page)
	var out bytes.Buffer
	u.WriteClean(&out)
	d2, _ := cos.Open(out.Bytes())
	lf := Load(d2, d2.Pages()[0].Resources["Font"].(cos.Dict)["F1"])
	var got strings.Builder
	for _, g := range lf.Glyphs(string(shown)) {
		got.WriteString(g.Text)
	}
	if got.String() != text || lf.Status != Subset {
		t.Errorf("read back %q (%v)", got.String(), lf.Status)
	}
	if pdftotext, err := exec.LookPath("pdftotext"); err == nil {
		p := filepath.Join(t.TempDir(), "cff.pdf")
		os.WriteFile(p, out.Bytes(), 0o644)
		res, err := exec.Command(pdftotext, p, "-").CombinedOutput()
		if err != nil || !strings.Contains(string(res), text) {
			t.Errorf("pdftotext: %v %q", err, res)
		}
	}
}
