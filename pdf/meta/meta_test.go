package meta

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"pdf-formatter/pdf/cos"
)

func TestWriteRead(t *testing.T) {
	b, err := os.ReadFile("../../testdata/corpus/cairo.pdf")
	if err != nil {
		t.Skip(err)
	}
	d, _ := cos.Open(b)
	before := Read(d, d.Trailer)
	if before.Producer == "" {
		t.Errorf("producer not read: %+v", before)
	}
	m := Metadata{Title: "Договор № 42", Author: "Иван Петров", Subject: "Тест", Keywords: "pdf, договор", Creator: "Word",
		Producer: "pdf-formatter", CreationDate: "2026-01-02T03:04:05+03:00", ModDate: "2026-10-09T12:00:00+03:00",
		Lang: "ru-RU", DisplayDocTitle: true, Custom: map[string]string{"Location": "Казань"}}
	u := cos.NewUpdate(d)
	Write(u, m)
	Clean(u, Sanitize{PieceInfo: true, Thumbnails: true, JavaScript: true, Embedded: true})
	var buf bytes.Buffer
	if err := u.WriteIncremental(&buf); err != nil {
		t.Fatal(err)
	}
	d2, err := cos.Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := Read(d2, d2.Trailer)
	if got.Title != m.Title || got.Author != m.Author || got.Custom["Location"] != "Казань" || got.Lang != "ru-RU" || !got.DisplayDocTitle {
		t.Errorf("read back %+v", got)
	}
	if got.CreationDate != m.CreationDate || got.ModDate != m.ModDate {
		t.Errorf("dates %s %s", got.CreationDate, got.ModDate)
	}
	// XMP carries the same values.
	cat := d2.Dict(d2.Trailer["Root"])
	x, _ := d2.Decode(d2.Stream(cat["Metadata"]))
	props := parseXMP(x)
	if props["title"] != m.Title || props["creator"] != m.Author || props["Producer"] != m.Producer {
		t.Errorf("xmp %v", props)
	}
	if !strings.Contains(string(x), "<?xpacket end") {
		t.Error("no packet trailer")
	}
	// XMP alone fills an empty Info.
	only := Read(d2, cos.Dict{"Root": d2.Trailer["Root"]})
	if only.Title != m.Title || only.ModDate != m.ModDate {
		t.Errorf("from xmp: %+v", only)
	}
}
