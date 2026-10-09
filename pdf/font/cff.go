package font

import (
	"encoding/binary"
	"errors"
)

// CFF subsetting (Adobe TN 5176): the charstrings of unused glyphs become a
// lone endchar, everything else is copied, so glyph ids, the charset and
// the subroutines stay valid. Offsets in the rebuilt dictionaries are
// written as 5-byte integers, which fixes their sizes before the layout.
// ponytail: subroutines are kept whole and seac accents (deprecated in
// Type 2) do not pull in their components; trim subrs if files stay large.

var errCFF = errors.New("font: bad CFF")

type cffIndex struct {
	items [][]byte
	start int // offset of the index
	end   int // offset just past it
}

func readIndex(b []byte, off int) (cffIndex, error) {
	if off < 0 || off+2 > len(b) {
		return cffIndex{}, errCFF
	}
	count := int(binary.BigEndian.Uint16(b[off:]))
	if count == 0 {
		return cffIndex{start: off, end: off + 2}, nil
	}
	if off+3 > len(b) {
		return cffIndex{}, errCFF
	}
	osz := int(b[off+2])
	if osz < 1 || osz > 4 || off+3+(count+1)*osz > len(b) {
		return cffIndex{}, errCFF
	}
	at := func(i int) int {
		v := 0
		for _, c := range b[off+3+i*osz : off+3+(i+1)*osz] {
			v = v<<8 | int(c)
		}
		return v
	}
	base := off + 3 + (count+1)*osz - 1 // offsets count from 1
	idx := cffIndex{start: off, end: base + at(count)}
	if idx.end > len(b) {
		return cffIndex{}, errCFF
	}
	for i := range count {
		a, z := base+at(i), base+at(i+1)
		if a > z || z > len(b) {
			return cffIndex{}, errCFF
		}
		idx.items = append(idx.items, b[a:z])
	}
	return idx, nil
}

func writeIndex(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte{0, 0}
	}
	total := 1
	for _, it := range items {
		total += len(it)
	}
	osz := 1
	for total >= 1<<(8*osz) {
		osz++
	}
	b := []byte{byte(len(items) >> 8), byte(len(items)), byte(osz)}
	put := func(v int) {
		for i := osz - 1; i >= 0; i-- {
			b = append(b, byte(v>>(8*i)))
		}
	}
	off := 1
	put(off)
	for _, it := range items {
		off += len(it)
		put(off)
	}
	for _, it := range items {
		b = append(b, it...)
	}
	return b
}

// dictEntry is an operator with its operands: raw bytes, and their values
// when they are integers.
type dictEntry struct {
	op   int // 12 x is 1200+x
	raw  []byte
	args []int
}

func parseDict(b []byte) ([]dictEntry, error) {
	var out []dictEntry
	start := 0
	var args []int
	for p := 0; p < len(b); {
		c := b[p]
		switch {
		case c <= 21:
			op := int(c)
			raw := b[start:p]
			p++
			if c == 12 {
				if p >= len(b) {
					return nil, errCFF
				}
				op = 1200 + int(b[p])
				p++
			}
			out = append(out, dictEntry{op, raw, args})
			start, args = p, nil
		case c == 28:
			if p+3 > len(b) {
				return nil, errCFF
			}
			args = append(args, int(int16(binary.BigEndian.Uint16(b[p+1:]))))
			p += 3
		case c == 29:
			if p+5 > len(b) {
				return nil, errCFF
			}
			args = append(args, int(int32(binary.BigEndian.Uint32(b[p+1:]))))
			p += 5
		case c == 30: // real: nibbles up to 0xf
			p++
			for p < len(b) && b[p]&0x0f != 0x0f && b[p]>>4 != 0x0f {
				p++
			}
			p++
			args = append(args, 0)
		case c >= 32 && c <= 246:
			args = append(args, int(c)-139)
			p++
		case c >= 247 && c <= 254:
			if p+2 > len(b) {
				return nil, errCFF
			}
			v := (int(c)-247)*256 + int(b[p+1]) + 108
			if c >= 251 {
				v = -(int(c)-251)*256 - int(b[p+1]) - 108
			}
			args = append(args, v)
			p += 2
		default:
			return nil, errCFF
		}
	}
	return out, nil
}

func int5(v int) []byte { return []byte{29, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }

// writeDict writes entries, with the operands of the given operators
// replaced by 5-byte integers.
func writeDict(entries []dictEntry, set map[int][]int) []byte {
	var b []byte
	for _, e := range entries {
		if v, ok := set[e.op]; ok {
			for _, x := range v {
				b = append(b, int5(x)...)
			}
		} else {
			b = append(b, e.raw...)
		}
		if e.op >= 1200 {
			b = append(b, 12, byte(e.op-1200))
		} else {
			b = append(b, byte(e.op))
		}
	}
	return b
}

func dictArgs(entries []dictEntry, op int) []int {
	for _, e := range entries {
		if e.op == op {
			return e.args
		}
	}
	return nil
}

// tableLen measures a charset, encoding or FDSelect so it can be copied.
func charsetLen(b []byte, off, glyphs int) (int, error) {
	if off >= len(b) {
		return 0, errCFF
	}
	switch b[off] {
	case 0:
		return 1 + 2*(glyphs-1), nil
	case 1, 2:
		p, left := off+1, glyphs-1
		for left > 0 {
			if p+3 > len(b) {
				return 0, errCFF
			}
			n := int(b[p+2])
			p += 3
			if b[off] == 2 {
				if p > len(b)-1 {
					return 0, errCFF
				}
				n = int(binary.BigEndian.Uint16(b[p-1:]))
				p++
			}
			left -= n + 1
		}
		return p - off, nil
	}
	return 0, errCFF
}

func encodingLen(b []byte, off int) (int, error) {
	if off+2 > len(b) {
		return 0, errCFF
	}
	n := 2 + int(b[off+1])
	if b[off]&0x7f == 1 {
		n = 2 + 2*int(b[off+1])
	}
	if b[off]&0x80 != 0 {
		if off+n >= len(b) {
			return 0, errCFF
		}
		n += 1 + 3*int(b[off+n])
	}
	return n, nil
}

func fdSelectLen(b []byte, off, glyphs int) (int, error) {
	if off+3 > len(b) {
		return 0, errCFF
	}
	switch b[off] {
	case 0:
		return 1 + glyphs, nil
	case 3:
		return 1 + 2 + 3*int(binary.BigEndian.Uint16(b[off+1:])) + 2, nil
	}
	return 0, errCFF
}

// private is a Private DICT followed by its local subroutines.
type private struct {
	data []byte
	size int // of the dictionary
}

// privateBlock rebuilds a Private DICT with its local subroutines right
// after it.
func privateBlock(b []byte, size, off int) (private, error) {
	if off < 0 || size < 0 || off+size > len(b) {
		return private{}, errCFF
	}
	entries, err := parseDict(b[off : off+size])
	if err != nil {
		return private{}, err
	}
	subrs := dictArgs(entries, 19)
	if len(subrs) != 1 {
		return private{b[off : off+size], size}, nil
	}
	idx, err := readIndex(b, off+subrs[0])
	if err != nil {
		return private{}, err
	}
	dict := writeDict(entries, map[int][]int{19: {0}})
	dict = writeDict(entries, map[int][]int{19: {len(dict)}})
	return private{append(dict, b[idx.start:idx.end]...), len(dict)}, nil
}

// subsetCFF returns a CFF table with only the used glyphs drawn.
func subsetCFF(b []byte, used map[uint16]bool) ([]byte, error) {
	if len(b) < 4 {
		return nil, errCFF
	}
	hdr := int(b[2])
	names, err := readIndex(b, hdr)
	if err != nil {
		return nil, err
	}
	tops, err := readIndex(b, names.end)
	if err != nil || len(tops.items) != 1 {
		return nil, errCFF
	}
	strs, err := readIndex(b, tops.end)
	if err != nil {
		return nil, err
	}
	gsubrs, err := readIndex(b, strs.end)
	if err != nil {
		return nil, err
	}
	top, err := parseDict(tops.items[0])
	if err != nil {
		return nil, err
	}
	csArg := dictArgs(top, 17)
	if len(csArg) != 1 {
		return nil, errCFF
	}
	cs, err := readIndex(b, csArg[0])
	if err != nil {
		return nil, err
	}
	glyphs := len(cs.items)
	strings := make([][]byte, glyphs)
	for g, s := range cs.items {
		strings[g] = s
		if g != 0 && !used[uint16(g)] {
			strings[g] = []byte{14} // endchar
		}
	}

	// Blocks after the fixed part, each with the operator that points to it.
	type block struct {
		op   int
		data []byte
	}
	var blocks []block
	copyAt := func(op int, measure func(off int) (int, error)) error {
		a := dictArgs(top, op)
		if len(a) != 1 || a[0] <= 2 { // predefined charset or encoding
			return nil
		}
		n, err := measure(a[0])
		if err != nil || a[0]+n > len(b) {
			return errCFF
		}
		blocks = append(blocks, block{op, b[a[0] : a[0]+n]})
		return nil
	}
	if err := copyAt(15, func(off int) (int, error) { return charsetLen(b, off, glyphs) }); err != nil {
		return nil, err
	}
	if err := copyAt(16, func(off int) (int, error) { return encodingLen(b, off) }); err != nil {
		return nil, err
	}
	if err := copyAt(1237, func(off int) (int, error) { return fdSelectLen(b, off, glyphs) }); err != nil {
		return nil, err
	}
	blocks = append(blocks, block{17, writeIndex(strings)})

	var privates []private // the top Private, or one per font dict
	var fds [][]dictEntry
	if fa := dictArgs(top, 1236); len(fa) == 1 {
		fdIdx, err := readIndex(b, fa[0])
		if err != nil {
			return nil, err
		}
		for _, item := range fdIdx.items {
			fd, err := parseDict(item)
			if err != nil {
				return nil, err
			}
			p := dictArgs(fd, 18)
			if len(p) != 2 {
				return nil, errCFF
			}
			pb, err := privateBlock(b, p[0], p[1])
			if err != nil {
				return nil, err
			}
			fds, privates = append(fds, fd), append(privates, pb)
		}
	} else if p := dictArgs(top, 18); len(p) == 2 {
		pb, err := privateBlock(b, p[0], p[1])
		if err != nil {
			return nil, err
		}
		privates = append(privates, pb)
	}

	// Layout: sizes are fixed (5-byte offsets), so offsets follow.
	set := map[int][]int{}
	for _, bl := range blocks {
		set[bl.op] = []int{0}
	}
	fdItems := func(privOffs []int) [][]byte {
		var out [][]byte
		for i, fd := range fds {
			out = append(out, writeDict(fd, map[int][]int{18: {privates[i].size, privOffs[i]}}))
		}
		return out
	}
	if len(fds) > 0 {
		set[1236] = []int{0}
	} else if len(privates) == 1 {
		set[18] = []int{privates[0].size, 0}
	}
	pos := hdr + (names.end - names.start) + len(writeIndex([][]byte{writeDict(top, set)})) + (strs.end - strs.start) + (gsubrs.end - gsubrs.start)
	for _, bl := range blocks {
		set[bl.op] = []int{pos}
		pos += len(bl.data)
	}
	zeros := make([]int, len(fds))
	var fdIndex []byte
	if len(fds) > 0 {
		set[1236] = []int{pos}
		pos += len(writeIndex(fdItems(zeros)))
	}
	privOffs := make([]int, len(privates))
	for i, p := range privates {
		privOffs[i] = pos
		pos += len(p.data)
	}
	if len(fds) > 0 {
		fdIndex = writeIndex(fdItems(privOffs))
	} else if len(privates) == 1 {
		set[18] = []int{privates[0].size, privOffs[0]}
	}

	out := append([]byte{}, b[:hdr]...)
	out = append(out, b[names.start:names.end]...)
	out = append(out, writeIndex([][]byte{writeDict(top, set)})...)
	out = append(out, b[strs.start:strs.end]...)
	out = append(out, b[gsubrs.start:gsubrs.end]...)
	for _, bl := range blocks {
		out = append(out, bl.data...)
	}
	out = append(out, fdIndex...)
	for _, p := range privates {
		out = append(out, p.data...)
	}
	if len(out) != pos {
		return nil, errCFF
	}
	return out, nil
}
