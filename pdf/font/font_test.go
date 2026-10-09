package font

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pdf-formatter/pdf/cos"

	"golang.org/x/image/font/sfnt"
)

func ptSans(t *testing.T) []*Face {
	data, err := os.ReadFile("../../fonts/free/PTSans.ttc")
	if err != nil {
		t.Skip(err)
	}
	faces, err := ParseFaces(data)
	if err != nil {
		t.Fatal(err)
	}
	return faces
}

func TestParseCollection(t *testing.T) {
	faces := ptSans(t)
	if len(faces) < 4 {
		t.Fatalf("PTSans.ttc: %d faces", len(faces))
	}
	var names []string
	for _, f := range faces {
		names = append(names, f.PSName)
		if !f.Has("Съешь же ещё этих мягких французских булок") {
			t.Errorf("%s: no Cyrillic", f.PSName)
		}
		// The extracted face must parse on its own.
		if _, err := sfnt.Parse(f.Data); err != nil {
			t.Errorf("%s: %v", f.PSName, err)
		}
	}
	if !strings.Contains(strings.Join(names, " "), "PTSans-Bold") {
		t.Errorf("faces: %v", names)
	}
	var b sfnt.Buffer
	lic, _ := faces[0].f.Name(&b, sfnt.NameIDLicense)
	t.Logf("license: %.120s", lic)
}

// minimal one-page document without xref (Open rebuilds it).
const blank = "%PDF-1.7\n1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj\n2 0 obj <</Type/Pages/Kids[3 0 R]/Count 1>> endobj\n3 0 obj <</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Resources<<>>>> endobj\ntrailer <</Root 1 0 R>>\n"

func TestEmbedRoundTrip(t *testing.T) {
	face := ptSans(t)[0]
	d, err := cos.Open([]byte(blank))
	if err != nil {
		t.Fatal(err)
	}
	u := cos.NewUpdate(d)
	e := NewEmbedding(face)
	text := "Привет, мир! Hello 123"
	shown := e.Encode(text)
	fontRef := e.Write(u)
	content := append([]byte("BT /F1 24 Tf 72 700 Td "), cos.AppendObject(nil, shown)...)
	content = append(content, " Tj ET"...)
	page := cos.Copy(d.Get(cos.Ref{Num: 3})).(cos.Dict)
	page["Resources"] = cos.Dict{"Font": cos.Dict{"F1": fontRef}}
	page["Contents"] = u.Add(&cos.Stream{Dict: cos.Dict{}, Data: content})
	u.Set(cos.Ref{Num: 3}, page)
	var buf bytes.Buffer
	if err := u.WriteClean(&buf); err != nil {
		t.Fatal(err)
	}

	d2, err := cos.Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	p := d2.Pages()[0]
	f := Load(d2, p.Resources["Font"].(cos.Dict)["F1"])
	if f.Status != Subset || f.Subtype != "Type0" || !f.HasText() {
		t.Errorf("loaded font: %+v", f)
	}
	var got strings.Builder
	w := 0.0
	for _, g := range f.Glyphs(string(shown)) {
		got.WriteString(g.Text)
		w += g.W
	}
	if got.String() != text {
		t.Errorf("text %q, want %q", got.String(), text)
	}
	if want := face.TextWidth(text) / 1000; abs(w-want) > 1e-6 {
		t.Errorf("width %v, want %v", w, want)
	}

	// Cross-check with poppler when available.
	if pdftotext, err := exec.LookPath("pdftotext"); err == nil {
		file := filepath.Join(t.TempDir(), "embed.pdf")
		os.WriteFile(file, buf.Bytes(), 0o644)
		out, err := exec.Command(pdftotext, file, "-").CombinedOutput()
		if err != nil || !strings.Contains(string(out), text) {
			t.Errorf("pdftotext: %v %q", err, out)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestSubset(t *testing.T) {
	face := ptSans(t)[0]
	text := "Ёжик й ё — Привет, мир! 123"
	used := map[uint16]bool{}
	for _, r := range text {
		used[face.Glyph(r)] = true
	}
	data, err := face.Subset(used)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= len(face.Data)/3 {
		t.Errorf("subset %d bytes of %d", len(data), len(face.Data))
	}
	sub, err := sfnt.Parse(data)
	if err != nil {
		t.Fatalf("subset does not parse: %v", err)
	}
	if sub.NumGlyphs() != face.f.NumGlyphs() {
		t.Errorf("glyph ids changed: %d vs %d", sub.NumGlyphs(), face.f.NumGlyphs())
	}
	// Used glyphs keep their outlines (composites included), others are empty.
	var b sfnt.Buffer
	for _, r := range text {
		g := face.Glyph(r)
		if r == ' ' {
			continue
		}
		segs, err := sub.LoadGlyph(&b, sfnt.GlyphIndex(g), 1000, nil)
		if err != nil || len(segs) == 0 {
			t.Errorf("glyph %q lost its outline: %v", r, err)
		}
	}
	if segs, _ := sub.LoadGlyph(&b, sfnt.GlyphIndex(face.Glyph('Z')), 1000, nil); len(segs) != 0 {
		t.Error("unused glyph kept")
	}
}
