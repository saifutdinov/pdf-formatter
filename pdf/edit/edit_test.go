package edit

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/layout"
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

// edited applies ops and re-opens the result.
func edited(t *testing.T, d *cos.Document, ops []Op) *cos.Document {
	t.Helper()
	u, err := Apply(d, ops, testEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := u.WriteIncremental(&buf); err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("EDIT_OUT"); out != "" {
		os.WriteFile(filepath.Join(out, t.Name()+".pdf"), buf.Bytes(), 0o644)
	}
	if q, err := exec.LookPath("qpdf"); err == nil {
		f := filepath.Join(t.TempDir(), "e.pdf")
		os.WriteFile(f, buf.Bytes(), 0o644)
		if out, err := exec.Command(q, "--check", f).CombinedOutput(); err != nil {
			t.Errorf("qpdf: %v\n%s", err, out)
		}
	}
	d2, err := cos.Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return d2
}

func blocksByText(pg *layout.Page) map[string]layout.Block {
	m := map[string]layout.Block{}
	for _, b := range pg.Blocks {
		if b.Kind == "text" {
			m[b.Text] = b
		}
	}
	return m
}

func near(a, b [8]float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 0.05 {
			return false
		}
	}
	return true
}

func find(t *testing.T, pg *layout.Page, text string) layout.Block {
	for _, b := range pg.Blocks {
		if strings.TrimSpace(b.Text) == strings.TrimSpace(text) { // Chrome ends lines with a space
			return b
		}
	}
	t.Fatalf("block %q not found", text)
	return layout.Block{}
}

const heading = "Договор оказания услуг № 42"

func TestMoveKeepsOthers(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[0]
	before := layout.Analyze(d, p, content.NewFontCache())
	h := find(t, before, heading)
	para := before.Blocks[2] // first paragraph: interleaved with relative positioning
	ops := []Op{
		{Op: "transform", Page: p.Ref.Num, Blocks: []string{h.ID}, Matrix: &[6]float64{1, 0, 0, 1, 30, -400}},
		{Op: "transform", Page: p.Ref.Num, Blocks: []string{para.ID}, Matrix: &[6]float64{1, 0, 0, 1, 0, 50}},
	}
	d2 := edited(t, d, ops)
	after := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	want := MapQuad(h.Quad, content.Matrix{1, 0, 0, 1, 30, -400})
	if got := find(t, after, heading).Quad; !near(got, want) {
		t.Errorf("heading quad %v, want %v", got, want)
	}
	if got := find(t, after, para.Text).Quad; !near(got, MapQuad(para.Quad, content.Matrix{1, 0, 0, 1, 0, 50})) {
		t.Errorf("paragraph moved wrong: %v", got)
	}
	// Everything else stays put.
	moved := map[string]bool{heading: true, para.Text: true}
	a := blocksByText(after)
	for text, b := range blocksByText(before) {
		if moved[text] {
			continue
		}
		if got, ok := a[text]; !ok || !near(got.Quad, b.Quad) {
			t.Errorf("%q moved from %v to %v", text, b.Quad, got.Quad)
		}
	}
}

func TestDeleteRecolorRotate(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[0]
	before := layout.Analyze(d, p, content.NewFontCache())
	stamp := find(t, before, "ОПЛАЧЕНО")
	h := find(t, before, heading)
	var grid layout.Block
	for _, b := range before.Blocks {
		if b.Kind == "path" && grid.ID == "" {
			grid = b
		}
	}
	cx, cy := (h.BBox[0]+h.BBox[2])/2, (h.BBox[1]+h.BBox[3])/2
	c, s := math.Cos(math.Pi/2), math.Sin(math.Pi/2)
	rot := [6]float64{c, s, -s, c, cx - c*cx + s*cy, cy - s*cx - c*cy} // 90° around the centre
	d2 := edited(t, d, []Op{
		{Op: "delete", Page: p.Ref.Num, Blocks: []string{stamp.ID}},
		{Op: "recolor", Page: p.Ref.Num, Blocks: []string{h.ID, grid.ID}, Color: "#ff0000"},
		{Op: "transform", Page: p.Ref.Num, Blocks: []string{h.ID}, Matrix: &rot},
	})
	after := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	if _, ok := blocksByText(after)["ОПЛАЧЕНО"]; ok {
		t.Error("stamp not deleted")
	}
	nh := find(t, after, heading)
	if math.Abs(nh.Angle-90) > 0.5 {
		t.Errorf("heading angle %.1f, want 90", nh.Angle)
	}
	if nh.Color != [3]float64{1, 0, 0} {
		t.Errorf("heading colour %v", nh.Color)
	}
	for _, b := range after.Blocks {
		if b.Kind == "path" && b.ID == grid.ID {
			continue
		}
	}
	// Text after the deleted stamp on the same line keeps its position.
	site := find(t, before, "Сайт исполнителя: example.com/contract.")
	if got := find(t, after, site.Text); !near(got.Quad, site.Quad) {
		t.Errorf("site line moved: %v -> %v", site.Quad, got.Quad)
	}
}

const formDoc = `%PDF-1.7
1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj
2 0 obj <</Type/Pages/Kids[3 0 R]/Count 1>> endobj
3 0 obj <</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Contents 4 0 R
  /Resources <</Font <</F1 5 0 R>> /XObject <</Fm1 6 0 R>>>>>> endobj
4 0 obj <</Length 48>> stream
BT /F1 12 Tf 72 700 Td (Page text) Tj ET /Fm1 Do
endstream endobj
5 0 obj <</Type/Font/Subtype/Type1/BaseFont/Helvetica>> endobj
6 0 obj <</Type/XObject/Subtype/Form/BBox[0 0 300 300]/Matrix[1 0 0 1 100 100]
  /Resources <</Font <</F1 7 0 R>> /XObject <</Fm2 8 0 R>>>> /Length 49>> stream
BT /F1 12 Tf 10 10 Td (Inside form) Tj ET /Fm2 Do
endstream endobj
7 0 obj <</Type/Font/Subtype/Type1/BaseFont/Times-Roman>> endobj
8 0 obj <</Type/XObject/Subtype/Form/BBox[0 0 300 300]
  /Resources <</Font <</F1 9 0 R>>>> /Length 35>> stream
BT /F1 10 Tf 0 50 Td (Nested) Tj ET
endstream endobj
9 0 obj <</Type/Font/Subtype/Type1/BaseFont/Courier>> endobj
trailer <</Root 1 0 R>>
`

func TestEditInsideForms(t *testing.T) {
	d, err := cos.Open([]byte(formDoc))
	if err != nil {
		t.Fatal(err)
	}
	p := d.Pages()[0]
	before := layout.Analyze(d, p, content.NewFontCache())
	inside := find(t, before, "Inside form")
	nested := find(t, before, "Nested")
	if !inside.InForm || !nested.InForm {
		t.Fatal("expected form blocks")
	}
	m := content.Matrix{1, 0, 0, 1, 50, 0}
	d2 := edited(t, d, []Op{
		{Op: "transform", Page: p.Ref.Num, Blocks: []string{inside.ID}, Matrix: (*[6]float64)(&m)},
		{Op: "recolor", Page: p.Ref.Num, Blocks: []string{nested.ID}, Color: "#0000ff"},
	})
	after := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	got := find(t, after, "Inside form")
	if !near(got.Quad, MapQuad(inside.Quad, m)) {
		t.Errorf("inside form: %v want %v", got.Quad, MapQuad(inside.Quad, m))
	}
	if got.Font.Name != "Times-Roman" {
		t.Errorf("font after inlining: %s (resource clash not renamed)", got.Font.Name)
	}
	n := find(t, after, "Nested")
	if !near(n.Quad, nested.Quad) || n.Font.Name != "Courier" || n.Color != [3]float64{0, 0, 1} {
		t.Errorf("nested: %v %s %v", n.Quad, n.Font.Name, n.Color)
	}
	if pt := find(t, after, "Page text"); pt.Font.Name != "Helvetica" || !near(pt.Quad, find(t, before, "Page text").Quad) {
		t.Errorf("page text changed: %s", pt.Font.Name)
	}
}

// An edit recorded with its items stays on them when blocks are formed
// differently later: here a block that (by its recorded items) also held
// the next block is deleted, and both go, whatever the blocks are now.
func TestEditKeepsRecordedItems(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[0]
	pg := layout.Analyze(d, p, content.NewFontCache())
	a := find(t, pg, heading)
	b := find(t, pg, "Сайт исполнителя: example.com/contract.")
	var items []string
	for _, blk := range []layout.Block{a, b} {
		for _, i := range blk.Items {
			items = append(items, layout.ItemID(pg.Items[i]))
		}
	}
	d2 := edited(t, d, []Op{{Op: "delete", Page: p.Ref.Num, Blocks: []string{a.ID}, Members: map[string][]string{a.ID: items}}})
	after := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	texts := func(pg *layout.Page) (n int) {
		for _, blk := range pg.Blocks {
			if blk.Kind == "text" {
				n++
			}
			if s := strings.TrimSpace(blk.Text); pg == after && (s == heading || s == strings.TrimSpace(b.Text)) {
				t.Errorf("block %q survived", s)
			}
		}
		return n
	}
	if before, left := texts(pg), texts(after); left != before-2 {
		t.Errorf("text blocks: %d → %d, want two fewer", before, left)
	}
}
