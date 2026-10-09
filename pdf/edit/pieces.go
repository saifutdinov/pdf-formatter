package edit

import (
	"encoding/json"
	"time"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/layout"
	"pdf-formatter/pdf/meta"
)

// Own objects stay editable in the file: their content is marked
// /PFObj <</ID (o1)>> BDC … EMC and their model sits in the page's piece
// dictionary (§14.5), so a downloaded file opens with them as objects again.

// persistent reports an object of the user (o1, o2 …), not page furniture
// generated from other settings (headers, footers, contents pages).
func persistent(o *Object) bool {
	return len(o.ID) > 1 && o.ID[0] == 'o' && o.ID[1] >= '0' && o.ID[1] <= '9'
}

// Piece is the model of one page's own objects as read back from a file.
type Piece struct {
	Page    cos.Ref
	Objects []Object
	Fonts   map[string]string   // library font id → PostScript name
	Assets  map[string][]byte   // uploaded images and SVG by name
	Members map[string][]string // object id → the items it was drawn with (layout.ItemID)
}

type pieceJSON struct {
	Version int               `json:"version"`
	Objects []Object          `json:"objects"`
	Fonts   map[string]string `json:"fonts"`
}

// piece writes the model of a page's own objects.
// ponytail: images are stored once more as uploaded; a JPEG could share the
// image XObject's bytes if files get large.
func (r *renderer) piece(objs []*Object) cos.Dict {
	fonts := map[string]string{}
	assets := cos.Dict{}
	note := func(id string) {
		if _, ok := fonts[id]; !ok {
			if f, err := r.env.Face(id); err == nil {
				fonts[id] = f.PSName
			}
		}
	}
	list := make([]Object, 0, len(objs))
	for _, o := range objs {
		list = append(list, *o)
		note(o.Font)
		for _, run := range o.Runs {
			note(run.Font)
		}
		if o.Asset != "" && assets[cos.Name(o.Asset)] == nil {
			if data, err := r.env.Asset(o.Asset); err == nil {
				assets[cos.Name(o.Asset)] = r.u.Add(stream(data))
			}
		}
	}
	j, _ := json.Marshal(pieceJSON{Version: 1, Objects: list, Fonts: fonts})
	return cos.Dict{
		"LastModified": cos.String(time.Now().UTC().Format("D:20060102150405Z")),
		"Private":      cos.Dict{"Objects": r.u.Add(stream(j)), "Assets": assets},
	}
}

// markOwn wraps the content of an own object so it can be found again.
func markOwn(o *Object, ops []content.Op) []content.Op {
	if !persistent(o) {
		return ops
	}
	out := append([]content.Op{op("BDC", cos.Name("PFObj"), cos.Dict{"ID": cos.String(o.ID)})}, ops...)
	return append(out, op("EMC"))
}

// ReadPieces returns the own objects stored in a file, page by page, with
// the items that show each of them now.
func ReadPieces(d *cos.Document, fonts *content.FontCache) []Piece {
	var out []Piece
	for _, p := range d.Pages() {
		pd := d.Dict(d.Dict(p.Dict["PieceInfo"])[meta.OwnPiece])
		priv := d.Dict(pd["Private"])
		s := d.Stream(priv["Objects"])
		if s == nil {
			continue
		}
		data, err := d.Decode(s)
		var pj pieceJSON
		if err != nil || json.Unmarshal(data, &pj) != nil || len(pj.Objects) == 0 {
			continue
		}
		pc := Piece{Page: p.Ref, Objects: pj.Objects, Fonts: pj.Fonts, Assets: map[string][]byte{}, Members: map[string][]string{}}
		for name, v := range d.Dict(priv["Assets"]) {
			if s := d.Stream(v); s != nil {
				if b, err := d.Decode(s); err == nil {
					pc.Assets[string(name)] = b
				}
			}
		}
		pg := layout.Analyze(d, p, fonts)
		for _, it := range pg.Items {
			if it.Own != "" {
				pc.Members[it.Own] = append(pc.Members[it.Own], layout.ItemID(it))
			}
		}
		out = append(out, pc)
	}
	return out
}
