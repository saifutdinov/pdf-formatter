// Package layout turns a page's painted items into editable blocks:
// text runs → lines → paragraphs, images, and groups of vector paths.
package layout

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/font"
)

type Page struct {
	Ops     []content.Op
	Items   []content.Item
	Blocks  []Block
	FontUse map[cos.Ref]*content.FontUse
}

type Block struct {
	ID        string
	Kind      string // text, image, path
	Items     []int  // indices into Page.Items
	Quad      [8]float64
	BBox      content.Rect
	Angle     float64   // degrees, counter-clockwise
	Text      string    // as shown: visual lines joined by '\n'
	Flow      string    // for re-wrapping: soft line breaks joined, hard ones kept as '\n'
	Runs      []TextRun // Flow by the item each part comes from (styles of the words)
	Font      *font.Font
	Size      float64
	Color     [3]float64
	Invisible bool
	Artifact  bool
	InForm    bool
}

// Analyze interprets a page and segments it into blocks.
func Analyze(g cos.Getter, p cos.Page, fonts *content.FontCache) *Page {
	ops := content.Parse(content.PageContent(g, p.Dict))
	in := content.NewInterp(g, fonts)
	in.Run(ops, p.Resources, content.Identity)
	pg := &Page{Ops: ops, Items: in.Items, FontUse: in.FontUse}
	area := (p.MediaBox[2] - p.MediaBox[0]) * (p.MediaBox[3] - p.MediaBox[1])
	pg.Blocks = append(pg.Blocks, textBlocks(pg.Items)...)
	pg.Blocks = append(pg.Blocks, pathBlocks(pg.Items, area)...)
	for i, it := range pg.Items {
		if it.Kind == content.Image {
			pg.Blocks = append(pg.Blocks, newBlock("image", pg.Items, []int{i}))
		}
	}
	// Stable order: by first op, so ids and z-order follow the content stream.
	slices.SortFunc(pg.Blocks, func(a, b Block) int { return cmp.Compare(a.ID, b.ID) })
	return pg
}

// ItemID names an item by its painting op (inside forms: the Do ops on
// the way); a block's id is the id of its first item.
func ItemID(it content.Item) string {
	var sb strings.Builder
	for _, f := range it.Form {
		fmt.Fprintf(&sb, "%06d.", f)
	}
	fmt.Fprintf(&sb, "%06d", it.Op)
	return sb.String()
}

func newBlock(kind string, items []content.Item, idx []int) Block {
	b := Block{Kind: kind, Items: idx}
	first := items[idx[0]]
	b.ID = ItemID(first)
	r := first.BBox
	for _, i := range idx {
		r = r.Union(items[i].BBox)
		b.InForm = b.InForm || len(items[i].Form) > 0
		b.Artifact = b.Artifact || items[i].Artifact
	}
	b.BBox = r
	b.Quad = [8]float64{r[0], r[1], r[2], r[1], r[2], r[3], r[0], r[3]}
	if kind == "image" {
		b.Quad = first.Quad
	}
	if kind == "path" { // the colour painted last: a node's fill, a grid's lines
		last := items[idx[len(idx)-1]]
		b.Color = last.State.Fill
		if last.Lines != nil {
			b.Color = last.State.Stroke
		}
	}
	return b
}

// TextRun is a part of a block's text and the item it was shown by; spaces
// and line breaks added between items belong to the part before them.
type TextRun struct {
	Item int
	Text string
}

type run struct {
	item      int
	u0, u1, v float64 // along / across the baseline in the rotated frame
	size      float64
	angle     float64
	asc, desc float64
	bold      bool
	blank     bool // whitespace only: joins a line, never starts one
}

type line struct {
	runs   []run
	u0, u1 float64
	v      float64
	size   float64         // largest: sets the baseline
	chars  map[float64]int // characters per font size
	body   float64         // the size most of the line is set in
	angle  float64
	asc    float64
	desc   float64
	bold   bool
}

func textBlocks(items []content.Item) []Block {
	var runs, blanks []run
	for i, it := range items {
		if it.Kind != content.Text || it.Text == "" {
			continue
		}
		theta := math.Atan2(it.TM[1], it.TM[0])
		c, s := math.Cos(theta), math.Sin(theta)
		ox, oy := it.TM[4], it.TM[5]
		u := ox*c + oy*s
		v := -ox*s + oy*c
		scale := math.Hypot(it.TM[0], it.TM[1])
		w := it.Width * scale
		r := run{item: i, u0: u, u1: u + w, v: v, size: it.Size, angle: theta, bold: it.State.Font != nil && it.State.Font.Bold}
		if f := it.State.Font; f != nil {
			r.asc, r.desc = f.Ascent*it.Size, f.Descent*it.Size
		}
		if w < 0 {
			r.u0, r.u1 = r.u1, r.u0
		}
		if strings.TrimSpace(it.Text) == "" {
			r.blank = true
			blanks = append(blanks, r)
			continue
		}
		runs = append(runs, r)
	}
	angleKey := func(a float64) int { return int(math.Round(a * 180 / math.Pi)) }
	slices.SortStableFunc(runs, func(a, b run) int {
		if c := cmp.Compare(angleKey(a.angle), angleKey(b.angle)); c != 0 {
			return c
		}
		if math.Abs(a.v-b.v) > 0.3*min(a.size, b.size) {
			return cmp.Compare(b.v, a.v)
		}
		return cmp.Compare(a.u0, b.u0)
	})

	// Runs → lines: same angle, close baselines, small horizontal gap.
	var lines []*line
	for _, r := range runs {
		var best *line
		for k := len(lines) - 1; k >= 0 && k >= len(lines)-12; k-- {
			l := lines[k]
			if angleKey(l.angle) != angleKey(r.angle) {
				continue
			}
			sz := max(l.size, r.size)
			if math.Abs(l.v-r.v) > 0.5*sz || r.u0 > l.u1+1.5*sz || r.u1 < l.u0-0.5*sz || overlaps(l, r) {
				continue
			}
			best = l
			break
		}
		if best == nil {
			best = &line{u0: r.u0, u1: r.u1, v: r.v, size: r.size, angle: r.angle, asc: r.asc, desc: r.desc, bold: r.bold}
			lines = append(lines, best)
		}
		best.runs = append(best.runs, r)
		if best.chars == nil {
			best.chars = map[float64]int{}
		}
		best.chars[math.Round(r.size*10)/10] += utf8.RuneCountInString(items[r.item].Text)
		best.u0, best.u1 = min(best.u0, r.u0), max(best.u1, r.u1)
		if r.size > best.size { // superscripts do not move the baseline
			best.size, best.v = r.size, r.v
		}
		best.asc, best.desc = max(best.asc, r.asc), min(best.desc, r.desc)
		best.bold = best.bold && r.bold
	}
	// Separately shown spaces go to the nearest line on their baseline, so
	// double spaces and indents survive; they do not widen the line's box.
	for _, r := range blanks {
		var best *line
		bestD := math.Inf(1)
		for _, l := range lines {
			sz := max(l.size, r.size)
			if angleKey(l.angle) != angleKey(r.angle) || math.Abs(l.v-r.v) > 0.5*sz || r.u0 > l.u1+1.5*sz || r.u1 < l.u0-0.5*sz {
				continue
			}
			if d := max(0, r.u0-l.u1, l.u0-r.u1) + math.Abs(l.v-r.v); d < bestD {
				best, bestD = l, d
			}
		}
		if best != nil {
			best.runs = append(best.runs, r)
		}
	}

	for _, l := range lines {
		for s, w := range l.chars {
			if w > l.chars[l.body] || w == l.chars[l.body] && s > l.body {
				l.body = s
			}
		}
	}

	// Lines → paragraphs: stacked lines with similar size and overlap; a
	// larger word or two does not make a line a heading.
	slices.SortStableFunc(lines, func(a, b *line) int {
		if c := cmp.Compare(angleKey(a.angle), angleKey(b.angle)); c != 0 {
			return c
		}
		return cmp.Compare(b.v, a.v)
	})
	type para struct {
		lines  []*line
		u0, u1 float64
	}
	var paras []*para
	for _, l := range lines {
		var best *para
		for _, p := range paras {
			last := p.lines[len(p.lines)-1]
			if angleKey(last.angle) != angleKey(l.angle) || last.bold != l.bold {
				continue
			}
			dv := last.v - l.v
			ratio := l.body / last.body
			overlap := min(p.u1, l.u1) - max(p.u0, l.u0)
			if dv > 0.5*l.size && dv < 1.9*max(l.size, last.size) && ratio > 0.8 && ratio < 1.25 && overlap > 0 {
				best = p
			}
		}
		if best == nil {
			best = &para{u0: l.u0, u1: l.u1}
			paras = append(paras, best)
		}
		best.lines = append(best.lines, l)
		best.u0, best.u1 = min(best.u0, l.u0), max(best.u1, l.u1)
	}

	var out []Block
	for _, p := range paras {
		var idx []int
		var parts []TextRun
		texts := make([]string, len(p.lines))
		for k, l := range p.lines {
			slices.SortFunc(l.runs, func(a, b run) int { return cmp.Compare(a.u0, b.u0) })
			var sb strings.Builder
			for j, r := range l.runs {
				t := items[r.item].Text
				// A visible gap between runs is a space unless the text
				// already has whitespace there.
				if j > 0 && r.u0-l.runs[j-1].u1 > 0.2*r.size && !endsSpace(sb.String()) && !startsSpace(t) {
					sb.WriteByte(' ')
					parts[len(parts)-1].Text += " "
				}
				sb.WriteString(t)
				parts = append(parts, TextRun{r.item, t})
				idx = append(idx, r.item)
			}
			texts[k] = sb.String()
			if k > 0 {
				// The break before this line goes to the last part of the previous one.
				first := len(parts) - len(l.runs) - 1
				parts[first].Text += lineBreak(texts[k-1], texts[k], p.u1-p.lines[k-1].u1, firstWord(items, l))
			}
		}
		var flow strings.Builder
		for _, part := range parts {
			flow.WriteString(part.Text)
		}
		slices.Sort(idx)
		b := newBlock("text", items, idx)
		first := p.lines[0]
		last := p.lines[len(p.lines)-1]
		theta := first.angle
		c, s := math.Cos(theta), math.Sin(theta)
		top := first.v + first.asc
		bottom := last.v + last.desc
		corner := func(u, v float64) (float64, float64) { return u*c - v*s, u*s + v*c }
		b.Quad[0], b.Quad[1] = corner(p.u0, bottom)
		b.Quad[2], b.Quad[3] = corner(p.u1, bottom)
		b.Quad[4], b.Quad[5] = corner(p.u1, top)
		b.Quad[6], b.Quad[7] = corner(p.u0, top)
		b.Angle = theta * 180 / math.Pi
		b.Text = strings.Join(texts, "\n")
		b.Flow = flow.String()
		b.Runs = parts
		lead := items[idx[0]]
		b.Font = lead.State.Font
		b.Size = first.size
		b.Color = lead.State.Fill
		b.Invisible = lead.State.Render == 3 || lead.State.Render == 7
		out = append(out, b)
	}
	return out
}

// overlaps reports whether r is drawn over text already on the line: a
// separate label (chart captions next to each other), not a continuation.
func overlaps(l *line, r run) bool {
	for _, x := range l.runs {
		if min(x.u1, r.u1)-max(x.u0, r.u0) > 0.3*min(x.size, r.size) {
			return true
		}
	}
	return false
}

func startsSpace(s string) bool { r, _ := utf8.DecodeRuneInString(s); return unicode.IsSpace(r) }
func endsSpace(s string) bool   { r, _ := utf8.DecodeLastRuneInString(s); return unicode.IsSpace(r) }

// firstWord estimates the width of a line's first word and the space
// before it, from the average glyph width of its first visible run.
func firstWord(items []content.Item, l *line) float64 {
	for _, r := range l.runs {
		if r.blank {
			continue
		}
		t := items[r.item].Text
		word := strings.FieldsFunc(t, unicode.IsSpace)
		if len(word) == 0 {
			return 0
		}
		avg := (r.u1 - r.u0) / float64(utf8.RuneCountInString(t))
		return avg*float64(utf8.RuneCountInString(word[0])) + 0.3*r.size
	}
	return 0
}

// lineBreak is what joins two visual lines when the paragraph is wrapped
// anew: nothing where the text already breaks (whitespace, a hyphen at the
// end), '\n' where the line ends early although the next word would fit
// (a deliberate break), otherwise a space.
func lineBreak(prev, next string, slack, word float64) string {
	if endsSpace(prev) || startsSpace(next) {
		return ""
	}
	if r := []rune(prev); len(r) >= 2 && (r[len(r)-1] == '-' && unicode.IsLetter(r[len(r)-2]) || r[len(r)-1] == '\u00ad') {
		return ""
	}
	if word > 0 && slack > word {
		return "\n"
	}
	return " "
}

// pathBlocks groups touching vector paths. Rules and boxes (horizontal and
// vertical pieces) join whatever they touch: a table grid is one block.
// Other pieces join only the group drawn just before them, so a chart's
// nodes and edges, separate shapes that touch, stay separate. Paths
// covering most of the page (backgrounds) stay on their own.
func pathBlocks(items []content.Item, pageArea float64) []Block {
	type group struct {
		items []int
		box   content.Rect
		axis  bool
	}
	big := func(r content.Rect) bool { return (r[2]-r[0])*(r[3]-r[1]) > 0.8*pageArea }
	var groups []*group
	var last *group // took the previous path
	for i, it := range items {
		if it.Kind != content.Path {
			continue
		}
		if big(it.BBox) {
			groups = append(groups, &group{items: []int{i}})
			last = nil
			continue
		}
		var hit []*group
		for _, g := range groups {
			if g == nil || g.items == nil || g != last && !(g.axis && it.Axis) || !touches(content.Item{BBox: g.box}, content.Item{BBox: it.BBox}) {
				continue
			}
			// ponytail: compares with every path of the group, newest first;
			// fine for hundreds of pieces, index them if drawings get huge.
			for k := len(g.items) - 1; k >= 0; k-- {
				if touches(items[g.items[k]], it) {
					hit = append(hit, g)
					break
				}
			}
		}
		if len(hit) == 0 {
			last = &group{items: []int{i}, box: it.BBox, axis: it.Axis}
			groups = append(groups, last)
			continue
		}
		last = hit[0]
		for _, g := range hit[1:] { // the path connects them
			last.items = append(last.items, g.items...)
			last.box, last.axis = last.box.Union(g.box), last.axis && g.axis
			g.items = nil
		}
		last.items = append(last.items, i)
		last.box, last.axis = last.box.Union(it.BBox), last.axis && it.Axis
	}
	out := make([]Block, 0, len(groups))
	for _, g := range groups {
		if g.items != nil {
			slices.Sort(g.items)
			out = append(out, newBlock("path", items, g.items))
		}
	}
	return out
}

// touches reports whether two painted paths meet: by bounding box, and for
// strokes (a diagonal line's box is mostly empty) by the stroked segments.
func touches(a, b content.Item) bool {
	const pad = 1.0
	r, q := a.BBox, b.BBox
	if r[0]-pad > q[2] || q[0]-pad > r[2] || r[1]-pad > q[3] || q[1]-pad > r[3] {
		return false
	}
	if a.Lines == nil {
		a, b = b, a
	}
	switch {
	case a.Lines == nil:
		return true
	case b.Lines == nil:
		d := pad + a.HalfWidth
		box := content.Rect{b.BBox[0] - d, b.BBox[1] - d, b.BBox[2] + d, b.BBox[3] + d}
		return anySegment(a.Lines, func(p, q [2]float64) bool { return segmentInRect(p, q, box) })
	}
	d := pad + a.HalfWidth + b.HalfWidth
	return anySegment(a.Lines, func(p, q [2]float64) bool {
		return anySegment(b.Lines, func(r, s [2]float64) bool { return segmentDist(p, q, r, s) <= d })
	})
}

// anySegment calls f for the segments of subpaths (a lone point is a
// zero-length segment) until it returns true.
func anySegment(lines [][]float64, f func(p, q [2]float64) bool) bool {
	for _, l := range lines {
		if len(l) == 2 && f([2]float64{l[0], l[1]}, [2]float64{l[0], l[1]}) {
			return true
		}
		for k := 0; k+3 < len(l); k += 2 {
			if f([2]float64{l[k], l[k+1]}, [2]float64{l[k+2], l[k+3]}) {
				return true
			}
		}
	}
	return false
}

func inRect(p [2]float64, r content.Rect) bool {
	return p[0] >= r[0] && p[0] <= r[2] && p[1] >= r[1] && p[1] <= r[3]
}

func segmentInRect(p, q [2]float64, r content.Rect) bool {
	if inRect(p, r) || inRect(q, r) {
		return true
	}
	c := [4][2]float64{{r[0], r[1]}, {r[2], r[1]}, {r[2], r[3]}, {r[0], r[3]}}
	for k := range c {
		if segmentDist(p, q, c[k], c[(k+1)%4]) == 0 {
			return true
		}
	}
	return false
}

// segmentDist is the distance between segments pq and rs.
func segmentDist(p, q, r, s [2]float64) float64 {
	cross := func(o, a, b [2]float64) float64 { return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0]) }
	d1, d2, d3, d4 := cross(r, s, p), cross(r, s, q), cross(p, q, r), cross(p, q, s)
	if (d1 > 0) != (d2 > 0) && (d3 > 0) != (d4 > 0) && d1 != 0 && d2 != 0 && d3 != 0 && d4 != 0 {
		return 0
	}
	return min(pointDist(p, r, s), pointDist(q, r, s), pointDist(r, p, q), pointDist(s, p, q))
}

// pointDist is the distance from p to segment ab.
func pointDist(p, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = max(0, min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/l))
	}
	return math.Hypot(p[0]-a[0]-t*dx, p[1]-a[1]-t*dy)
}
