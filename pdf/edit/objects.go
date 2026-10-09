package edit

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strings"
	"unicode"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/font"
	"pdf-formatter/pdf/svg"

	_ "golang.org/x/image/webp"
)

// Object is an object owned by the editor: generated entirely from this
// model. Its local frame has the origin at the top-left corner, x right,
// y up; the box is [0,W]×[-H,0]. Matrix places the frame on the page.
type Object struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"` // text | rect | ellipse | line | image
	Matrix      [6]float64 `json:"matrix"`
	W           float64    `json:"w"`
	H           float64    `json:"h"`
	Padding     [4]float64 `json:"padding"` // top, right, bottom, left
	Fill        string     `json:"fill,omitempty"`
	Stroke      string     `json:"stroke,omitempty"`
	StrokeWidth float64    `json:"strokeWidth,omitempty"`
	Opacity     float64    `json:"opacity"`
	// Text: Runs give parts of it their own style; Text is then their
	// concatenation.
	Text       string  `json:"text,omitempty"`
	Runs       []Run   `json:"runs,omitempty"`
	Font       string  `json:"font,omitempty"` // library font id; "" = default
	Size       float64 `json:"size,omitempty"`
	Color      string  `json:"color,omitempty"`
	Align      string  `json:"align,omitempty"`  // left | center | right | justify
	VAlign     string  `json:"valign,omitempty"` // top | middle | bottom
	LineHeight float64 `json:"lineHeight,omitempty"`
	AutoHeight bool    `json:"autoHeight,omitempty"`
	// Image
	Asset string `json:"asset,omitempty"`
	// Artifact marks generated page furniture (Header, Footer) for tagged
	// PDF and screen readers (§14.8.2.2).
	Artifact string `json:"artifact,omitempty"`
	// Table: text style comes from the text fields, borders from Stroke.
	Cols       []float64  `json:"cols,omitempty"` // column widths (pt)
	Rows       []TableRow `json:"rows,omitempty"`
	HeaderFill string     `json:"headerFill,omitempty"` // background of the first row; "" = no header row
	CellPad    float64    `json:"cellPad,omitempty"`
}

// TableRow is one table row. ponytail: no merged cells and no splitting
// across pages; both fit this model (spans per cell, a row cursor per page).
type TableRow struct {
	Cells  []string `json:"cells"`
	Height float64  `json:"height,omitempty"` // minimum height (pt), 0 = by content
}

// TableLayout returns row heights: each row is as tall as its tallest cell.
func (o *Object) TableLayout(face *font.Face) []float64 {
	size := o.Size
	if size <= 0 {
		size = 10
	}
	asc, desc, _, _, _ := face.Metrics()
	lineH := (asc - desc) / 1000 * size
	heights := make([]float64, len(o.Rows))
	for r, row := range o.Rows {
		h := row.Height
		for c, w := range o.Cols {
			text := ""
			if c < len(row.Cells) {
				text = row.Cells[c]
			}
			n := max(1, len(Wrap(face, text, size, max(1, w-2*o.CellPad))))
			h = max(h, float64(n-1)*o.lineHeight()*size+lineH+2*o.CellPad)
		}
		heights[r] = h
	}
	return heights
}

// TableSize computes the table width and height.
func (o *Object) TableSize(face *font.Face) (w, h float64) {
	for _, c := range o.Cols {
		w += c
	}
	for _, rh := range o.TableLayout(face) {
		h += rh
	}
	return
}

// Quad returns the outline in user space: bl, br, tr, tl.
func (o *Object) Quad() [8]float64 {
	m := content.Matrix(o.Matrix)
	var q [8]float64
	for k, p := range [][2]float64{{0, -o.H}, {o.W, -o.H}, {o.W, 0}, {0, 0}} {
		q[2*k], q[2*k+1] = m.Apply(p[0], p[1])
	}
	return q
}

// QuadMatrix places a box on a quad (bl, br, tr, tl): the inverse of Quad.
func QuadMatrix(q [8]float64) (m [6]float64, w, h float64) {
	tlx, tly := q[6], q[7]
	w = math.Hypot(q[4]-tlx, q[5]-tly)
	h = math.Hypot(tlx-q[0], tly-q[1])
	if w == 0 || h == 0 {
		return [6]float64{1, 0, 0, 1, tlx, tly}, w, h
	}
	return [6]float64{(q[4] - tlx) / w, (q[5] - tly) / w, (tlx - q[0]) / h, (tly - q[1]) / h, tlx, tly}, w, h
}

// Env gives Apply access to fonts and uploaded files.
type Env struct {
	Fonts *content.FontCache
	Face  func(id string) (*font.Face, error) // library font; "" = default
	Asset func(name string) ([]byte, error)
	PDF   Sources // uploaded PDFs for page.import; defaults to parsing Asset
}

// Sources returns the uploaded-PDF opener; each returned opener parses a
// file once.
func (e Env) Sources() Sources {
	if e.PDF != nil {
		return e.PDF
	}
	cache := map[string]*cos.Document{}
	return func(asset string) *cos.Document {
		if d, ok := cache[asset]; ok {
			return d
		}
		var d *cos.Document
		if e.Asset != nil {
			if data, err := e.Asset(asset); err == nil {
				d, _ = cos.Open(data)
			}
		}
		cache[asset] = d
		return d
	}
}

func (o *Object) contentW() float64 { return max(1, o.W-o.Padding[1]-o.Padding[3]) }

func (o *Object) lineHeight() float64 {
	if o.LineHeight > 0 {
		return o.LineHeight
	}
	return 1.2
}

// renderer turns objects into content operators and resources.
type renderer struct {
	u      *cos.Update
	env    Env
	res    cos.Dict
	embeds map[string]*fontEmbed // by library font id, shared across pages
	images map[string]cos.Ref    // by asset name
	gs     map[float64]cos.Name
	n      int
	refs   map[int]cos.Ref // page id → page reference in the output
	svgs   map[string]*svgForm
}

type fontEmbed struct {
	e   *font.Embedding
	ref cos.Ref // allocated before encoding, written at the end
}

func (r *renderer) name(prefix string) cos.Name {
	r.n++
	return cos.Name(fmt.Sprintf("%s%d", prefix, r.n))
}

func (r *renderer) addRes(kind cos.Name, name cos.Name, v cos.Object) {
	d, _ := r.res[kind].(cos.Dict)
	if d == nil {
		d = cos.Copy(r.u.Dict(r.res[kind])).(cos.Dict)
		if d == nil {
			d = cos.Dict{}
		}
		r.res[kind] = d
	}
	d[name] = v
}

// embed returns the embedding of a library font, shared by the document.
func (r *renderer) embed(id string) (*fontEmbed, error) {
	fe, ok := r.embeds[id]
	if !ok {
		face, err := r.env.Face(id)
		if err != nil {
			return nil, err
		}
		fe = &fontEmbed{e: font.NewEmbedding(face), ref: r.u.Add(nil)}
		r.embeds[id] = fe
	}
	return fe, nil
}

func (r *renderer) fontFor(id string) (*fontEmbed, cos.Name, error) {
	fe, err := r.embed(id)
	if err != nil {
		return nil, "", err
	}
	n := r.name("PFf")
	r.addRes("Font", n, fe.ref)
	return fe, n, nil
}

func (r *renderer) opacity(a float64) []content.Op {
	if a <= 0 || a >= 1 {
		return nil
	}
	n, ok := r.gs[a]
	if !ok {
		n = r.name("PFgs")
		r.gs[a] = n
	}
	r.addRes("ExtGState", n, cos.Dict{"Type": cos.Name("ExtGState"), "ca": cos.Real(a), "CA": cos.Real(a)})
	return []content.Op{op("gs", n)}
}

func colorArgs(hex string) ([]cos.Object, bool) {
	c, err := ParseColor(hex)
	if err != nil {
		return nil, false
	}
	return reals(c[0], c[1], c[2]), true
}

// boxPath draws the object box (or an ellipse inscribed in it).
func boxPath(o *Object, ellipse bool) []content.Op {
	if !ellipse {
		return []content.Op{op("re", reals(0, -o.H, o.W, o.H)...)}
	}
	const k = 0.5523 // Bézier approximation of a quarter circle
	rx, ry := o.W/2, o.H/2
	cx, cy := rx, -ry
	return []content.Op{
		op("m", reals(cx+rx, cy)...),
		op("c", reals(cx+rx, cy+k*ry, cx+k*rx, cy+ry, cx, cy+ry)...),
		op("c", reals(cx-k*rx, cy+ry, cx-rx, cy+k*ry, cx-rx, cy)...),
		op("c", reals(cx-rx, cy-k*ry, cx-k*rx, cy-ry, cx, cy-ry)...),
		op("c", reals(cx+k*rx, cy-ry, cx+rx, cy-k*ry, cx+rx, cy)...),
		op("h"),
	}
}

// paint fills and/or strokes a path with the object's colours.
func paint(o *Object, path []content.Op) []content.Op {
	fill, hasFill := colorArgs(o.Fill)
	stroke, hasStroke := colorArgs(o.Stroke)
	hasStroke = hasStroke && o.StrokeWidth > 0
	if !hasFill && !hasStroke {
		return nil
	}
	out := []content.Op{}
	if hasFill {
		out = append(out, op("rg", fill...))
	}
	if hasStroke {
		out = append(out, op("RG", stroke...), op("w", cos.Real(o.StrokeWidth)))
	}
	out = append(out, path...)
	switch {
	case hasFill && hasStroke:
		out = append(out, op("B"))
	case hasFill:
		out = append(out, op("f"))
	default:
		out = append(out, op("S"))
	}
	return out
}

func (r *renderer) object(o *Object) ([]content.Op, error) {
	out := []content.Op{op("q"), op("cm", reals(o.Matrix[:]...)...)}
	if o.Artifact != "" {
		out = append([]content.Op{op("BDC", cos.Name("Artifact"), cos.Dict{"Type": cos.Name("Pagination"), "Subtype": cos.Name(o.Artifact)})}, out...)
	}
	out = append(out, r.opacity(o.Opacity)...)
	switch o.Kind {
	case "rect", "text":
		out = append(out, paint(o, boxPath(o, false))...)
	case "ellipse":
		out = append(out, paint(o, boxPath(o, true))...)
	case "line":
		if stroke, ok := colorArgs(o.Stroke); ok && o.StrokeWidth > 0 {
			out = append(out, op("RG", stroke...), op("w", cos.Real(o.StrokeWidth)), op("J", cos.Int(1)),
				op("m", reals(0, -o.H/2)...), op("l", reals(o.W, -o.H/2)...), op("S"))
		}
	case "table":
		ops, err := r.table(o)
		if err != nil {
			return nil, err
		}
		out = append(out, ops...)
	case "image":
		ref, err := r.image(o.Asset)
		if err != nil {
			return nil, err
		}
		n := r.name("PFi")
		r.addRes("XObject", n, ref)
		out = append(out, op("cm", reals(o.W, 0, 0, o.H, 0, -o.H)...), op("Do", n))
	case "svg":
		f, err := r.svgForm(o.Asset)
		if err != nil {
			return nil, err
		}
		n := r.name("PFs")
		r.addRes("XObject", n, f.ref)
		// The viewBox goes into the box; "meet" keeps proportions, centred.
		vw, vh := f.res.ViewBox[2], f.res.ViewBox[3]
		sx, sy := o.W/vw, o.H/vh
		dx, dy := 0.0, 0.0
		if f.res.Aspect != "none" {
			s := min(sx, sy)
			sx, sy = s, s
			dx, dy = (o.W-s*vw)/2, (o.H-s*vh)/2
		}
		out = append(out, op("cm", reals(sx, 0, 0, sy, dx, -o.H+dy)...), op("Do", n))
	}
	if o.Kind == "text" && strings.TrimSpace(o.Text) != "" {
		ops, err := r.text(o)
		if err != nil {
			return nil, err
		}
		out = append(out, ops...)
	}
	out = append(out, op("Q"))
	if o.Artifact != "" {
		out = append(out, op("EMC"))
	}
	return out, nil
}

// table draws backgrounds, cell texts and the grid.
func (r *renderer) table(o *Object) ([]content.Op, error) {
	face, err := r.env.Face(o.Font)
	if err != nil {
		return nil, err
	}
	heights := o.TableLayout(face)
	w, h := 0.0, 0.0
	for _, c := range o.Cols {
		w += c
	}
	for _, rh := range heights {
		h += rh
	}
	var out []content.Op
	if fill, ok := colorArgs(o.Fill); ok {
		out = append(out, op("rg", fill...), op("re", reals(0, -h, w, h)...), op("f"))
	}
	if fill, ok := colorArgs(o.HeaderFill); ok && len(heights) > 0 {
		out = append(out, op("rg", fill...), op("re", reals(0, -heights[0], w, heights[0])...), op("f"))
	}
	y := 0.0
	for ri, row := range o.Rows {
		x := 0.0
		for c, cw := range o.Cols {
			if c < len(row.Cells) && strings.TrimSpace(row.Cells[c]) != "" {
				cell := &Object{Kind: "text", Matrix: [6]float64{1, 0, 0, 1, x, y}, W: cw, H: heights[ri], Text: row.Cells[c],
					Font: o.Font, Size: o.Size, Color: o.Color, Align: o.Align, VAlign: "top", LineHeight: o.LineHeight,
					Padding: [4]float64{o.CellPad, o.CellPad, o.CellPad, o.CellPad}}
				ops, err := r.text(cell)
				if err != nil {
					return nil, err
				}
				out = append(out, op("q"), op("cm", reals(1, 0, 0, 1, x, y)...))
				out = append(out, ops...)
				out = append(out, op("Q"))
			}
			x += cw
		}
		y -= heights[ri]
	}
	if stroke, ok := colorArgs(o.Stroke); ok && o.StrokeWidth > 0 {
		out = append(out, op("RG", stroke...), op("w", cos.Real(o.StrokeWidth)), op("re", reals(0, -h, w, h)...))
		x := 0.0
		for _, cw := range o.Cols[:max(0, len(o.Cols)-1)] {
			x += cw
			out = append(out, op("m", reals(x, 0)...), op("l", reals(x, -h)...))
		}
		y := 0.0
		for _, rh := range heights[:max(0, len(heights)-1)] {
			y -= rh
			out = append(out, op("m", reals(0, y)...), op("l", reals(w, y)...))
		}
		out = append(out, op("S"))
	}
	return out, nil
}

type svgForm struct {
	ref cos.Ref
	res *svg.Result
}

// svgForm converts an uploaded SVG into a form XObject once per document.
func (r *renderer) svgForm(asset string) (*svgForm, error) {
	if f, ok := r.svgs[asset]; ok {
		return f, nil
	}
	data, err := r.env.Asset(asset)
	if err != nil {
		return nil, err
	}
	res, err := svg.Convert(data)
	if err != nil {
		return nil, err
	}
	gs := cos.Dict{}
	for name, a := range res.Alphas {
		gs[name] = cos.Dict{"Type": cos.Name("ExtGState"), "ca": cos.Real(a[0]), "CA": cos.Real(a[1])}
	}
	resources := cos.Dict{}
	if len(gs) > 0 {
		resources["ExtGState"] = gs
	}
	form := &cos.Stream{Dict: cos.Dict{"Type": cos.Name("XObject"), "Subtype": cos.Name("Form"), "Filter": cos.Name("FlateDecode"),
		"BBox": cos.Array{cos.Int(0), cos.Int(0), cos.Real(res.ViewBox[2]), cos.Real(res.ViewBox[3])}, "Resources": resources},
		Data: cos.Deflate(content.Write(res.Ops))}
	f := &svgForm{ref: r.u.Add(form), res: res}
	r.svgs[asset] = f
	return f, nil
}

// image builds an image XObject from an uploaded JPEG or PNG.
func (r *renderer) image(asset string) (cos.Ref, error) {
	if ref, ok := r.images[asset]; ok {
		return ref, nil
	}
	data, err := r.env.Asset(asset)
	if err != nil {
		return cos.Ref{}, err
	}
	xo, err := ImageXObject(r.u, data)
	if err != nil {
		return cos.Ref{}, err
	}
	ref := r.u.Add(xo)
	r.images[asset] = ref
	return ref, nil
}

// ImageXObject converts JPEG (kept as is) or PNG/other formats (Flate,
// alpha as /SMask) into an image XObject.
func ImageXObject(u *cos.Update, data []byte) (*cos.Stream, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("edit: unsupported image: %w", err)
	}
	d := cos.Dict{"Type": cos.Name("XObject"), "Subtype": cos.Name("Image"), "Width": cos.Int(cfg.Width), "Height": cos.Int(cfg.Height), "BitsPerComponent": cos.Int(8)}
	if format == "jpeg" {
		switch cfg.ColorModel {
		case color.GrayModel:
			d["ColorSpace"] = cos.Name("DeviceGray")
		case color.CMYKModel:
			d["ColorSpace"] = cos.Name("DeviceCMYK")
			d["Decode"] = cos.Array{cos.Int(1), cos.Int(0), cos.Int(1), cos.Int(0), cos.Int(1), cos.Int(0), cos.Int(1), cos.Int(0)} // Adobe inverted CMYK
		default:
			d["ColorSpace"] = cos.Name("DeviceRGB")
		}
		d["Filter"] = cos.Name("DCTDecode")
		return &cos.Stream{Dict: d, Data: data}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
	alpha := make([]byte, 0, b.Dx()*b.Dy())
	opaque := true
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			rgb = append(rgb, c.R, c.G, c.B)
			alpha = append(alpha, c.A)
			opaque = opaque && c.A == 255
		}
	}
	d["ColorSpace"] = cos.Name("DeviceRGB")
	d["Filter"] = cos.Name("FlateDecode")
	if !opaque {
		d["SMask"] = u.Add(&cos.Stream{Dict: cos.Dict{"Type": cos.Name("XObject"), "Subtype": cos.Name("Image"), "Width": cos.Int(b.Dx()),
			"Height": cos.Int(b.Dy()), "BitsPerComponent": cos.Int(8), "ColorSpace": cos.Name("DeviceGray"), "Filter": cos.Name("FlateDecode")}, Data: cos.Deflate(alpha)})
	}
	return &cos.Stream{Dict: d, Data: cos.Deflate(rgb)}, nil
}

// ImageSize returns the pixel size of an image file.
func ImageSize(data []byte) (int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	return cfg.Width, cfg.Height, err
}

// isOwn reports whether an id names an editor object (not an original block).
func isOwn(id string) bool { return id != "" && !unicode.IsDigit(rune(id[0])) }
