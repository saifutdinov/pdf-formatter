package cos

import (
	"bytes"
	"errors"
	"sync"
)

var (
	ErrNotPDF    = errors.New("pdf: not a PDF file")
	ErrEncrypted = errors.New("pdf: encrypted documents are not supported")
)

// Document is a parsed, immutable PDF file. Objects are parsed lazily and
// cached forever: the source bytes never change. Safe for concurrent use.
type Document struct {
	resolver
	data []byte
	base int // offset of "%PDF-": some files have junk before the header

	Trailer       Dict
	Version       string
	Reconstructed bool // xref was rebuilt by scanning: rewrite before editing

	xref       map[int]xrefEntry
	startxref  int  // offset of the newest xref section (for /Prev)
	xrefStream bool // newest section is a cross-reference stream

	mu      sync.Mutex
	inner   resolver // lock-free resolver for use while mu is held
	cache   map[int]Object
	objstms map[int]bool
	pages   []Page
}

type xrefEntry struct {
	typ byte // 0 free, 1 at offset, 2 inside an object stream
	off int  // offset, or object stream number
	gen int  // generation, or index inside the object stream
}

// Open parses the file structure. Objects are read on demand.
func Open(data []byte) (*Document, error) {
	hdr := bytes.Index(data[:min(len(data), 1024)], []byte("%PDF-"))
	if hdr < 0 {
		return nil, ErrNotPDF
	}
	d := &Document{data: data, base: hdr}
	d.resolver = resolver{get: d.Get}
	d.inner = resolver{get: func(r Ref) Object { return d.get(r.Num) }}
	v := data[hdr+5:]
	n := 0
	for n < len(v) && n < 8 && (v[n] >= '0' && v[n] <= '9' || v[n] == '.') {
		n++
	}
	d.Version = string(v[:n])

	d.mu.Lock()
	defer d.mu.Unlock()
	d.resetCache()
	if err := d.loadXref(); err != nil || !d.rootOK() {
		if err := d.reconstruct(); err != nil {
			return nil, err
		}
		d.Reconstructed = true
	}
	if d.Trailer["Encrypt"] != nil {
		return d, ErrEncrypted
	}
	return d, nil
}

func (d *Document) resetCache() {
	d.cache = map[int]Object{}
	d.objstms = map[int]bool{}
}

func (d *Document) rootOK() bool {
	root := d.inner.Dict(d.Trailer["Root"])
	return root != nil && d.inner.Dict(root["Pages"]) != nil
}

// Data returns the original file bytes. Do not modify.
func (d *Document) Data() []byte { return d.data }

// MaxNum is the highest object number in use.
func (d *Document) MaxNum() int {
	m, _ := d.inner.Int(d.Trailer["Size"])
	m--
	for n := range d.xref {
		m = max(m, n)
	}
	return m
}

// Get returns the object with the given reference, or nil.
func (d *Document) Get(r Ref) Object {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.get(r.Num)
}

func (d *Document) get(num int) Object {
	if o, ok := d.cache[num]; ok {
		return o
	}
	var o Object
	switch e, ok := d.xref[num]; {
	case !ok:
	case e.typ == 1:
		o, _, _ = d.parseIndirectAt(d.base + e.off)
	case e.typ == 2:
		d.loadObjStm(e.off)
		o = d.cache[num]
	}
	d.cache[num] = o
	return o
}

// parseIndirectAt parses "num gen obj ... endobj" at off and returns the
// object, its number and the position after it.
func (d *Document) parseIndirectAt(off int) (Object, int, int) {
	if off < 0 || off >= len(d.data) {
		return nil, -1, off
	}
	l := &Lexer{B: d.data, Pos: off, Refs: true}
	t1, t2, t3 := l.Next(), l.Next(), l.Next()
	if t1.Kind != TInt || t2.Kind != TInt || t3.Kind != TKeyword || t3.S != "obj" {
		return nil, -1, off
	}
	o, err := l.ReadObject(l.Next())
	if err != nil {
		return nil, int(t1.I), l.Pos
	}
	if dict, ok := o.(Dict); ok {
		save := l.Pos
		if t := l.Next(); t.Kind == TKeyword && t.S == "stream" {
			o = d.readStream(l, dict)
		} else {
			l.Pos = save
		}
	}
	save := l.Pos
	if t := l.Next(); t.Kind != TKeyword || t.S != "endobj" {
		l.Pos = save
	}
	return o, int(t1.I), l.Pos
}

// readStream reads stream data after the "stream" keyword, trusting /Length
// only when "endstream" follows it.
func (d *Document) readStream(l *Lexer, dict Dict) *Stream {
	b := l.B
	p := l.Pos
	for p < len(b) && (b[p] == ' ' || b[p] == '\t') {
		p++
	}
	if p < len(b) && b[p] == '\r' {
		p++
	}
	if p < len(b) && b[p] == '\n' {
		p++
	}
	start := p
	if n, ok := d.inner.Int(dict["Length"]); ok && n >= 0 && start+n <= len(b) {
		end := Lexer{B: b, Pos: start + n}
		if t := end.Next(); t.Kind == TKeyword && t.S == "endstream" {
			l.Pos = end.Pos
			return &Stream{Dict: dict, Data: b[start : start+n : start+n]}
		}
	}
	i := bytes.Index(b[start:], []byte("endstream"))
	if i < 0 {
		l.Pos = len(b)
		return &Stream{Dict: dict, Data: b[start:len(b):len(b)]}
	}
	end := start + i
	l.Pos = end + len("endstream")
	if end > start && b[end-1] == '\n' {
		end--
	}
	if end > start && b[end-1] == '\r' {
		end--
	}
	return &Stream{Dict: dict, Data: b[start:end:end]}
}

func (d *Document) loadObjStm(num int) {
	if d.objstms[num] {
		return
	}
	d.objstms[num] = true
	s, _ := d.get(num).(*Stream)
	if s == nil {
		return
	}
	data, err := d.inner.Decode(s)
	if err != nil && len(data) == 0 {
		return
	}
	n, _ := d.inner.Int(s.Dict["N"])
	first, _ := d.inner.Int(s.Dict["First"])
	hdr := &Lexer{B: data}
	for range n {
		t1, t2 := hdr.Next(), hdr.Next()
		if t1.Kind != TInt || t2.Kind != TInt {
			return
		}
		objNum := int(t1.I)
		if e, ok := d.xref[objNum]; ok && (e.typ != 2 || e.off != num) {
			continue // a newer version lives elsewhere
		}
		if _, done := d.cache[objNum]; done {
			continue
		}
		l := &Lexer{B: data, Pos: first + int(t2.I), Refs: true}
		if o, err := l.ReadObject(l.Next()); err == nil {
			d.cache[objNum] = o
		}
	}
}

// Page is a leaf of the page tree with inherited attributes applied (§7.7.3.4).
type Page struct {
	Ref       Ref
	Dict      Dict
	Resources Dict
	MediaBox  [4]float64
	CropBox   [4]float64
	Rotate    int // 0, 90, 180 or 270
}

// Pages returns the document's pages, computed once.
func (d *Document) Pages() []Page {
	d.mu.Lock()
	cached := d.pages
	d.mu.Unlock()
	if cached != nil {
		return cached
	}
	pages := PageList(d, d.Trailer["Root"])
	d.mu.Lock()
	d.pages = pages
	d.mu.Unlock()
	return pages
}

// PageList walks the page tree under the catalog root.
func PageList(g Getter, root Object) []Page {
	out := []Page{}
	cat := g.Dict(root)
	if cat == nil {
		return out
	}
	type inherited struct {
		res         Object
		media, crop Object
		rotate      Object
	}
	seen := map[Ref]bool{}
	var walk func(node Object, inh inherited, depth int)
	walk = func(node Object, inh inherited, depth int) {
		ref, _ := node.(Ref)
		if ref != (Ref{}) {
			if seen[ref] {
				return
			}
			seen[ref] = true
		}
		dict := g.Dict(node)
		if dict == nil || depth > 64 {
			return
		}
		if v, ok := dict["Resources"]; ok {
			inh.res = v
		}
		if v, ok := dict["MediaBox"]; ok {
			inh.media = v
		}
		if v, ok := dict["CropBox"]; ok {
			inh.crop = v
		}
		if v, ok := dict["Rotate"]; ok {
			inh.rotate = v
		}
		kids := g.Array(dict["Kids"])
		if g.Name(dict["Type"]) == "Page" || (kids == nil && g.Name(dict["Type"]) != "Pages") {
			p := Page{Ref: ref, Dict: dict, Resources: g.Dict(inh.res)}
			var ok bool
			if p.MediaBox, ok = Rect(g, inh.media); !ok {
				p.MediaBox = [4]float64{0, 0, 612, 792}
			}
			if p.CropBox, ok = Rect(g, inh.crop); !ok {
				p.CropBox = p.MediaBox
			}
			r, _ := g.Int(inh.rotate)
			p.Rotate = ((r%360 + 360) % 360) / 90 * 90
			out = append(out, p)
			return
		}
		for _, k := range kids {
			walk(k, inh, depth+1)
		}
	}
	walk(cat["Pages"], inherited{}, 0)
	return out
}
