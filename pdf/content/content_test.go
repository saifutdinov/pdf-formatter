package content

import (
	"fmt"
	"os"
	"testing"

	"pdf-formatter/pdf/cos"
)

func TestParseWriteRoundTrip(t *testing.T) {
	for _, name := range []string{"chrome.pdf", "cairo.pdf", "quartz.pdf"} {
		b, err := os.ReadFile("../../testdata/corpus/" + name)
		if err != nil {
			t.Skip(err)
		}
		d, _ := cos.Open(b)
		for i, p := range d.Pages() {
			ops := Parse(PageContent(d, p.Dict))
			if fmt.Sprint(ops) != fmt.Sprint(Parse(Write(ops))) {
				t.Errorf("%s page %d: round trip differs", name, i)
			}
		}
	}
}

func TestInlineImage(t *testing.T) {
	src := []byte("q 10 0 0 10 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00EI\xff\nEI Q")
	ops := Parse(src)
	if len(ops) != 4 || ops[2].Name != "BI" || string(ops[2].Data) != "\x00EI\xff" {
		t.Fatalf("got %+v", ops)
	}
	if again := Parse(Write(ops)); fmt.Sprint(again) != fmt.Sprint(ops) {
		t.Errorf("round trip: %+v", again)
	}
}
