// Package font reads PDF font dictionaries (§9.5–9.10): how to split
// strings into codes, glyph widths for geometry, and Unicode for text.
package font

import (
	"math"
	"strconv"
	"strings"

	"pdf-formatter/pdf/cos"
)

// Status of a font for display and editing.
const (
	Embedded = "embedded" // full font program: displays and edits
	Subset   = "subset"   // only used glyphs: displays, limited editing
	Missing  = "missing"  // not embedded: viewers substitute
	Standard = "standard" // one of the standard 14, not embedded
)

type Font struct {
	BaseFont  string // as in the file, e.g. "ABCDEF+Arial-BoldMT"
	Name      string // without the subset prefix
	Subtype   string
	Status    string
	ToUnicode bool // has a ToUnicode CMap
	Std14     string
	Bold      bool
	Italic    bool
	Vertical  bool
	Ascent    float64 // text space units per 1 pt of font size
	Descent   float64 // negative

	enc    *cmap // composite fonts: code → CID; nil for simple fonts
	toUni  *cmap
	names  [256]string // simple fonts: code → glyph name
	widths map[int]float64
	dw     float64
	scale  float64 // glyph space → text space (0.001, or Type3 FontMatrix)
}

// Glyph is one character code of a shown string.
type Glyph struct {
	Code  uint32
	W     float64 // advance in text space units at font size 1
	Text  string
	Space bool // single-byte code 32: word spacing applies (§9.3.3)
}

func isSubsetTag(s string) bool {
	if len(s) < 7 || s[6] != '+' {
		return false
	}
	for i := range 6 {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// Load reads a font dictionary. It never fails: unknown fonts degrade to
// default widths and Latin-1 text.
func Load(g cos.Getter, o cos.Object) *Font {
	d := g.Dict(o)
	f := &Font{widths: map[int]float64{}, dw: 1000, scale: 0.001, Ascent: 0.75, Descent: -0.25}
	if d == nil {
		f.Status = Missing
		return f
	}
	f.Subtype = string(g.Name(d["Subtype"]))
	f.BaseFont = string(g.Name(d["BaseFont"]))
	f.Name = f.BaseFont
	subset := isSubsetTag(f.BaseFont)
	if subset {
		f.Name = f.BaseFont[7:]
	}
	desc := g.Dict(d["FontDescriptor"])
	if f.Subtype == "Type0" {
		f.loadComposite(g, d)
		if dfs := g.Array(d["DescendantFonts"]); len(dfs) > 0 {
			desc = g.Dict(g.Dict(dfs[0])["FontDescriptor"])
		}
	} else {
		f.loadSimple(g, d, desc)
	}
	if tu := g.Stream(d["ToUnicode"]); tu != nil {
		if data, err := g.Decode(tu); err == nil || len(data) > 0 {
			f.toUni = parseCMap(data)
			f.ToUnicode = true
		}
	}

	embedded := f.Subtype == "Type3"
	var flags int
	if desc != nil {
		embedded = embedded || desc["FontFile"] != nil || desc["FontFile2"] != nil || desc["FontFile3"] != nil
		flags, _ = g.Int(desc["Flags"])
		if a, ok := g.Num(desc["Ascent"]); ok && a != 0 {
			f.Ascent = a / 1000
		}
		if v, ok := g.Num(desc["Descent"]); ok && v != 0 {
			f.Descent = -math.Abs(v) / 1000 // some writers (Skia) store it positive
		}
		if w, ok := g.Num(desc["FontWeight"]); ok && w >= 600 {
			f.Bold = true
		}
	}
	lower := strings.ToLower(f.Name)
	f.Bold = f.Bold || flags&(1<<18) != 0 || strings.Contains(lower, "bold") || strings.Contains(lower, "black") || strings.Contains(lower, "heavy")
	f.Italic = flags&(1<<6) != 0 || strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
	switch {
	case embedded && subset:
		f.Status = Subset
	case embedded:
		f.Status = Embedded
	case f.Std14 != "":
		f.Status = Standard
	default:
		f.Status = Missing
	}
	return f
}

func (f *Font) loadComposite(g cos.Getter, d cos.Dict) {
	switch e := g.Resolve(d["Encoding"]).(type) {
	case cos.Name:
		f.enc = identityCMap() // ponytail: predefined CJK CMaps treated as Identity
		f.Vertical = strings.HasSuffix(string(e), "-V")
	case *cos.Stream:
		if data, err := g.Decode(e); err == nil || len(data) > 0 {
			f.enc = parseCMap(data)
			if len(f.enc.spaces) == 0 {
				f.enc.spaces = identityCMap().spaces
			}
		}
	}
	if f.enc == nil {
		f.enc = identityCMap()
	}
	dfs := g.Array(d["DescendantFonts"])
	if len(dfs) == 0 {
		return
	}
	cf := g.Dict(dfs[0])
	if w, ok := g.Num(cf["DW"]); ok {
		f.dw = w
	}
	w := g.Array(cf["W"])
	for i := 0; i+1 < len(w); {
		first, _ := g.Int(w[i])
		if arr := g.Array(w[i+1]); arr != nil {
			for k, e := range arr {
				if v, ok := g.Num(e); ok {
					f.widths[first+k] = v
				}
			}
			i += 2
			continue
		}
		if i+2 >= len(w) {
			break
		}
		last, _ := g.Int(w[i+1])
		v, _ := g.Num(w[i+2])
		for c := first; c <= last && c-first < 0x10000; c++ {
			f.widths[c] = v
		}
		i += 3
	}
}

func (f *Font) loadSimple(g cos.Getter, d, desc cos.Dict) {
	if std, ok := stdAliases[strings.ReplaceAll(f.Name, " ", "")]; ok && desc["FontFile"] == nil && desc["FontFile2"] == nil && desc["FontFile3"] == nil {
		f.Std14 = std
	}
	if f.Subtype == "Type3" {
		if m := g.Array(d["FontMatrix"]); len(m) == 6 {
			f.scale, _ = g.Num(m[0])
		}
	}
	flags, _ := g.Int(desc["Flags"])
	symbolic := flags&4 != 0 && flags&32 == 0
	base := &standardEncoding
	switch {
	case f.Std14 == "Symbol":
		base = &symbolEncoding
	case f.Std14 == "ZapfDingbats":
		base = &zapfDingbatsEncoding
	case f.Subtype == "TrueType" && !symbolic:
		base = &winAnsiEncoding
	case symbolic && f.Std14 == "":
		base = nil // built-in encoding of the font program: no names
	}
	var diffs cos.Array
	switch e := g.Resolve(d["Encoding"]).(type) {
	case cos.Name:
		if b := namedEncoding(e); b != nil {
			base = b
		}
	case cos.Dict:
		if b := namedEncoding(g.Name(e["BaseEncoding"])); b != nil {
			base = b
		}
		diffs = g.Array(e["Differences"])
	}
	if base != nil {
		f.names = *base
	}
	code := 0
	for _, e := range diffs {
		switch v := g.Resolve(e).(type) {
		case cos.Int:
			code = int(v)
		case cos.Name:
			if code >= 0 && code < 256 {
				f.names[code] = string(v)
			}
			code++
		}
	}
	first, _ := g.Int(d["FirstChar"])
	for i, e := range g.Array(d["Widths"]) {
		if v, ok := g.Num(e); ok {
			f.widths[first+i] = v
		}
	}
	if mw, ok := g.Num(desc["MissingWidth"]); ok && mw > 0 {
		f.dw = mw
	} else {
		f.dw = 500
	}
	if len(f.widths) == 0 && f.Std14 != "" {
		for c, name := range f.names {
			if name != "" {
				f.widths[c] = stdWidth(f.Std14, name)
			}
		}
	}
}

func namedEncoding(n cos.Name) *[256]string {
	switch n {
	case "WinAnsiEncoding":
		return &winAnsiEncoding
	case "MacRomanEncoding":
		return &macRomanEncoding
	case "StandardEncoding":
		return &standardEncoding
	case "MacExpertEncoding":
		return &macExpertEncoding
	}
	return nil
}

func stdWidth(font, glyph string) float64 {
	if strings.HasPrefix(font, "Courier") {
		return 600
	}
	if w, ok := stdWidths[font][glyph]; ok {
		return float64(w)
	}
	return 500
}

// glyphRune maps a glyph name to Unicode: the glyph list, then uniXXXX,
// uXXXX[XX] and a few numeric conventions.
func glyphRune(name string) (rune, bool) {
	if r, ok := glyphUnicode[name]; ok {
		return r, true
	}
	if i := strings.IndexByte(name, '.'); i > 0 { // "a.sc", "one.oldstyle"
		return glyphRune(name[:i])
	}
	if strings.HasPrefix(name, "uni") && len(name) >= 7 {
		if v, err := strconv.ParseUint(name[3:7], 16, 32); err == nil {
			return rune(v), true
		}
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			return rune(v), true
		}
	}
	return 0, false
}

// Glyphs splits a shown string into character codes (§9.4.3).
func (f *Font) Glyphs(s string) []Glyph {
	out := make([]Glyph, 0, len(s))
	for len(s) > 0 {
		var g Glyph
		n := 1
		if f.enc != nil {
			g.Code, n = f.enc.next(s)
			cid := f.enc.cidOf(g.Code)
			w, ok := f.widths[cid]
			if !ok {
				w = f.dw
			}
			g.W = w * f.scale
		} else {
			g.Code = uint32(s[0])
			w, ok := f.widths[int(g.Code)]
			if !ok {
				w = f.dw
			}
			g.W = w * f.scale
			g.Space = s[0] == ' '
		}
		if n == 1 && f.enc != nil && s[0] == ' ' {
			g.Space = true
		}
		g.Text = f.text(g.Code)
		out = append(out, g)
		s = s[n:]
	}
	return out
}

func (f *Font) text(code uint32) string {
	if f.toUni != nil {
		if t, ok := f.toUni.text(code); ok {
			return t
		}
	}
	if f.enc == nil {
		if name := f.names[code&0xff]; name != "" {
			// WinAnsi 0240 and MacRoman 0312 are "space" by name but a
			// no-break space by meaning (Annex D note).
			if name == "space" && code != 32 {
				return "\u00a0"
			}
			if r, ok := glyphRune(name); ok {
				return string(r)
			}
		}
		if code >= 32 && code < 127 {
			return string(rune(code))
		}
	}
	return ""
}

// HasText reports whether text can be extracted reliably.
func (f *Font) HasText() bool {
	return f.ToUnicode || (f.enc == nil && f.Subtype != "Type3")
}
