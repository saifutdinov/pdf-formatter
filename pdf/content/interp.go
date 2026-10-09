package content

import (
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/font"
)

// Matrix is a PDF transformation [a b c d e f] (§8.3.3).
type Matrix [6]float64

var Identity = Matrix{1, 0, 0, 1, 0, 0}

// Mul returns m×n: apply m first, then n.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m Matrix) Apply(x, y float64) (float64, float64) {
	return x*m[0] + y*m[2] + m[4], x*m[1] + y*m[3] + m[5]
}

func (m Matrix) Invert() (Matrix, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if math.Abs(det) < 1e-12 {
		return Identity, false
	}
	return Matrix{
		m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det,
	}, true
}

// Rect is [x0 y0 x1 y1].
type Rect [4]float64

var emptyRect = Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}

func (r *Rect) add(x, y float64) {
	r[0], r[1] = min(r[0], x), min(r[1], y)
	r[2], r[3] = max(r[2], x), max(r[3], y)
}

func (r Rect) Empty() bool { return r[0] > r[2] || r[1] > r[3] }

func (r Rect) Union(o Rect) Rect {
	return Rect{min(r[0], o[0]), min(r[1], o[1]), max(r[2], o[2]), max(r[3], o[3])}
}

// State is the graphics state (§8.4) plus the text state (§9.3). It is a
// value type: slices are never appended in place, so a struct copy is a
// cheap snapshot. The *Ops fields remember the operators that produced the
// state so it can be re-emitted when a segment is moved.
type State struct {
	CTM         Matrix
	Fill        [3]float64 // approximate RGB, 0..1
	Stroke      [3]float64
	FillOps     []Op // cs + colour operator(s)
	StrokeOps   []Op
	LineWidth   float64
	LineOps     []Op // w J j M d ri i
	GSOps       []Op // gs, in order
	ClipOps     []Op // path + W/W* + n, for each clip in effect
	fillN       int  // components of the fill colour space
	strokeN     int
	Font        *font.Font
	FontRes     cos.Name
	FontRef     cos.Ref // font dictionary, when indirect
	FontSize    float64
	CharSpace   float64
	WordSpace   float64
	HScale      float64 // 1 = 100%
	Leading     float64
	Rise        float64
	Render      int
	TextOps     []Op // Tc Tw Tz TL Ts Tr (Tf is re-emitted from FontRes/FontSize)
	MarkedStack []Op // open BDC/BMC
}

func newState(ctm Matrix) State {
	return State{CTM: ctm, LineWidth: 1, HScale: 1, fillN: 1, strokeN: 1}
}

func setOp(list []Op, op Op) []Op {
	out := make([]Op, 0, len(list)+1)
	for _, o := range list {
		if o.Name != op.Name {
			out = append(out, o)
		}
	}
	return append(out, op)
}

type Kind uint8

const (
	Text Kind = iota + 1
	Image
	Path
)

func (k Kind) String() string {
	return [...]string{"", "text", "image", "path"}[k]
}

// Item is something painted on the page.
type Item struct {
	Kind  Kind
	Start int   // first op of the item (path construction start)
	Op    int   // the painting op
	Form  []int // Do ops leading into nested forms; Start/Op index the innermost
	Quad  [8]float64
	BBox  Rect
	State *State
	// Text
	Text  string
	TM    Matrix  // text space → user space at the start of the run (Tm×CTM)
	Tm    Matrix  // text matrix at the start of the run
	Tlm   Matrix  // text line matrix at the start of the run
	Size  float64 // effective font size in user space
	Width float64 // advance in text space
	// Stroke-only path: subpaths as x0 y0 x1 y1 … in user space (curves by
	// their control points) and half the line width, for precise hit tests.
	Lines     [][]float64
	HalfWidth float64
	Axis      bool // a path of horizontal and vertical segments only (rules, boxes)
	// Image or form XObject
	Name     cos.Name
	Artifact bool   // inside /Artifact marked content
	Own      string // inside /PFObj marked content: the editor object it draws
}

// FontCache shares fonts across pages: one *font.Font per font dictionary.
type FontCache struct {
	mu sync.Mutex
	m  map[cos.Ref]*font.Font
}

func NewFontCache() *FontCache { return &FontCache{m: map[cos.Ref]*font.Font{}} }

func (c *FontCache) Get(g cos.Getter, o cos.Object) *font.Font {
	r, isRef := o.(cos.Ref)
	if !isRef {
		return font.Load(g, o)
	}
	c.mu.Lock()
	f, ok := c.m[r]
	c.mu.Unlock()
	if ok {
		return f
	}
	f = font.Load(g, r)
	c.mu.Lock()
	c.m[r] = f
	c.mu.Unlock()
	return f
}

// All returns the cached fonts.
func (c *FontCache) All() map[cos.Ref]*font.Font {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.m)
}

// Interp interprets content streams and collects painted items.
type Interp struct {
	G     cos.Getter
	Fonts *FontCache
	Items []Item
	// FontUse records the fonts used by text items (by font dict reference).
	FontUse map[cos.Ref]*FontUse
	forms   map[cos.Ref][]Op
}

// FontUse aggregates what a page needs from a font.
type FontUse struct {
	Font  *font.Font
	Chars map[rune]bool
}

func NewInterp(g cos.Getter, fonts *FontCache) *Interp {
	return &Interp{G: g, Fonts: fonts, FontUse: map[cos.Ref]*FontUse{}, forms: map[cos.Ref][]Op{}}
}

// Run interprets ops with the given resources and initial CTM.
func (in *Interp) Run(ops []Op, res cos.Dict, ctm Matrix) {
	st := newState(ctm)
	in.run(ops, res, st, nil, 0)
}

func num(a []cos.Object, i int) float64 {
	if i >= len(a) {
		return 0
	}
	switch v := a[i].(type) {
	case cos.Int:
		return float64(v)
	case cos.Real:
		return float64(v)
	}
	return 0
}

func matrixOf(a []cos.Object) (Matrix, bool) {
	if len(a) < 6 {
		return Identity, false
	}
	var m Matrix
	for i := range m {
		m[i] = num(a, i)
	}
	return m, true
}

func gray(v float64) [3]float64 { return [3]float64{v, v, v} }

func cmyk(c, m, y, k float64) [3]float64 {
	return [3]float64{(1 - c) * (1 - k), (1 - m) * (1 - k), (1 - y) * (1 - k)}
}

func colorOf(n int, a []cos.Object) [3]float64 {
	switch {
	case n == 1 && len(a) >= 1:
		return gray(num(a, 0))
	case n == 3 && len(a) >= 3:
		return [3]float64{num(a, 0), num(a, 1), num(a, 2)}
	case n == 4 && len(a) >= 4:
		return cmyk(num(a, 0), num(a, 1), num(a, 2), num(a, 3))
	}
	return [3]float64{}
}

// components returns the number of colour components of a colour space.
func (in *Interp) components(res cos.Dict, cs cos.Object) int {
	g := in.G
	switch v := g.Resolve(cs).(type) {
	case cos.Name:
		switch v {
		case "DeviceGray", "G", "CalGray":
			return 1
		case "DeviceRGB", "RGB", "CalRGB", "Lab":
			return 3
		case "DeviceCMYK", "CMYK":
			return 4
		case "Pattern":
			return 0
		}
		if def := g.Dict(res["ColorSpace"])[v]; def != nil {
			return in.components(nil, def)
		}
	case cos.Array:
		if len(v) == 0 {
			return 1
		}
		switch g.Name(v[0]) {
		case "ICCBased":
			if len(v) > 1 {
				if n, ok := g.Int(g.Dict(v[1])["N"]); ok {
					return n
				}
			}
		case "CalRGB", "Lab":
			return 3
		case "DeviceN":
			if len(v) > 1 {
				return len(g.Array(v[1]))
			}
		case "Pattern":
			return 0
		}
		return 1 // Separation, Indexed, CalGray
	}
	return 1
}

type point struct{ x, y float64 }

// axisAligned reports whether a path has segments and all of them are
// horizontal or vertical (a dot of zero length is not a rule).
func axisAligned(path []point, subs []int) bool {
	const eps = 0.01
	seg := false
	for k := 1; k < len(path); k++ {
		if slices.Contains(subs, k) {
			continue // a new subpath starts here
		}
		dx, dy := math.Abs(path[k].x-path[k-1].x), math.Abs(path[k].y-path[k-1].y)
		if dx > eps && dy > eps {
			return false
		}
		seg = seg || dx > eps || dy > eps
	}
	return seg
}

// polylines splits a path into subpaths of flat coordinates.
func polylines(path []point, subs []int, closed bool) [][]float64 {
	if len(subs) == 0 || subs[0] != 0 {
		subs = append([]int{0}, subs...)
	}
	out := make([][]float64, 0, len(subs))
	for k, s := range subs {
		e := len(path)
		if k+1 < len(subs) {
			e = subs[k+1]
		}
		if s >= e {
			continue
		}
		l := make([]float64, 0, 2*(e-s)+2)
		for _, p := range path[s:e] {
			l = append(l, p.x, p.y)
		}
		if closed {
			l = append(l, path[s].x, path[s].y)
		}
		out = append(out, l)
	}
	return out
}

func (in *Interp) run(ops []Op, res cos.Dict, st State, form []int, depth int) {
	g := in.G
	var stack []State
	tm, tlm := Identity, Identity
	var path []point
	var subs []int // where each subpath starts in path
	pathStart := -1
	clip := false
	for i, op := range ops {
		a := op.Args
		switch op.Name {
		case "q":
			stack = append(stack, st)
		case "Q":
			if n := len(stack); n > 0 {
				marked := st.MarkedStack // marked content is not part of the graphics state
				st = stack[n-1]
				st.MarkedStack = marked
				stack = stack[:n-1]
			}
		case "cm":
			if m, ok := matrixOf(a); ok {
				st.CTM = m.Mul(st.CTM)
			}
		case "w":
			st.LineWidth = num(a, 0)
			st.LineOps = setOp(st.LineOps, op)
		case "J", "j", "M", "d", "ri", "i":
			st.LineOps = setOp(st.LineOps, op)
		case "gs":
			st.GSOps = append(slices.Clip(st.GSOps), op)
			if len(a) > 0 {
				if n, ok := a[0].(cos.Name); ok {
					gs := g.Dict(g.Dict(res["ExtGState"])[n])
					if lw, ok := g.Num(gs["LW"]); ok {
						st.LineWidth = lw
					}
					if fa := g.Array(gs["Font"]); len(fa) == 2 {
						st.Font = in.Fonts.Get(g, fa[0])
						st.FontSize, _ = g.Num(fa[1])
					}
				}
			}
		case "g":
			st.Fill, st.fillN, st.FillOps = gray(num(a, 0)), 1, []Op{op}
		case "G":
			st.Stroke, st.strokeN, st.StrokeOps = gray(num(a, 0)), 1, []Op{op}
		case "rg":
			st.Fill, st.fillN, st.FillOps = colorOf(3, a), 3, []Op{op}
		case "RG":
			st.Stroke, st.strokeN, st.StrokeOps = colorOf(3, a), 3, []Op{op}
		case "k":
			st.Fill, st.fillN, st.FillOps = colorOf(4, a), 4, []Op{op}
		case "K":
			st.Stroke, st.strokeN, st.StrokeOps = colorOf(4, a), 4, []Op{op}
		case "cs":
			if len(a) > 0 {
				st.fillN = in.components(res, a[0])
			}
			st.Fill, st.FillOps = [3]float64{}, []Op{op}
		case "CS":
			if len(a) > 0 {
				st.strokeN = in.components(res, a[0])
			}
			st.Stroke, st.StrokeOps = [3]float64{}, []Op{op}
		case "sc", "scn":
			st.Fill = colorOf(st.fillN, a)
			st.FillOps = keepCS(st.FillOps, "cs", op)
		case "SC", "SCN":
			st.Stroke = colorOf(st.strokeN, a)
			st.StrokeOps = keepCS(st.StrokeOps, "CS", op)

		case "m", "l", "c", "v", "y", "re", "h":
			if pathStart < 0 {
				pathStart = i
			}
			if op.Name == "m" || op.Name == "re" {
				subs = append(subs, len(path))
			}
			if op.Name == "h" && len(subs) > 0 && subs[len(subs)-1] < len(path) {
				path = append(path, path[subs[len(subs)-1]])
			}
			if op.Name != "re" {
				for k := 0; k+1 < len(a); k += 2 {
					x, y := st.CTM.Apply(num(a, k), num(a, k+1))
					path = append(path, point{x, y})
				}
			} else if len(a) == 4 {
				x, y, w, h := num(a, 0), num(a, 1), num(a, 2), num(a, 3)
				for _, p := range [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}, {x, y}} {
					px, py := st.CTM.Apply(p[0], p[1])
					path = append(path, point{px, py})
				}
			}
		case "W", "W*":
			clip = true
		case "S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n":
			if pathStart >= 0 && op.Name != "n" && len(path) > 0 {
				r := emptyRect
				for _, p := range path {
					r.add(p.x, p.y)
				}
				it := Item{Kind: Path, Start: pathStart, Op: i, Form: form}
				if op.Name != "f" && op.Name != "F" && op.Name != "f*" {
					hw := st.LineWidth * math.Hypot(st.CTM[0], st.CTM[1]) / 2
					r = Rect{r[0] - hw, r[1] - hw, r[2] + hw, r[3] + hw}
					if op.Name == "S" || op.Name == "s" {
						it.Lines, it.HalfWidth = polylines(path, subs, op.Name == "s"), hw
					}
				}
				it.BBox, it.Quad, it.Axis = r, quadOf(r), axisAligned(path, subs)
				in.add(it, &st)
			}
			if clip && pathStart >= 0 {
				st.ClipOps = append(slices.Clip(st.ClipOps), ops[pathStart:i+1]...)
			}
			path, subs, pathStart, clip = path[:0], subs[:0], -1, false

		case "BT":
			tm, tlm = Identity, Identity
		case "Tc":
			st.CharSpace = num(a, 0)
			st.TextOps = setOp(st.TextOps, op)
		case "Tw":
			st.WordSpace = num(a, 0)
			st.TextOps = setOp(st.TextOps, op)
		case "Tz":
			st.HScale = num(a, 0) / 100
			st.TextOps = setOp(st.TextOps, op)
		case "TL":
			st.Leading = num(a, 0)
			st.TextOps = setOp(st.TextOps, op)
		case "Ts":
			st.Rise = num(a, 0)
			st.TextOps = setOp(st.TextOps, op)
		case "Tr":
			st.Render = int(num(a, 0))
			st.TextOps = setOp(st.TextOps, op)
		case "Tf":
			if len(a) >= 2 {
				if n, ok := a[0].(cos.Name); ok {
					fo := g.Dict(res["Font"])[n]
					st.FontRes = n
					st.FontRef, _ = fo.(cos.Ref)
					st.Font = in.Fonts.Get(g, fo)
				}
				st.FontSize = num(a, 1)
			}
		case "Td", "TD":
			if op.Name == "TD" {
				st.Leading = -num(a, 1)
			}
			tlm = Matrix{1, 0, 0, 1, num(a, 0), num(a, 1)}.Mul(tlm)
			tm = tlm
		case "Tm":
			if m, ok := matrixOf(a); ok {
				tm, tlm = m, m
			}
		case "T*":
			tlm = Matrix{1, 0, 0, 1, 0, -st.Leading}.Mul(tlm)
			tm = tlm
		case "Tj":
			in.show(i, form, &st, &tm, tlm, a)
		case "'":
			tlm = Matrix{1, 0, 0, 1, 0, -st.Leading}.Mul(tlm)
			tm = tlm
			in.show(i, form, &st, &tm, tlm, a)
		case "\"":
			if len(a) == 3 {
				st.WordSpace, st.CharSpace = num(a, 0), num(a, 1)
				st.TextOps = setOp(setOp(st.TextOps, Op{Name: "Tw", Args: a[:1]}), Op{Name: "Tc", Args: a[1:2]})
			}
			tlm = Matrix{1, 0, 0, 1, 0, -st.Leading}.Mul(tlm)
			tm = tlm
			if len(a) == 3 {
				in.show(i, form, &st, &tm, tlm, a[2:])
			}
		case "TJ":
			if len(a) > 0 {
				if arr, ok := a[0].(cos.Array); ok {
					in.show(i, form, &st, &tm, tlm, arr)
				}
			}

		case "Do":
			if len(a) == 0 {
				continue
			}
			name, _ := a[0].(cos.Name)
			ref := g.Dict(res["XObject"])[name]
			xo := g.Stream(ref)
			if xo == nil {
				continue
			}
			switch g.Name(xo.Dict["Subtype"]) {
			case "Image":
				r, q := unitSquare(st.CTM)
				in.add(Item{Kind: Image, Start: i, Op: i, Form: form, BBox: r, Quad: q, Name: name}, &st)
			case "Form":
				r, isRef := ref.(cos.Ref)
				if depth > 12 || !isRef {
					continue
				}
				fops, ok := in.forms[r]
				if !ok {
					data, _ := g.Decode(xo)
					fops = Parse(data)
					in.forms[r] = fops
				}
				inner := st
				if m, ok := matrixOf(g.Array(xo.Dict["Matrix"])); ok {
					inner.CTM = m.Mul(st.CTM)
				}
				fres := g.Dict(xo.Dict["Resources"])
				if fres == nil {
					fres = res
				}
				in.run(fops, fres, inner, append(slices.Clip(form), i), depth+1)
			}
		case "BI":
			r, q := unitSquare(st.CTM)
			in.add(Item{Kind: Image, Start: i, Op: i, Form: form, BBox: r, Quad: q}, &st)
		case "BDC", "BMC":
			st.MarkedStack = append(slices.Clip(st.MarkedStack), op)
		case "EMC":
			if n := len(st.MarkedStack); n > 0 {
				st.MarkedStack = st.MarkedStack[:n-1]
			}
		}
	}
}

func keepCS(ops []Op, csName string, op Op) []Op {
	if len(ops) > 0 && ops[0].Name == csName {
		return []Op{ops[0], op}
	}
	return []Op{op}
}

func unitSquare(m Matrix) (Rect, [8]float64) {
	var q [8]float64
	r := emptyRect
	for k, p := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		x, y := m.Apply(p[0], p[1])
		q[2*k], q[2*k+1] = x, y
		r.add(x, y)
	}
	return r, q
}

func quadOf(r Rect) [8]float64 {
	return [8]float64{r[0], r[1], r[2], r[1], r[2], r[3], r[0], r[3]}
}

func (in *Interp) add(it Item, st *State) {
	snap := *st
	it.State = &snap
	for _, m := range st.MarkedStack {
		if len(m.Args) > 0 && m.Args[0] == cos.Name("Artifact") {
			it.Artifact = true
		}
		if len(m.Args) > 1 && m.Args[0] == cos.Name("PFObj") {
			if p, ok := m.Args[1].(cos.Dict); ok {
				if id, ok := p["ID"].(cos.String); ok {
					it.Own = string(id)
				}
			}
		}
	}
	in.Items = append(in.Items, it)
}

// show advances the text matrix over a shown string or TJ array and
// records the run as a text item.
func (in *Interp) show(i int, form []int, st *State, tm *Matrix, tlm Matrix, elems []cos.Object) {
	f := st.Font
	if f == nil {
		f = font.Load(in.G, nil)
		st.Font = f
	}
	tfs, th := st.FontSize, st.HScale
	start := *tm
	var text strings.Builder
	tx := 0.0
	gap, last := false, ' ' // last: the previous character, ' ' at the start
	for _, e := range elems {
		switch v := e.(type) {
		case cos.Int, cos.Real:
			n := num([]cos.Object{v}, 0)
			tx -= n / 1000 * tfs * th
			// A large negative kern is a word gap in practice: a space is
			// added only where the shown text has no whitespace of its own.
			gap = gap || n < -200
		case cos.String:
			for _, gl := range f.Glyphs(string(v)) {
				w := gl.W*tfs + st.CharSpace
				if gl.Space {
					w += st.WordSpace
				}
				tx += w * th
				if gl.Text == "" {
					continue
				}
				first, _ := utf8.DecodeRuneInString(gl.Text)
				if gap && !unicode.IsSpace(last) && !unicode.IsSpace(first) {
					text.WriteByte(' ')
				}
				gap = false
				last, _ = utf8.DecodeLastRuneInString(gl.Text)
				text.WriteString(gl.Text)
			}
		}
	}
	*tm = Matrix{1, 0, 0, 1, tx, 0}.Mul(*tm)

	trm := start.Mul(st.CTM)
	y0 := f.Descent*tfs + st.Rise
	y1 := f.Ascent*tfs + st.Rise
	var q [8]float64
	r := emptyRect
	for k, p := range [][2]float64{{0, y0}, {tx, y0}, {tx, y1}, {0, y1}} {
		x, y := trm.Apply(p[0], p[1])
		q[2*k], q[2*k+1] = x, y
		r.add(x, y)
	}
	s := text.String()
	in.add(Item{Kind: Text, Start: i, Op: i, Form: form, BBox: r, Quad: q, Text: s, TM: trm, Tm: start, Tlm: tlm,
		Size: math.Abs(tfs) * math.Hypot(trm[2], trm[3]), Width: tx}, st)

	if st.FontRef != (cos.Ref{}) {
		u := in.FontUse[st.FontRef]
		if u == nil {
			u = &FontUse{Font: f, Chars: map[rune]bool{}}
			in.FontUse[st.FontRef] = u
		}
		for _, c := range s {
			u.Chars[c] = true
		}
	}
}
