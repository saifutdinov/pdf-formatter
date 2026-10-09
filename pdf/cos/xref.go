package cos

import (
	"bytes"
	"errors"
)

var errXref = errors.New("pdf: bad cross-reference")

func (d *Document) findStartxref() (int, error) {
	tail := d.data[max(0, len(d.data)-4096):]
	i := bytes.LastIndex(tail, []byte("startxref"))
	if i < 0 {
		return 0, errXref
	}
	l := Lexer{B: tail, Pos: i + len("startxref")}
	t := l.Next()
	if t.Kind != TInt {
		return 0, errXref
	}
	return int(t.I), nil
}

// loadXref reads the chain of xref sections, newest first. Entries already
// seen in a newer section win.
func (d *Document) loadXref() error {
	d.xref = map[int]xrefEntry{}
	off, err := d.findStartxref()
	if err != nil {
		return err
	}
	d.startxref = off
	seen := map[int]bool{}
	for first := true; ; first = false {
		if seen[off] {
			break
		}
		seen[off] = true
		trailer, isStream, err := d.readXrefSection(off)
		if err != nil {
			if first {
				return err
			}
			break // broken older section: keep what we have
		}
		if first {
			d.Trailer, d.xrefStream = trailer, isStream
		}
		prev, ok := trailer["Prev"].(Int)
		if !ok {
			break
		}
		off = int(prev)
	}
	return nil
}

func (d *Document) merge(num int, e xrefEntry) {
	if _, ok := d.xref[num]; !ok && num > 0 {
		d.xref[num] = e
	}
}

func (d *Document) readXrefSection(off int) (Dict, bool, error) {
	l := &Lexer{B: d.data, Pos: d.base + off, Refs: true}
	t := l.Next()
	if t.Kind == TKeyword && t.S == "xref" {
		entries, trailer, err := readXrefTable(l)
		if err != nil {
			return nil, false, err
		}
		// Hybrid files: the hidden xref stream takes precedence over the table.
		if xs, ok := trailer["XRefStm"].(Int); ok {
			d.readXrefSection(int(xs))
		}
		for num, e := range entries {
			d.merge(num, e)
		}
		return trailer, false, nil
	}
	o, _, _ := d.parseIndirectAt(d.base + off)
	s, ok := o.(*Stream)
	if !ok || d.inner.Name(s.Dict["Type"]) != "XRef" {
		return nil, false, errXref
	}
	return s.Dict, true, d.readXrefStream(s)
}

func readXrefTable(l *Lexer) (map[int]xrefEntry, Dict, error) {
	entries := map[int]xrefEntry{}
	for {
		t := l.Next()
		if t.Kind == TKeyword && t.S == "trailer" {
			break
		}
		cnt := l.Next()
		if t.Kind != TInt || cnt.Kind != TInt {
			return nil, nil, errXref
		}
		start := int(t.I)
		for i := range int(cnt.I) {
			ot, gt, kt := l.Next(), l.Next(), l.Next()
			if ot.Kind != TInt || gt.Kind != TInt || kt.Kind != TKeyword {
				return nil, nil, errXref
			}
			// Common writer bug: subsection "1 n" that really starts at 0.
			if i == 0 && start == 1 && kt.S == "f" && gt.I == 65535 {
				start = 0
			}
			e := xrefEntry{typ: 1, off: int(ot.I), gen: int(gt.I)}
			if kt.S == "f" {
				e.typ = 0
			}
			if _, ok := entries[start+i]; !ok {
				entries[start+i] = e
			}
		}
	}
	o, err := l.ReadObject(l.Next())
	trailer, ok := o.(Dict)
	if err != nil || !ok {
		return nil, nil, errXref
	}
	return entries, trailer, nil
}

func (d *Document) readXrefStream(s *Stream) error {
	data, err := d.inner.Decode(s)
	if err != nil && len(data) == 0 {
		return err
	}
	var w [3]int
	wa := d.inner.Array(s.Dict["W"])
	if len(wa) < 3 {
		return errXref
	}
	for i := range w {
		w[i], _ = d.inner.Int(wa[i])
		if w[i] < 0 || w[i] > 8 {
			return errXref
		}
	}
	size, _ := d.inner.Int(s.Dict["Size"])
	index := []int{0, size}
	if ia := d.inner.Array(s.Dict["Index"]); len(ia) >= 2 {
		index = index[:0]
		for _, v := range ia {
			n, _ := d.inner.Int(v)
			index = append(index, n)
		}
	}
	field := func(p, n int) int {
		v := 0
		for i := 0; i < n; i++ {
			v = v<<8 | int(data[p+i])
		}
		return v
	}
	rec := w[0] + w[1] + w[2]
	if rec == 0 {
		return errXref
	}
	p := 0
	for i := 0; i+1 < len(index); i += 2 {
		for num := index[i]; num < index[i]+index[i+1]; num++ {
			if p+rec > len(data) {
				return nil
			}
			typ := 1
			if w[0] > 0 {
				typ = field(p, w[0])
			}
			e := xrefEntry{typ: byte(typ), off: field(p+w[0], w[1]), gen: field(p+w[0]+w[1], w[2])}
			p += rec
			if typ <= 2 {
				d.merge(num, e)
			}
		}
	}
	return nil
}

// reconstruct rebuilds the xref by scanning for "num gen obj" (§7.5.4 is
// optional for readers in practice: many real files have broken offsets).
func (d *Document) reconstruct() error {
	d.xref = map[int]xrefEntry{}
	d.resetCache()
	d.Trailer = Dict{}
	d.xrefStream = false
	data := d.data
	var objstms []int
	for i := d.base; i < len(data); {
		j := bytes.Index(data[i:], []byte("obj"))
		if j < 0 {
			break
		}
		p := i + j
		i = p + 3
		if p+3 < len(data) && isRegular(data[p+3]) {
			continue
		}
		start := objStart(data, p)
		if start < 0 {
			continue
		}
		o, num, end := d.parseIndirectAt(start)
		if num < 0 {
			continue
		}
		d.xref[num] = xrefEntry{typ: 1, off: start - d.base}
		d.cache[num] = o
		i = max(i, end)
		if s, ok := o.(*Stream); ok {
			switch d.inner.Name(s.Dict["Type"]) {
			case "XRef":
				for k, v := range s.Dict {
					if k == "Root" || k == "Info" || k == "ID" {
						d.Trailer[k] = v
					}
				}
			case "ObjStm":
				objstms = append(objstms, num)
			}
		}
	}
	for _, num := range objstms {
		s, ok := d.cache[num].(*Stream)
		if !ok {
			continue // overridden by a later object with the same number
		}
		dec, _ := d.inner.Decode(s)
		n, _ := d.inner.Int(s.Dict["N"])
		hdr := &Lexer{B: dec}
		for k := range n {
			t1, t2 := hdr.Next(), hdr.Next()
			if t1.Kind != TInt || t2.Kind != TInt {
				break
			}
			if _, ok := d.xref[int(t1.I)]; !ok {
				d.xref[int(t1.I)] = xrefEntry{typ: 2, off: num, gen: k}
			}
		}
	}
	for i := 0; ; {
		j := bytes.Index(data[i:], []byte("trailer"))
		if j < 0 {
			break
		}
		l := &Lexer{B: data, Pos: i + j + len("trailer"), Refs: true}
		i = l.Pos
		if o, err := l.ReadObject(l.Next()); err == nil {
			if t, ok := o.(Dict); ok {
				for k, v := range t {
					d.Trailer[k] = v
				}
			}
		}
	}
	delete(d.Trailer, "Prev")
	delete(d.Trailer, "XRefStm")
	if !d.rootOK() {
		delete(d.Trailer, "Root")
		for num := range d.xref {
			if dict := d.inner.Dict(d.get(num)); d.inner.Name(dict["Type"]) == "Catalog" {
				d.Trailer["Root"] = Ref{Num: num}
				if d.rootOK() {
					break
				}
			}
		}
	}
	if !d.rootOK() {
		return errors.New("pdf: no document catalog found")
	}
	d.Trailer["Size"] = Int(d.MaxNum() + 1)
	return nil
}

// objStart walks back from "obj" over "num gen " and returns where num starts.
func objStart(b []byte, p int) int {
	k := p - 1
	digits := func() bool {
		e := k
		for k >= 0 && b[k] >= '0' && b[k] <= '9' {
			k--
		}
		return k < e
	}
	white := func() bool {
		e := k
		for k >= 0 && IsWhite(b[k]) {
			k--
		}
		return k < e
	}
	if !white() || !digits() || !white() || !digits() {
		return -1
	}
	if k >= 0 && isRegular(b[k]) {
		return -1
	}
	return k + 1
}
