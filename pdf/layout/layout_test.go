package layout

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
)

func open(t *testing.T, name string) *cos.Document {
	b, err := os.ReadFile("../../testdata/corpus/" + name)
	if err != nil {
		t.Skip(err)
	}
	d, err := cos.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestChromeBlocks(t *testing.T) {
	d := open(t, "chrome.pdf")
	fc := content.NewFontCache()
	pg := Analyze(d, d.Pages()[0], fc)
	var texts []string
	for _, b := range pg.Blocks {
		if testing.Verbose() {
			t.Logf("%s %-5s size=%.1f angle=%.0f bbox=%.0f text=%q", b.ID, b.Kind, b.Size, b.Angle, b.BBox, b.Text)
		}
		if b.Kind == "text" {
			texts = append(texts, b.Text)
		}
	}
	all := strings.Join(texts, "\n")
	for _, want := range []string{"Договор оказания услуг № 42", "ООО «Ромашка»", "ОПЛАЧЕНО", "29 500"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	for _, b := range pg.Blocks {
		if b.Text == "ОПЛАЧЕНО" && (b.Angle < 5 || b.Angle > 20) {
			t.Errorf("rotated stamp angle %.1f, want ~12", b.Angle)
		}
	}
	for ref, u := range pg.FontUse {
		t.Logf("font %v %s %s status=%s chars=%d", ref, u.Font.Name, u.Font.Subtype, u.Font.Status, len(u.Chars))
	}
}

func TestCorpusPages(t *testing.T) {
	for _, name := range []string{"quartz.pdf", "cairo.pdf", "chrome.pdf"} {
		d := open(t, name)
		fc := content.NewFontCache()
		n := 0
		for _, p := range d.Pages() {
			pg := Analyze(d, p, fc)
			n += len(pg.Blocks)
			for _, b := range pg.Blocks {
				if b.BBox.Empty() {
					t.Errorf("%s: empty bbox %s", name, b.ID)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no blocks", name)
		}
	}
}

func TestTableGridIsOneBlock(t *testing.T) {
	d := open(t, "chrome.pdf")
	pg := Analyze(d, d.Pages()[0], content.NewFontCache())
	for _, b := range pg.Blocks {
		if b.Kind == "path" && b.ID == "000012" {
			// The table (header fill + borders) spans the table only, not the heading above it.
			if b.BBox[3] > 610 || b.BBox[1] < 510 {
				t.Errorf("table grid bbox %v", b.BBox)
			}
		}
	}
}

// Text comes out exactly as shown: repeated spaces, no-break spaces and
// separately shown spaces are kept, and kerning gaps never double a space.
func TestWhitespaceKept(t *testing.T) {
	stream := `BT /F1 12 Tf
72 700 Td (a  b\240c) Tj
0 -50 Td (one) Tj ( ) Tj ( ) Tj (two) Tj
0 -50 Td [(gap) -300 (word) -300 ( space)] TJ
0 -100 Td (Wrapped soft line here) Tj 0 -14 Td (end) Tj
0 -50 Td (hyphen-) Tj 0 -14 Td (ated text) Tj
0 -50 Td (Short) Tj 0 -14 Td (A much longer second line) Tj
ET`
	doc := fmt.Sprintf(`%%PDF-1.7
1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj
2 0 obj <</Type/Pages/Kids[3 0 R]/Count 1>> endobj
3 0 obj <</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Contents 4 0 R/Resources <</Font <</F1 5 0 R>>>>>> endobj
4 0 obj <</Length %d>> stream
%s
endstream endobj
5 0 obj <</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>> endobj
trailer <</Root 1 0 R>>
`, len(stream), stream)
	d, err := cos.Open([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	pg := Analyze(d, d.Pages()[0], content.NewFontCache())
	got := map[string]string{} // text → flow
	for _, b := range pg.Blocks {
		got[b.Text] = b.Flow
	}
	for text, flow := range map[string]string{
		"a  b c":                           "a  b c",
		"one  two":                         "one  two",
		"gap word space":                   "gap word space",
		"Wrapped soft line here\nend":      "Wrapped soft line here end",
		"hyphen-\nated text":               "hyphen-ated text",
		"Short\nA much longer second line": "Short\nA much longer second line",
	} {
		if f, ok := got[text]; !ok || f != flow {
			t.Errorf("block %q: flow %q (found %v), want %q", text, f, ok, flow)
		}
	}
	if t.Failed() {
		t.Logf("blocks: %q", got)
	}
}

// A chart: nodes (dots drawn as round-capped zero-length strokes, as Quartz
// does) and edges with arrowheads drawn one after another, all touching,
// plus captions written over each other. Each node, each edge and each
// caption is a block; a table grid drawn the same way stays one block.
func TestChartPieces(t *testing.T) {
	stream := `1 J 0.5 w
100 100 m 200 200 l S 196 194 m 200 200 l 194 196 l S 104 106 m 100 100 l 106 104 l S
300 300 m 400 250 l S
20 w 100 100 m 100 100 l S 18 w 100 100 m 100 100 l S
20 w 200 200 m 200 200 l S 18 w 200 200 m 200 200 l S
0.5 w 300 100 m 500 100 l S 300 150 m 500 150 l S 300 100 m 300 150 l S 400 100 m 400 150 l S 500 100 m 500 150 l S
BT /F1 6 Tf 100 300 Td (First caption here) Tj 40 -1 Td (Second caption) Tj ET`
	doc := fmt.Sprintf(`%%PDF-1.7
1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj
2 0 obj <</Type/Pages/Kids[3 0 R]/Count 1>> endobj
3 0 obj <</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Contents 4 0 R/Resources <</Font <</F1 5 0 R>>>>>> endobj
4 0 obj <</Length %d>> stream
%s
endstream endobj
5 0 obj <</Type/Font/Subtype/Type1/BaseFont/Helvetica>> endobj
trailer <</Root 1 0 R>>
`, len(stream), stream)
	d, err := cos.Open([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	pg := Analyze(d, d.Pages()[0], content.NewFontCache())
	var paths []int
	var texts []string
	for _, b := range pg.Blocks {
		switch b.Kind {
		case "path":
			paths = append(paths, len(b.Items))
		case "text":
			texts = append(texts, b.Text)
		}
	}
	// edge + 2 arrowheads, another edge, node, node, grid of 5 lines
	if want := []int{3, 1, 2, 2, 5}; !slices.Equal(paths, want) {
		t.Errorf("path blocks (items each) %v, want %v", paths, want)
	}
	slices.Sort(texts)
	if want := []string{"First caption here", "Second caption"}; !slices.Equal(texts, want) {
		t.Errorf("captions %q, want %q", texts, want)
	}
}
