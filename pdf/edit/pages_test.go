package edit

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/layout"
	"pdf-formatter/pdf/nav"
)

func pageTexts(d *cos.Document, i int) []string {
	pg := layout.Analyze(d, d.Pages()[i], content.NewFontCache())
	var out []string
	for _, b := range pg.Blocks {
		if b.Kind == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

func has(texts []string, s string) bool {
	for _, t := range texts {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func TestPageOperations(t *testing.T) {
	d := open(t, "chrome.pdf")
	p1, p2 := d.Pages()[0].Ref.Num, d.Pages()[1].Ref.Num
	ops := []Op{
		{Op: "page.insert", NewID: -1, After: p1, Size: [2]float64{400, 300}},
		{Op: "add", Page: -1, Object: &Object{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 40, 260}, W: 300, H: 30, Text: "Вставленная страница", Size: 14, Opacity: 1}},
		{Op: "page.move", Page: p2, After: 0}, // page 2 first
		{Op: "page.rotate", Page: p1, Rotate: 90},
		{Op: "page.insert", NewID: -2, After: -1},
		{Op: "page.delete", Page: -2},
	}
	specs, changed := Pages(d, ops, nil)
	if !changed || len(specs) != 3 || specs[0].ID != p2 || specs[1].ID != p1 || specs[2].ID != -1 {
		t.Fatalf("specs %+v", specs)
	}
	d2 := edited(t, d, ops)
	pages := d2.Pages()
	if len(pages) != 3 {
		t.Fatalf("%d pages", len(pages))
	}
	if !has(pageTexts(d2, 0), "Порядок расчётов") || !has(pageTexts(d2, 1), "Договор оказания") || !has(pageTexts(d2, 2), "Вставленная страница") {
		t.Errorf("order: %v | %v | %v", pageTexts(d2, 0), pageTexts(d2, 1), pageTexts(d2, 2))
	}
	if pages[1].Rotate != 90 || pages[2].MediaBox != [4]float64{0, 0, 400, 300} {
		t.Errorf("rotate %d, media %v", pages[1].Rotate, pages[2].MediaBox)
	}
}

func TestMarginsLabelsFooter(t *testing.T) {
	d := open(t, "chrome.pdf")
	p1, p2 := d.Pages()[0].Ref.Num, d.Pages()[1].Ref.Num
	before := layout.Analyze(d, d.Pages()[0], content.NewFontCache())
	h := find(t, before, heading)
	ops := []Op{
		{Op: "page.margins", Pages: []int{p2}, Mode: "resize", Margins: [4]float64{0, 50, 0, 50}},
		{Op: "page.margins", Pages: []int{p1}, Mode: "fit", Margins: [4]float64{100, 100, 100, 100}},
		{Op: "labels", Labels: []Label{{Start: 0, Style: "r"}, {Start: 1, Style: "D", Prefix: "Стр. "}}},
		{Op: "hf", HF: []HFItem{{Text: "{label} из {pages}", Position: "bottom-center", Size: 9, Offset: 20, Side: 40}}},
	}
	d2 := edited(t, d, ops)
	pages := d2.Pages()
	w := pages[1].MediaBox[2] - pages[1].MediaBox[0]
	if math.Abs(w-(d.Pages()[1].MediaBox[2]-d.Pages()[1].MediaBox[0]+100)) > 0.01 {
		t.Errorf("resized width %v", w)
	}
	// Fit: the heading shrank towards the centre, inside the new margins.
	after := layout.Analyze(d2, pages[0], content.NewFontCache())
	nh := find(t, after, heading)
	if nh.Size >= h.Size || nh.BBox[0] < 100 {
		t.Errorf("fit: size %.1f→%.1f bbox %v", h.Size, nh.Size, nh.BBox)
	}
	if got := ReadLabels(d2, d2.Dict(d2.Trailer["Root"])); len(got) != 2 || got[1].Prefix != "Стр. " {
		t.Errorf("labels %+v", got)
	}
	if !has(pageTexts(d2, 0), "i из 2") || !has(pageTexts(d2, 1), "Стр. 1 из 2") {
		t.Errorf("footers: %v / %v", pageTexts(d2, 0), pageTexts(d2, 1))
	}
	for _, b := range after.Blocks {
		if strings.Contains(b.Text, "из 2") {
			if !b.Artifact {
				t.Error("footer not marked as artifact")
			}
			// Footers are not scaled with the content.
			if b.BBox[1] > 40 || b.Size < 8.9 {
				t.Errorf("footer scaled: %v size %.1f", b.BBox, b.Size)
			}
		}
	}
	for n, want := range map[int]string{0: "i", 3: "iv", 1: "Стр. 1"} {
		labels := []Label{{Start: 0, Style: "r"}, {Start: 1, Style: "D", Prefix: "Стр. "}}
		if n == 3 {
			labels = labels[:1]
		}
		if got := LabelOf(labels, n); got != want {
			t.Errorf("label %d: %q want %q", n, got, want)
		}
	}
}

func TestTOCPage(t *testing.T) {
	d := open(t, "chrome.pdf")
	p1, p2 := d.Pages()[0].Ref.Num, d.Pages()[1].Ref.Num
	top := 700.0
	ops := []Op{
		{Op: "outline", Outline: []nav.Outline{{Title: "Договор", Page: p1, Items: []nav.Outline{{Title: "Стоимость", Page: p1, Top: &top}}}, {Title: "Порядок расчётов", Page: p2}}},
		{Op: "toc", NewID: -5, After: 0, TOC: &TOC{Title: "Оглавление", MaxLevel: 2}},
	}
	d2 := edited(t, d, ops)
	if len(d2.Pages()) != 3 {
		t.Fatalf("%d pages", len(d2.Pages()))
	}
	texts := pageTexts(d2, 0)
	if !has(texts, "Оглавление") || !has(texts, "Договор") || !has(texts, "Порядок расчётов") {
		t.Errorf("toc text: %v", texts)
	}
	links := nav.ReadLinks(d2, d2.Dict(d2.Trailer["Root"]), d2.Pages()[0].Dict)
	if len(links) != 3 || links[2].Page != d2.Pages()[2].Ref.Num {
		t.Errorf("toc links: %+v", links)
	}
	// Page numbers count the contents page itself: «Договор» is on page 2.
	if !has(texts, "2") || !has(texts, "3") {
		t.Errorf("toc numbers: %v", texts)
	}
}

func TestPageImport(t *testing.T) {
	d := open(t, "quartz.pdf")
	other, err := os.ReadFile("../../testdata/corpus/chrome.pdf")
	if err != nil {
		t.Skip(err)
	}
	testAssets["other.pdf"] = other
	p1 := d.Pages()[0].Ref.Num
	ops := []Op{
		{Op: "page.import", Asset: "other.pdf", After: p1, NewIDs: []int{-10, -11}},
		{Op: "add", Page: -11, Object: &Object{ID: "o1", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, 50, 100}, W: 300, H: 30, Text: "Добавлено на импортированной странице", Size: 12, Opacity: 1}},
	}
	d2 := edited(t, d, ops)
	if len(d2.Pages()) != 3 {
		t.Fatalf("%d pages", len(d2.Pages()))
	}
	if !has(pageTexts(d2, 1), "Договор оказания") || !has(pageTexts(d2, 2), "Порядок расчётов") || !has(pageTexts(d2, 2), "Добавлено на импортированной") {
		t.Errorf("imported pages: %v / %v", pageTexts(d2, 1), pageTexts(d2, 2))
	}
	if !has(pageTexts(d2, 0), "Отчёт по проекту") {
		t.Error("original page lost")
	}
}

func TestLongTOC(t *testing.T) {
	d := open(t, "chrome.pdf")
	p1 := d.Pages()[0].Ref.Num
	var items []nav.Outline
	for i := range 90 {
		items = append(items, nav.Outline{Title: fmt.Sprintf("Раздел %d", i+1), Page: p1})
	}
	ops := []Op{{Op: "outline", Outline: items}, {Op: "toc", NewID: -1, After: 0, TOC: &TOC{Title: "Содержание", MaxLevel: 1}}}
	d2 := edited(t, d, ops)
	pages := d2.Pages()
	if len(pages) < 5 { // 2 original + at least 3 contents pages for 90 entries
		t.Fatalf("%d pages", len(pages))
	}
	tocPages := len(pages) - 2
	if !has(pageTexts(d2, 0), "Раздел 1 ") && !has(pageTexts(d2, 0), "Раздел 1.") {
		t.Errorf("first entry missing: %v", pageTexts(d2, 0)[:3])
	}
	if last := pageTexts(d2, tocPages-1); !has(last, "Раздел 90") {
		t.Errorf("last entry missing on page %d: %v", tocPages, last)
	}
	// Entries point at the original first page, now after the contents.
	if !has(pageTexts(d2, 0), fmt.Sprint(tocPages+1)) {
		t.Errorf("page numbers should count the contents pages")
	}
}

func TestFooterOnRotatedPage(t *testing.T) {
	d := open(t, "chrome.pdf")
	p1 := d.Pages()[0].Ref.Num
	ops := []Op{
		{Op: "page.rotate", Page: p1, Rotate: 90},
		{Op: "hf", HF: []HFItem{{Text: "Подвал {page}", Position: "bottom-center", Size: 10, Offset: 20, Side: 40}}},
	}
	d2 := edited(t, d, ops)
	pg := layout.Analyze(d2, d2.Pages()[0], content.NewFontCache())
	for _, b := range pg.Blocks {
		if strings.HasPrefix(b.Text, "Подвал") {
			// Upright for the reader: text runs up the page in user space,
			// at the visible bottom, which is the right edge (large x).
			if math.Abs(b.Angle-90) > 0.5 || b.BBox[0] < 500 {
				t.Errorf("footer on rotated page: angle %.1f bbox %v", b.Angle, b.BBox)
			}
			return
		}
	}
	t.Error("footer not found")
}

// Original blocks of a page copied from another PDF are edited like those
// of the document's own pages: found in the source, rewritten in the copy.
func TestEditImportedPage(t *testing.T) {
	d := open(t, "quartz.pdf")
	other, err := os.ReadFile("../../testdata/corpus/chrome.pdf")
	if err != nil {
		t.Skip(err)
	}
	testAssets["other.pdf"] = other
	src, _ := cos.Open(other)
	pg := layout.Analyze(src, src.Pages()[0], content.NewFontCache())
	site := find(t, pg, "Сайт исполнителя: example.com/contract.")
	heading := find(t, pg, "Договор оказания услуг № 42")
	p1 := d.Pages()[0].Ref.Num
	move := [6]float64{1, 0, 0, 1, 0, -100}
	ops := []Op{
		{Op: "page.import", Asset: "other.pdf", After: p1, NewIDs: []int{-10}, From: []int{0}},
		{Op: "delete", Page: -10, Blocks: []string{site.ID}},
		{Op: "transform", Page: -10, Blocks: []string{heading.ID}, Matrix: &move},
	}
	d2 := edited(t, d, ops)
	texts := pageTexts(d2, 1)
	if has(texts, "Сайт исполнителя") || !has(texts, "Договор оказания") {
		t.Errorf("imported page: %v", texts)
	}
	after := layout.Analyze(d2, d2.Pages()[1], content.NewFontCache())
	if got := find(t, after, heading.Text); math.Abs(got.Quad[1]-(heading.Quad[1]-100)) > 0.5 {
		t.Errorf("heading at %v, want moved down by 100 from %v", got.Quad[1], heading.Quad[1])
	}
}
