package edit

import (
	"math"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/form"
)

// flatWidget is a form widget drawn into the page content (§12.5.5).
type flatWidget struct {
	ap    cos.Ref
	cm    [6]float64 // maps the appearance onto the widget rectangle
	annot cos.Ref
}

// fill sets form values (§12.7.4): /V on the field, the state of check
// boxes and radio buttons, and new appearances for text and choices. The
// appearances use the default library font, so any script shows.
func (r *renderer) fill(fields []form.Field, values map[string]string) error {
	u := r.u
	for name, v := range values {
		f := form.Find(fields, name)
		if f == nil || f.Ref == (cos.Ref{}) || f.ReadOnly || !f.Valid(v) {
			continue
		}
		fd := cos.Copy(u.Dict(f.Ref)).(cos.Dict)
		switch f.Type {
		case "checkbox", "radio":
			fd["V"] = cos.Name(v)
			u.Set(f.Ref, fd)
			for _, w := range f.Widgets {
				wd := cos.Copy(u.Dict(w.Ref)).(cos.Dict)
				wd["AS"] = cos.Name("Off")
				if w.On == v {
					wd["AS"] = cos.Name(v)
				}
				u.Set(w.Ref, wd)
			}
		case "text", "choice":
			fd["V"] = cos.TextString(v)
			u.Set(f.Ref, fd)
			shown := v
			for _, o := range f.Options {
				if o.Value == v {
					shown = o.Label
				}
			}
			for _, w := range f.Widgets {
				wd := cos.Copy(u.Dict(w.Ref)).(cos.Dict)
				ap, err := r.fieldAppearance(f, wd, w, shown)
				if err != nil {
					return err
				}
				wd["AP"] = cos.Dict{"N": ap}
				u.Set(w.Ref, wd)
			}
		}
	}
	return nil
}

// fieldAppearance draws a text or choice field: background and border from
// /MK, the text clipped to the box, one line centred vertically or several
// from the top.
func (r *renderer) fieldAppearance(f *form.Field, wd cos.Dict, w form.Widget, text string) (cos.Ref, error) {
	fe, err := r.embed("")
	if err != nil {
		return cos.Ref{}, err
	}
	face := fe.e.Face
	wid, hgt := math.Abs(w.Rect[2]-w.Rect[0]), math.Abs(w.Rect[3]-w.Rect[1])
	mk := r.u.Dict(wd["MK"])
	var ops []content.Op
	if bg, ok := mkColor(r.u, mk["BG"]); ok {
		ops = append(ops, op("rg", bg...), op("re", reals(0, 0, wid, hgt)...), op("f"))
	}
	if bc, ok := mkColor(r.u, mk["BC"]); ok {
		bw := 1.0
		if v, ok := r.u.Num(r.u.Dict(wd["BS"])["W"]); ok {
			bw = v
		}
		if bw > 0 {
			ops = append(ops, op("RG", bc...), op("w", cos.Real(bw)), op("re", reals(bw/2, bw/2, wid-bw, hgt-bw)...), op("S"))
		}
	}
	size := f.FontSize
	if size <= 0 { // auto: fit the box
		size = math.Min(12, math.Max(4, (hgt-4)*0.7))
		if f.Multiline {
			size = math.Min(size, 10)
		}
	}
	asc, desc, _, _, _ := face.Metrics()
	asc, desc = asc/1000*size, desc/1000*size
	lines := []Line{{Text: text, Width: face.TextWidth(text) / 1000 * size}}
	if f.Multiline {
		lines = Wrap(face, text, size, wid-4)
	}
	ops = append(ops, op("BMC", cos.Name("Tx")), op("q"), op("re", reals(1, 1, wid-2, hgt-2)...), op("W"), op("n"),
		op("BT"), op("Tf", cos.Name("F1"), cos.Real(size)), op("rg", reals(f.Color[0], f.Color[1], f.Color[2])...))
	y := (hgt-(asc-desc))/2 - desc
	if f.Multiline {
		y = hgt - 2 - asc
	}
	for i, l := range lines {
		x := 2.0
		switch f.Align {
		case 1:
			x = (wid - l.Width) / 2
		case 2:
			x = wid - 2 - l.Width
		}
		ops = append(ops, op("Tm", reals(1, 0, 0, 1, x, y-float64(i)*size*1.15)...), op("Tj", fe.e.Encode(l.Text)))
	}
	ops = append(ops, op("ET"), op("Q"), op("EMC"))
	s := stream(content.Write(ops))
	s.Dict["Type"], s.Dict["Subtype"] = cos.Name("XObject"), cos.Name("Form")
	s.Dict["BBox"] = rectArray([4]float64{0, 0, wid, hgt})
	s.Dict["Resources"] = cos.Dict{"Font": cos.Dict{"F1": fe.ref}}
	return r.u.Add(s), nil
}

// mkColor reads a colour array of /MK: none, gray, RGB or CMYK.
func mkColor(g cos.Getter, o cos.Object) ([]cos.Object, bool) {
	a := g.Array(o)
	n := func(i int) float64 { v, _ := g.Num(a[i]); return v }
	switch len(a) {
	case 1:
		return reals(n(0), n(0), n(0)), true
	case 3:
		return reals(n(0), n(1), n(2)), true
	case 4:
		k := n(3)
		return reals((1-n(0))*(1-k), (1-n(1))*(1-k), (1-n(2))*(1-k)), true
	}
	return nil, false
}

// flattenPlan lists, per page, the visible widgets with their current
// appearance (§12.5.5: the appearance box is fitted onto the rectangle).
func (r *renderer) flattenPlan(fields []form.Field) map[cos.Ref][]flatWidget {
	u := r.u
	out := map[cos.Ref][]flatWidget{}
	for _, f := range fields {
		for _, w := range f.Widgets {
			wd := u.Dict(w.Ref)
			if flags, _ := u.Int(wd["F"]); flags&2 != 0 || w.Page == (cos.Ref{}) { // hidden
				continue
			}
			n := u.Dict(wd["AP"])["N"]
			if states := u.Dict(n); states != nil && u.Stream(n) == nil {
				n = states[u.Name(wd["AS"])]
			}
			ref, ok := n.(cos.Ref)
			ap := u.Stream(n)
			if !ok || ap == nil {
				continue
			}
			bbox, ok := cos.Rect(u, ap.Dict["BBox"])
			if !ok || bbox[2] == bbox[0] || bbox[3] == bbox[1] {
				continue
			}
			m := content.Identity
			if a := u.Array(ap.Dict["Matrix"]); len(a) == 6 {
				for i := range m {
					m[i], _ = u.Num(a[i])
				}
			}
			// The transformed box, fitted onto the rectangle.
			box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
			for _, p := range [][2]float64{{bbox[0], bbox[1]}, {bbox[2], bbox[1]}, {bbox[2], bbox[3]}, {bbox[0], bbox[3]}} {
				x, y := m.Apply(p[0], p[1])
				box = [4]float64{math.Min(box[0], x), math.Min(box[1], y), math.Max(box[2], x), math.Max(box[3], y)}
			}
			rect := w.Rect
			sx, sy := (rect[2]-rect[0])/(box[2]-box[0]), (rect[3]-rect[1])/(box[3]-box[1])
			if ap.Dict["Subtype"] != cos.Name("Form") { // Do needs a form XObject
				c := &cos.Stream{Dict: cos.Copy(ap.Dict).(cos.Dict), Data: ap.Data}
				c.Dict["Type"], c.Dict["Subtype"] = cos.Name("XObject"), cos.Name("Form")
				u.Set(ref, c)
			}
			out[w.Page] = append(out[w.Page], flatWidget{ap: ref, annot: w.Ref,
				cm: [6]float64{sx, 0, 0, sy, rect[0] - box[0]*sx, rect[1] - box[1]*sy}})
		}
	}
	return out
}
