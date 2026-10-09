package font

import (
	"unicode/utf16"

	"pdf-formatter/pdf/cos"
)

// cmap is a parsed CMap (§9.7.5) or ToUnicode CMap (§9.10.3): code space
// ranges plus code→text and code→CID mappings.
type cmap struct {
	spaces []codespace
	uni    map[uint32]string
	uniR   []uniRange
	cid    map[uint32]int
	cidR   []cidRange
}

type codespace struct {
	n      int // bytes
	lo, hi uint32
}

type uniRange struct {
	lo, hi uint32
	base   []uint16   // dst for lo; incremented for the rest
	list   [][]uint16 // or an explicit list
}

type cidRange struct {
	lo, hi uint32
	cid    int
}

func be(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

func utf16be(b []byte) []uint16 {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	if len(b)%2 == 1 { // single byte destination: treat as a code unit
		u = append(u, uint16(b[len(b)-1]))
	}
	return u
}

func parseCMap(data []byte) *cmap {
	m := &cmap{uni: map[uint32]string{}, cid: map[uint32]int{}}
	l := &cos.Lexer{B: data}
	var stack []cos.Token
	for {
		t := l.Next()
		if t.Kind == cos.TEOF {
			break
		}
		if t.Kind != cos.TKeyword {
			if t.Kind == cos.TArrayOpen {
				// bfrange with an explicit destination array
				var arr []cos.Token
				for {
					e := l.Next()
					if e.Kind == cos.TArrayClose || e.Kind == cos.TEOF {
						break
					}
					arr = append(arr, e)
				}
				stack = append(stack, cos.Token{Kind: cos.TArrayOpen, S: encodeList(arr)})
				continue
			}
			stack = append(stack, t)
			continue
		}
		switch t.S {
		case "endcodespacerange":
			for i := 0; i+1 < len(stack); i += 2 {
				lo, hi := stack[i].S, stack[i+1].S
				if len(lo) > 0 && len(lo) <= 4 && len(lo) == len(hi) {
					m.spaces = append(m.spaces, codespace{len(lo), be([]byte(lo)), be([]byte(hi))})
				}
			}
		case "endbfchar":
			for i := 0; i+1 < len(stack); i += 2 {
				src, dst := stack[i], stack[i+1]
				if dst.Kind == cos.TName {
					if r, ok := glyphRune(dst.S); ok {
						m.uni[be([]byte(src.S))] = string(r)
					}
					continue
				}
				m.uni[be([]byte(src.S))] = string(utf16.Decode(utf16be([]byte(dst.S))))
			}
		case "endbfrange":
			for i := 0; i+2 < len(stack); i += 3 {
				r := uniRange{lo: be([]byte(stack[i].S)), hi: be([]byte(stack[i+1].S))}
				if r.hi < r.lo || r.hi-r.lo > 0xffff {
					continue
				}
				if d := stack[i+2]; d.Kind == cos.TArrayOpen {
					for _, s := range decodeList(d.S) {
						r.list = append(r.list, utf16be([]byte(s)))
					}
				} else {
					r.base = utf16be([]byte(d.S))
				}
				m.uniR = append(m.uniR, r)
			}
		case "endcidchar":
			for i := 0; i+1 < len(stack); i += 2 {
				m.cid[be([]byte(stack[i].S))] = int(stack[i+1].I)
			}
		case "endcidrange":
			for i := 0; i+2 < len(stack); i += 3 {
				m.cidR = append(m.cidR, cidRange{be([]byte(stack[i].S)), be([]byte(stack[i+1].S)), int(stack[i+2].I)})
			}
		}
		stack = stack[:0]
	}
	return m
}

// encodeList packs string tokens for the stack; decodeList unpacks them.
func encodeList(ts []cos.Token) string {
	var b []byte
	for _, t := range ts {
		b = append(b, byte(len(t.S)>>8), byte(len(t.S)))
		b = append(b, t.S...)
	}
	return string(b)
}

func decodeList(s string) []string {
	var out []string
	for i := 0; i+2 <= len(s); {
		n := int(s[i])<<8 | int(s[i+1])
		i += 2
		if i+n > len(s) {
			break
		}
		out = append(out, s[i:i+n])
		i += n
	}
	return out
}

// next splits off the next code from s, returning the code and its length.
func (m *cmap) next(s string) (uint32, int) {
	for n := 1; n <= 4 && n <= len(s); n++ {
		v := be([]byte(s[:n]))
		for _, sp := range m.spaces {
			if sp.n == n && v >= sp.lo && v <= sp.hi {
				return v, n
			}
		}
	}
	// No matching range: use the shortest declared length (§9.7.6.3 says
	// to consume as many bytes as the shortest code space).
	n := 1
	if len(m.spaces) > 0 {
		n = m.spaces[0].n
		for _, sp := range m.spaces {
			n = min(n, sp.n)
		}
	}
	n = min(n, len(s))
	return be([]byte(s[:n])), n
}

func (m *cmap) text(code uint32) (string, bool) {
	if s, ok := m.uni[code]; ok {
		return s, true
	}
	for _, r := range m.uniR {
		if code < r.lo || code > r.hi {
			continue
		}
		off := int(code - r.lo)
		if r.list != nil {
			if off < len(r.list) {
				return string(utf16.Decode(r.list[off])), true
			}
			return "", false
		}
		u := append([]uint16(nil), r.base...)
		if len(u) > 0 {
			u[len(u)-1] += uint16(off)
		}
		return string(utf16.Decode(u)), true
	}
	return "", false
}

func (m *cmap) cidOf(code uint32) int {
	if c, ok := m.cid[code]; ok {
		return c
	}
	for _, r := range m.cidR {
		if code >= r.lo && code <= r.hi {
			return r.cid + int(code-r.lo)
		}
	}
	return 0
}

// identityCMap is the predefined Identity-H/V: two-byte codes, CID = code.
func identityCMap() *cmap {
	return &cmap{
		spaces: []codespace{{2, 0, 0xffff}},
		cidR:   []cidRange{{0, 0xffff, 0}},
	}
}
