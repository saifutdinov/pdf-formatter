package edit

import (
	"os"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/form"
	"pdf-formatter/pdf/layout"
)

func acroDoc(t *testing.T) *cos.Document {
	b, err := os.ReadFile("../../testdata/forms/form.pdf")
	if err != nil {
		t.Skip(err)
	}
	d, err := cos.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var formOps = []Op{
	{Op: "field", Field: "name", Value: "Иван Петров"},
	{Op: "field", Field: "person.city", Value: "Москва, улица с длинным названием, дом 1"},
	{Op: "field", Field: "agree", Value: "Yes"},
	{Op: "field", Field: "gender", Value: "f"},
	{Op: "field", Field: "country", Value: "kz"},
}

// Filling sets the values, the button states and new appearances that
// show any script.
func TestFillForm(t *testing.T) {
	d := edited(t, acroDoc(t), formOps)
	fields := form.Read(d, d.Dict(d.Trailer["Root"]))
	for name, want := range map[string]string{"name": "Иван Петров", "agree": "Yes", "gender": "f", "country": "kz"} {
		if f := form.Find(fields, name); f.Value != want {
			t.Errorf("%s = %q, want %q", name, f.Value, want)
		}
	}
	g := form.Find(fields, "gender")
	if d.Name(d.Dict(g.Widgets[0].Ref)["AS"]) != "Off" || d.Name(d.Dict(g.Widgets[1].Ref)["AS"]) != "f" {
		t.Error("radio widgets not switched")
	}
	// The appearance of the text field shows the value (read back as text).
	ap := d.Stream(d.Dict(d.Dict(form.Find(fields, "name").Widgets[0].Ref)["AP"])["N"])
	in := content.NewInterp(d, content.NewFontCache())
	in.Run(content.Parse(must(d.Decode(ap))), d.Dict(ap.Dict["Resources"]), content.Identity)
	var text strings.Builder
	for _, it := range in.Items {
		text.WriteString(it.Text)
	}
	if text.String() != "Иван Петров" {
		t.Errorf("appearance shows %q", text.String())
	}
}

// Flattening draws the fields into the page and drops the form.
func TestFlattenForm(t *testing.T) {
	d := edited(t, acroDoc(t), append(formOps, Op{Op: "flatten"}))
	if d.Dict(d.Trailer["Root"])["AcroForm"] != nil {
		t.Error("AcroForm left")
	}
	p := d.Pages()[0]
	for _, a := range d.Array(p.Dict["Annots"]) {
		if d.Name(d.Dict(a)["Subtype"]) == "Widget" {
			t.Error("widget left")
		}
	}
	pg := layout.Analyze(d, p, content.NewFontCache())
	var all []string
	for _, b := range pg.Blocks {
		all = append(all, b.Text)
	}
	joined := strings.Join(all, "|")
	if !strings.Contains(joined, "Иван Петров") || !strings.Contains(joined, "Kazakhstan") {
		t.Errorf("page text: %s", joined)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
