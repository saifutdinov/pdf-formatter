// Package cos implements the PDF object layer (ISO 32000-1 §7.2–7.5):
// parsing objects and file structure, resolving references, and writing
// files back as incremental updates or full rewrites.
package cos

import "fmt"

// Object is any PDF object. A PDF null is the Go nil.
type Object any

type (
	Bool   bool
	Int    int64
	Real   float64
	Name   string
	String string // raw bytes, not necessarily UTF-8
	Array  []Object
	Dict   map[Name]Object
	Ref    struct{ Num, Gen int }
)

// Stream holds the raw (still encoded) data; see Decode.
type Stream struct {
	Dict Dict
	Data []byte
}

func (r Ref) String() string { return fmt.Sprintf("%d %d R", r.Num, r.Gen) }

// Getter fetches indirect objects. Implemented by *Document and *Update,
// both of which get the typed helpers below through an embedded resolver.
type Getter interface {
	Get(Ref) Object
	Resolve(Object) Object
	Dict(Object) Dict
	Array(Object) Array
	Stream(Object) *Stream
	Num(Object) (float64, bool)
	Int(Object) (int, bool)
	Name(Object) Name
	Decode(*Stream) ([]byte, error)
}

type resolver struct{ get func(Ref) Object }

func (r resolver) Get(ref Ref) Object { return r.get(ref) }

// Resolve follows references until a direct object (or nil) is reached.
func (r resolver) Resolve(o Object) Object {
	for range 32 {
		ref, ok := o.(Ref)
		if !ok {
			return o
		}
		o = r.get(ref)
	}
	return nil
}

func (r resolver) Dict(o Object) Dict {
	switch v := r.Resolve(o).(type) {
	case Dict:
		return v
	case *Stream:
		return v.Dict
	}
	return nil
}

func (r resolver) Array(o Object) Array {
	a, _ := r.Resolve(o).(Array)
	return a
}

func (r resolver) Stream(o Object) *Stream {
	s, _ := r.Resolve(o).(*Stream)
	return s
}

func (r resolver) Num(o Object) (float64, bool) {
	switch v := r.Resolve(o).(type) {
	case Int:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

func (r resolver) Int(o Object) (int, bool) {
	switch v := r.Resolve(o).(type) {
	case Int:
		return int(v), true
	case Real:
		return int(v), true
	}
	return 0, false
}

func (r resolver) Name(o Object) Name {
	n, _ := r.Resolve(o).(Name)
	return n
}

// Rect reads a rectangle array, normalised so that x0<x1, y0<y1.
func Rect(g Getter, o Object) ([4]float64, bool) {
	a := g.Array(o)
	var r [4]float64
	if len(a) < 4 {
		return r, false
	}
	for i := range r {
		v, ok := g.Num(a[i])
		if !ok {
			return r, false
		}
		r[i] = v
	}
	if r[0] > r[2] {
		r[0], r[2] = r[2], r[0]
	}
	if r[1] > r[3] {
		r[1], r[3] = r[3], r[1]
	}
	return r, true
}

// Copy returns a deep copy of direct objects; references are kept as is.
func Copy(o Object) Object {
	switch v := o.(type) {
	case Array:
		c := make(Array, len(v))
		for i, e := range v {
			c[i] = Copy(e)
		}
		return c
	case Dict:
		c := make(Dict, len(v))
		for k, e := range v {
			c[k] = Copy(e)
		}
		return c
	case *Stream:
		return &Stream{Dict: Copy(v.Dict).(Dict), Data: v.Data}
	}
	return o
}
