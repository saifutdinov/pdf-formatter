package edit

import (
	"cmp"
	"strings"
	"unicode"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/font"
)

// Run is a part of a text object in its own style; empty fields take the
// object's font, size and colour.
type Run struct {
	Text      string  `json:"text"`
	Font      string  `json:"font,omitempty"` // library font id
	Size      float64 `json:"size,omitempty"`
	Color     string  `json:"color,omitempty"`
	Underline bool    `json:"underline,omitempty"`
}

// RunsText is the plain text of runs.
func RunsText(runs []Run) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// style is a run's resolved style.
type style struct {
	font      string
	face      *font.Face
	size      float64
	color     string
	underline bool
}

// runs returns the text as styled runs: Runs, or all of Text in the
// object's style.
func (o *Object) runs() []Run {
	if len(o.Runs) > 0 {
		return o.Runs
	}
	return []Run{{Text: o.Text}}
}

func (o *Object) styles(faces func(string) (*font.Face, error)) ([]style, error) {
	runs := o.runs()
	out := make([]style, len(runs))
	for i, r := range runs {
		s := style{font: cmp.Or(r.Font, o.Font), size: cmp.Or(r.Size, o.Size, 12), color: cmp.Or(r.Color, o.Color), underline: r.Underline}
		f, err := faces(s.font)
		if err != nil {
			return nil, err
		}
		s.face = f
		out[i] = s
	}
	return out, nil
}

// Seg is a part of a laid-out line in one style.
type Seg struct {
	Text  string
	Style int     // index into the runs
	Width float64 // pt, trailing spaces included
}

// Line is one laid-out line of a text object.
type Line struct {
	Text    string  // including the spaces it breaks at: lines concatenate to the paragraph
	Width   float64 // pt, without trailing spaces
	Justify bool    // stretch to the box width (not the last line of a paragraph)
	Segs    []Seg
	Size    float64 // largest font size on the line
	Asc     float64 // pt above the baseline
	Desc    float64 // pt below it (negative)
}

func isBreak(r rune) bool { return r == ' ' || r == '\t' } // a no-break space is not

// wordEnd is where the word starting at i ends: at a space or tab, or after
// a hyphen between letters (пере-|нос), where a line may break as well.
func wordEnd(r []rune, i int) int {
	for ; i < len(r); i++ {
		if isBreak(r[i]) {
			return i
		}
		if r[i] == '-' && i > 0 && unicode.IsLetter(r[i-1]) && i+1 < len(r) && unicode.IsLetter(r[i+1]) {
			return i + 1
		}
	}
	return len(r)
}

// layoutText breaks styled text into lines no wider than max (pt): greedy at
// spaces and tabs, long words by characters; paragraphs end at '\n'. Every
// character is kept: the spaces at a break stay at the end of the line.
func layoutText(runs []Run, styles []style, limit float64) []Line {
	var out []Line
	// One paragraph at a time, as runes with their style and advance.
	var text []rune
	var st []int
	var adv []float64
	paraStyle := 0
	flush := func() {
		w := func(a, b int) float64 {
			s := 0.0
			for _, v := range adv[a:b] {
				s += v
			}
			return s
		}
		visible := func(a, b int) bool {
			for _, c := range text[a:b] {
				if !isBreak(c) {
					return true
				}
			}
			return false
		}
		emit := func(a, b int, justify bool) {
			l := Line{Text: string(text[a:b]), Justify: justify}
			e := b
			for e > a && isBreak(text[e-1]) {
				e--
			}
			l.Width = w(a, e)
			metrics := func(i int) {
				s := styles[i]
				asc, desc, _, _, _ := s.face.Metrics()
				l.Size = max(l.Size, s.size)
				l.Asc = max(l.Asc, asc/1000*s.size)
				l.Desc = min(l.Desc, desc/1000*s.size)
			}
			if a == b {
				metrics(paraStyle)
			}
			for i := a; i < b; i++ {
				if i == a || st[i] != st[i-1] {
					l.Segs = append(l.Segs, Seg{Style: st[i]})
					metrics(st[i])
				}
				seg := &l.Segs[len(l.Segs)-1]
				seg.Text += string(text[i])
				seg.Width += adv[i]
			}
			out = append(out, l)
		}
		start, lineW := 0, 0.0
		for pos := 0; pos < len(text); {
			we := wordEnd(text, pos)
			se := we
			for se < len(text) && isBreak(text[se]) {
				se++
			}
			wordW, spW := w(pos, we), w(we, se)
			if lineW+wordW <= limit || !visible(start, pos) && wordW <= limit {
				lineW += wordW + spW
				pos = se
				continue
			}
			if visible(start, pos) {
				emit(start, pos, true)
				start = pos
			}
			for ws := pos; w(start, we) > limit && we-ws > 1; { // break a long word by characters
				n := 1
				for n < we-ws && w(start, ws+n+1) <= limit {
					n++
				}
				emit(start, ws+n, false)
				start, ws = ws+n, ws+n
			}
			lineW = w(start, se)
			pos = se
		}
		emit(start, len(text), false)
		text, st, adv = text[:0], st[:0], adv[:0]
	}
	for i, r := range runs {
		s := styles[i]
		for _, c := range r.Text {
			if c == '\n' {
				flush()
				paraStyle = i
				continue
			}
			text = append(text, c)
			st = append(st, i)
			adv = append(adv, s.face.Advance(s.face.Glyph(c))/1000*s.size)
		}
	}
	flush()
	return out
}

// Wrap lays out text in one style.
func Wrap(face *font.Face, text string, size, max float64) []Line {
	return layoutText([]Run{{Text: text}}, []style{{face: face, size: size}}, max)
}

// textHeight is the height of laid-out lines: ascent of the first, line
// gaps, descent of the last.
func textHeight(lines []Line, lineHeight float64) float64 {
	if len(lines) == 0 {
		return 0
	}
	h := lines[0].Asc - lines[len(lines)-1].Desc
	for _, l := range lines[1:] {
		h += lineHeight * l.Size
	}
	return h
}

// Lines lays out the object's text.
func (o *Object) Lines(faces func(string) (*font.Face, error)) ([]Line, error) {
	st, err := o.styles(faces)
	if err != nil {
		return nil, err
	}
	return layoutText(o.runs(), st, o.contentW()), nil
}

// TextHeight is the height of laid-out text including padding.
func (o *Object) TextHeight(faces func(string) (*font.Face, error)) float64 {
	st, err := o.styles(faces)
	if err != nil {
		return o.H
	}
	return o.Padding[0] + o.Padding[2] + textHeight(layoutText(o.runs(), st, o.contentW()), o.lineHeight())
}

func (r *renderer) text(o *Object) ([]content.Op, error) {
	styles, err := o.styles(r.env.Face)
	if err != nil {
		return nil, err
	}
	type res struct {
		fe   *fontEmbed
		name cos.Name
	}
	fonts := map[string]res{}
	for _, s := range styles {
		if _, ok := fonts[s.font]; !ok {
			fe, n, err := r.fontFor(s.font)
			if err != nil {
				return nil, err
			}
			fonts[s.font] = res{fe, n}
		}
	}
	lines := layoutText(o.runs(), styles, o.contentW())
	lh := o.lineHeight()
	top := -o.Padding[0]
	switch inner := o.H - o.Padding[0] - o.Padding[2]; o.VAlign {
	case "middle":
		top -= (inner - textHeight(lines, lh)) / 2
	case "bottom":
		top -= inner - textHeight(lines, lh)
	}
	out := []content.Op{op("BT")}
	var deco []content.Op // underlines, drawn after the text
	curFont, curSize, curColor := "", 0.0, "-"
	cw := o.contentW()
	y := top
	for i, l := range lines {
		if i == 0 {
			y -= l.Asc
		} else {
			y -= lh * l.Size
		}
		if l.Text == "" {
			continue
		}
		x := o.Padding[3]
		switch o.Align {
		case "center":
			x += (cw - l.Width) / 2
		case "right":
			x += cw - l.Width
		}
		out = append(out, op("Tm", reals(1, 0, 0, 1, x, y)...))
		// Trailing spaces are not shown; justified lines widen their gaps
		// with TJ, as Tw does not apply to 2-byte codes (§9.3.3).
		shown := len([]rune(strings.TrimRightFunc(l.Text, isBreak)))
		extra := 0.0
		if spaces := strings.Count(strings.TrimRightFunc(l.Text, isBreak), " "); o.Align == "justify" && l.Justify && spaces > 0 {
			extra = (cw - l.Width) / float64(spaces)
		}
		for _, seg := range l.Segs {
			all := []rune(seg.Text)
			text, tail := all, ""
			if len(all) > shown {
				text, tail = all[:max(0, shown)], string(all[max(0, shown):])
			}
			shown -= len(all)
			s := styles[seg.Style]
			f := fonts[s.font]
			if s.font != curFont || s.size != curSize {
				out = append(out, op("Tf", f.name, cos.Real(s.size)))
				curFont, curSize = s.font, s.size
			}
			if s.color != curColor {
				col := []cos.Object{cos.Int(0), cos.Int(0), cos.Int(0)}
				if c, ok := colorArgs(s.color); ok {
					col = c
				}
				out = append(out, op("rg", col...))
				curColor = s.color
			}
			w := s.face.TextWidth(string(text)) / 1000 * s.size
			if extra > 0 && strings.ContainsRune(string(text), ' ') {
				gap := cos.Real(-extra / s.size * 1000)
				var arr cos.Array
				for k, part := range strings.Split(string(text), " ") {
					if k > 0 {
						arr = append(arr, f.fe.e.Encode(" "), gap)
						w += extra
					}
					arr = append(arr, f.fe.e.Encode(part))
				}
				out = append(out, op("TJ", append(arr, f.fe.e.Encode(tail))))
			} else {
				// Trailing spaces are kept in the file (text extraction gives
				// back every character) but take no part in the layout.
				out = append(out, op("Tj", f.fe.e.Encode(string(text)+tail)))
			}
			if s.underline && len(text) > 0 {
				col, ok := colorArgs(s.color)
				if !ok {
					col = []cos.Object{cos.Int(0), cos.Int(0), cos.Int(0)}
				}
				t := 0.05 * s.size
				deco = append(deco, op("rg", col...), op("re", reals(x, y-0.1*s.size-t/2, w, t)...), op("f"))
			}
			x += w
		}
	}
	out = append(out, op("ET"))
	if len(deco) > 0 {
		out = append(append(append(out, op("q")), deco...), op("Q"))
	}
	return out, nil
}
