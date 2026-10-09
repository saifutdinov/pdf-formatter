package font

import (
	"encoding/binary"
	"errors"
	"slices"
)

// Tables a PDF viewer needs from an embedded TrueType font (§9.9).
// Layout (GSUB/GPOS), bitmaps, colour and variation tables are dropped.
var keepTables = map[string]bool{
	"head": true, "hhea": true, "hmtx": true, "maxp": true, "loca": true, "glyf": true,
	"cvt ": true, "fpgm": true, "prep": true, "cmap": true, "name": true, "OS/2": true, "post": true,
}

type table struct {
	tag  string
	data []byte
}

func readTables(b []byte) (map[string][]byte, error) {
	if len(b) < 12 {
		return nil, errors.New("font: truncated")
	}
	n := int(binary.BigEndian.Uint16(b[4:]))
	if 12+16*n > len(b) {
		return nil, errors.New("font: bad table directory")
	}
	out := map[string][]byte{}
	for i := range n {
		r := b[12+16*i:]
		off, ln := int(binary.BigEndian.Uint32(r[8:])), int(binary.BigEndian.Uint32(r[12:]))
		if off < 0 || ln < 0 || off+ln > len(b) {
			return nil, errors.New("font: table out of range")
		}
		out[string(r[:4])] = b[off : off+ln]
	}
	return out, nil
}

// Tables kept around a subset CFF: outlines, metrics, character map.
var keepCFFTables = map[string]bool{
	"head": true, "hhea": true, "hmtx": true, "maxp": true, "cmap": true, "name": true, "OS/2": true, "post": true,
}

// Subset returns the font with only the given glyphs drawn: other glyphs
// become empty, so glyph ids stay the same and CIDToGIDMap Identity keeps
// working. TrueType composite glyphs bring their components along; CFF
// fonts get their charstrings emptied (cff.go).
func (f *Face) Subset(used map[uint16]bool) ([]byte, error) {
	if f.CFF {
		return f.subsetCFF(used)
	}
	t, err := readTables(f.Data)
	if err != nil {
		return nil, err
	}
	head, maxp, loca, glyf := t["head"], t["maxp"], t["loca"], t["glyf"]
	if len(head) < 54 || len(maxp) < 6 || loca == nil || glyf == nil {
		return f.Data, nil // not a glyf font after all
	}
	numGlyphs := int(binary.BigEndian.Uint16(maxp[4:]))
	long := binary.BigEndian.Uint16(head[50:]) == 1
	offset := func(i int) int {
		if long {
			if 4*i+4 > len(loca) {
				return len(glyf)
			}
			return int(binary.BigEndian.Uint32(loca[4*i:]))
		}
		if 2*i+2 > len(loca) {
			return len(glyf)
		}
		return int(binary.BigEndian.Uint16(loca[2*i:])) * 2
	}
	glyph := func(g int) []byte {
		a, b := offset(g), offset(g+1)
		if a < 0 || b > len(glyf) || a >= b {
			return nil
		}
		return glyf[a:b]
	}

	// Closure over composite glyphs.
	keep := map[int]bool{0: true}
	queue := []int{0}
	for g := range used {
		if int(g) < numGlyphs && !keep[int(g)] {
			keep[int(g)] = true
			queue = append(queue, int(g))
		}
	}
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		data := glyph(g)
		if len(data) < 10 || int16(binary.BigEndian.Uint16(data)) >= 0 {
			continue
		}
		for p := 10; p+4 <= len(data); {
			flags := binary.BigEndian.Uint16(data[p:])
			comp := int(binary.BigEndian.Uint16(data[p+2:]))
			if comp < numGlyphs && !keep[comp] {
				keep[comp] = true
				queue = append(queue, comp)
			}
			p += 4
			if flags&0x0001 != 0 { // ARG_1_AND_2_ARE_WORDS
				p += 4
			} else {
				p += 2
			}
			switch {
			case flags&0x0008 != 0: // WE_HAVE_A_SCALE
				p += 2
			case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
				p += 4
			case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
				p += 8
			}
			if flags&0x0020 == 0 { // MORE_COMPONENTS
				break
			}
		}
	}

	var newGlyf []byte
	newLoca := make([]byte, 4*(numGlyphs+1))
	for g := range numGlyphs {
		binary.BigEndian.PutUint32(newLoca[4*g:], uint32(len(newGlyf)))
		if keep[g] {
			newGlyf = append(newGlyf, glyph(g)...)
			for len(newGlyf)%4 != 0 {
				newGlyf = append(newGlyf, 0)
			}
		}
	}
	binary.BigEndian.PutUint32(newLoca[4*numGlyphs:], uint32(len(newGlyf)))

	newHead := slices.Clone(head)
	binary.BigEndian.PutUint16(newHead[50:], 1) // long loca offsets
	binary.BigEndian.PutUint32(newHead[8:], 0)  // checkSumAdjustment, set below
	out := []table{{"head", newHead}, {"loca", newLoca}, {"glyf", newGlyf}}
	for tag, data := range t {
		if !keepTables[tag] || tag == "head" || tag == "loca" || tag == "glyf" {
			continue
		}
		if tag == "post" && len(data) > 32 { // drop glyph names: format 3
			data = slices.Clone(data[:32])
			binary.BigEndian.PutUint32(data, 0x00030000)
		}
		out = append(out, table{tag, data})
	}
	slices.SortFunc(out, func(a, b table) int {
		if a.tag < b.tag {
			return -1
		}
		return 1
	})
	return writeSfnt(f.Data[:4], out), nil
}

func (f *Face) subsetCFF(used map[uint16]bool) ([]byte, error) {
	t, err := readTables(f.Data)
	if err != nil {
		return nil, err
	}
	cff, err := subsetCFF(t["CFF "], used)
	if err != nil {
		return nil, err
	}
	out := []table{{"CFF ", cff}}
	for tag, data := range t {
		if !keepCFFTables[tag] {
			continue
		}
		switch tag {
		case "head":
			data = slices.Clone(data)
			if len(data) >= 12 {
				binary.BigEndian.PutUint32(data[8:], 0) // checkSumAdjustment, set by writeSfnt
			}
		case "post":
			if len(data) > 32 { // drop glyph names: format 3
				data = slices.Clone(data[:32])
				binary.BigEndian.PutUint32(data, 0x00030000)
			}
		}
		out = append(out, table{tag, data})
	}
	slices.SortFunc(out, func(a, b table) int {
		if a.tag < b.tag {
			return -1
		}
		return 1
	})
	return writeSfnt(f.Data[:4], out), nil
}

// writeSfnt assembles a font file and fixes the head checksum adjustment.
func writeSfnt(version []byte, tables []table) []byte {
	n := len(tables)
	b := make([]byte, 12+16*n)
	copy(b, version)
	binary.BigEndian.PutUint16(b[4:], uint16(n))
	es := 0
	for 1<<(es+1) <= n {
		es++
	}
	binary.BigEndian.PutUint16(b[6:], uint16(16<<es))
	binary.BigEndian.PutUint16(b[8:], uint16(es))
	binary.BigEndian.PutUint16(b[10:], uint16(16*n-(16<<es)))
	headAt := -1
	for i, t := range tables {
		pos := len(b)
		if t.tag == "head" {
			headAt = pos
		}
		b = append(b, t.data...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		r := b[12+16*i:]
		copy(r, t.tag)
		binary.BigEndian.PutUint32(r[4:], checksum(t.data))
		binary.BigEndian.PutUint32(r[8:], uint32(pos))
		binary.BigEndian.PutUint32(r[12:], uint32(len(t.data)))
	}
	if headAt >= 0 {
		binary.BigEndian.PutUint32(b[headAt+8:], 0xB1B0AFBA-checksum(b))
	}
	return b
}
