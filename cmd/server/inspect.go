package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"pdf-formatter/pdf/cos"
)

type inspected struct {
	Ref     string `json:"ref"`
	Type    string `json:"type"`
	Text    string `json:"text"`              // PDF syntax, pretty-printed
	Decoded string `json:"decoded,omitempty"` // stream data, text or hex dump
	Length  int    `json:"length,omitempty"`  // decoded length
	Error   string `json:"error,omitempty"`
}

func inspectRef(d *cos.Document, num int) inspected {
	r := cos.Ref{Num: num}
	return inspect(d, r.String(), d.Get(r))
}

func inspect(d *cos.Document, ref string, o cos.Object) inspected {
	out := inspected{Ref: ref, Type: typeName(o)}
	var sb strings.Builder
	pretty(&sb, o, "")
	out.Text = sb.String()
	if s, ok := o.(*cos.Stream); ok {
		data, err := d.Decode(s)
		if err != nil {
			out.Error = err.Error()
		}
		out.Length = len(data)
		out.Decoded = preview(data)
	}
	return out
}

func typeName(o cos.Object) string {
	switch o.(type) {
	case nil:
		return "null"
	case cos.Dict:
		return "dict"
	case cos.Array:
		return "array"
	case *cos.Stream:
		return "stream"
	}
	return strings.ToLower(strings.TrimPrefix(fmt.Sprintf("%T", o), "cos."))
}

// pretty prints dicts and arrays on several lines when they are large.
func pretty(sb *strings.Builder, o cos.Object, indent string) {
	switch v := o.(type) {
	case *cos.Stream:
		pretty(sb, v.Dict, indent)
		fmt.Fprintf(sb, "\nstream (%d bytes encoded)", len(v.Data))
	case cos.Dict:
		if flat := string(cos.AppendObject(nil, v)); len(flat) <= 80 {
			sb.WriteString(flat)
			return
		}
		sb.WriteString("<<\n")
		for _, k := range sortedKeys(v) {
			sb.WriteString(indent + "  " + string(cos.AppendObject(nil, k)) + " ")
			pretty(sb, v[k], indent+"  ")
			sb.WriteString("\n")
		}
		sb.WriteString(indent + ">>")
	case cos.Array:
		if flat := string(cos.AppendObject(nil, v)); len(flat) <= 80 {
			sb.WriteString(flat)
			return
		}
		sb.WriteString("[\n")
		for _, e := range v {
			sb.WriteString(indent + "  ")
			pretty(sb, e, indent+"  ")
			sb.WriteString("\n")
		}
		sb.WriteString(indent + "]")
	default:
		sb.Write(cos.AppendObject(nil, o))
	}
}

func sortedKeys(d cos.Dict) []cos.Name { return slices.Sorted(maps.Keys(d)) }

// preview shows text as is and binary data as a hex dump, both truncated.
func preview(b []byte) string {
	const limit = 64 << 10
	bin := 0
	for _, c := range b[:min(len(b), 4096)] {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			bin++
		}
	}
	if bin*20 > min(len(b), 4096) {
		b = b[:min(len(b), 4096)]
		var sb strings.Builder
		for i := 0; i < len(b); i += 16 {
			fmt.Fprintf(&sb, "%08x  % x\n", i, b[i:min(i+16, len(b))])
		}
		return sb.String()
	}
	if len(b) > limit {
		return string(b[:limit]) + "\n… (обрезано)"
	}
	return string(b)
}
