// Package meta reads and writes document metadata: the Info dictionary
// (§14.3.3) and the XMP metadata stream (§14.3.2), kept in sync, plus
// removal of hidden data.
package meta

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"pdf-formatter/pdf/cos"
)

// Metadata is what the editor shows and edits. Dates are RFC 3339.
type Metadata struct {
	Title           string            `json:"title"`
	Author          string            `json:"author"`
	Subject         string            `json:"subject"`
	Keywords        string            `json:"keywords"`
	Creator         string            `json:"creator"`  // application that made the original
	Producer        string            `json:"producer"` // application that made the PDF
	CreationDate    string            `json:"creationDate"`
	ModDate         string            `json:"modDate"`
	Lang            string            `json:"lang"`
	DisplayDocTitle bool              `json:"displayDocTitle"`
	Custom          map[string]string `json:"custom"`
}

var standard = map[cos.Name]bool{"Title": true, "Author": true, "Subject": true, "Keywords": true, "Creator": true,
	"Producer": true, "CreationDate": true, "ModDate": true, "Trapped": true}

func dateRFC(s cos.String) string {
	if t, ok := cos.ParseDate(string(s)); ok {
		return t.Format(time.RFC3339)
	}
	return ""
}

// Read collects metadata: Info first, XMP for what Info lacks.
func Read(g cos.Getter, trailer cos.Dict) Metadata {
	m := Metadata{Custom: map[string]string{}}
	info := g.Dict(trailer["Info"])
	str := func(k cos.Name) string {
		s, _ := g.Resolve(info[k]).(cos.String)
		return cos.Text(s)
	}
	m.Title, m.Author, m.Subject, m.Keywords = str("Title"), str("Author"), str("Subject"), str("Keywords")
	m.Creator, m.Producer = str("Creator"), str("Producer")
	if s, ok := g.Resolve(info["CreationDate"]).(cos.String); ok {
		m.CreationDate = dateRFC(s)
	}
	if s, ok := g.Resolve(info["ModDate"]).(cos.String); ok {
		m.ModDate = dateRFC(s)
	}
	for k, v := range info {
		if s, ok := g.Resolve(v).(cos.String); ok && !standard[k] {
			m.Custom[string(k)] = cos.Text(s)
		}
	}
	cat := g.Dict(trailer["Root"])
	if l, ok := g.Resolve(cat["Lang"]).(cos.String); ok {
		m.Lang = cos.Text(l)
	}
	if b, ok := g.Resolve(g.Dict(cat["ViewerPreferences"])["DisplayDocTitle"]).(cos.Bool); ok {
		m.DisplayDocTitle = bool(b)
	}
	if s := g.Stream(cat["Metadata"]); s != nil {
		if data, err := g.Decode(s); err == nil {
			x := parseXMP(data)
			fill := func(dst *string, v string) {
				if *dst == "" {
					*dst = v
				}
			}
			fill(&m.Title, x["title"])
			fill(&m.Author, x["creator"])
			fill(&m.Subject, x["description"])
			fill(&m.Keywords, x["Keywords"])
			fill(&m.Creator, x["CreatorTool"])
			fill(&m.Producer, x["Producer"])
			fill(&m.CreationDate, x["CreateDate"])
			fill(&m.ModDate, x["ModifyDate"])
		}
	}
	return m
}

// parseXMP extracts simple properties by local name. Language alternatives
// and sequences yield their first item.
func parseXMP(data []byte) map[string]string {
	out := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(data))
	var stack []string
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			for _, a := range t.Attr { // properties written as attributes of rdf:Description
				if a.Name.Space != "" && a.Name.Space != "http://www.w3.org/1999/02/22-rdf-syntax-ns#" {
					if _, seen := out[a.Name.Local]; !seen {
						out[a.Name.Local] = a.Value
					}
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s == "" || len(stack) == 0 {
				continue
			}
			// Property is the nearest ancestor that is not rdf structure.
			for i := len(stack) - 1; i >= 0; i-- {
				switch stack[i] {
				case "li", "Alt", "Seq", "Bag":
					continue
				}
				if _, seen := out[stack[i]]; !seen {
					out[stack[i]] = s
				}
				break
			}
		}
	}
}

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// xmp builds a fresh XMP packet: old packets often carry edit history.
func xmp(m Metadata) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xpacket begin="` + "\ufeff" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmlns:pdf="http://ns.adobe.com/pdf/1.3/">
`)
	if m.Title != "" {
		fmt.Fprintf(&sb, "<dc:title><rdf:Alt><rdf:li xml:lang=\"x-default\">%s</rdf:li></rdf:Alt></dc:title>\n", esc(m.Title))
	}
	if m.Author != "" {
		fmt.Fprintf(&sb, "<dc:creator><rdf:Seq><rdf:li>%s</rdf:li></rdf:Seq></dc:creator>\n", esc(m.Author))
	}
	if m.Subject != "" {
		fmt.Fprintf(&sb, "<dc:description><rdf:Alt><rdf:li xml:lang=\"x-default\">%s</rdf:li></rdf:Alt></dc:description>\n", esc(m.Subject))
	}
	if m.Lang != "" {
		fmt.Fprintf(&sb, "<dc:language><rdf:Bag><rdf:li>%s</rdf:li></rdf:Bag></dc:language>\n", esc(m.Lang))
	}
	for _, p := range [][2]string{{"pdf:Keywords", m.Keywords}, {"pdf:Producer", m.Producer}, {"xmp:CreatorTool", m.Creator},
		{"xmp:CreateDate", m.CreationDate}, {"xmp:ModifyDate", m.ModDate}, {"xmp:MetadataDate", m.ModDate}} {
		if p[1] != "" {
			fmt.Fprintf(&sb, "<%s>%s</%s>\n", p[0], esc(p[1]), p[0])
		}
	}
	sb.WriteString("</rdf:Description>\n</rdf:RDF>\n</x:xmpmeta>\n")
	sb.WriteString(strings.Repeat(strings.Repeat(" ", 99)+"\n", 20)) // padding for in-place editors
	sb.WriteString(`<?xpacket end="w"?>`)
	return []byte(sb.String())
}

// Write sets Info and XMP from m. Old values stay in earlier revisions of
// an incremental update; a clean rewrite drops them.
func Write(u *cos.Update, m Metadata) {
	g := cos.Getter(u)
	info := cos.Copy(g.Dict(u.Doc.Trailer["Info"])).(cos.Dict)
	if info == nil {
		info = cos.Dict{}
	}
	set := func(k cos.Name, v string) {
		if v == "" {
			delete(info, k)
		} else {
			info[k] = cos.TextString(v)
		}
	}
	set("Title", m.Title)
	set("Author", m.Author)
	set("Subject", m.Subject)
	set("Keywords", m.Keywords)
	set("Creator", m.Creator)
	set("Producer", m.Producer)
	for k, v := range map[cos.Name]string{"CreationDate": m.CreationDate, "ModDate": m.ModDate} {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			info[k] = cos.FormatDate(t)
		} else {
			delete(info, k)
		}
	}
	for k := range info {
		if !standard[k] {
			delete(info, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(m.Custom)) {
		if k != "" && m.Custom[k] != "" && !standard[cos.Name(k)] {
			info[cos.Name(k)] = cos.TextString(m.Custom[k])
		}
	}
	if ref, ok := u.Doc.Trailer["Info"].(cos.Ref); ok {
		u.Set(ref, info)
	} else {
		u.Trailer["Info"] = u.Add(info)
	}

	rootRef, _ := u.Root().(cos.Ref)
	cat := cos.Copy(g.Dict(rootRef)).(cos.Dict)
	cat["Metadata"] = u.Add(&cos.Stream{Dict: cos.Dict{"Type": cos.Name("Metadata"), "Subtype": cos.Name("XML")}, Data: xmp(m)})
	if m.Lang != "" {
		cat["Lang"] = cos.TextString(m.Lang)
	} else {
		delete(cat, "Lang")
	}
	vp := cos.Copy(g.Dict(cat["ViewerPreferences"])).(cos.Dict)
	if vp == nil {
		vp = cos.Dict{}
	}
	if m.DisplayDocTitle {
		vp["DisplayDocTitle"] = cos.Bool(true)
	} else {
		delete(vp, "DisplayDocTitle")
	}
	if len(vp) > 0 {
		cat["ViewerPreferences"] = vp
	} else {
		delete(cat, "ViewerPreferences")
	}
	u.Set(rootRef, cat)
}

// Sanitize options.
type Sanitize struct {
	PieceInfo  bool `json:"pieceInfo"`  // private data of other applications (Illustrator keeps the whole .ai there)
	Thumbnails bool `json:"thumbnails"` // embedded page thumbnails
	JavaScript bool `json:"javascript"` // document scripts and automatic actions
	Embedded   bool `json:"embedded"`   // attached files
}

func (s Sanitize) Any() bool { return s.PieceInfo || s.Thumbnails || s.JavaScript || s.Embedded }

// Clean removes hidden data from the catalog and pages.
// OwnPiece is the key of the editor's data in page piece dictionaries.
const OwnPiece = "PDFFormatter"

func Clean(u *cos.Update, s Sanitize) {
	if !s.Any() {
		return
	}
	g := cos.Getter(u)
	rootRef, _ := u.Root().(cos.Ref)
	cat := cos.Copy(g.Dict(rootRef)).(cos.Dict)
	names := cos.Copy(g.Dict(cat["Names"])).(cos.Dict)
	if s.PieceInfo {
		delete(cat, "PieceInfo")
	}
	if s.JavaScript {
		delete(names, "JavaScript")
		delete(cat, "AA")
		if a := g.Dict(cat["OpenAction"]); a != nil && g.Name(a["S"]) == "JavaScript" {
			delete(cat, "OpenAction")
		}
	}
	if s.Embedded {
		delete(names, "EmbeddedFiles")
		delete(cat, "AF")
	}
	if names != nil {
		cat["Names"] = names
	}
	u.Set(rootRef, cat)
	for _, p := range u.Pages() {
		page := cos.Copy(g.Dict(p.Ref)).(cos.Dict)
		changed := false
		for k, on := range map[cos.Name]bool{"PieceInfo": s.PieceInfo, "Thumb": s.Thumbnails, "AA": s.JavaScript, "AF": s.Embedded} {
			if _, ok := page[k]; on && ok {
				delete(page, k)
				changed = true
			}
		}
		// The editor's own piece stays: it keeps its objects editable.
		if own := g.Dict(g.Dict(p.Dict["PieceInfo"])[OwnPiece]); s.PieceInfo && own != nil {
			page["PieceInfo"] = cos.Dict{OwnPiece: own}
		}
		if changed {
			u.Set(p.Ref, page)
		}
	}
}
