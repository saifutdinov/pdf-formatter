package edit

import (
	"math"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/layout"
	"pdf-formatter/pdf/nav"
)

func TestLinksAndOutline(t *testing.T) {
	d := open(t, "chrome.pdf")
	p1, p2 := d.Pages()[0], d.Pages()[1]
	orig := nav.ReadLinks(d, d.Dict(d.Trailer["Root"]), p1.Dict)
	if len(orig) == 0 {
		t.Fatal("chrome.pdf should have links")
	}
	var uri *nav.Link
	for i := range orig {
		if orig[i].Kind == "uri" {
			uri = &orig[i]
		}
	}
	if uri == nil || uri.URI != "https://example.com/contract" {
		t.Fatalf("original links: %+v", orig)
	}
	before := layout.Analyze(d, p1, content.NewFontCache())
	h := find(t, before, heading)
	top := 700.0
	ops := []Op{
		{Op: "link", Page: p1.Ref.Num, Link: &nav.Link{ID: "l1", Kind: "page", Page: p2.Ref.Num, Top: &top, Attach: h.ID}},
		{Op: "transform", Page: p1.Ref.Num, Blocks: []string{h.ID}, Matrix: &[6]float64{1, 0, 0, 1, 0, -400}},
		{Op: "link", Page: p1.Ref.Num, Link: &nav.Link{ID: "l2", Kind: "uri", URI: "https://example.org", Quad: [8]float64{50, 50, 150, 50, 150, 70, 50, 70}}},
		{Op: "delete", Page: p1.Ref.Num, Blocks: []string{uri.ID}},
		{Op: "outline", Outline: []nav.Outline{
			{Title: "Договор", Page: p1.Ref.Num, Open: true, Items: []nav.Outline{{Title: "1. Стоимость", Page: p1.Ref.Num, Top: &top}}},
			{Title: "2. Порядок расчётов", Page: p2.Ref.Num},
		}},
	}
	d2 := edited(t, d, ops)
	cat := d2.Dict(d2.Trailer["Root"])
	links := nav.ReadLinks(d2, cat, d2.Pages()[0].Dict)
	var toPage, toURI *nav.Link
	for i, l := range links {
		switch {
		case l.Kind == "uri" && l.URI == "https://example.com/contract":
			t.Error("deleted link survived")
		case l.Kind == "uri":
			toURI = &links[i]
		case l.Kind == "page" && l.Page == d2.Pages()[1].Ref.Num:
			toPage = &links[i]
		}
	}
	if toURI == nil || toPage == nil {
		t.Fatalf("links after edit: %+v", links)
	}
	// The page link follows the moved heading.
	want := nav.QuadRect(MapQuad(h.Quad, content.Matrix{1, 0, 0, 1, 0, -400}))
	got := nav.QuadRect(toPage.Quad)
	for i := range want {
		if math.Abs(want[i]-got[i]) > 0.1 {
			t.Fatalf("attached link rect %v, want %v", got, want)
		}
	}
	if toPage.Top == nil || *toPage.Top != 700 {
		t.Errorf("destination top %v", toPage.Top)
	}
	ol := nav.ReadOutline(d2, cat)
	if len(ol) != 2 || ol[0].Title != "Договор" || len(ol[0].Items) != 1 || ol[0].Items[0].Title != "1. Стоимость" || !ol[0].Open || ol[1].Page != d2.Pages()[1].Ref.Num {
		t.Errorf("outline: %+v", ol)
	}
	if d2.Name(cat["PageMode"]) != "UseOutlines" {
		t.Error("PageMode not set")
	}
}
