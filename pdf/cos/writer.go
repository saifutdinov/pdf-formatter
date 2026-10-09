package cos

import (
	"crypto/rand"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
)

// AppendObject serialises a direct object (§7.3).
func AppendObject(b []byte, o Object) []byte {
	switch v := o.(type) {
	case nil:
		return append(b, "null"...)
	case Bool:
		return strconv.AppendBool(b, bool(v))
	case Int:
		return strconv.AppendInt(b, int64(v), 10)
	case Real:
		return AppendReal(b, float64(v))
	case Name:
		return appendName(b, v)
	case String:
		return appendString(b, string(v))
	case Ref:
		b = strconv.AppendInt(b, int64(v.Num), 10)
		b = append(b, ' ')
		b = strconv.AppendInt(b, int64(v.Gen), 10)
		return append(b, " R"...)
	case Array:
		b = append(b, '[')
		for i, e := range v {
			if i > 0 {
				b = append(b, ' ')
			}
			b = AppendObject(b, e)
		}
		return append(b, ']')
	case Dict:
		b = append(b, "<<"...)
		for _, k := range slices.Sorted(mapsKeys(v)) {
			if v[k] == nil {
				continue
			}
			b = appendName(b, k)
			b = append(b, ' ')
			b = AppendObject(b, v[k])
		}
		return append(b, ">>"...)
	case *Stream:
		d := Copy(v.Dict).(Dict)
		d["Length"] = Int(len(v.Data))
		b = AppendObject(b, d)
		b = append(b, "\nstream\n"...)
		b = append(b, v.Data...)
		return append(b, "\nendstream"...)
	}
	panic(fmt.Sprintf("cos: cannot serialise %T", o))
}

func mapsKeys(d Dict) func(func(Name) bool) {
	return func(yield func(Name) bool) {
		for k := range d {
			if !yield(k) {
				return
			}
		}
	}
}

// AppendReal writes a number without exponent, rounded to 9 decimals
// (enough to keep parsed values, short for computed ones).
func AppendReal(b []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(b, '0')
	}
	f = math.Round(f*1e9) / 1e9
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.AppendInt(b, int64(f), 10)
	}
	return strconv.AppendFloat(b, f, 'f', -1, 64)
}

func appendName(b []byte, n Name) []byte {
	b = append(b, '/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < 0x21 || c > 0x7e || c == '#' || IsDelim(c) {
			b = append(b, '#', "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&15])
		} else {
			b = append(b, c)
		}
	}
	return b
}

func appendString(b []byte, s string) []byte {
	bin := 0
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\n' && c != '\r' && c != '\t' || c > 0x7e {
			bin++
		}
	}
	if bin > len(s)/4 {
		b = append(b, '<')
		for i := 0; i < len(s); i++ {
			b = append(b, "0123456789ABCDEF"[s[i]>>4], "0123456789ABCDEF"[s[i]&15])
		}
		return append(b, '>')
	}
	b = append(b, '(')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '(', ')', '\\':
			b = append(b, '\\', c)
		case '\r':
			b = append(b, `\r`...)
		default:
			if c < 0x20 && c != '\n' && c != '\t' || c > 0x7e {
				b = append(b, '\\', '0'+c>>6, '0'+(c>>3)&7, '0'+c&7)
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, ')')
}

func appendIndirect(b []byte, r Ref, o Object) []byte {
	b = strconv.AppendInt(b, int64(r.Num), 10)
	b = append(b, ' ')
	b = strconv.AppendInt(b, int64(r.Gen), 10)
	b = append(b, " obj\n"...)
	b = AppendObject(b, o)
	return append(b, "\nendobj\n"...)
}

// Update is a set of changed and new objects layered over a Document.
// The Document stays untouched; Get sees the changes.
type Update struct {
	resolver
	Doc     *Document
	Trailer Dict // entries overriding the document trailer (e.g. Info)
	// Decrypt drops the encryption of an encrypted document in WriteClean.
	// Otherwise everything written is encrypted like the original; an
	// incremental update always is.
	Decrypt bool
	objs    map[int]Object
	gens    map[int]int
	next    int
}

func NewUpdate(d *Document) *Update {
	u := &Update{Doc: d, Trailer: Dict{}, objs: map[int]Object{}, gens: map[int]int{}, next: d.MaxNum() + 1}
	u.resolver = resolver{get: u.Get}
	return u
}

func (u *Update) Get(r Ref) Object {
	if o, ok := u.objs[r.Num]; ok {
		return o
	}
	return u.Doc.Get(r)
}

// Set replaces an object. Callers must not mutate objects returned by Get:
// copy them (Copy) and Set the copy.
func (u *Update) Set(r Ref, o Object) {
	u.objs[r.Num] = o
	u.gens[r.Num] = r.Gen
}

// Add stores a new object and returns its reference.
func (u *Update) Add(o Object) Ref {
	r := Ref{Num: u.next}
	u.next++
	u.Set(r, o)
	return r
}

// Changed reports whether anything was set.
func (u *Update) Changed() bool { return len(u.objs) > 0 }

// Root returns the catalog reference.
func (u *Update) Root() Object { return u.trailer()["Root"] }

func (u *Update) trailer() Dict {
	t := Copy(u.Doc.Trailer).(Dict)
	maps.Copy(t, u.Trailer)
	return t
}

// Pages walks the page tree with the changes applied.
func (u *Update) Pages() []Page {
	if !u.Changed() {
		return u.Doc.Pages()
	}
	return PageList(u, u.Root())
}

func newID() String {
	b := make([]byte, 16)
	rand.Read(b)
	return String(b)
}

// WriteIncremental writes the original bytes followed by an update section
// (§7.5.6) containing only the changed objects.
func (u *Update) WriteIncremental(w io.Writer) error {
	if _, err := w.Write(u.Doc.data); err != nil {
		return err
	}
	_, err := w.Write(u.Tail())
	return err
}

// Tail returns the update section that follows the original bytes.
func (u *Update) Tail() []byte {
	d := u.Doc
	orig := d.data
	var b []byte
	if len(orig) > 0 && orig[len(orig)-1] != '\n' && orig[len(orig)-1] != '\r' {
		b = append(b, '\n')
	}
	pos := func() int { return len(orig) + len(b) - d.base }
	nums := slices.Sorted(func(yield func(int) bool) {
		for n := range u.objs {
			if !yield(n) {
				return
			}
		}
	})
	offsets := map[int]int{}
	for _, n := range nums {
		offsets[n] = pos()
		b = appendIndirect(b, Ref{n, u.gens[n]}, d.crypt.sealed(u.objs[n], n, u.gens[n]))
	}
	t := u.trailer()
	for _, k := range []Name{"Prev", "XRefStm", "Type", "W", "Index", "Filter", "DecodeParms", "Length", "DL"} {
		delete(t, k)
	}
	// A rebuilt xref has no valid section to point back to: write a complete
	// cross-reference stream instead of a chained one.
	full := d.Reconstructed
	if !full {
		t["Prev"] = Int(d.startxref)
	}
	size := max(u.next, d.MaxNum()+1)
	if id := d.inner.Array(t["ID"]); len(id) == 2 {
		t["ID"] = Array{id[0], newID()}
	} else {
		id := newID()
		t["ID"] = Array{id, id}
	}
	xrefOff := pos()
	if d.xrefStream || full {
		self := size
		size++
		offsets[self] = xrefOff
		entries := map[int]xrefEntry{}
		if full {
			maps.Copy(entries, d.xref)
		}
		for _, n := range append(nums, self) {
			entries[n] = xrefEntry{typ: 1, off: offsets[n], gen: u.gens[n]}
		}
		t["Type"] = Name("XRef")
		t["Size"] = Int(size)
		t["W"] = Array{Int(1), Int(4), Int(2)}
		var idx Array
		var data []byte
		for _, run := range runs(slices.Collect(maps.Keys(entries))) {
			idx = append(idx, Int(run[0]), Int(len(run)))
			for _, n := range run {
				e := entries[n]
				data = append(data, e.typ, byte(e.off>>24), byte(e.off>>16), byte(e.off>>8), byte(e.off), byte(e.gen>>8), byte(e.gen))
			}
		}
		t["Index"] = idx
		t["Filter"] = Name("FlateDecode")
		b = appendIndirect(b, Ref{Num: self}, &Stream{Dict: t, Data: Deflate(data)})
	} else {
		t["Size"] = Int(size)
		b = append(b, "xref\n"...)
		for _, run := range runs(nums) {
			b = fmt.Appendf(b, "%d %d\n", run[0], len(run))
			for _, n := range run {
				b = fmt.Appendf(b, "%010d %05d n\r\n", offsets[n], u.gens[n])
			}
		}
		b = append(b, "trailer\n"...)
		b = AppendObject(b, t)
		b = append(b, '\n')
	}
	return fmt.Appendf(b, "startxref\n%d\n%%%%EOF\n", xrefOff)
}

// runs splits sorted numbers into consecutive runs (xref subsections).
func runs(nums []int) [][]int {
	slices.Sort(nums)
	var out [][]int
	for i, n := range nums {
		if i == 0 || n != nums[i-1]+1 {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], n)
	}
	return out
}

// WriteClean writes a fresh file with only the objects reachable from the
// trailer, renumbered from 1. Old revisions and garbage are dropped.
func (u *Update) WriteClean(w io.Writer) error {
	t := u.trailer()
	keep := Dict{"Root": t["Root"]}
	if t["Info"] != nil {
		keep["Info"] = t["Info"]
	}
	c := u.Doc.crypt
	if u.Decrypt {
		c = nil
	}
	encRef, _ := t["Encrypt"].(Ref)
	if c != nil {
		keep["Encrypt"] = t["Encrypt"]
	}
	var order []Ref
	newNum := map[int]int{}
	stack := []Object{keep} // iterative: long /Next chains would blow the stack
	for len(stack) > 0 {
		o := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v := o.(type) {
		case Ref:
			if _, ok := newNum[v.Num]; ok {
				continue
			}
			obj := u.Get(v)
			if obj == nil {
				continue
			}
			newNum[v.Num] = len(order) + 1
			order = append(order, v)
			stack = append(stack, obj)
		case Array:
			for i := len(v) - 1; i >= 0; i-- {
				stack = append(stack, v[i])
			}
		case Dict:
			for _, k := range slices.Sorted(mapsKeys(v)) {
				stack = append(stack, v[k])
			}
		case *Stream:
			stack = append(stack, v.Dict)
		}
	}
	var remap func(o Object) Object
	remap = func(o Object) Object {
		switch v := o.(type) {
		case Ref:
			if n, ok := newNum[v.Num]; ok {
				return Ref{Num: n}
			}
			return nil
		case Array:
			c := make(Array, len(v))
			for i, e := range v {
				c[i] = remap(e)
			}
			return c
		case Dict:
			c := make(Dict, len(v))
			for k, e := range v {
				if r := remap(e); r != nil {
					c[k] = r
				}
			}
			return c
		case *Stream:
			return &Stream{Dict: remap(v.Dict).(Dict), Data: v.Data}
		}
		return o
	}
	version := u.Doc.Version
	if version < "1.4" {
		version = "1.4"
	}
	b := []byte("%PDF-" + version + "\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(order)+1)
	for i, r := range order {
		offsets[i+1] = len(b)
		o := remap(u.Get(r))
		if r != encRef {
			o = c.sealed(o, i+1, 0)
		}
		b = appendIndirect(b, Ref{Num: i + 1}, o)
	}
	xrefOff := len(b)
	b = fmt.Appendf(b, "xref\n0 %d\n0000000000 65535 f\r\n", len(order)+1)
	for _, off := range offsets[1:] {
		b = fmt.Appendf(b, "%010d 00000 n\r\n", off)
	}
	nt := remap(keep).(Dict)
	nt["Size"] = Int(len(order) + 1)
	id := newID()
	nt["ID"] = Array{id, id}
	if old := u.Doc.inner.Array(t["ID"]); c != nil && len(old) == 2 {
		nt["ID"] = Array{old[0], id} // the key derives from the first id
	}
	b = append(b, "trailer\n"...)
	b = AppendObject(b, nt)
	b = fmt.Appendf(b, "\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	_, err := w.Write(b)
	return err
}

// Import deep-copies an object graph from another document into u,
// returning the copied object. memo maps source refs to new refs and can
// be shared across calls to keep shared resources shared.
func (u *Update) Import(src Getter, o Object, memo map[Ref]Ref) Object {
	switch v := o.(type) {
	case Ref:
		if r, ok := memo[v]; ok {
			return r
		}
		r := u.Add(nil)
		memo[v] = r
		u.Set(r, u.Import(src, src.Get(v), memo))
		return r
	case Array:
		c := make(Array, len(v))
		for i, e := range v {
			c[i] = u.Import(src, e, memo)
		}
		return c
	case Dict:
		c := make(Dict, len(v))
		for k, e := range v {
			if k == "Parent" {
				continue // never drag in the source page tree
			}
			c[k] = u.Import(src, e, memo)
		}
		return c
	case *Stream:
		return &Stream{Dict: u.Import(src, v.Dict, memo).(Dict), Data: v.Data}
	}
	return o
}
