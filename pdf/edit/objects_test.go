package edit

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/font"
	"pdf-formatter/pdf/layout"
)

var testAssets = map[string][]byte{}

func testEnv(t *testing.T) Env {
	data, err := os.ReadFile("../../fonts/free/PTSans.ttc")
	if err != nil {
		t.Skip(err)
	}
	faces, err := font.ParseFaces(data)
	if err != nil {
		t.Fatal(err)
	}
	return Env{
		Face: func(id string) (*font.Face, error) {
			for _, f := range faces {
				if f.PSName == id || id == "" && f.PSName == "PTSans-Regular" {
					return f, nil
				}
			}
			return faces[0], nil
		},
		Asset: func(name string) ([]byte, error) { return testAssets[name], nil },
	}
}

func pngWithAlpha() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for x := range 8 {
		for y := range 4 {
			img.Set(x, y, color.NRGBA{uint8(x * 30), 0, 200, uint8(y * 80)})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestTextObjectLayout(t *testing.T) {
	env := testEnv(t)
	face, _ := env.Face("")
	lines := Wrap(face, "Съешь же ещё этих мягких французских булок, да выпей чаю.\nВторой абзац", 12, 150)
	if len(lines) < 3 {
		t.Fatalf("expected wrapping, got %+v", lines)
	}
	for _, l := range lines {
		if l.Width > 150.01 {
			t.Errorf("line too wide: %+v", l)
		}
	}
	if lines[len(lines)-1].Text != "Второй абзац" || lines[len(lines)-1].Justify {
		t.Errorf("last line: %+v", lines[len(lines)-1])
	}
	// A word longer than the box is broken by characters.
	if l := Wrap(face, "Оченьдлинноесловобезпробелов", 12, 50); len(l) < 2 {
		t.Errorf("long word not broken: %+v", l)
	}
}

// Wrapping only decides where lines break: the lines put together give
// back the paragraph character for character.
func TestWrapKeepsEveryCharacter(t *testing.T) {
	face, _ := testEnv(t).Face("")
	for _, s := range []string{"a  b\u00a0c\td  ", "   отступ и  два пробела", "пере-нос черно-белый", "Оченьдлинноесловобезпробелов  и ещё", "  "} {
		for _, w := range []float64{10, 50, 120, 1000} {
			var got strings.Builder
			for _, l := range Wrap(face, s, 12, w) {
				got.WriteString(l.Text)
			}
			if got.String() != s {
				t.Errorf("width %v: %q became %q", w, s, got.String())
			}
		}
	}
	width := func(s string) float64 { return face.TextWidth(s)/1000*12 + 0.5 }
	// A no-break space holds its words together; a hyphen lets a word break.
	if l := Wrap(face, "слово\u00a0слово слово", 12, width("слово слово")); len(l) != 2 || l[0].Text != "слово\u00a0слово " {
		t.Errorf("no-break space: %+v", l)
	}
	if l := Wrap(face, "пере-нос", 12, width("пере-")); len(l) != 2 || l[0].Text != "пере-" {
		t.Errorf("hyphen: %+v", l)
	}
}

// An edited text keeps every character through the PDF and back. (A tab
// has no glyph of its own: it is drawn as, and reads back as, a space.)
func TestTextRoundTrip(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[0]
	text := "Два  пробела,\u00a0неразрывный  и   три "
	d2 := edited(t, d, []Op{{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o1", Kind: "text",
		Matrix: [6]float64{1, 0, 0, 1, 60, 300}, W: 400, H: 30, Text: text, Size: 12, Opacity: 1}}})
	for _, b := range layout.Analyze(d2, d2.Pages()[0], content.NewFontCache()).Blocks {
		if strings.HasPrefix(b.Text, "Два") {
			if b.Text != text {
				t.Errorf("got %q, want %q", b.Text, text)
			}
			return
		}
	}
	t.Error("text not found")
}

func TestObjects(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[0]
	before := layout.Analyze(d, p, content.NewFontCache())
	stamp := find(t, before, "ОПЛАЧЕНО")
	m, w, h := QuadMatrix(stamp.Quad)
	if math.Abs(w-(stamp.BBox[2]-stamp.BBox[0])) > 30 || h <= 0 {
		t.Errorf("quad matrix size %v×%v", w, h)
	}
	testAssets["pic.png"] = pngWithAlpha()
	text := "Новый текстовый блок с переносом строк и выравниванием по центру"
	ops := []Op{
		{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 60, 300}, W: 200, H: 80,
			Text: text, Size: 14, Color: "#203080", Align: "center", Fill: "#ffffe0", Stroke: "#999999", StrokeWidth: 1,
			Padding: [4]float64{6, 6, 6, 6}, Opacity: 1}},
		{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o2", Kind: "text", Matrix: m, W: w * 2, H: h, Text: "ОПЛАЧЕНО ПОЛНОСТЬЮ", Size: 11, Color: "#bb0000", Opacity: 1}, Replaces: []string{stamp.ID}},
		{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o3", Kind: "ellipse", Matrix: [6]float64{1, 0, 0, 1, 300, 300}, W: 80, H: 40, Fill: "#00aa55", Opacity: 0.5}},
		{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o4", Kind: "line", Matrix: [6]float64{1, 0, 0, 1, 60, 200}, W: 300, H: 4, Stroke: "#000000", StrokeWidth: 2, Opacity: 1}},
		{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o5", Kind: "image", Matrix: [6]float64{1, 0, 0, 1, 400, 300}, W: 80, H: 40, Asset: "pic.png", Opacity: 1}},
		{Op: "transform", Page: p.Ref.Num, Blocks: []string{"o3"}, Matrix: &[6]float64{1, 0, 0, 1, 10, 0}},
	}
	d2 := edited(t, d, ops)
	after := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	var all []string
	images, paths := 0, 0
	for _, b := range after.Blocks {
		switch b.Kind {
		case "text":
			all = append(all, b.Flow)
		case "image":
			images++
		case "path":
			paths++
		}
	}
	joined := strings.Join(all, "\n")
	if !strings.Contains(joined, text) {
		t.Errorf("new text missing:\n%s", joined)
	}
	if !strings.Contains(joined, "ОПЛАЧЕНО ПОЛНОСТЬЮ") || strings.Count(joined, "ОПЛАЧЕНО") != 1 {
		t.Errorf("stamp not replaced:\n%s", joined)
	}
	if images != 2 {
		t.Errorf("images: %d, want 2 (original + added)", images)
	}
	// Centred text stays inside its box.
	for _, b := range after.Blocks {
		if b.Kind == "text" && strings.HasPrefix(b.Text, "Новый") {
			if b.BBox[0] < 66 || b.BBox[2] > 254 || b.BBox[3] > 300 || b.BBox[1] < 220 {
				t.Errorf("text block outside its box: %v", b.BBox)
			}
		}
	}
	// Original content is untouched apart from the replaced stamp.
	site := find(t, before, "Сайт исполнителя: example.com/contract.")
	if got := find(t, after, site.Text); !near(got.Quad, site.Quad) {
		t.Errorf("original text moved")
	}
}

func TestObjectsOnUntouchedPage(t *testing.T) {
	d := open(t, "cairo.pdf") // xref streams: the update must be one too
	p := d.Pages()[1]
	d2 := edited(t, d, []Op{{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 50, 100}, W: 300, H: 30, Text: "Подпись: ____", Size: 12, Opacity: 1}}})
	pg := layout.Analyze(d2, d2.Pages()[1], content.NewFontCache())
	found := false
	for _, b := range pg.Blocks {
		found = found || b.Text == "Подпись: ____"
	}
	if !found {
		t.Error("added text not found")
	}
	var _ cos.Ref
}

func TestTable(t *testing.T) {
	env := testEnv(t)
	d := open(t, "chrome.pdf")
	p := d.Pages()[1]
	tbl := &Object{ID: "o1", Kind: "table", Matrix: [6]float64{1, 0, 0, 1, 60, 500}, Cols: []float64{150, 80, 120},
		Rows:       []TableRow{{Cells: []string{"Наименование", "Кол-во", "Сумма"}}, {Cells: []string{"Очень длинное название позиции, которое переносится", "2", "1 000"}}, {Cells: []string{"Итого", "", "1 000"}}},
		HeaderFill: "#e8eef7", Stroke: "#555555", StrokeWidth: 0.8, CellPad: 4, Size: 10, Opacity: 1}
	face, _ := env.Face("")
	heights := tbl.TableLayout(face)
	if len(heights) != 3 || heights[1] <= heights[0] {
		t.Fatalf("row heights %v: the wrapped row must be taller", heights)
	}
	w, h := tbl.TableSize(face)
	if w != 350 || math.Abs(h-(heights[0]+heights[1]+heights[2])) > 1e-9 {
		t.Errorf("size %v×%v", w, h)
	}
	d2 := edited(t, d, []Op{{Op: "add", Page: p.Ref.Num, Object: tbl}})
	texts := pageTexts(d2, 1)
	for _, want := range []string{"Наименование", "Кол-во", "Итого", "1 000"} {
		if !has(texts, want) {
			t.Errorf("missing %q in %v", want, texts)
		}
	}
	// Grid lines: one path block covering the table.
	pg := layout.Analyze(d2, d2.Pages()[1], content.NewFontCache())
	found := false
	for _, b := range pg.Blocks {
		if b.Kind == "path" && math.Abs(b.BBox[0]-60) < 2 && math.Abs(b.BBox[2]-410) < 2 && math.Abs(b.BBox[3]-500) < 2 {
			found = true
		}
	}
	if !found {
		t.Error("table grid not found")
	}
}

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100" viewBox="0 0 200 100">
  <rect x="5" y="5" width="60" height="70" rx="8" fill="#2a7"/>
  <circle cx="105" cy="40" r="30" fill="none" stroke="#c33" stroke-width="5"/>
  <path d="M10 90 A 20 10 0 0 1 50 90 Q 70 70 90 90 T 130 90" fill="none" stroke="black" stroke-width="2"/>
  <g opacity="0.5"><rect x="140" y="10" width="50" height="50" fill="blue" transform="rotate(20 165 35)"/></g>
</svg>`

func TestSVGObject(t *testing.T) {
	d := open(t, "quartz.pdf")
	p := d.Pages()[0]
	testAssets["logo.svg"] = []byte(testSVG)
	// A box twice as tall as the drawing: "meet" keeps proportions, centred vertically.
	d2 := edited(t, d, []Op{{Op: "add", Page: p.Ref.Num, Object: &Object{ID: "o1", Kind: "svg", Matrix: [6]float64{1, 0, 0, 1, 100, 500}, W: 300, H: 300, Asset: "logo.svg", Opacity: 1}}})
	pg := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	var box content.Rect
	first := true
	for _, it := range pg.Items {
		if it.Kind == content.Path && len(it.Form) > 0 {
			if first {
				box, first = it.BBox, false
			} else {
				box = box.Union(it.BBox)
			}
		}
	}
	if first {
		t.Fatal("no SVG paths in the page")
	}
	// Drawing is 300×150 in the box: x 100..400, top at y 500-75. (Parts
	// outside the viewBox are clipped by the form BBox when rendered.)
	if box[0] < 100 || box[2] > 400 || box[3] > 500-75+1 || box[3] < 400 {
		t.Errorf("svg bbox %v", box)
	}
}

// Runs: parts of a text object in their own font, size, colour, underline.
func TestStyledRuns(t *testing.T) {
	d := open(t, "chrome.pdf")
	p := d.Pages()[0]
	runs := []Run{
		{Text: "Обычный текст, "},
		{Text: "жирный красный", Font: "PTSans-Bold", Color: "#cc0000", Size: 16},
		{Text: " и подчёркнутый хвост абзаца, который переносится на следующую строку.", Underline: true},
	}
	obj := &Object{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 60, 300}, W: 220, H: 80, Size: 12, Opacity: 1,
		Text: RunsText(runs), Runs: runs, AutoHeight: true}
	env := testEnv(t)
	face, _ := env.Face("")
	if h := obj.TextHeight(env.Face); h <= 2*12 {
		t.Errorf("text height %v: expected several lines", h)
	}
	st, _ := obj.styles(env.Face)
	lines := layoutText(obj.runs(), st, obj.contentW())
	if lines[0].Size != 16 || len(lines[0].Segs) < 2 {
		t.Errorf("first line %+v", lines[0])
	}
	var all strings.Builder
	for _, l := range lines {
		all.WriteString(l.Text)
	}
	if all.String() != obj.Text {
		t.Errorf("layout lost text: %q", all.String())
	}
	_ = face

	d2 := edited(t, d, []Op{{Op: "add", Page: p.Ref.Num, Object: obj}})
	pg := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	var flow string
	sizes, reds, rules := map[float64]bool{}, 0, 0
	for _, b := range pg.Blocks {
		if b.Kind == "text" && strings.HasPrefix(b.Text, "Обычный") {
			flow = b.Flow
			for _, i := range b.Items {
				it := pg.Items[i]
				sizes[math.Round(it.Size)] = true
				if it.State.Fill == [3]float64{0.8, 0, 0} {
					reds++
				}
			}
		}
		if b.Kind == "path" && b.BBox[2]-b.BBox[0] > 20 && b.BBox[3]-b.BBox[1] < 2 {
			rules++
		}
	}
	if flow != obj.Text {
		t.Errorf("read back %q", flow)
	}
	if !sizes[12] || !sizes[16] || reds == 0 {
		t.Errorf("styles not applied: sizes %v, red runs %d", sizes, reds)
	}
	if rules == 0 {
		t.Error("no underline")
	}
}

// Merged cells: the text spans the merged width, a cell merged down makes
// the rows tall enough, the covered cells are not drawn.
func TestTableSpans(t *testing.T) {
	env := testEnv(t)
	face, _ := env.Face("")
	long := "Объединённая по вертикали ячейка с длинным текстом, которому нужно много строк"
	tbl := &Object{ID: "o1", Kind: "table", Matrix: [6]float64{1, 0, 0, 1, 60, 500}, Cols: []float64{120, 80, 80},
		Rows: []TableRow{
			{Cells: []string{"Шапка таблицы, занимающая всю её ширину", "скрыто", "скрыто"}},
			{Cells: []string{long, "a", "b"}},
			{Cells: []string{"", "c", "d"}},
		},
		Spans:  []Span{{Row: 0, Col: 0, Rows: 1, Cols: 3}, {Row: 1, Col: 0, Rows: 2, Cols: 1}},
		Stroke: "#000000", StrokeWidth: 0.5, CellPad: 3, Size: 10, Opacity: 1}
	plain := *tbl
	plain.Spans = nil
	h, hp := tbl.TableLayout(face), plain.TableLayout(face)
	if h[0] >= hp[0] { // one line across three columns instead of wrapping in one
		t.Errorf("header %v vs %v", h[0], hp[0])
	}
	need := Wrap(face, long, 10, 120-6)
	if got := h[1] + h[2]; got < float64(len(need)-1)*1.2*10+10 {
		t.Errorf("merged rows %v too short for %d lines", h, len(need))
	}
	if s, first := tbl.SpanAt(2, 0); first || s.Row != 1 {
		t.Errorf("covered cell: %+v %v", s, first)
	}
	d := open(t, "chrome.pdf")
	d2 := edited(t, d, []Op{{Op: "add", Page: d.Pages()[1].Ref.Num, Object: tbl}})
	texts := strings.Join(pageTexts(d2, 1), "|")
	if strings.Contains(texts, "скрыто") || !strings.Contains(texts, "Шапка таблицы, занимающая всю её ширину") {
		t.Errorf("page text: %s", texts)
	}
}
