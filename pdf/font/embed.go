package font

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"pdf-formatter/pdf/cos"
)

// Embedding writes a Face as a composite font (§9.7): Type0 with
// Identity-H encoding, so shown strings are 2-byte glyph ids.
// TrueType fonts are subset (unused glyphs emptied, ids kept); CFF fonts
// are embedded whole.
type Embedding struct {
	Face *Face
	used map[uint16]string // glyph → text, for ToUnicode
}

func NewEmbedding(f *Face) *Embedding {
	return &Embedding{Face: f, used: map[uint16]string{}}
}

// Encode returns the shown string for s and records the glyphs as used.
// Runes the font lacks become glyph 0 (.notdef).
func (e *Embedding) Encode(s string) cos.String {
	b := make([]byte, 0, 2*len(s))
	for _, r := range s {
		g := e.Face.Glyph(r)
		b = append(b, byte(g>>8), byte(g))
		// A glyph shared by several runes (a space standing in for a
		// missing no-break space) reads back as the plain space.
		if _, ok := e.used[g]; g != 0 && (!ok || r == ' ') {
			e.used[g] = string(r)
		}
	}
	return cos.String(b)
}

// Write adds the font objects to u and returns the Type0 font reference.
func (e *Embedding) Write(u *cos.Update) cos.Ref {
	r := u.Add(nil)
	e.WriteAt(u, r)
	return r
}

// WriteAt writes the font with the Type0 dictionary at ref (allocated
// earlier, so pages can refer to it before all text is encoded).
func (e *Embedding) WriteAt(u *cos.Update, ref cos.Ref) {
	f := e.Face
	asc, desc, capH, bbox, italic := f.Metrics()
	name := cos.Name(strings.Map(func(r rune) rune {
		if r <= ' ' || r > '~' || strings.ContainsRune("()<>[]{}/%#", r) {
			return -1
		}
		return r
	}, f.PSName))
	if name == "" {
		name = "Font"
	}
	// Only the used glyphs; the subset tag marks it as such (§9.6.4).
	used := map[uint16]bool{}
	for g := range e.used {
		used[g] = true
	}
	file := &cos.Stream{Dict: cos.Dict{"Filter": cos.Name("FlateDecode"), "Subtype": cos.Name("OpenType")}, Data: f.Deflated()}
	fileKey, subtype := cos.Name("FontFile3"), cos.Name("CIDFontType0")
	if f.CFF {
		if data, err := f.Subset(used); err == nil {
			file.Data = cos.Deflate(data)
			name = cos.Name(subsetTag(e.gids()) + "+" + string(name))
		}
	} else {
		if data, err := f.Subset(used); err == nil {
			file = &cos.Stream{Dict: cos.Dict{"Filter": cos.Name("FlateDecode"), "Length1": cos.Int(len(data))}, Data: cos.Deflate(data)}
			name = cos.Name(subsetTag(e.gids()) + "+" + string(name))
		} else {
			file = &cos.Stream{Dict: cos.Dict{"Filter": cos.Name("FlateDecode"), "Length1": cos.Int(len(f.Data))}, Data: f.Deflated()}
		}
		fileKey, subtype = "FontFile2", "CIDFontType2"
	}
	flags := 32 // nonsymbolic
	if f.Italic {
		flags |= 64
	}
	fd := cos.Dict{
		"Type": cos.Name("FontDescriptor"), "FontName": name, "Flags": cos.Int(flags),
		"FontBBox":    cos.Array{cos.Real(bbox[0]), cos.Real(bbox[1]), cos.Real(bbox[2]), cos.Real(bbox[3])},
		"ItalicAngle": cos.Real(italic), "Ascent": cos.Real(asc), "Descent": cos.Real(desc),
		"CapHeight": cos.Real(capH), "StemV": cos.Int(80), fileKey: u.Add(file),
	}
	cid := cos.Dict{
		"Type": cos.Name("Font"), "Subtype": subtype, "BaseFont": name,
		"CIDSystemInfo":  cos.Dict{"Registry": cos.String("Adobe"), "Ordering": cos.String("Identity"), "Supplement": cos.Int(0)},
		"FontDescriptor": u.Add(fd), "DW": cos.Int(1000), "W": e.widths(),
	}
	if !f.CFF {
		cid["CIDToGIDMap"] = cos.Name("Identity")
	}
	tu := &cos.Stream{Dict: cos.Dict{"Filter": cos.Name("FlateDecode")}, Data: cos.Deflate(e.toUnicode())}
	u.Set(ref, cos.Dict{
		"Type": cos.Name("Font"), "Subtype": cos.Name("Type0"), "BaseFont": name,
		"Encoding": cos.Name("Identity-H"), "DescendantFonts": cos.Array{u.Add(cid)}, "ToUnicode": u.Add(tu),
	})
}

// subsetTag is six capital letters derived from the glyph set.
func subsetTag(gids []uint16) string {
	h := uint32(2166136261)
	for _, g := range gids {
		h = (h ^ uint32(g)) * 16777619
	}
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = byte('A' + h%26)
		h /= 26
	}
	return string(tag)
}

func (e *Embedding) gids() []uint16 {
	g := make([]uint16, 0, len(e.used))
	for k := range e.used {
		g = append(g, k)
	}
	slices.Sort(g)
	return g
}

// widths builds the /W array: runs of consecutive glyphs share one entry.
func (e *Embedding) widths() cos.Array {
	var w cos.Array
	var run cos.Array
	start, prev := -1, -2
	flush := func() {
		if start >= 0 {
			w = append(w, cos.Int(start), run)
		}
	}
	for _, g := range e.gids() {
		if int(g) != prev+1 {
			flush()
			start, run = int(g), cos.Array{}
		}
		run = append(run, cos.Real(e.Face.Advance(g)))
		prev = int(g)
	}
	flush()
	return w
}

func (e *Embedding) toUnicode() []byte {
	var sb strings.Builder
	sb.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n" +
		"/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n" +
		"/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n" +
		"1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	gids := e.gids()
	for i := 0; i < len(gids); i += 100 {
		chunk := gids[i:min(i+100, len(gids))]
		fmt.Fprintf(&sb, "%d beginbfchar\n", len(chunk))
		for _, g := range chunk {
			fmt.Fprintf(&sb, "<%04X> <", g)
			for _, u := range utf16.Encode([]rune(e.used[g])) {
				fmt.Fprintf(&sb, "%04X", u)
			}
			sb.WriteString(">\n")
		}
		sb.WriteString("endbfchar\n")
	}
	sb.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return []byte(sb.String())
}
