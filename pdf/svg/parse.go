package svg

import (
	"math"
	"strconv"
	"strings"

	"pdf-formatter/pdf/content"
)

// length reads a number with an optional unit, in px (user units).
func length(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, "%") {
		return 0, false
	}
	units := map[string]float64{"px": 1, "pt": 4.0 / 3, "pc": 16, "mm": 96 / 25.4, "cm": 96 / 2.54, "in": 96, "em": 16, "ex": 8}
	k := 1.0
	for u, f := range units {
		if strings.HasSuffix(s, u) {
			s, k = strings.TrimSuffix(s, u), f
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v * k, true
}

// numbers splits a list of numbers: "1,2 -3.5e2.5" → 1 2 -350 0.5.
type numbers struct {
	s string
	i int
}

func (n *numbers) skip() {
	for n.i < len(n.s) && (n.s[n.i] == ' ' || n.s[n.i] == ',' || n.s[n.i] == '\t' || n.s[n.i] == '\n' || n.s[n.i] == '\r') {
		n.i++
	}
}

func (n *numbers) next() (float64, bool) {
	n.skip()
	start := n.i
	if n.i < len(n.s) && (n.s[n.i] == '+' || n.s[n.i] == '-') {
		n.i++
	}
	digits, dot := false, false
	for n.i < len(n.s) {
		c := n.s[n.i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case c == '.' && !dot:
			dot = true
		case (c == 'e' || c == 'E') && digits && n.i+1 < len(n.s) && (n.s[n.i+1] >= '0' && n.s[n.i+1] <= '9' || n.s[n.i+1] == '-' || n.s[n.i+1] == '+'):
			n.i++
			if n.s[n.i] == '-' || n.s[n.i] == '+' {
				n.i++
			}
			for n.i < len(n.s) && n.s[n.i] >= '0' && n.s[n.i] <= '9' {
				n.i++
			}
			v, err := strconv.ParseFloat(n.s[start:n.i], 64)
			return v, err == nil
		default:
			goto done
		}
		n.i++
	}
done:
	if !digits {
		n.i = start
		return 0, false
	}
	v, err := strconv.ParseFloat(n.s[start:n.i], 64)
	return v, err == nil
}

// flag reads an arc flag, which may be written without a separator.
func (n *numbers) flag() (float64, bool) {
	n.skip()
	if n.i < len(n.s) && (n.s[n.i] == '0' || n.s[n.i] == '1') {
		n.i++
		return float64(n.s[n.i-1] - '0'), true
	}
	return 0, false
}

func parseNums(s string) []float64 {
	n := &numbers{s: s}
	var out []float64
	for {
		v, ok := n.next()
		if !ok {
			return out
		}
		out = append(out, v)
	}
}

// parseTransform composes a transform list ("translate(…) rotate(…)").
func parseTransform(s string) (content.Matrix, bool) {
	total := content.Identity
	found := false
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		open := strings.IndexByte(s, '(')
		end := strings.IndexByte(s, ')')
		if open < 0 || end < open {
			break
		}
		name := strings.TrimSpace(strings.TrimLeft(s[:open], ", "))
		a := parseNums(s[open+1 : end])
		s = s[end+1:]
		arg := func(i int, def float64) float64 {
			if i < len(a) {
				return a[i]
			}
			return def
		}
		var m content.Matrix
		switch name {
		case "matrix":
			if len(a) != 6 {
				continue
			}
			copy(m[:], a)
		case "translate":
			m = content.Matrix{1, 0, 0, 1, arg(0, 0), arg(1, 0)}
		case "scale":
			sx := arg(0, 1)
			m = content.Matrix{sx, 0, 0, arg(1, sx), 0, 0}
		case "rotate":
			r := arg(0, 0) * math.Pi / 180
			c, sn := math.Cos(r), math.Sin(r)
			m = content.Matrix{c, sn, -sn, c, 0, 0}
			if len(a) == 3 { // around (cx, cy)
				cx, cy := a[1], a[2]
				m = content.Matrix{1, 0, 0, 1, -cx, -cy}.Mul(m).Mul(content.Matrix{1, 0, 0, 1, cx, cy})
			}
		case "skewX":
			m = content.Matrix{1, 0, math.Tan(arg(0, 0) * math.Pi / 180), 1, 0, 0}
		case "skewY":
			m = content.Matrix{1, math.Tan(arg(0, 0) * math.Pi / 180), 0, 1, 0, 0}
		default:
			continue
		}
		// The rightmost transform applies to the points first.
		total = m.Mul(total)
		found = true
	}
	return total, found
}

var named = map[string]uint32{
	"black": 0x000000, "white": 0xffffff, "red": 0xff0000, "green": 0x008000, "blue": 0x0000ff, "yellow": 0xffff00,
	"cyan": 0x00ffff, "aqua": 0x00ffff, "magenta": 0xff00ff, "fuchsia": 0xff00ff, "gray": 0x808080, "grey": 0x808080,
	"silver": 0xc0c0c0, "maroon": 0x800000, "olive": 0x808000, "lime": 0x00ff00, "teal": 0x008080, "navy": 0x000080,
	"purple": 0x800080, "orange": 0xffa500, "pink": 0xffc0cb, "brown": 0xa52a2a, "gold": 0xffd700, "darkgray": 0xa9a9a9,
	"darkgrey": 0xa9a9a9, "lightgray": 0xd3d3d3, "lightgrey": 0xd3d3d3, "darkblue": 0x00008b, "darkgreen": 0x006400,
	"darkred": 0x8b0000, "lightblue": 0xadd8e6, "lightgreen": 0x90ee90, "coral": 0xff7f50, "crimson": 0xdc143c,
	"indigo": 0x4b0082, "violet": 0xee82ee, "khaki": 0xf0e68c, "salmon": 0xfa8072, "tomato": 0xff6347, "orangered": 0xff4500,
	"steelblue": 0x4682b4, "skyblue": 0x87ceeb, "royalblue": 0x4169e1, "slategray": 0x708090, "slategrey": 0x708090,
	"beige": 0xf5f5dc, "ivory": 0xfffff0, "tan": 0xd2b48c, "chocolate": 0xd2691e, "forestgreen": 0x228b22, "seagreen": 0x2e8b57,
	"darkorange": 0xff8c00, "dodgerblue": 0x1e90ff, "firebrick": 0xb22222, "whitesmoke": 0xf5f5f5, "gainsboro": 0xdcdcdc,
	"lightyellow": 0xffffe0, "turquoise": 0x40e0d0, "plum": 0xdda0dd, "orchid": 0xda70d6, "sienna": 0xa0522d, "wheat": 0xf5deb3,
	"lavender": 0xe6e6fa, "mintcream": 0xf5fffa, "honeydew": 0xf0fff0, "snow": 0xfffafa, "linen": 0xfaf0e6,
}

func parseColor(v string) ([3]float64, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	rgb := func(u uint32) [3]float64 {
		return [3]float64{float64(u>>16&255) / 255, float64(u>>8&255) / 255, float64(u&255) / 255}
	}
	switch {
	case strings.HasPrefix(v, "#"):
		h := v[1:]
		if len(h) == 3 || len(h) == 4 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) == 8 {
			h = h[:6]
		}
		u, err := strconv.ParseUint(h, 16, 32)
		if err != nil || len(h) != 6 {
			return [3]float64{}, false
		}
		return rgb(uint32(u)), true
	case strings.HasPrefix(v, "rgb"):
		open, end := strings.IndexByte(v, '('), strings.IndexByte(v, ')')
		if open < 0 || end < open {
			return [3]float64{}, false
		}
		parts := strings.FieldsFunc(v[open+1:end], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(parts) < 3 {
			return [3]float64{}, false
		}
		var c [3]float64
		for i := range c {
			p := parts[i]
			if strings.HasSuffix(p, "%") {
				f, _ := strconv.ParseFloat(strings.TrimSuffix(p, "%"), 64)
				c[i] = f / 100
			} else {
				f, _ := strconv.ParseFloat(p, 64)
				c[i] = f / 255
			}
			c[i] = math.Max(0, math.Min(1, c[i]))
		}
		return c, true
	case strings.HasPrefix(v, "hsl"):
		a := parseNums(strings.NewReplacer("%", " ", "deg", " ").Replace(v[strings.IndexByte(v, '(')+1:]))
		if len(a) < 3 {
			return [3]float64{}, false
		}
		h, s, l := math.Mod(a[0], 360)/360, a[1]/100, a[2]/100
		if h < 0 {
			h++
		}
		hue := func(p, q, t float64) float64 {
			t = t - math.Floor(t)
			switch {
			case t < 1.0/6:
				return p + (q-p)*6*t
			case t < 0.5:
				return q
			case t < 2.0/3:
				return p + (q-p)*(2.0/3-t)*6
			}
			return p
		}
		q := l * (1 + s)
		if l >= 0.5 {
			q = l + s - l*s
		}
		p := 2*l - q
		return [3]float64{hue(p, q, h+1.0/3), hue(p, q, h), hue(p, q, h-1.0/3)}, true
	}
	if u, ok := named[v]; ok {
		return rgb(u), true
	}
	return [3]float64{}, false
}

const kappa = 0.5522847498 // cubic Bézier approximation of a quarter circle

func ellipse(cx, cy, rx, ry float64) []content.Op {
	kx, ky := kappa*rx, kappa*ry
	return []content.Op{
		op("m", cx+rx, cy),
		op("c", cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry),
		op("c", cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy),
		op("c", cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry),
		op("c", cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy),
		op("h"),
	}
}

func roundRect(x, y, w, h, rx, ry float64) []content.Op {
	kx, ky := kappa*rx, kappa*ry
	return []content.Op{
		op("m", x+rx, y),
		op("l", x+w-rx, y),
		op("c", x+w-rx+kx, y, x+w, y+ry-ky, x+w, y+ry),
		op("l", x+w, y+h-ry),
		op("c", x+w, y+h-ry+ky, x+w-rx+kx, y+h, x+w-rx, y+h),
		op("l", x+rx, y+h),
		op("c", x+rx-kx, y+h, x, y+h-ry+ky, x, y+h-ry),
		op("l", x, y+ry),
		op("c", x, y+ry-ky, x+rx-kx, y, x+rx, y),
		op("h"),
	}
}

// pathOps converts SVG path data (§9 of SVG 1.1) to PDF path operators.
func pathOps(d string) []content.Op {
	n := &numbers{s: d}
	var out []content.Op
	var cx, cy, sx, sy float64 // current point, subpath start
	var lcx, lcy float64       // last control point (for S and T)
	var prev byte
	var cmd byte
	for {
		n.skip()
		if n.i >= len(n.s) {
			return out
		}
		if c := n.s[n.i]; c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			cmd = c
			n.i++
		} else if cmd == 0 {
			return out
		}
		rel := cmd >= 'a'
		var ox, oy float64
		if rel {
			ox, oy = cx, cy
		}
		read := func(k int) ([]float64, bool) {
			v := make([]float64, k)
			for i := range v {
				var ok bool
				if v[i], ok = n.next(); !ok {
					return nil, false
				}
			}
			return v, true
		}
		upper := cmd &^ 0x20
		switch upper {
		case 'Z':
			out = append(out, op("h"))
			cx, cy = sx, sy
			prev = 'Z'
			continue
		case 'M':
			v, ok := read(2)
			if !ok {
				return out
			}
			cx, cy = ox+v[0], oy+v[1]
			sx, sy = cx, cy
			out = append(out, op("m", cx, cy))
			// Further pairs are implicit lineto.
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
		case 'L':
			v, ok := read(2)
			if !ok {
				return out
			}
			cx, cy = ox+v[0], oy+v[1]
			out = append(out, op("l", cx, cy))
		case 'H':
			v, ok := read(1)
			if !ok {
				return out
			}
			cx = ox + v[0]
			out = append(out, op("l", cx, cy))
		case 'V':
			v, ok := read(1)
			if !ok {
				return out
			}
			if rel {
				cy += v[0]
			} else {
				cy = v[0]
			}
			out = append(out, op("l", cx, cy))
		case 'C':
			v, ok := read(6)
			if !ok {
				return out
			}
			x1, y1, x2, y2 := ox+v[0], oy+v[1], ox+v[2], oy+v[3]
			cx, cy = ox+v[4], oy+v[5]
			out = append(out, op("c", x1, y1, x2, y2, cx, cy))
			lcx, lcy = x2, y2
		case 'S':
			v, ok := read(4)
			if !ok {
				return out
			}
			x1, y1 := cx, cy
			if prev == 'C' || prev == 'S' {
				x1, y1 = 2*cx-lcx, 2*cy-lcy
			}
			x2, y2 := ox+v[0], oy+v[1]
			cx, cy = ox+v[2], oy+v[3]
			out = append(out, op("c", x1, y1, x2, y2, cx, cy))
			lcx, lcy = x2, y2
		case 'Q', 'T':
			var qx, qy float64
			if upper == 'Q' {
				v, ok := read(4)
				if !ok {
					return out
				}
				qx, qy = ox+v[0], oy+v[1]
				ox, oy = ox+v[2], oy+v[3]
			} else {
				v, ok := read(2)
				if !ok {
					return out
				}
				qx, qy = cx, cy
				if prev == 'Q' || prev == 'T' {
					qx, qy = 2*cx-lcx, 2*cy-lcy
				}
				ox, oy = ox+v[0], oy+v[1]
			}
			// Quadratic → cubic: control points at 2/3 towards the quad point.
			out = append(out, op("c", cx+2.0/3*(qx-cx), cy+2.0/3*(qy-cy), ox+2.0/3*(qx-ox), oy+2.0/3*(qy-oy), ox, oy))
			cx, cy, lcx, lcy = ox, oy, qx, qy
		case 'A':
			v := make([]float64, 7)
			ok := true
			for i := range v {
				if i == 3 || i == 4 {
					v[i], ok = n.flag()
				} else {
					v[i], ok = n.next()
				}
				if !ok {
					return out
				}
			}
			x2, y2 := ox+v[5], oy+v[6]
			out = append(out, arc(cx, cy, v[0], v[1], v[2], v[3] != 0, v[4] != 0, x2, y2)...)
			cx, cy = x2, y2
		default:
			return out
		}
		prev = upper
	}
}

// arc converts an elliptical arc to cubic Béziers (SVG 1.1 F.6.5).
func arc(x1, y1, rx, ry, angle float64, large, sweep bool, x2, y2 float64) []content.Op {
	if x1 == x2 && y1 == y2 {
		return nil
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		return []content.Op{op("l", x2, y2)}
	}
	phi := angle * math.Pi / 180
	cs, sn := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p, y1p := cs*dx+sn*dy, -sn*dx+cs*dy
	if l := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); l > 1 {
		rx, ry = rx*math.Sqrt(l), ry*math.Sqrt(l)
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := math.Sqrt(math.Max(0, num/den))
	if large == sweep {
		coef = -coef
	}
	cxp, cyp := coef*rx*y1p/ry, -coef*ry*x1p/rx
	cx := cs*cxp - sn*cyp + (x1+x2)/2
	cy := sn*cxp + cs*cyp + (y1+y2)/2
	ang := func(ux, uy, vx, vy float64) float64 { return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy) }
	t1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dt := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	} else if sweep && dt < 0 {
		dt += 2 * math.Pi
	}
	segs := int(math.Ceil(math.Abs(dt) / (math.Pi / 2)))
	step := dt / float64(segs)
	alpha := 4.0 / 3 * math.Tan(step/4)
	point := func(t float64) (float64, float64) {
		return cx + rx*math.Cos(t)*cs - ry*math.Sin(t)*sn, cy + rx*math.Cos(t)*sn + ry*math.Sin(t)*cs
	}
	deriv := func(t float64) (float64, float64) {
		return -rx*math.Sin(t)*cs - ry*math.Cos(t)*sn, -rx*math.Sin(t)*sn + ry*math.Cos(t)*cs
	}
	var out []content.Op
	for i := range segs {
		a, b := t1+float64(i)*step, t1+float64(i+1)*step
		ax, ay := point(a)
		bx, by := point(b)
		dax, day := deriv(a)
		dbx, dby := deriv(b)
		if i == segs-1 {
			bx, by = x2, y2 // land exactly on the end point
		}
		out = append(out, op("c", ax+alpha*dax, ay+alpha*day, bx-alpha*dbx, by-alpha*dby, bx, by))
	}
	return out
}
