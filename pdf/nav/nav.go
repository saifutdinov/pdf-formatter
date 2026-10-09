// Package nav reads and writes navigation: link annotations (§12.5.6.5)
// and the document outline (§12.3.3).
package nav

import (
	"fmt"
	"strings"

	"pdf-formatter/pdf/cos"
)

// Link is a link annotation as the editor sees it.
type Link struct {
	ID     string     `json:"id"`   // "a<obj>" / "ai<index>" for links in the file, "l…" for new ones
	Kind   string     `json:"kind"` // uri | page | other (kept as is)
	Quad   [8]float64 `json:"quad"` // user space: bl, br, tr, tl
	URI    string     `json:"uri,omitempty"`
	Page   int        `json:"page,omitempty"`   // target page object number
	Top    *float64   `json:"top,omitempty"`    // target y in user space; nil = whole page
	Attach string     `json:"attach,omitempty"` // block or object whose outline the link follows
}

// Dest is a resolved destination.
type Dest struct {
	Page cos.Ref
	Top  *float64
}

func rectQuad(r [4]float64) [8]float64 {
	return [8]float64{r[0], r[1], r[2], r[1], r[2], r[3], r[0], r[3]}
}

// QuadRect is the bounding rectangle of a quad.
func QuadRect(q [8]float64) [4]float64 {
	r := [4]float64{q[0], q[1], q[0], q[1]}
	for k := 0; k < 8; k += 2 {
		r[0], r[1] = min(r[0], q[k]), min(r[1], q[k+1])
		r[2], r[3] = max(r[2], q[k]), max(r[3], q[k+1])
	}
	return r
}

// ResolveDest follows names and actions to a page and position (§12.3.2).
func ResolveDest(g cos.Getter, catalog cos.Dict, o cos.Object) (Dest, bool) {
	for range 8 {
		switch v := g.Resolve(o).(type) {
		case cos.Array:
			if len(v) == 0 {
				return Dest{}, false
			}
			d := Dest{}
			d.Page, _ = v[0].(cos.Ref)
			if len(v) >= 4 && g.Name(v[1]) == "XYZ" {
				if t, ok := g.Num(v[3]); ok {
					d.Top = &t
				}
			} else if len(v) >= 3 && (g.Name(v[1]) == "FitH" || g.Name(v[1]) == "FitBH") {
				if t, ok := g.Num(v[2]); ok {
					d.Top = &t
				}
			}
			return d, d.Page != (cos.Ref{})
		case cos.Dict:
			if v["D"] != nil {
				o = v["D"] // dictionary form of a named destination, or a GoTo action
				continue
			}
			return Dest{}, false
		case cos.Name:
			o = g.Dict(catalog["Dests"])[v]
		case cos.String:
			o = lookupName(g, g.Dict(g.Dict(catalog["Names"])["Dests"]), string(v), 0)
		default:
			return Dest{}, false
		}
	}
	return Dest{}, false
}

// lookupName searches a name tree (§7.9.6).
func lookupName(g cos.Getter, node cos.Dict, key string, depth int) cos.Object {
	if node == nil || depth > 32 {
		return nil
	}
	names := g.Array(node["Names"])
	for i := 0; i+1 < len(names); i += 2 {
		if s, ok := g.Resolve(names[i]).(cos.String); ok && string(s) == key {
			return names[i+1]
		}
	}
	for _, kid := range g.Array(node["Kids"]) {
		k := g.Dict(kid)
		if lim := g.Array(k["Limits"]); len(lim) == 2 {
			lo, _ := g.Resolve(lim[0]).(cos.String)
			hi, _ := g.Resolve(lim[1]).(cos.String)
			if key < string(lo) || key > string(hi) {
				continue
			}
		}
		if v := lookupName(g, k, key, depth+1); v != nil {
			return v
		}
	}
	return nil
}

// ReadLinks returns the link annotations of a page.
func ReadLinks(g cos.Getter, catalog, page cos.Dict) []Link {
	var out []Link
	for i, a := range g.Array(page["Annots"]) {
		d := g.Dict(a)
		if g.Name(d["Subtype"]) != "Link" {
			continue
		}
		r, ok := cos.Rect(g, d["Rect"])
		if !ok {
			continue
		}
		l := Link{ID: fmt.Sprintf("ai%d", i), Kind: "other", Quad: rectQuad(r)}
		if ref, ok := a.(cos.Ref); ok {
			l.ID = fmt.Sprintf("a%d", ref.Num)
		}
		act := g.Dict(d["A"])
		switch {
		case g.Name(act["S"]) == "URI":
			s, _ := g.Resolve(act["URI"]).(cos.String)
			l.Kind, l.URI = "uri", string(s)
		case d["Dest"] != nil || g.Name(act["S"]) == "GoTo":
			src := d["Dest"]
			if src == nil {
				src = act["D"]
			}
			if dst, ok := ResolveDest(g, catalog, src); ok {
				l.Kind, l.Page, l.Top = "page", dst.Page.Num, dst.Top
			}
		}
		out = append(out, l)
	}
	return out
}

// SafeURI allows web, mail and phone links only.
func SafeURI(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"http://", "https://", "mailto:", "tel:"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// Annot builds a link annotation dictionary.
func Annot(l Link, pageRef cos.Ref, pageRefs map[int]cos.Ref) cos.Dict {
	r := QuadRect(l.Quad)
	d := cos.Dict{
		"Type": cos.Name("Annot"), "Subtype": cos.Name("Link"),
		"Rect":   cos.Array{cos.Real(r[0]), cos.Real(r[1]), cos.Real(r[2]), cos.Real(r[3])},
		"Border": cos.Array{cos.Int(0), cos.Int(0), cos.Int(0)},
		"P":      pageRef,
	}
	// QuadPoints for rotated links: counter-clockwise from bottom-left as in
	// the spec; viewers fall back to Rect.
	if r != [4]float64{l.Quad[0], l.Quad[1], l.Quad[4], l.Quad[5]} {
		q := make(cos.Array, 8)
		for i, v := range l.Quad {
			q[i] = cos.Real(v)
		}
		d["QuadPoints"] = q
	}
	switch l.Kind {
	case "uri":
		d["A"] = cos.Dict{"S": cos.Name("URI"), "URI": cos.String(l.URI)}
	case "page":
		target, ok := pageRefs[l.Page]
		if !ok {
			return nil
		}
		if l.Top != nil {
			d["Dest"] = cos.Array{target, cos.Name("XYZ"), nil, cos.Real(*l.Top), nil}
		} else {
			d["Dest"] = cos.Array{target, cos.Name("Fit")}
		}
	default:
		return nil
	}
	return d
}

// Outline is one bookmark with its children.
type Outline struct {
	Title string    `json:"title"`
	Page  int       `json:"page"` // target page object number (0 = none)
	Top   *float64  `json:"top,omitempty"`
	Open  bool      `json:"open,omitempty"`
	Items []Outline `json:"items,omitempty"`
}

// ReadOutline reads the bookmark tree.
func ReadOutline(g cos.Getter, catalog cos.Dict) []Outline {
	seen := map[cos.Ref]bool{}
	var read func(first cos.Object, depth int) []Outline
	read = func(first cos.Object, depth int) []Outline {
		var out []Outline
		for node := first; node != nil && depth < 32; {
			ref, isRef := node.(cos.Ref)
			if isRef {
				if seen[ref] {
					break
				}
				seen[ref] = true
			}
			d := g.Dict(node)
			if d == nil {
				break
			}
			title, _ := g.Resolve(d["Title"]).(cos.String)
			o := Outline{Title: cos.Text(title)}
			src := d["Dest"]
			if src == nil {
				if a := g.Dict(d["A"]); g.Name(a["S"]) == "GoTo" {
					src = a["D"]
				}
			}
			if dst, ok := ResolveDest(g, catalog, src); ok {
				o.Page, o.Top = dst.Page.Num, dst.Top
			}
			if c, ok := g.Int(d["Count"]); ok && c > 0 {
				o.Open = true
			}
			o.Items = read(d["First"], depth+1)
			out = append(out, o)
			node = d["Next"]
		}
		return out
	}
	return read(g.Dict(catalog["Outlines"])["First"], 0)
}

// WriteOutline replaces the outline and makes viewers open the bookmarks
// panel. pageRefs maps page object numbers to page references.
func WriteOutline(u *cos.Update, items []Outline, pageRefs map[int]cos.Ref) {
	rootRef, _ := u.Root().(cos.Ref)
	cat := cos.Copy(u.Dict(rootRef)).(cos.Dict)
	if len(items) == 0 {
		delete(cat, "Outlines")
		if u.Name(cat["PageMode"]) == "UseOutlines" {
			delete(cat, "PageMode")
		}
		u.Set(rootRef, cat)
		return
	}
	top := u.Add(nil)
	var write func(parent cos.Ref, list []Outline) (first, last cos.Ref, visible int)
	write = func(parent cos.Ref, list []Outline) (cos.Ref, cos.Ref, int) {
		refs := make([]cos.Ref, len(list))
		for i := range list {
			refs[i] = u.Add(nil)
		}
		visible := 0
		for i, o := range list {
			d := cos.Dict{"Title": cos.TextString(o.Title), "Parent": parent}
			if i > 0 {
				d["Prev"] = refs[i-1]
			}
			if i+1 < len(refs) {
				d["Next"] = refs[i+1]
			}
			if target, ok := pageRefs[o.Page]; ok {
				if o.Top != nil {
					d["Dest"] = cos.Array{target, cos.Name("XYZ"), nil, cos.Real(*o.Top), nil}
				} else {
					d["Dest"] = cos.Array{target, cos.Name("Fit")}
				}
			}
			visible++
			if len(o.Items) > 0 {
				f, l, n := write(refs[i], o.Items)
				d["First"], d["Last"] = f, l
				if o.Open {
					d["Count"] = cos.Int(n)
					visible += n
				} else {
					d["Count"] = cos.Int(-n)
				}
			}
			u.Set(refs[i], d)
		}
		return refs[0], refs[len(refs)-1], visible
	}
	f, l, n := write(top, items)
	u.Set(top, cos.Dict{"Type": cos.Name("Outlines"), "First": f, "Last": l, "Count": cos.Int(n)})
	cat["Outlines"] = top
	cat["PageMode"] = cos.Name("UseOutlines")
	u.Set(rootRef, cat)
}
