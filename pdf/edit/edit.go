// Package edit applies an operation log to an original document. The
// original is never modified: Apply returns a cos.Update with new page
// content, so the output is the original bytes plus an incremental update.
//
// Edits on existing content work in place: the painting operators of a
// block are wrapped in "q <matrix> cm … Q". The graphics state at that
// point is already right, so nothing has to be re-established except the
// text matrices when a text object is split.
package edit

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/layout"
	"pdf-formatter/pdf/meta"
	"pdf-formatter/pdf/nav"
)

// Op is one entry of the operation log.
type Op struct {
	Op       string      `json:"op"`                 // see the cases in Fold and Pages
	Page     int         `json:"page"`               // page id: original object number, negative for inserted pages
	Blocks   []string    `json:"blocks,omitempty"`   // original block ids, editor object ids or link ids
	Matrix   *[6]float64 `json:"matrix,omitempty"`   // transform: in page user space
	Color    string      `json:"color,omitempty"`    // recolor: #rrggbb
	Object   *Object     `json:"object,omitempty"`   // add, update
	Replaces []string    `json:"replaces,omitempty"` // add: original blocks the object replaces
	// Original blocks of Blocks and Replaces → the items (layout.ItemID) they
	// were made of when the edit was made: the edit stays on that content
	// even if a later version forms blocks differently.
	Members  map[string][]string `json:"members,omitempty"`
	Meta     *meta.Metadata      `json:"meta,omitempty"`     // meta: the whole metadata set
	Sanitize *meta.Sanitize      `json:"sanitize,omitempty"` // sanitize
	Link     *nav.Link           `json:"link,omitempty"`     // link: add or replace a link annotation
	Outline  []nav.Outline       `json:"outline,omitempty"`  // outline: the whole bookmark tree
	// Page operations: page.insert page.delete page.move page.rotate page.margins toc
	After   int        `json:"after,omitempty"`   // insert/move/toc: after this page id (0 = at the start)
	NewID   int        `json:"newId,omitempty"`   // insert/toc: id of the new page (negative)
	Size    [2]float64 `json:"size,omitempty"`    // insert: width, height in pt
	Rotate  int        `json:"rotate,omitempty"`  // rotate: degrees to add
	Pages   []int      `json:"pages,omitempty"`   // margins: page ids, empty = all
	Mode    string     `json:"mode,omitempty"`    // margins: resize | fit
	Margins [4]float64 `json:"margins,omitempty"` // margins: top, right, bottom, left (pt)
	TOC     *TOC       `json:"toc,omitempty"`
	Labels  []Label    `json:"labels,omitempty"` // labels: the whole set
	HF      []HFItem   `json:"hf,omitempty"`     // hf: all headers and footers
	Asset   string     `json:"asset,omitempty"`  // page.import: uploaded PDF
	From    []int      `json:"from,omitempty"`   // page.import: page indices, empty = all
	NewIDs  []int      `json:"newIds,omitempty"` // page.import: ids of the copies (negative)
}

// IsLink reports whether an id names a link annotation.
func IsLink(id string) bool { return strings.HasPrefix(id, "a") || strings.HasPrefix(id, "l") }

// Effect is the combined result of the log on one block.
type Effect struct {
	Matrix content.Matrix // user space; Identity when not moved
	Color  *[3]float64
	Delete bool
}

func (e Effect) moved() bool { return e.Matrix != content.Identity }

// ParseColor reads "#rrggbb".
func ParseColor(s string) (*[3]float64, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return nil, fmt.Errorf("edit: bad colour %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil, err
	}
	return &[3]float64{float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255}, nil
}

// PageEdit is the folded result of the log for one page.
type PageEdit struct {
	Blocks   map[string]Effect   // original blocks
	Members  map[string][]string // their items, where recorded (Op.Members)
	owner    map[string]string   // item → block of Members
	Objects  []*Object           // editor objects, in paint order
	Links    []*nav.Link         // added or replaced links
	DelLinks map[string]bool     // removed links (from the file or added earlier)
	Fit      *[6]float64         // original content scaled into margins (set by Apply)
}

func newPageEdit() *PageEdit {
	return &PageEdit{Blocks: map[string]Effect{}, Members: map[string][]string{}, owner: map[string]string{}, DelLinks: map[string]bool{}}
}

func (pe *PageEdit) empty() bool {
	return len(pe.Blocks) == 0 && len(pe.Objects) == 0 && len(pe.Links) == 0 && len(pe.DelLinks) == 0 && pe.Fit == nil
}

func (pe *PageEdit) setLink(l nav.Link) {
	for i, x := range pe.Links {
		if x.ID == l.ID {
			pe.Links[i] = &l
			return
		}
	}
	pe.Links = append(pe.Links, &l)
}

func (pe *PageEdit) object(id string) (int, *Object) {
	for i, o := range pe.Objects {
		if o.ID == id {
			return i, o
		}
	}
	return -1, nil
}

// EffectFor returns the effect on a block, given its id and items as
// formed now: an edit recorded with its items applies to the block holding
// them, other edits to the block with the same id.
func (pe *PageEdit) EffectFor(id string, items ...string) (Effect, bool) {
	for _, it := range items {
		if b, ok := pe.owner[it]; ok {
			return pe.Blocks[b], true
		}
	}
	if _, recorded := pe.Members[id]; recorded {
		return Effect{}, false
	}
	e, ok := pe.Blocks[id]
	return e, ok
}

func (pe *PageEdit) effect(id string) Effect {
	e, ok := pe.Blocks[id]
	if !ok {
		e.Matrix = content.Identity
	}
	return e
}

// Edits is the folded log.
type Edits struct {
	Pages    map[int]*PageEdit // by page object number
	Meta     *meta.Metadata    // last metadata set, if any
	Sanitize meta.Sanitize     // union of all cleanups
	Outline  *[]nav.Outline    // last outline set, if any
	Labels   *[]Label          // last page labels, if any
	HF       []HFItem          // headers and footers
}

// Fold replays the log into per-page edits.
func Fold(ops []Op) (*Edits, error) {
	ed := &Edits{Pages: map[int]*PageEdit{}}
	out := ed.Pages
	for _, op := range ops {
		switch op.Op {
		case "meta":
			if op.Meta == nil {
				return nil, fmt.Errorf("edit: meta without metadata")
			}
			m := *op.Meta
			ed.Meta = &m
			continue
		case "outline":
			o := op.Outline
			ed.Outline = &o
			continue
		case "labels":
			l := op.Labels
			ed.Labels = &l
			continue
		case "hf":
			ed.HF = op.HF
			continue
		case "page.insert", "page.delete", "page.move", "page.rotate", "page.margins", "toc", "page.import":
			continue // folded by Pages
		case "sanitize":
			if op.Sanitize == nil {
				return nil, fmt.Errorf("edit: sanitize without options")
			}
			s := op.Sanitize
			ed.Sanitize.PieceInfo = ed.Sanitize.PieceInfo || s.PieceInfo
			ed.Sanitize.Thumbnails = ed.Sanitize.Thumbnails || s.Thumbnails
			ed.Sanitize.JavaScript = ed.Sanitize.JavaScript || s.JavaScript
			ed.Sanitize.Embedded = ed.Sanitize.Embedded || s.Embedded
			continue
		}
		pe := out[op.Page]
		if pe == nil {
			pe = newPageEdit()
			out[op.Page] = pe
		}
		switch op.Op {
		case "link":
			if op.Link == nil || op.Link.ID == "" {
				return nil, fmt.Errorf("edit: link without id")
			}
			pe.setLink(*op.Link)
			continue
		case "add", "update":
			if op.Object == nil || op.Object.ID == "" {
				return nil, fmt.Errorf("edit: %s without object", op.Op)
			}
			o := *op.Object
			if i, _ := pe.object(o.ID); i >= 0 {
				pe.Objects[i] = &o
			} else if op.Op == "add" {
				pe.Objects = append(pe.Objects, &o)
			}
			for _, id := range op.Replaces {
				e := pe.effect(id)
				e.Delete = true
				pe.Blocks[id] = e
				pe.member(id, op.Members[id])
			}
			continue
		}
		for _, id := range op.Blocks {
			if IsLink(id) {
				if op.Op == "delete" {
					pe.Links = slices.DeleteFunc(pe.Links, func(l *nav.Link) bool { return l.ID == id })
					pe.DelLinks[id] = true
				}
				continue
			}
			if i, o := pe.object(id); o != nil {
				switch op.Op {
				case "transform":
					if op.Matrix == nil {
						return nil, fmt.Errorf("edit: transform without matrix")
					}
					o.Matrix = [6]float64(content.Matrix(o.Matrix).Mul(content.Matrix(*op.Matrix)))
				case "recolor":
					switch {
					case o.Kind == "text":
						o.Color = op.Color
					case o.Kind == "line" || o.Fill == "":
						o.Stroke = op.Color
					default:
						o.Fill = op.Color
					}
				case "delete":
					pe.Objects = slices.Delete(pe.Objects, i, i+1)
				}
				continue
			}
			e := pe.effect(id)
			switch op.Op {
			case "transform":
				if op.Matrix == nil {
					return nil, fmt.Errorf("edit: transform without matrix")
				}
				e.Matrix = e.Matrix.Mul(content.Matrix(*op.Matrix))
			case "recolor":
				c, err := ParseColor(op.Color)
				if err != nil {
					return nil, err
				}
				e.Color = c
			case "delete":
				e.Delete = true
			default:
				return nil, fmt.Errorf("edit: unknown operation %q", op.Op)
			}
			pe.Blocks[id] = e
			pe.member(id, op.Members[id])
		}
	}
	return ed, nil
}

// member records the items of a block the first time an edit names them.
func (pe *PageEdit) member(id string, items []string) {
	if len(items) == 0 || pe.Members[id] != nil {
		return
	}
	pe.Members[id] = items
	for _, it := range items {
		pe.owner[it] = id
	}
}

// Apply builds the edited document.
func Apply(d *cos.Document, ops []Op, env Env) (*cos.Update, error) {
	ed, err := Fold(ops)
	if err != nil {
		return nil, err
	}
	edits := ed.Pages
	if env.Fonts == nil {
		env.Fonts = content.NewFontCache()
	}
	u := cos.NewUpdate(d)
	r := &renderer{u: u, env: env, embeds: map[string]*fontEmbed{}, images: map[string]cos.Ref{}, refs: map[int]cos.Ref{}, svgs: map[string]*svgForm{}}
	sources := env.Sources()
	specs, structural := Pages(d, ops, sources)
	orig := d.Pages()
	memo := map[string]map[cos.Ref]cos.Ref{} // per source: shared fonts and images stay shared
	for _, sp := range specs {
		switch {
		case sp.Orig >= 0:
			r.refs[sp.ID] = orig[sp.Orig].Ref
		case sp.Import != nil:
			src := sources(sp.Import.Asset)
			if src == nil {
				return nil, fmt.Errorf("edit: missing source %s", sp.Import.Asset)
			}
			if memo[sp.Import.Asset] == nil {
				memo[sp.Import.Asset] = map[cos.Ref]cos.Ref{}
			}
			p := src.Pages()[sp.Import.Index]
			page := cos.Copy(p.Dict).(cos.Dict)
			// Inherited attributes become explicit: the source tree is not copied.
			page["Resources"], page["MediaBox"] = p.Resources, rectArray(p.MediaBox)
			delete(page, "Parent")
			delete(page, "Annots") // links point into the other document
			r.refs[sp.ID] = u.Add(u.Import(src, page, memo[sp.Import.Asset]))
		default:
			r.refs[sp.ID] = u.Add(cos.Dict{"Type": cos.Name("Page"), "MediaBox": rectArray(sp.Media), "Resources": cos.Dict{}})
		}
	}
	catalog := d.Dict(d.Trailer["Root"])
	labels := ReadLabels(d, catalog)
	if ed.Labels != nil {
		labels = *ed.Labels
	}
	outline := nav.ReadOutline(d, catalog)
	if ed.Outline != nil {
		outline = *ed.Outline
	}
	title := meta.Read(d, d.Trailer).Title
	if ed.Meta != nil {
		title = ed.Meta.Title
	}
	for i, sp := range specs {
		pe := edits[sp.ID]
		if pe == nil {
			pe = newPageEdit()
		}
		// Generated content joins the page's own objects for this render only.
		extra := *pe
		extra.Objects = slices.Clone(pe.Objects)
		extra.Links = slices.Clone(pe.Links)
		if sp.TOC != nil {
			objs, links, err := r.tocPage(sp, outline, specs, labels)
			if err != nil {
				return nil, err
			}
			extra.Objects = append(objs, extra.Objects...)
			extra.Links = append(links, extra.Links...)
		}
		extra.Objects = append(extra.Objects, hfObjects(ed.HF, sp, i, len(specs), labels, title)...)
		extra.Fit = sp.Fit
		if extra.empty() {
			continue
		}
		var cp cos.Page
		if sp.Orig >= 0 {
			cp = orig[sp.Orig]
		} else {
			ref := r.refs[sp.ID]
			res := u.Dict(u.Dict(ref)["Resources"])
			if res == nil {
				res = cos.Dict{}
			}
			cp = cos.Page{Ref: ref, Dict: u.Dict(ref), Resources: res, MediaBox: sp.Media, CropBox: sp.Crop}
		}
		if err := r.page(cp, &extra); err != nil {
			return nil, err
		}
	}
	r.pageTree(specs, structural)
	for _, fe := range r.embeds {
		fe.e.WriteAt(u, fe.ref)
	}
	if ed.Outline != nil {
		nav.WriteOutline(u, *ed.Outline, r.refs)
	}
	if ed.Labels != nil {
		writeLabels(u, *ed.Labels)
	}
	if ed.Meta != nil {
		meta.Write(u, *ed.Meta)
	}
	meta.Clean(u, ed.Sanitize)
	return u, nil
}

// pageTree writes page attributes (rotation, boxes, scaling) and, when the
// order changed, a flat page tree with inherited attributes made explicit.
func (r *renderer) pageTree(specs []PageSpec, structural bool) {
	u := r.u
	orig := u.Doc.Pages()
	rootRef, _ := u.Dict(u.Doc.Trailer["Root"])["Pages"].(cos.Ref)
	if rootRef == (cos.Ref{}) {
		return
	}
	var kids cos.Array
	for _, sp := range specs {
		ref := r.refs[sp.ID]
		kids = append(kids, ref)
		if !structural && !sp.dirty {
			continue
		}
		page := cos.Copy(u.Dict(ref)).(cos.Dict)
		if structural {
			page["Parent"] = rootRef
			if sp.Orig >= 0 && page["Resources"] == nil {
				page["Resources"] = orig[sp.Orig].Resources
			}
		}
		page["MediaBox"] = rectArray(sp.Media)
		if sp.Crop != sp.Media {
			page["CropBox"] = rectArray(sp.Crop)
		} else {
			delete(page, "CropBox")
		}
		page["Rotate"] = cos.Int(sp.Rotate)
		u.Set(ref, page)
	}
	if structural {
		root := cos.Copy(u.Dict(rootRef)).(cos.Dict)
		root["Kids"] = kids
		root["Count"] = cos.Int(len(kids))
		u.Set(rootRef, root)
	}
}

func stream(data []byte) *cos.Stream {
	return &cos.Stream{Dict: cos.Dict{"Filter": cos.Name("FlateDecode")}, Data: cos.Deflate(data)}
}

// page writes one edited page: original content (rewritten in place when
// blocks are edited) followed by the editor objects, then the links.
func (r *renderer) page(p cos.Page, pe *PageEdit) error {
	u := r.u
	res := cos.Copy(p.Resources).(cos.Dict)
	if res == nil {
		res = cos.Dict{}
	}
	page := cos.Copy(u.Get(p.Ref)).(cos.Dict)
	var pg *layout.Page
	analysis := func() *layout.Page {
		if pg == nil {
			pg = layout.Analyze(u.Doc, p, r.env.Fonts)
		}
		return pg
	}
	resChanged := false
	var body []content.Op
	if len(pe.Blocks) > 0 {
		var changed bool
		body, changed = rewriteContent(u, analysis(), pe, p.Resources, res)
		resChanged = changed
	}
	var objOps []content.Op
	if len(pe.Objects) > 0 {
		r.res, r.gs = res, map[float64]cos.Name{}
		for _, o := range pe.Objects {
			ops, err := r.object(o)
			if err != nil {
				return err
			}
			objOps = append(objOps, ops...)
		}
		resChanged = true
	}
	// Original content, possibly rewritten and scaled into margins.
	var fitOp []content.Op
	if pe.Fit != nil {
		fitOp = []content.Op{op("cm", reals(pe.Fit[:]...)...)}
	}
	switch {
	case body == nil && len(objOps) == 0 && fitOp == nil:
		// Only links changed: content stays.
	case body != nil:
		var data []byte
		if len(objOps) > 0 || fitOp != nil {
			// Isolate the original graphics state from the objects.
			data = append(data, "q\n"...)
			data = append(data, content.Write(fitOp)...)
			data = append(data, content.Write(body)...)
			data = append(data, "\nQ\n"...)
			data = append(data, content.Write(objOps)...)
		} else {
			data = content.Write(body)
		}
		page["Contents"] = u.Add(stream(data))
	default:
		// Original content untouched: keep its streams, wrap them in q … Q.
		contents := cos.Array{u.Add(&cos.Stream{Dict: cos.Dict{}, Data: append([]byte("q\n"), content.Write(fitOp)...)})}
		switch c := page["Contents"].(type) {
		case cos.Ref:
			if arr, ok := u.Resolve(c).(cos.Array); ok {
				contents = append(contents, arr...)
			} else {
				contents = append(contents, c)
			}
		case cos.Array:
			contents = append(contents, c...)
		case *cos.Stream:
			contents = append(contents, u.Add(c))
		}
		contents = append(contents, u.Add(stream(append([]byte("\nQ\n"), content.Write(objOps)...))))
		page["Contents"] = contents
	}
	if resChanged {
		page["Resources"] = res
	}
	if len(pe.Links) > 0 || len(pe.DelLinks) > 0 || pe.Fit != nil {
		page["Annots"] = r.annots(p, pe, page, analysis)
	}
	u.Set(p.Ref, page)
	return nil
}

// AttachQuad finds the current outline of the block or object a link
// follows. ok is false when it no longer exists.
func AttachQuad(id string, pe *PageEdit, pg func() *layout.Page) ([8]float64, bool) {
	if _, o := pe.object(id); o != nil {
		return o.Quad(), true
	}
	if pe.Blocks[id].Delete {
		return [8]float64{}, false
	}
	for _, b := range pg().Blocks {
		if b.ID == id {
			q := b.Quad
			if e, ok := pe.Blocks[id]; ok {
				q = MapQuad(q, e.Matrix)
			}
			if pe.Fit != nil {
				q = MapQuad(q, content.Matrix(*pe.Fit))
			}
			return q, true
		}
	}
	return [8]float64{}, false
}

func (r *renderer) annots(p cos.Page, pe *PageEdit, page cos.Dict, pg func() *layout.Page) cos.Array {
	u := r.u
	replaced := map[string]bool{}
	for _, l := range pe.Links {
		replaced[l.ID] = true
	}
	var out cos.Array
	for i, a := range u.Array(page["Annots"]) {
		id := fmt.Sprintf("ai%d", i)
		if ref, ok := a.(cos.Ref); ok {
			id = fmt.Sprintf("a%d", ref.Num)
		}
		if pe.DelLinks[id] || replaced[id] {
			continue
		}
		if pe.Fit != nil { // annotations follow the scaled content
			d := cos.Copy(u.Dict(a)).(cos.Dict)
			if rc, ok := cos.Rect(u, d["Rect"]); ok {
				q := MapQuad([8]float64{rc[0], rc[1], rc[2], rc[1], rc[2], rc[3], rc[0], rc[3]}, content.Matrix(*pe.Fit))
				d["Rect"] = rectArray(nav.QuadRect(q))
				delete(d, "QuadPoints")
			}
			a = u.Add(d)
		}
		out = append(out, a)
	}
	refs := r.refs
	for _, l := range pe.Links {
		l := *l
		if l.Attach != "" {
			q, ok := AttachQuad(l.Attach, pe, pg)
			if !ok {
				continue // its block was deleted
			}
			l.Quad = q
		}
		if d := nav.Annot(l, p.Ref, refs); d != nil {
			out = append(out, u.Add(d))
		}
	}
	return out
}

// target is an item with its effect.
type target struct {
	item *content.Item
	eff  Effect
}

// level holds the targets of one content stream: the page or a form.
type level struct {
	items map[int]*target // by item start op
	forms map[int]*level  // by the Do op that paints the form
}

func newLevel() *level { return &level{items: map[int]*target{}, forms: map[int]*level{}} }

// rewriteContent applies effects to the original blocks of a page and
// returns the new operators. Inlined forms add to res.
func rewriteContent(g cos.Getter, pg *layout.Page, pe *PageEdit, pageRes, res cos.Dict) ([]content.Op, bool) {
	root := newLevel()
	for _, b := range pg.Blocks {
		for _, i := range b.Items {
			it := &pg.Items[i]
			e, ok := pe.EffectFor(b.ID, layout.ItemID(*it))
			if !ok {
				continue
			}
			lv := root
			for _, f := range it.Form {
				if lv.forms[f] == nil {
					lv.forms[f] = newLevel()
				}
				lv = lv.forms[f]
			}
			lv.items[it.Start] = &target{it, e}
		}
	}
	w := &writer{g: g, res: res}
	return w.rewrite(pg.Ops, root, pageRes, nil), w.resChanged
}

type writer struct {
	g          cos.Getter
	res        cos.Dict // page resources being extended by inlined forms
	resChanged bool
	nforms     int
}

func op(name string, args ...cos.Object) content.Op { return content.Op{Name: name, Args: args} }

func reals(v ...float64) []cos.Object {
	out := make([]cos.Object, len(v))
	for i, x := range v {
		out[i] = cos.Real(x)
	}
	return out
}

func matrixOp(m content.Matrix) content.Op { return op("cm", reals(m[:]...)...) }

// inCTM converts a user space transform e for content drawn under ctm:
// the cm operand M with M×C = C×E.
func inCTM(e, ctm content.Matrix) content.Matrix {
	inv, ok := ctm.Invert()
	if !ok {
		return content.Identity
	}
	return ctm.Mul(e).Mul(inv)
}

func colorOps(c *[3]float64, fill, stroke bool) []content.Op {
	var out []content.Op
	if fill {
		out = append(out, op("rg", reals(c[0], c[1], c[2])...))
	}
	if stroke {
		out = append(out, op("RG", reals(c[0], c[1], c[2])...))
	}
	return out
}

// rename maps resource names of an inlined form to names in the page.
type rename map[cos.Name]map[cos.Name]cos.Name

func (w *writer) rewrite(ops []content.Op, lv *level, res cos.Dict, rn rename) []content.Op {
	out := make([]content.Op, 0, len(ops)+16)
	for i := 0; i < len(ops); i++ {
		o := renameOp(ops[i], rn)
		if sub, ok := lv.forms[i]; ok {
			out = append(out, w.inlineForm(ops[i], sub, res, rn)...)
			continue
		}
		t, ok := lv.items[i]
		if !ok {
			out = append(out, o)
			continue
		}
		it := t.item
		switch it.Kind {
		case content.Text:
			out = append(out, textEdit(o, it, t.eff)...)
		case content.Path:
			seg := make([]content.Op, 0, it.Op-it.Start+1)
			for k := it.Start; k <= it.Op; k++ {
				seg = append(seg, renameOp(ops[k], rn))
			}
			out = append(out, pathEdit(seg, it, t.eff)...)
			i = it.Op
		case content.Image:
			if !t.eff.Delete {
				out = append(out, op("q"))
				if t.eff.moved() {
					out = append(out, matrixOp(inCTM(t.eff.Matrix, it.State.CTM)))
				}
				out = append(out, o, op("Q"))
			}
		}
	}
	return out
}

// advance moves the text position by dx text space units without
// drawing: a TJ with a lone number changes Tm but not Tlm.
func advance(it *content.Item, dx float64) []content.Op {
	st := it.State
	k := st.FontSize * st.HScale
	if k == 0 || dx == 0 {
		return nil
	}
	return []content.Op{op("TJ", cos.Array{cos.Real(-dx * 1000 / k)})}
}

func textEdit(o content.Op, it *content.Item, e Effect) []content.Op {
	var pre []content.Op
	show := o
	switch o.Name {
	case "'":
		pre = []content.Op{op("T*")}
		show = op("Tj", o.Args...)
	case "\"":
		if len(o.Args) == 3 {
			pre = []content.Op{op("Tw", o.Args[0]), op("Tc", o.Args[1]), op("T*")}
			show = op("Tj", o.Args[2])
		}
	}
	if e.Delete {
		return append(pre, advance(it, it.Width)...)
	}
	// Split the text object around the run, then restore the line matrix
	// and the position after the run for the rest of the object.
	out := append(pre, op("ET"), op("q"))
	if e.moved() {
		out = append(out, matrixOp(inCTM(e.Matrix, it.State.CTM)))
	}
	out = append(out, op("BT"))
	if e.Color != nil {
		r := it.State.Render
		out = append(out, colorOps(e.Color, r != 1 && r != 5, r == 1 || r == 2 || r == 5 || r == 6)...)
	}
	out = append(out, op("Tm", reals(it.Tm[:]...)...), show, op("ET"), op("Q"), op("BT"), op("Tm", reals(it.Tlm[:]...)...))
	// Tm = translate(x0) × Tlm at the start; after the run x0 + width.
	inv, _ := it.Tlm.Invert()
	x0 := it.Tm.Mul(inv)[4]
	return append(out, advance(it, x0+it.Width)...)
}

func pathEdit(seg []content.Op, it *content.Item, e Effect) []content.Op {
	var clip []content.Op
	paint := seg[len(seg)-1]
	var body []content.Op
	for _, o := range seg[:len(seg)-1] {
		if o.Name == "W" || o.Name == "W*" {
			clip = append(clip, o)
			continue
		}
		body = append(body, o)
	}
	var out []content.Op
	if !e.Delete {
		out = append(out, op("q"))
		if e.moved() {
			out = append(out, matrixOp(inCTM(e.Matrix, it.State.CTM)))
		}
		if e.Color != nil {
			stroke := slices.Contains([]string{"S", "s", "B", "B*", "b", "b*"}, paint.Name)
			fill := paint.Name != "S" && paint.Name != "s"
			out = append(out, colorOps(e.Color, fill, stroke)...)
		}
		out = append(out, body...)
		out = append(out, paint, op("Q"))
	}
	if len(clip) > 0 { // the clip outlives the path: keep it in place
		out = append(out, body...)
		out = append(out, clip...)
		out = append(out, op("n"))
	}
	return out
}

var resKinds = []cos.Name{"Font", "XObject", "ExtGState", "ColorSpace", "Pattern", "Shading", "Properties"}

// inlineForm replaces "Do" of a form with its (edited) content.
func (w *writer) inlineForm(do content.Op, lv *level, res cos.Dict, rn rename) []content.Op {
	name, _ := do.Args[0].(cos.Name)
	xo := w.g.Stream(w.g.Dict(res["XObject"])[name]) // names as in the original stream
	if xo == nil {
		return []content.Op{renameOp(do, rn)}
	}
	data, _ := w.g.Decode(xo)
	fops := content.Parse(data)
	fres := w.g.Dict(xo.Dict["Resources"])
	if fres == nil {
		fres = res
	}
	sub := w.mergeResources(fres)
	out := []content.Op{op("q")}
	if m := w.g.Array(xo.Dict["Matrix"]); len(m) == 6 {
		out = append(out, op("cm", m...))
	}
	if bb, ok := cos.Rect(w.g, xo.Dict["BBox"]); ok {
		out = append(out, op("re", reals(bb[0], bb[1], bb[2]-bb[0], bb[3]-bb[1])...), op("W"), op("n"))
	}
	out = append(out, w.rewrite(fops, lv, fres, sub)...)
	return append(out, op("Q"))
}

// mergeResources adds a form's resources to the page, renaming clashes.
func (w *writer) mergeResources(fres cos.Dict) rename {
	w.nforms++
	rn := rename{}
	for _, kind := range resKinds {
		src := w.g.Dict(fres[kind])
		if len(src) == 0 {
			continue
		}
		dst := cos.Copy(w.g.Dict(w.res[kind])).(cos.Dict)
		if dst == nil {
			dst = cos.Dict{}
		}
		rn[kind] = map[cos.Name]cos.Name{}
		for _, n := range slices.Sorted(maps.Keys(src)) {
			v := src[n]
			target := n
			if old, exists := dst[n]; exists && old != v {
				target = cos.Name(fmt.Sprintf("F%d_%s", w.nforms, n))
			}
			dst[target] = v
			rn[kind][n] = target
		}
		w.res[kind] = dst
		w.resChanged = true
	}
	return rn
}

// renameOp rewrites resource names used by an operator.
func renameOp(o content.Op, rn rename) content.Op {
	if len(rn) == 0 || len(o.Args) == 0 {
		return o
	}
	sub := func(kind cos.Name, i int) {
		if i < 0 || i >= len(o.Args) {
			return
		}
		if n, ok := o.Args[i].(cos.Name); ok {
			if m, ok := rn[kind][n]; ok {
				o.Args = slices.Clone(o.Args)
				o.Args[i] = m
			}
		}
	}
	switch o.Name {
	case "Tf":
		sub("Font", 0)
	case "Do":
		sub("XObject", 0)
	case "gs":
		sub("ExtGState", 0)
	case "cs", "CS":
		sub("ColorSpace", 0)
	case "scn", "SCN":
		sub("Pattern", len(o.Args)-1)
	case "sh":
		sub("Shading", 0)
	case "BDC":
		sub("Properties", 1)
	}
	return o
}

// MapQuad transforms a block outline by an effect matrix.
func MapQuad(q [8]float64, m content.Matrix) [8]float64 {
	for k := 0; k < 8; k += 2 {
		q[k], q[k+1] = m.Apply(q[k], q[k+1])
	}
	return q
}
