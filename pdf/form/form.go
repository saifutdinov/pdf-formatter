// Package form reads interactive forms (AcroForm, §12.7): fields, their
// values and the widget annotations that show them.
package form

import (
	"slices"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
)

// Field is a terminal form field.
type Field struct {
	Name      string   `json:"name"` // fully qualified (§12.7.4.2)
	Type      string   `json:"type"` // text | checkbox | radio | choice | button | signature
	Value     string   `json:"value"`
	Options   []Option `json:"options,omitempty"` // choice: what can be picked
	Multiline bool     `json:"multiline,omitempty"`
	Combo     bool     `json:"combo,omitempty"` // choice: a drop-down rather than a list
	ReadOnly  bool     `json:"readOnly,omitempty"`
	MaxLen    int      `json:"maxLen,omitempty"`
	Align     int      `json:"align,omitempty"`    // /Q: 0 left, 1 centre, 2 right
	FontSize  float64  `json:"fontSize,omitempty"` // from /DA; 0 = fit the box
	Color     [3]float64
	Widgets   []Widget `json:"widgets"`
	Ref       cos.Ref  `json:"-"` // the dictionary holding /V
}

// Option is a choice entry: the value stored and the text shown.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Widget is one place a field is shown.
type Widget struct {
	Ref  cos.Ref    `json:"-"`
	Page cos.Ref    `json:"-"`
	Rect [4]float64 `json:"rect"`
	On   string     `json:"on,omitempty"` // checkbox, radio: the state that means "on"
}

// Field flags (§12.7.3.1, Tables 226–231), as bit masks.
const (
	ffReadOnly   = 1 << 0
	ffMultiline  = 1 << 12
	ffRadio      = 1 << 15
	ffPushButton = 1 << 16
	ffCombo      = 1 << 17
)

// Read returns the fields of the form under the catalog, in order.
func Read(g cos.Getter, catalog cos.Dict) []Field {
	acro := g.Dict(catalog["AcroForm"])
	if acro == nil {
		return nil
	}
	pageOf := map[cos.Ref]cos.Ref{} // widget → page, for widgets without /P
	for _, p := range cos.PageList(g, catalog) {
		for _, a := range g.Array(p.Dict["Annots"]) {
			if r, ok := a.(cos.Ref); ok {
				pageOf[r] = p.Ref
			}
		}
	}
	type inherited struct {
		ft    cos.Name
		ff    int
		v, da cos.Object
		q     int
		opt   cos.Array
		max   int
	}
	var out []Field
	seen := map[cos.Ref]bool{}
	var walk func(o cos.Object, name string, inh inherited, depth int)
	walk = func(o cos.Object, name string, inh inherited, depth int) {
		ref, _ := o.(cos.Ref)
		if seen[ref] || depth > 32 {
			return
		}
		seen[ref] = true
		d := g.Dict(o)
		if d == nil {
			return
		}
		if t, ok := g.Resolve(d["T"]).(cos.String); ok {
			if name != "" {
				name += "."
			}
			name += cos.Text(t)
		}
		if v := g.Name(d["FT"]); v != "" {
			inh.ft = v
		}
		if v, ok := g.Int(d["Ff"]); ok {
			inh.ff = v
		}
		if v, ok := d["V"]; ok {
			inh.v = v
		}
		if v, ok := d["DA"]; ok {
			inh.da = v
		}
		if v, ok := g.Int(d["Q"]); ok {
			inh.q = v
		}
		if v := g.Array(d["Opt"]); v != nil {
			inh.opt = v
		}
		if v, ok := g.Int(d["MaxLen"]); ok {
			inh.max = v
		}
		kids := g.Array(d["Kids"])
		fieldKids := slices.ContainsFunc(kids, func(k cos.Object) bool { return g.Dict(k)["T"] != nil })
		if fieldKids {
			for _, k := range kids {
				walk(k, name, inh, depth+1)
			}
			return
		}
		f := Field{Name: name, Ref: ref, Align: inh.q, MaxLen: inh.max, ReadOnly: inh.ff&ffReadOnly != 0}
		switch inh.ft {
		case "Tx":
			f.Type, f.Multiline = "text", inh.ff&ffMultiline != 0
		case "Btn":
			switch {
			case inh.ff&ffPushButton != 0:
				f.Type = "button"
			case inh.ff&ffRadio != 0:
				f.Type = "radio"
			default:
				f.Type = "checkbox"
			}
		case "Ch":
			f.Type, f.Combo = "choice", inh.ff&ffCombo != 0
		case "Sig":
			f.Type = "signature"
		default:
			return
		}
		f.Value = value(g, inh.v)
		f.FontSize, f.Color = parseDA(g, inh.da)
		for _, o := range inh.opt {
			if pair := g.Array(o); len(pair) == 2 {
				f.Options = append(f.Options, Option{text(g, pair[0]), text(g, pair[1])})
			} else {
				t := text(g, o)
				f.Options = append(f.Options, Option{t, t})
			}
		}
		widgets := kids
		if len(kids) == 0 {
			widgets = cos.Array{o} // field and widget in one dictionary
		}
		for _, w := range widgets {
			wd := g.Dict(w)
			if wd == nil {
				continue
			}
			wr, _ := w.(cos.Ref)
			rect, _ := cos.Rect(g, wd["Rect"])
			wg := Widget{Ref: wr, Rect: rect, On: OnState(g, wd)}
			if p, ok := wd["P"].(cos.Ref); ok {
				wg.Page = p
			} else {
				wg.Page = pageOf[wr]
			}
			f.Widgets = append(f.Widgets, wg)
		}
		out = append(out, f)
	}
	for _, f := range g.Array(acro["Fields"]) {
		walk(f, "", inherited{}, 0)
	}
	return out
}

// OnState is the appearance state of a check box or radio button that
// means "on": any name but Off in its normal appearances.
func OnState(g cos.Getter, widget cos.Dict) string {
	n := g.Dict(g.Dict(widget["AP"])["N"])
	for k := range n {
		if k != "Off" {
			return string(k)
		}
	}
	return ""
}

func text(g cos.Getter, o cos.Object) string {
	if s, ok := g.Resolve(o).(cos.String); ok {
		return cos.Text(s)
	}
	return string(g.Name(o))
}

// value reads /V: text, a name (buttons) or the first of several choices.
func value(g cos.Getter, o cos.Object) string {
	o = g.Resolve(o)
	if a, ok := o.(cos.Array); ok {
		if len(a) == 0 {
			return ""
		}
		o = g.Resolve(a[0])
	}
	return text(g, o)
}

// parseDA reads the size and fill colour of a default appearance string
// such as "/Helv 0 Tf 0 g" (§12.7.4.3).
func parseDA(g cos.Getter, o cos.Object) (size float64, col [3]float64) {
	s, _ := g.Resolve(o).(cos.String)
	for _, op := range content.Parse([]byte(s)) {
		n := func(i int) float64 {
			if i < len(op.Args) {
				v, _ := g.Num(op.Args[i])
				return v
			}
			return 0
		}
		switch op.Name {
		case "Tf":
			size = n(1)
		case "g":
			col = [3]float64{n(0), n(0), n(0)}
		case "rg":
			col = [3]float64{n(0), n(1), n(2)}
		case "k":
			c, m, y, k := n(0), n(1), n(2), n(3)
			col = [3]float64{(1 - c) * (1 - k), (1 - m) * (1 - k), (1 - y) * (1 - k)}
		}
	}
	return size, col
}

// Find returns the field with a name.
func Find(fields []Field, name string) *Field {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

// Valid reports whether value suits the field: an on state or Off for
// buttons, an option for a list, any text up to MaxLen otherwise.
func (f *Field) Valid(value string) bool {
	switch f.Type {
	case "checkbox", "radio":
		return value == "Off" || slices.ContainsFunc(f.Widgets, func(w Widget) bool { return w.On == value })
	case "choice":
		return f.Combo || slices.ContainsFunc(f.Options, func(o Option) bool { return o.Value == value })
	case "text":
		return f.MaxLen == 0 || len([]rune(value)) <= f.MaxLen
	}
	return false
}
