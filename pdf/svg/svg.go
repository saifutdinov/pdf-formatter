// Package svg converts a practical subset of SVG into PDF content
// operators: shapes, paths, groups, transforms, fills, strokes and
// opacity. Everything else is skipped and reported in Warnings.
package svg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
)

// Result is a converted drawing in viewBox units, y axis pointing down
// as in SVG; Ops start with the flip to PDF space (y up) inside the box.
type Result struct {
	Ops      []content.Op
	ViewBox  [4]float64 // min x, min y, width, height
	Width    float64    // natural size in pt
	Height   float64
	Aspect   string                  // preserveAspectRatio: "none" or "meet"
	Alphas   map[cos.Name][2]float64 // ExtGState name → fill, stroke alpha
	Warnings []string                // skipped features, counted: "text ×2"
}

type node struct {
	name     string
	attrs    map[string]string
	children []*node
	text     string
}

func parseXML(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var root *node
	var stack []*node
	for {
		tok, err := d.Token()
		if err != nil {
			if root == nil {
				return nil, errors.New("svg: no <svg> element")
			}
			return root, nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				key := a.Name.Local
				if a.Name.Space == "xlink" || a.Name.Space == "http://www.w3.org/1999/xlink" {
					key = "xlink:" + key
				}
				n.attrs[key] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			} else if n.name == "svg" {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
}

// style is the inherited presentation state.
type style struct {
	fill, stroke                        string
	strokeWidth                         float64
	fillOpacity, strokeOpacity, opacity float64
	fillRule                            string
	lineCap, lineJoin                   string
	miterLimit                          float64
	dash                                []float64
	dashOffset                          float64
	color                               string
	hidden                              bool
}

func defaultStyle() style {
	return style{fill: "black", stroke: "none", strokeWidth: 1, fillOpacity: 1, strokeOpacity: 1, opacity: 1, miterLimit: 4, color: "black"}
}

type converter struct {
	ids      map[string]*node
	classes  map[string]map[string]string // ".cls" or "elem" → declarations
	gradient map[string]string            // gradient id → first stop colour
	ops      []content.Op
	warn     map[string]int
	alphas   map[[2]float64]cos.Name
	depth    int
}

func (c *converter) warnf(what string) { c.warn[what]++ }

// Convert parses an SVG document.
func Convert(data []byte) (*Result, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	c := &converter{ids: map[string]*node{}, classes: map[string]map[string]string{}, gradient: map[string]string{}, warn: map[string]int{}, alphas: map[[2]float64]cos.Name{}}
	c.index(root)
	res := &Result{Aspect: "meet"}
	w, wok := length(root.attrs["width"])
	h, hok := length(root.attrs["height"])
	vb := parseNums(root.attrs["viewBox"])
	switch {
	case len(vb) == 4 && vb[2] > 0 && vb[3] > 0:
		copy(res.ViewBox[:], vb)
	case wok && hok:
		res.ViewBox = [4]float64{0, 0, w, h}
	default:
		res.ViewBox = [4]float64{0, 0, 300, 150} // the SVG default size
	}
	if !wok {
		w = res.ViewBox[2]
	}
	if !hok {
		h = res.ViewBox[3]
	}
	res.Width, res.Height = w*0.75, h*0.75 // px → pt
	if strings.HasPrefix(strings.TrimSpace(root.attrs["preserveAspectRatio"]), "none") {
		res.Aspect = "none"
	}
	// viewBox space with y down → form space with y up.
	vbx, vby, vbh := res.ViewBox[0], res.ViewBox[1], res.ViewBox[3]
	c.ops = append(c.ops, op("cm", 1, 0, 0, -1, -vbx, vbh+vby))
	c.group(root, defaultStyle())
	res.Ops = c.ops
	res.Alphas = map[cos.Name][2]float64{}
	for a, n := range c.alphas {
		res.Alphas[n] = a
	}
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range c.warn {
			if !yield(k) {
				return
			}
		}
	}) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s ×%d", k, c.warn[k]))
	}
	return res, nil
}

func op(name string, nums ...float64) content.Op {
	args := make([]cos.Object, len(nums))
	for i, v := range nums {
		args[i] = cos.Real(v)
	}
	return content.Op{Name: name, Args: args}
}

// index collects ids, CSS rules and gradients.
func (c *converter) index(n *node) {
	if id := n.attrs["id"]; id != "" {
		c.ids[id] = n
	}
	switch n.name {
	case "style":
		c.css(n.text)
	case "linearGradient", "radialGradient":
		for _, s := range n.children {
			if s.name == "stop" {
				col := s.attrs["stop-color"]
				if v := declarations(s.attrs["style"])["stop-color"]; v != "" {
					col = v
				}
				if col != "" {
					c.gradient[n.attrs["id"]] = col
					break
				}
			}
		}
		if _, ok := c.gradient[n.attrs["id"]]; !ok { // stops inherited from another gradient
			if ref := strings.TrimPrefix(n.attrs["xlink:href"]+n.attrs["href"], "#"); ref != "" {
				if g := c.ids[ref]; g != nil {
					c.index(g)
					c.gradient[n.attrs["id"]] = c.gradient[ref]
				}
			}
		}
	}
	for _, ch := range n.children {
		c.index(ch)
	}
}

// css reads simple rules: class and element selectors, comma separated.
func (c *converter) css(text string) {
	for _, rule := range strings.Split(text, "}") {
		sel, body, ok := strings.Cut(rule, "{")
		if !ok {
			continue
		}
		decl := declarations(body)
		for _, s := range strings.Split(sel, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if strings.ContainsAny(s, " >+~:[#") {
				c.warnf("CSS-селекторы сложнее .класс")
				continue
			}
			if c.classes[s] == nil {
				c.classes[s] = map[string]string{}
			}
			for k, v := range decl {
				c.classes[s][k] = v
			}
		}
	}
}

func declarations(s string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(d, ":")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))
		}
	}
	return out
}

// props merges presentation attributes, CSS rules and inline style
// (later wins, as in the CSS cascade for these sources).
func (c *converter) props(n *node) map[string]string {
	p := map[string]string{}
	for k, v := range n.attrs {
		p[k] = v
	}
	for k, v := range c.classes[n.name] {
		p[k] = v
	}
	for _, cls := range strings.Fields(n.attrs["class"]) {
		for k, v := range c.classes["."+cls] {
			p[k] = v
		}
	}
	for k, v := range declarations(n.attrs["style"]) {
		p[k] = v
	}
	return p
}

func (c *converter) inherit(s style, p map[string]string) style {
	num := func(v string, def float64) float64 {
		if f, ok := length(v); ok {
			return f
		}
		return def
	}
	if v, ok := p["fill"]; ok && v != "inherit" {
		s.fill = v
	}
	if v, ok := p["stroke"]; ok && v != "inherit" {
		s.stroke = v
	}
	if v, ok := p["color"]; ok {
		s.color = v
	}
	if v, ok := p["stroke-width"]; ok {
		s.strokeWidth = num(v, s.strokeWidth)
	}
	if v, ok := p["fill-opacity"]; ok {
		s.fillOpacity = opacity(v, s.fillOpacity)
	}
	if v, ok := p["stroke-opacity"]; ok {
		s.strokeOpacity = opacity(v, s.strokeOpacity)
	}
	if v, ok := p["opacity"]; ok { // approximation: multiplies into children
		s.opacity *= opacity(v, 1)
	}
	if v, ok := p["fill-rule"]; ok {
		s.fillRule = v
	}
	if v, ok := p["stroke-linecap"]; ok {
		s.lineCap = v
	}
	if v, ok := p["stroke-linejoin"]; ok {
		s.lineJoin = v
	}
	if v, ok := p["stroke-miterlimit"]; ok {
		s.miterLimit = num(v, s.miterLimit)
	}
	if v, ok := p["stroke-dasharray"]; ok {
		s.dash = nil
		if v != "none" {
			s.dash = parseNums(v)
		}
	}
	if v, ok := p["stroke-dashoffset"]; ok {
		s.dashOffset = num(v, 0)
	}
	if p["display"] == "none" || p["visibility"] == "hidden" {
		s.hidden = true
	}
	return s
}

func opacity(v string, def float64) float64 {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "%") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
			return math.Max(0, math.Min(1, f/100))
		}
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return math.Max(0, math.Min(1, f))
	}
	return def
}

func (c *converter) group(n *node, s style) {
	for _, ch := range n.children {
		c.element(ch, s)
	}
}

func (c *converter) element(n *node, parent style) {
	if c.depth > 64 {
		return
	}
	c.depth++
	defer func() { c.depth-- }()
	p := c.props(n)
	s := c.inherit(parent, p)
	if s.hidden {
		return
	}
	switch n.name {
	case "defs", "title", "desc", "metadata", "style", "linearGradient", "radialGradient", "stop", "symbol", "sodipodi:namedview", "namedview":
		return
	case "g", "a", "switch":
		c.withTransform(p["transform"], func() { c.group(n, s) })
	case "svg": // nested viewport: content drawn without its own viewBox
		x, _ := length(p["x"])
		y, _ := length(p["y"])
		if p["viewBox"] != "" {
			c.warnf("вложенный <svg> с viewBox")
		}
		c.ops = append(c.ops, op("q"), op("cm", 1, 0, 0, 1, x, y))
		c.group(n, s)
		c.ops = append(c.ops, op("Q"))
	case "use":
		ref := strings.TrimPrefix(p["xlink:href"]+p["href"], "#")
		target := c.ids[ref]
		if target == nil || target == n {
			return
		}
		x, _ := length(p["x"])
		y, _ := length(p["y"])
		c.withTransform(p["transform"], func() {
			c.ops = append(c.ops, op("q"), op("cm", 1, 0, 0, 1, x, y))
			if target.name == "symbol" {
				c.group(target, s)
			} else {
				c.element(target, s)
			}
			c.ops = append(c.ops, op("Q"))
		})
	case "path":
		c.withTransform(p["transform"], func() { c.paint(pathOps(p["d"]), s) })
	case "rect":
		x, _ := length(p["x"])
		y, _ := length(p["y"])
		w, _ := length(p["width"])
		h, _ := length(p["height"])
		if w <= 0 || h <= 0 {
			return
		}
		rx, okx := length(p["rx"])
		ry, oky := length(p["ry"])
		if !okx {
			rx = ry
		}
		if !oky {
			ry = rx
		}
		rx, ry = math.Min(rx, w/2), math.Min(ry, h/2)
		var path []content.Op
		if rx > 0 && ry > 0 {
			path = roundRect(x, y, w, h, rx, ry)
		} else {
			path = []content.Op{op("re", x, y, w, h)}
		}
		c.withTransform(p["transform"], func() { c.paint(path, s) })
	case "circle", "ellipse":
		cx, _ := length(p["cx"])
		cy, _ := length(p["cy"])
		rx, _ := length(p["r"])
		ry := rx
		if n.name == "ellipse" {
			rx, _ = length(p["rx"])
			ry, _ = length(p["ry"])
		}
		if rx <= 0 || ry <= 0 {
			return
		}
		c.withTransform(p["transform"], func() { c.paint(ellipse(cx, cy, rx, ry), s) })
	case "line":
		x1, _ := length(p["x1"])
		y1, _ := length(p["y1"])
		x2, _ := length(p["x2"])
		y2, _ := length(p["y2"])
		s.fill = "none"
		c.withTransform(p["transform"], func() { c.paint([]content.Op{op("m", x1, y1), op("l", x2, y2)}, s) })
	case "polyline", "polygon":
		pts := parseNums(p["points"])
		if len(pts) < 4 {
			return
		}
		path := []content.Op{op("m", pts[0], pts[1])}
		for i := 2; i+1 < len(pts); i += 2 {
			path = append(path, op("l", pts[i], pts[i+1]))
		}
		if n.name == "polygon" {
			path = append(path, op("h"))
		}
		c.withTransform(p["transform"], func() { c.paint(path, s) })
	case "text", "tspan", "textPath":
		c.warnf("текст (<text>)")
	case "image":
		c.warnf("растровые картинки внутри SVG")
	case "clipPath", "mask", "filter", "pattern", "marker":
		c.warnf("<" + n.name + ">")
	default:
		if n.name != "" && !strings.Contains(n.name, ":") {
			c.warnf("<" + n.name + ">")
		}
	}
}

func (c *converter) withTransform(t string, f func()) {
	m, ok := parseTransform(t)
	if !ok {
		f()
		return
	}
	c.ops = append(c.ops, op("q"), op("cm", m[:]...))
	f()
	c.ops = append(c.ops, op("Q"))
}

// paint fills and strokes a path with the current style.
func (c *converter) paint(path []content.Op, s style) {
	if len(path) == 0 {
		return
	}
	fill, hasFill := c.color(s.fill, s.color)
	stroke, hasStroke := c.color(s.stroke, s.color)
	hasStroke = hasStroke && s.strokeWidth > 0
	if !hasFill && !hasStroke {
		return
	}
	out := []content.Op{op("q")}
	fa, sa := s.fillOpacity*s.opacity, s.strokeOpacity*s.opacity
	if (hasFill && fa < 1) || (hasStroke && sa < 1) {
		key := [2]float64{math.Round(fa*1000) / 1000, math.Round(sa*1000) / 1000}
		name, ok := c.alphas[key]
		if !ok {
			name = cos.Name(fmt.Sprintf("SVGa%d", len(c.alphas)))
			c.alphas[key] = name
		}
		out = append(out, content.Op{Name: "gs", Args: []cos.Object{name}})
	}
	if hasFill {
		out = append(out, op("rg", fill[0], fill[1], fill[2]))
	}
	if hasStroke {
		out = append(out, op("RG", stroke[0], stroke[1], stroke[2]), op("w", s.strokeWidth))
		if v := map[string]float64{"round": 1, "square": 2}[s.lineCap]; v > 0 {
			out = append(out, op("J", v))
		}
		if v := map[string]float64{"round": 1, "bevel": 2}[s.lineJoin]; v > 0 {
			out = append(out, op("j", v))
		}
		if s.miterLimit != 10 && s.miterLimit >= 1 {
			out = append(out, op("M", s.miterLimit))
		}
		if len(s.dash) > 0 && slices.ContainsFunc(s.dash, func(v float64) bool { return v > 0 }) {
			arr := make(cos.Array, len(s.dash))
			for i, v := range s.dash {
				arr[i] = cos.Real(v)
			}
			out = append(out, content.Op{Name: "d", Args: []cos.Object{arr, cos.Real(s.dashOffset)}})
		}
	}
	out = append(out, path...)
	even := s.fillRule == "evenodd"
	switch {
	case hasFill && hasStroke && even:
		out = append(out, op("B*"))
	case hasFill && hasStroke:
		out = append(out, op("B"))
	case hasFill && even:
		out = append(out, op("f*"))
	case hasFill:
		out = append(out, op("f"))
	default:
		out = append(out, op("S"))
	}
	c.ops = append(c.ops, append(out, op("Q"))...)
}

// color resolves a paint: none, colours, currentColor, gradients (first
// stop colour as an approximation).
func (c *converter) color(v, current string) ([3]float64, bool) {
	v = strings.TrimSpace(v)
	switch {
	case v == "" || v == "none" || v == "transparent":
		return [3]float64{}, false
	case v == "currentColor":
		v = current
	case strings.HasPrefix(v, "url("):
		id := strings.TrimSuffix(strings.TrimPrefix(strings.Fields(v)[0], "url(#"), ")")
		id = strings.Trim(id, `'"`)
		if col, ok := c.gradient[id]; ok {
			c.warnf("градиенты (взят цвет первой точки)")
			v = col
		} else {
			c.warnf("заливка-ссылка без градиента")
			return [3]float64{}, false
		}
	}
	return parseColor(v)
}
