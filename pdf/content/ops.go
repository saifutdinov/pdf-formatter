// Package content parses, interprets and writes content streams (§7.8, §8, §9).
package content

import (
	"bytes"
	"maps"
	"slices"

	"pdf-formatter/pdf/cos"
)

// Op is one operator with its operands. Inline images are a single "BI" op
// with the image dictionary in Args[0] and the raw data in Data.
type Op struct {
	Name string
	Args []cos.Object
	Data []byte
}

// Parse tokenizes a content stream. It never fails: junk is skipped.
func Parse(data []byte) []Op {
	l := &cos.Lexer{B: data}
	var ops []Op
	var args []cos.Object
	for {
		t := l.Next()
		if t.Kind == cos.TEOF {
			return ops
		}
		if t.Kind != cos.TKeyword {
			o, err := l.ReadObject(t)
			if err == nil {
				args = append(args, o)
			}
			continue
		}
		switch t.S {
		case "true", "false", "null":
			o, _ := l.ReadObject(t)
			args = append(args, o)
			continue
		case "BI":
			ops = append(ops, inlineImage(l))
		default:
			ops = append(ops, Op{Name: t.S, Args: args})
		}
		args = nil
	}
}

func inlineImage(l *cos.Lexer) Op {
	dict := cos.Dict{}
	for {
		t := l.Next()
		if t.Kind == cos.TEOF {
			return Op{Name: "BI", Args: []cos.Object{dict}}
		}
		if t.Kind == cos.TKeyword && t.S == "ID" {
			break
		}
		if t.Kind != cos.TName {
			continue
		}
		v, _ := l.ReadObject(l.Next())
		dict[cos.Name(t.S)] = v
	}
	b := l.B
	start := l.Pos
	if start < len(b) && cos.IsWhite(b[start]) {
		start++
	}
	end := -1
	if n, ok := dict["L"].(cos.Int); ok && start+int(n) <= len(b) {
		end = start + int(n)
		l.Pos = end
		if t := l.Next(); t.Kind != cos.TKeyword || t.S != "EI" {
			end = -1
		}
	}
	if end < 0 {
		end = len(b)
		l.Pos = len(b)
		for i := start; ; {
			k := bytes.Index(b[i:], []byte("EI"))
			if k < 0 {
				break
			}
			p := i + k
			if p > start && cos.IsWhite(b[p-1]) && (p+2 == len(b) || cos.IsWhite(b[p+2])) {
				end = p - 1
				l.Pos = p + 2
				break
			}
			i = p + 2
		}
	}
	return Op{Name: "BI", Args: []cos.Object{dict}, Data: b[start:end:end]}
}

// Write serialises ops back into a content stream.
func Write(ops []Op) []byte {
	var b []byte
	for _, op := range ops {
		b = AppendOp(b, op)
	}
	return b
}

func AppendOp(b []byte, op Op) []byte {
	if op.Name == "BI" {
		b = append(b, "BI"...)
		if len(op.Args) > 0 {
			if d, ok := op.Args[0].(cos.Dict); ok {
				for _, k := range slices.Sorted(maps.Keys(d)) {
					b = append(b, ' ')
					b = cos.AppendObject(b, k)
					b = append(b, ' ')
					b = cos.AppendObject(b, d[k])
				}
			}
		}
		b = append(b, " ID "...)
		b = append(b, op.Data...)
		return append(b, "\nEI\n"...)
	}
	for _, a := range op.Args {
		b = cos.AppendObject(b, a)
		b = append(b, ' ')
	}
	b = append(b, op.Name...)
	return append(b, '\n')
}

// PageContent returns the decoded, concatenated content streams of a page.
func PageContent(g cos.Getter, page cos.Dict) []byte {
	var parts []*cos.Stream
	switch c := g.Resolve(page["Contents"]).(type) {
	case *cos.Stream:
		parts = append(parts, c)
	case cos.Array:
		for _, e := range c {
			if s := g.Stream(e); s != nil {
				parts = append(parts, s)
			}
		}
	}
	var out []byte
	for _, s := range parts {
		data, _ := g.Decode(s)
		out = append(out, data...)
		out = append(out, '\n') // streams may split tokens only at whitespace
	}
	return out
}
