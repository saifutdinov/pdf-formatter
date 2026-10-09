package font

import (
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"unicode"

	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"pdf-formatter/pdf/cos"
)

// Face is one font from a TTF/OTF/TTC file, ready for layout and embedding.
type Face struct {
	Data   []byte // standalone sfnt: collections are split per face
	PSName string
	Family string
	Style  string
	Full   string
	CFF    bool // OpenType with CFF outlines (otherwise TrueType glyf)
	Bold   bool
	Italic bool

	f        *sfnt.Font
	upem     float64
	mu       sync.Mutex
	adv      map[uint16]float64
	glyphs   map[rune]uint16
	deflated []byte // compressed font program, cached across revisions
}

// Deflated returns the font program compressed for FontFile2/FontFile3.
func (f *Face) Deflated() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deflated == nil {
		f.deflated = cos.Deflate(f.Data)
	}
	return f.deflated
}

// ParseFaces reads a font file. Collections yield one face per font.
func ParseFaces(data []byte) ([]*Face, error) {
	if len(data) >= 12 && string(data[:4]) == "ttcf" {
		n := int(binary.BigEndian.Uint32(data[8:12]))
		var faces []*Face
		for i := 0; i < n && 12+4*i+4 <= len(data); i++ {
			off := int(binary.BigEndian.Uint32(data[12+4*i:]))
			single, err := extractFace(data, off)
			if err != nil {
				continue
			}
			if f, err := newFace(single); err == nil {
				faces = append(faces, f)
			}
		}
		if len(faces) == 0 {
			return nil, errors.New("font: no usable faces in collection")
		}
		return faces, nil
	}
	f, err := newFace(data)
	if err != nil {
		return nil, err
	}
	return []*Face{f}, nil
}

func newFace(data []byte) (*Face, error) {
	sf, err := sfnt.Parse(data)
	if err != nil {
		return nil, err
	}
	var b sfnt.Buffer
	name := func(ids ...sfnt.NameID) string {
		for _, id := range ids {
			if s, err := sf.Name(&b, id); err == nil && s != "" {
				return s
			}
		}
		return ""
	}
	f := &Face{
		Data:   data,
		f:      sf,
		upem:   float64(sf.UnitsPerEm()),
		CFF:    string(data[:4]) == "OTTO",
		PSName: name(sfnt.NameIDPostScript),
		Family: name(sfnt.NameIDTypographicFamily, sfnt.NameIDFamily),
		Style:  name(sfnt.NameIDTypographicSubfamily, sfnt.NameIDSubfamily),
		Full:   name(sfnt.NameIDFull),
		adv:    map[uint16]float64{},
		glyphs: map[rune]uint16{},
	}
	if f.PSName == "" {
		f.PSName = strings.ReplaceAll(f.Full, " ", "")
	}
	st := strings.ToLower(f.Style + " " + f.PSName)
	f.Bold = strings.Contains(st, "bold") || strings.Contains(st, "black") || strings.Contains(st, "heavy")
	f.Italic = strings.Contains(st, "italic") || strings.Contains(st, "oblique")
	return f, nil
}

// extractFace rebuilds a standalone sfnt from the face at off in a TTC.
func extractFace(ttc []byte, off int) ([]byte, error) {
	if off+12 > len(ttc) {
		return nil, errors.New("font: bad collection offset")
	}
	n := int(binary.BigEndian.Uint16(ttc[off+4:]))
	if off+12+16*n > len(ttc) {
		return nil, errors.New("font: bad table directory")
	}
	type rec struct {
		tag  []byte
		data []byte
	}
	recs := make([]rec, 0, n)
	for i := range n {
		r := ttc[off+12+16*i:]
		to := int(binary.BigEndian.Uint32(r[8:]))
		tl := int(binary.BigEndian.Uint32(r[12:]))
		if to+tl > len(ttc) {
			return nil, errors.New("font: table out of range")
		}
		recs = append(recs, rec{r[:4], ttc[to : to+tl]})
	}
	out := make([]byte, 12+16*n)
	copy(out, ttc[off:off+12])
	headAt := -1
	for i, r := range recs {
		pos := len(out)
		if string(r.tag) == "head" {
			headAt = pos
		}
		out = append(out, r.data...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		d := out[12+16*i:]
		copy(d, r.tag)
		binary.BigEndian.PutUint32(d[4:], checksum(r.data))
		binary.BigEndian.PutUint32(d[8:], uint32(pos))
		binary.BigEndian.PutUint32(d[12:], uint32(len(r.data)))
	}
	if headAt >= 0 && headAt+12 <= len(out) {
		binary.BigEndian.PutUint32(out[headAt+8:], 0)
		binary.BigEndian.PutUint32(out[headAt+8:], 0xB1B0AFBA-checksum(out))
	}
	return out, nil
}

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:min(i+4, len(b))])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}

// Glyph returns the glyph id for r, or 0 when the font lacks it.
func (f *Face) Glyph(r rune) uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.glyphs[r]; ok {
		return g
	}
	var b sfnt.Buffer
	g, err := f.f.GlyphIndex(&b, r)
	if err != nil {
		g = 0
	}
	if g == 0 && r != ' ' && unicode.IsSpace(r) {
		// A font without a no-break space or tab still shows a gap.
		g, _ = f.f.GlyphIndex(&b, ' ')
	}
	f.glyphs[r] = uint16(g)
	return uint16(g)
}

// Advance returns the advance width of a glyph in 1/1000 em.
func (f *Face) Advance(gid uint16) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.adv[gid]; ok {
		return a
	}
	var b sfnt.Buffer
	ppem := fixed.Int26_6(f.upem * 64)
	a, err := f.f.GlyphAdvance(&b, sfnt.GlyphIndex(gid), ppem, xfont.HintingNone)
	w := 0.0
	if err == nil {
		w = float64(a) / 64 / f.upem * 1000
	}
	f.adv[gid] = w
	return w
}

// Has reports whether every rune of s (except spaces) has a glyph.
func (f *Face) Has(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\n' && f.Glyph(r) == 0 {
			return false
		}
	}
	return true
}

// Missing returns the runes of s the font lacks.
func (f *Face) Missing(s string) []rune {
	var out []rune
	seen := map[rune]bool{}
	for _, r := range s {
		if r != ' ' && r != '\n' && !seen[r] && f.Glyph(r) == 0 {
			out = append(out, r)
			seen[r] = true
		}
	}
	return out
}

// TextWidth returns the advance of s in 1/1000 em.
func (f *Face) TextWidth(s string) float64 {
	w := 0.0
	for _, r := range s {
		w += f.Advance(f.Glyph(r))
	}
	return w
}

// Metrics in 1/1000 em: ascent, descent (negative), cap height, bbox.
func (f *Face) Metrics() (asc, desc, capH float64, bbox [4]float64, italicAngle float64) {
	var b sfnt.Buffer
	ppem := fixed.Int26_6(f.upem * 64)
	k := 1000 / f.upem / 64
	if m, err := f.f.Metrics(&b, ppem, xfont.HintingNone); err == nil {
		asc, desc, capH = float64(m.Ascent)*k, -float64(m.Descent)*k, float64(m.CapHeight)*k
	}
	if r, err := f.f.Bounds(&b, ppem, xfont.HintingNone); err == nil {
		// sfnt uses y-down coordinates.
		bbox = [4]float64{float64(r.Min.X) * k, -float64(r.Max.Y) * k, float64(r.Max.X) * k, -float64(r.Min.Y) * k}
	}
	if capH == 0 {
		capH = asc * 0.7
	}
	if p := f.f.PostTable(); p != nil {
		italicAngle = p.ItalicAngle
	}
	return
}
