package edit

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/nav"
)

// PageSpec is a page of the edited document.
type PageSpec struct {
	ID     int         `json:"id"`   // original page object number, negative for inserted pages
	Orig   int         `json:"orig"` // index in the original document, -1 for inserted pages
	Media  [4]float64  `json:"media"`
	Crop   [4]float64  `json:"crop"`
	Rotate int         `json:"rotate"`
	Fit    *[6]float64 `json:"fit,omitempty"`    // content scaled into margins
	TOC    *TOC        `json:"toc,omitempty"`    // generated table of contents page
	Part   [2]int      `json:"part,omitempty"`   // TOC entries [from, to) on this page
	Import *Source     `json:"import,omitempty"` // page copied from another PDF
	dirty  bool        // attributes differ from the original page
}

// Source is a page of an uploaded PDF.
type Source struct {
	Asset string `json:"asset"`
	Index int    `json:"index"`
}

// Sources opens uploaded PDFs by asset name (nil when unavailable).
type Sources func(asset string) *cos.Document

// Box is the visible area: crop box clipped to the media box.
func (p PageSpec) Box() [4]float64 {
	b := [4]float64{max(p.Crop[0], p.Media[0]), max(p.Crop[1], p.Media[1]), min(p.Crop[2], p.Media[2]), min(p.Crop[3], p.Media[3])}
	if b[2] <= b[0] || b[3] <= b[1] {
		return p.Media
	}
	return b
}

// TOC settings for a generated contents page.
type TOC struct {
	Title    string  `json:"title"`
	MaxLevel int     `json:"maxLevel"` // 1..3
	Font     string  `json:"font,omitempty"`
	Size     float64 `json:"size,omitempty"`
}

// Label is a page label range (§12.4.2).
type Label struct {
	Start  int    `json:"start"`  // first page index of the range
	Style  string `json:"style"`  // D (1 2 3), R (I II), r (i ii), A (A B), a (a b), "" (prefix only)
	Prefix string `json:"prefix"` // e.g. "Прил. "
	First  int    `json:"first"`  // number of the first page, default 1
}

// HFItem is one header or footer text.
type HFItem struct {
	Text      string  `json:"text"`     // {page} {pages} {label} {title} {date}
	Position  string  `json:"position"` // top-left | top-center | top-right | bottom-left | bottom-center | bottom-right
	Font      string  `json:"font,omitempty"`
	Size      float64 `json:"size"`
	Color     string  `json:"color,omitempty"`
	Offset    float64 `json:"offset"` // pt from the top or bottom edge
	Side      float64 `json:"side"`   // pt from the left and right edges
	From      int     `json:"from"`   // first page number (1-based), 0 = from the start
	To        int     `json:"to"`     // last page number, 0 = to the end
	Parity    string  `json:"parity"` // all | odd | even
	SkipFirst bool    `json:"skipFirst"`
}

// Pages folds the page operations over the original page list. changed
// reports whether the page tree itself (order, inserts, deletes) changed.
func Pages(d *cos.Document, ops []Op, src Sources) (specs []PageSpec, changed bool) {
	for i, p := range d.Pages() {
		specs = append(specs, PageSpec{ID: p.Ref.Num, Orig: i, Media: p.MediaBox, Crop: p.CropBox, Rotate: p.Rotate})
	}
	find := func(id int) int {
		return slices.IndexFunc(specs, func(s PageSpec) bool { return s.ID == id })
	}
	after := func(id int) int { // insertion index after page id (0 = at start)
		if id == 0 {
			return 0
		}
		if i := find(id); i >= 0 {
			return i + 1
		}
		return len(specs)
	}
	for _, op := range ops {
		switch op.Op {
		case "page.insert", "toc":
			if find(op.NewID) >= 0 {
				if op.Op == "toc" { // re-run with new settings
					specs[find(op.NewID)].TOC = op.TOC
				}
				continue
			}
			w, h := op.Size[0], op.Size[1]
			if w <= 0 || h <= 0 {
				w, h = 595.28, 841.89 // A4
			}
			sp := PageSpec{ID: op.NewID, Orig: -1, Media: [4]float64{0, 0, w, h}, Crop: [4]float64{0, 0, w, h}, TOC: op.TOC, dirty: true}
			specs = slices.Insert(specs, after(op.After), sp)
			changed = true
		case "page.import":
			var doc *cos.Document
			if src != nil {
				doc = src(op.Asset)
			}
			if doc == nil {
				continue
			}
			pages := doc.Pages()
			from := op.From
			if len(from) == 0 {
				for i := range pages {
					from = append(from, i)
				}
			}
			at := after(op.After)
			for k, i := range from {
				if i < 0 || i >= len(pages) || k >= len(op.NewIDs) || find(op.NewIDs[k]) >= 0 {
					continue
				}
				p := pages[i]
				specs = slices.Insert(specs, at, PageSpec{ID: op.NewIDs[k], Orig: -1, Media: p.MediaBox, Crop: p.CropBox, Rotate: p.Rotate,
					Import: &Source{Asset: op.Asset, Index: i}, dirty: true})
				at++
				changed = true
			}
		case "page.delete":
			if i := find(op.Page); i >= 0 && len(specs) > 1 {
				specs = slices.Delete(specs, i, i+1)
				changed = true
			}
		case "page.move":
			if i := find(op.Page); i >= 0 && op.After != op.Page {
				sp := specs[i]
				specs = slices.Delete(specs, i, i+1)
				specs = slices.Insert(specs, after(op.After), sp)
				changed = true
			}
		case "page.rotate":
			if i := find(op.Page); i >= 0 {
				specs[i].Rotate = ((specs[i].Rotate+op.Rotate)%360 + 360) % 360
				specs[i].dirty = true
			}
		case "page.margins":
			for i := range specs {
				if len(op.Pages) > 0 && !slices.Contains(op.Pages, specs[i].ID) {
					continue
				}
				t, r, b, l := op.Margins[0], op.Margins[1], op.Margins[2], op.Margins[3]
				sp := &specs[i]
				box := sp.Box()
				switch op.Mode {
				case "resize": // the sheet grows or shrinks, content stays
					nb := [4]float64{box[0] - l, box[1] - b, box[2] + r, box[3] + t}
					if nb[2]-nb[0] > 10 && nb[3]-nb[1] > 10 {
						sp.Media, sp.Crop = nb, nb
					}
				case "fit": // the content shrinks into the margins, sheet stays
					w, h := box[2]-box[0], box[3]-box[1]
					tw, th := w-l-r, h-t-b
					if tw <= 10 || th <= 10 {
						continue
					}
					s := min(tw/w, th/h)
					e := box[0] + l + (tw-s*w)/2 - s*box[0]
					f := box[1] + b + (th-s*h)/2 - s*box[1]
					m := [6]float64{s, 0, 0, s, e, f}
					if sp.Fit != nil {
						m = mul(*sp.Fit, m)
					}
					sp.Fit = &m
				}
				sp.dirty = true
			}
		}
	}
	specs = expandTOC(d, ops, specs)
	return specs, changed
}

const tocMargin = 64.0

func tocSize(t *TOC) float64 {
	if t.Size > 0 {
		return t.Size
	}
	return 12
}

// tocLines is how many entries fit on a contents page (the first one also
// carries the title).
func tocLines(t *TOC, h float64, first bool) int {
	size := tocSize(t)
	avail := h - 2*tocMargin - 1.4*size
	if first {
		avail -= 3 * size
	}
	return max(1, int(avail/(1.6*size))+1)
}

// tocPartID names continuation pages of a contents block.
func tocPartID(id, k int) int { return -(1_000_000_000 + (-id)*1000 + k) }

// expandTOC splits contents pages over as many pages as the final outline
// needs; continuation pages follow the first one.
func expandTOC(d *cos.Document, ops []Op, specs []PageSpec) []PageSpec {
	var outline []nav.Outline
	loaded := false
	for i := 0; i < len(specs); i++ {
		sp := specs[i]
		if sp.TOC == nil || sp.Part != [2]int{} {
			continue
		}
		if !loaded {
			outline = nav.ReadOutline(d, d.Dict(d.Trailer["Root"]))
			for _, op := range ops {
				if op.Op == "outline" {
					outline = op.Outline
				}
			}
			loaded = true
		}
		var entries []tocEntry
		tocItems(outline, 0, min(3, max(1, sp.TOC.MaxLevel)), &entries)
		h := sp.Box()[3] - sp.Box()[1]
		n := min(len(entries), tocLines(sp.TOC, h, true))
		specs[i].Part = [2]int{0, max(n, 0)}
		for k, from := 1, n; from < len(entries); k++ {
			to := min(len(entries), from+tocLines(sp.TOC, h, false))
			part := sp
			part.ID, part.Part = tocPartID(sp.ID, k), [2]int{from, to}
			specs = slices.Insert(specs, i+k, part)
			from = to
		}
	}
	return specs
}

func mul(a, b [6]float64) [6]float64 {
	return [6]float64{
		a[0]*b[0] + a[1]*b[2], a[0]*b[1] + a[1]*b[3],
		a[2]*b[0] + a[3]*b[2], a[2]*b[1] + a[3]*b[3],
		a[4]*b[0] + a[5]*b[2] + b[4], a[4]*b[1] + a[5]*b[3] + b[5],
	}
}

func roman(n int) string {
	if n <= 0 || n >= 4000 {
		return strconv.Itoa(n)
	}
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for n >= v {
			sb.WriteString(syms[i])
			n -= v
		}
	}
	return sb.String()
}

func letters(n int) string { // 1→A, 26→Z, 27→AA (§12.4.2)
	if n <= 0 {
		return ""
	}
	return strings.Repeat(string(rune('A'+(n-1)%26)), (n-1)/26+1)
}

// LabelOf formats the label of page index i.
func LabelOf(labels []Label, i int) string {
	var cur *Label
	for k := range labels {
		if labels[k].Start <= i && (cur == nil || labels[k].Start >= cur.Start) {
			cur = &labels[k]
		}
	}
	if cur == nil {
		return strconv.Itoa(i + 1)
	}
	n := i - cur.Start + max(1, cur.First)
	switch cur.Style {
	case "D":
		return cur.Prefix + strconv.Itoa(n)
	case "R":
		return cur.Prefix + roman(n)
	case "r":
		return cur.Prefix + strings.ToLower(roman(n))
	case "A":
		return cur.Prefix + letters(n)
	case "a":
		return cur.Prefix + strings.ToLower(letters(n))
	}
	return cur.Prefix
}

// ReadLabels reads /PageLabels (a number tree, flattened).
func ReadLabels(g cos.Getter, catalog cos.Dict) []Label {
	var out []Label
	var walk func(node cos.Dict, depth int)
	walk = func(node cos.Dict, depth int) {
		if node == nil || depth > 16 {
			return
		}
		nums := g.Array(node["Nums"])
		for i := 0; i+1 < len(nums); i += 2 {
			start, _ := g.Int(nums[i])
			d := g.Dict(nums[i+1])
			l := Label{Start: start, Style: string(g.Name(d["S"])), First: 1}
			if p, ok := g.Resolve(d["P"]).(cos.String); ok {
				l.Prefix = cos.Text(p)
			}
			if st, ok := g.Int(d["St"]); ok {
				l.First = st
			}
			out = append(out, l)
		}
		for _, k := range g.Array(node["Kids"]) {
			walk(g.Dict(k), depth+1)
		}
	}
	walk(g.Dict(catalog["PageLabels"]), 0)
	slices.SortFunc(out, func(a, b Label) int { return a.Start - b.Start })
	return out
}

func writeLabels(u *cos.Update, labels []Label) {
	rootRef, _ := u.Root().(cos.Ref)
	cat := cos.Copy(u.Dict(rootRef)).(cos.Dict)
	if len(labels) == 0 {
		delete(cat, "PageLabels")
	} else {
		sorted := slices.Clone(labels)
		slices.SortFunc(sorted, func(a, b Label) int { return a.Start - b.Start })
		var nums cos.Array
		for _, l := range sorted {
			d := cos.Dict{}
			if l.Style != "" {
				d["S"] = cos.Name(l.Style)
			}
			if l.Prefix != "" {
				d["P"] = cos.TextString(l.Prefix)
			}
			if l.First > 1 {
				d["St"] = cos.Int(l.First)
			}
			nums = append(nums, cos.Int(l.Start), d)
		}
		cat["PageLabels"] = cos.Dict{"Nums": nums}
	}
	u.Set(rootRef, cat)
}

// hfObjects lays out headers and footers on page i of n.
// visualFrame describes the page as the reader sees it after /Rotate:
// its width and height, and the matrix from that upright frame (origin at
// the visible bottom-left) to user space.
func visualFrame(box [4]float64, rotate int) (w, h float64, m content.Matrix) {
	bw, bh := box[2]-box[0], box[3]-box[1]
	switch rotate {
	case 90:
		return bh, bw, content.Matrix{0, 1, -1, 0, box[0] + bw, box[1]}
	case 180:
		return bw, bh, content.Matrix{-1, 0, 0, -1, box[2], box[3]}
	case 270:
		return bh, bw, content.Matrix{0, -1, 1, 0, box[0], box[3]}
	}
	return bw, bh, content.Matrix{1, 0, 0, 1, box[0], box[1]}
}

func hfObjects(items []HFItem, sp PageSpec, i, n int, labels []Label, title string) []*Object {
	var out []*Object
	vw, vh, frame := visualFrame(sp.Box(), sp.Rotate)
	for k, it := range items {
		num := i + 1
		switch {
		case it.From > 0 && num < it.From, it.To > 0 && num > it.To:
			continue
		case it.SkipFirst && i == 0:
			continue
		case it.Parity == "odd" && num%2 == 0, it.Parity == "even" && num%2 == 1:
			continue
		}
		text := strings.NewReplacer("{page}", strconv.Itoa(num), "{pages}", strconv.Itoa(n), "{label}", LabelOf(labels, i),
			"{title}", title, "{date}", time.Now().Format("02.01.2006")).Replace(it.Text)
		size := it.Size
		if size <= 0 {
			size = 10
		}
		h := size * 1.4
		pos := strings.SplitN(it.Position, "-", 2)
		if len(pos) != 2 {
			pos = []string{"bottom", "center"}
		}
		top := vh - it.Offset
		kind := "Header"
		if pos[0] == "bottom" {
			top = it.Offset + h
			kind = "Footer"
		}
		place := content.Matrix{1, 0, 0, 1, it.Side, top}.Mul(frame)
		o := &Object{ID: fmt.Sprintf("hf%d", k), Kind: "text", Matrix: [6]float64(place),
			W: max(10, vw-2*it.Side), H: h, Text: text, Font: it.Font, Size: size, Color: it.Color,
			Align: pos[1], VAlign: "middle", Opacity: 1, Artifact: kind}
		if o.Align == "center" || o.Align == "right" || o.Align == "left" {
			out = append(out, o)
		}
	}
	return out
}

// tocItems flattens the outline down to maxLevel.
func tocItems(items []nav.Outline, level, maxLevel int, out *[]tocEntry) {
	for _, it := range items {
		if level < maxLevel {
			*out = append(*out, tocEntry{it, level})
			tocItems(it.Items, level+1, maxLevel, out)
		}
	}
}

type tocEntry struct {
	nav.Outline
	level int
}

// tocPage lays out a contents page: the title (first page only), then one
// line per bookmark with dot leaders and the page number, each a link.
func (r *renderer) tocPage(sp PageSpec, outline []nav.Outline, specs []PageSpec, labels []Label) ([]*Object, []*nav.Link, error) {
	t := sp.TOC
	face, err := r.env.Face(t.Font)
	if err != nil {
		return nil, nil, err
	}
	size := t.Size
	if size <= 0 {
		size = 12
	}
	maxLevel := min(3, max(1, t.MaxLevel))
	var entries []tocEntry
	tocItems(outline, 0, maxLevel, &entries)
	box := sp.Box()
	const margin = tocMargin
	left, right := box[0]+margin, box[2]-margin
	y := box[3] - margin
	title := t.Title
	if title == "" {
		title = "Содержание"
	}
	var objs []*Object
	if sp.Part[0] == 0 {
		objs = append(objs, &Object{ID: "toc-title", Kind: "text", Matrix: [6]float64{1, 0, 0, 1, left, y}, W: right - left, H: size * 2.2,
			Text: title, Font: t.Font, Size: size * 1.6, Opacity: 1})
		y -= size * 3
	}
	var links []*nav.Link
	dot := face.TextWidth(".") / 1000 * size
	from, to := sp.Part[0], min(sp.Part[1], len(entries))
	if to <= from && from == 0 {
		to = len(entries)
	}
	for k := from; k < to; k++ {
		e := entries[k]
		if y-size*1.4 < box[1]+margin {
			break
		}
		idx := slices.IndexFunc(specs, func(s PageSpec) bool { return s.ID == e.Page })
		if idx < 0 {
			continue
		}
		num := LabelOf(labels, idx)
		indent := float64(e.level) * size * 1.5
		numW := face.TextWidth(num)/1000*size + 2
		titleText := e.Title
		avail := right - left - indent - numW - 3*dot
		for face.TextWidth(titleText)/1000*size > avail && len([]rune(titleText)) > 1 { // shorten long titles
			rs := []rune(titleText)
			titleText = string(rs[:len(rs)-2]) + "…"
		}
		tw := face.TextWidth(titleText)/1000*size + dot
		dots := strings.Repeat(".", max(0, int((right-numW-(left+indent+tw))/dot)))
		h := size * 1.4
		objs = append(objs,
			&Object{ID: fmt.Sprintf("toc%d", k), Kind: "text", Matrix: [6]float64{1, 0, 0, 1, left + indent, y}, W: right - left - indent, H: h,
				Text: titleText + " " + dots, Font: t.Font, Size: size, Opacity: 1},
			&Object{ID: fmt.Sprintf("tocn%d", k), Kind: "text", Matrix: [6]float64{1, 0, 0, 1, right - numW, y}, W: numW, H: h,
				Text: num, Font: t.Font, Size: size, Align: "right", Opacity: 1})
		links = append(links, &nav.Link{ID: fmt.Sprintf("ltoc%d", k), Kind: "page", Page: e.Page, Top: e.Top,
			Quad: [8]float64{left + indent, y - h, right, y - h, right, y, left + indent, y}})
		y -= size * 1.6
	}
	return objs, links, nil
}

func rectArray(r [4]float64) cos.Array {
	return cos.Array{cos.Real(r[0]), cos.Real(r[1]), cos.Real(r[2]), cos.Real(r[3])}
}
