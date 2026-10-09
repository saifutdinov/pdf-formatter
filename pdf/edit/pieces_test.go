package edit

import (
	"bytes"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/layout"
	"pdf-formatter/pdf/meta"
)

// Own objects survive a download: the file carries their model, and
// replacing the items that show them with the objects again gives the
// same page, each object editable and drawn once.
func TestPiecesRoundTrip(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[1]
	testAssets["pic.png"] = pngWithAlpha()
	runs := []Run{{Text: "Свой "}, {Text: "текст", Font: "PTSans-Bold"}}
	objs := []*Object{
		{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 60, 300}, W: 200, H: 30, Text: RunsText(runs), Runs: runs, Size: 12, Opacity: 1},
		{ID: "o2", Kind: "rect", Matrix: [6]float64{1, 0, 0, 1, 300, 300}, W: 50, H: 30, Fill: "#00aa55", Opacity: 1},
		{ID: "o3", Kind: "image", Matrix: [6]float64{1, 0, 0, 1, 400, 300}, W: 40, H: 20, Asset: "pic.png", Opacity: 1},
		{ID: "o4", Kind: "table", Matrix: [6]float64{1, 0, 0, 1, 60, 200}, Cols: []float64{80, 80}, Rows: []TableRow{{Cells: []string{"А", "Б"}}}, Stroke: "#000000", StrokeWidth: 0.5, CellPad: 3, Size: 10, Opacity: 1},
	}
	var ops []Op
	for _, o := range objs {
		ops = append(ops, Op{Op: "add", Page: p.Ref.Num, Object: o})
	}
	d2 := edited(t, d, ops)
	pieces := ReadPieces(d2, content.NewFontCache())
	if len(pieces) != 1 || len(pieces[0].Objects) != 4 {
		t.Fatalf("pieces %+v", pieces)
	}
	pc := pieces[0]
	for _, o := range objs {
		if len(pc.Members[o.ID]) == 0 {
			t.Errorf("no items for %s", o.ID)
		}
	}
	if !bytes.Equal(pc.Assets["pic.png"], testAssets["pic.png"]) || pc.Fonts["PTSans-Bold"] != "PTSans-Bold" {
		t.Errorf("assets %d bytes, fonts %v", len(pc.Assets["pic.png"]), pc.Fonts)
	}
	if got := pc.Objects[0]; got.Text != "Свой текст" || len(got.Runs) != 2 {
		t.Errorf("text object %+v", got)
	}

	// Opened again: each object replaces the items that showed it.
	var again []Op
	for _, o := range pc.Objects {
		o := o
		m := pc.Members[o.ID]
		again = append(again, Op{Op: "add", Page: pc.Page.Num, Object: &o, Replaces: []string{m[0]}, Members: map[string][]string{m[0]: m}})
	}
	d3 := edited(t, d2, again)
	pg := layout.Analyze(d3, d3.Pages()[1], content.NewFontCache())
	var texts []string
	for _, b := range pg.Blocks {
		texts = append(texts, b.Text)
	}
	if n := strings.Count(strings.Join(texts, "|"), "Свой текст"); n != 1 {
		t.Errorf("text drawn %d times: %q", n, texts)
	}
	// A clean copy keeps the model; sanitizing removes only foreign pieces.
	var buf bytes.Buffer
	u, _ := Apply(d3, append(again, Op{Op: "sanitize", Sanitize: sanitizeAll()}), testEnv(t))
	u.WriteClean(&buf)
	d4, _ := cos.Open(buf.Bytes())
	if len(ReadPieces(d4, content.NewFontCache())) != 1 {
		t.Error("the model did not survive a clean, sanitized copy")
	}
}

func sanitizeAll() *meta.Sanitize {
	return &meta.Sanitize{PieceInfo: true, Thumbnails: true, JavaScript: true, Embedded: true}
}
