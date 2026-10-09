package form

import (
	"os"
	"testing"

	"pdf-formatter/pdf/cos"
)

func TestRead(t *testing.T) {
	b, err := os.ReadFile("../../testdata/forms/form.pdf")
	if err != nil {
		t.Skip(err)
	}
	d, err := cos.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	fields := Read(d, d.Dict(d.Trailer["Root"]))
	page := d.Pages()[0].Ref
	want := map[string]string{"name": "text", "person.city": "text", "agree": "checkbox", "gender": "radio", "country": "choice"}
	if len(fields) != len(want) {
		t.Fatalf("fields %+v", fields)
	}
	for _, f := range fields {
		if want[f.Name] != f.Type || len(f.Widgets) == 0 || f.Widgets[0].Page != page {
			t.Errorf("field %+v", f)
		}
	}
	if f := Find(fields, "person.city"); !f.Multiline || f.FontSize != 0 {
		t.Errorf("city %+v", f)
	}
	if f := Find(fields, "name"); f.FontSize != 12 || f.Color != [3]float64{0, 0, 0.6} {
		t.Errorf("name %+v", f)
	}
	g := Find(fields, "gender")
	if len(g.Widgets) != 2 || g.Widgets[0].On != "m" || g.Widgets[1].On != "f" || g.Value != "Off" {
		t.Errorf("gender %+v", g)
	}
	if !g.Valid("f") || g.Valid("x") {
		t.Error("radio states")
	}
	c := Find(fields, "country")
	if !c.Combo || c.Value != "ru" || len(c.Options) != 2 || c.Options[1] != (Option{"kz", "Kazakhstan"}) {
		t.Errorf("country %+v", c)
	}
}
