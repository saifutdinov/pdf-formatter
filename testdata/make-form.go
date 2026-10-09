//go:build ignore

// Writes forms/form.pdf: an AcroForm with every kind of field the editor
// fills. Run from testdata: go run make-form.go
package main

import (
	"fmt"
	"os"
	"strings"
)

func stream(dict, data string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data)
}

func main() {
	objs := []string{
		// 1 catalog, 2 pages, 3 page
		`<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 5 0 R 7 0 R 10 0 R 13 0 R] /DA (/Helv 0 Tf 0 g) /DR << /Font << /Helv 14 0 R >> >> >> >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 14 0 R >> >> /Contents 15 0 R /Annots [4 0 R 6 0 R 7 0 R 11 0 R 12 0 R 13 0 R] >>`,
		// 4 text field and widget in one dictionary
		`<< /FT /Tx /T (name) /Type /Annot /Subtype /Widget /Rect [150 700 400 720] /P 3 0 R /DA (/Helv 12 Tf 0 0 0.6 rg) /MK << /BG [0.93 0.95 1] /BC [0.5 0.5 0.5] >> /F 4 >>`,
		// 5 non-terminal field "person" with 6 the multiline "person.city"
		`<< /T (person) /Kids [6 0 R] >>`,
		`<< /FT /Tx /Ff 4096 /T (city) /Parent 5 0 R /Type /Annot /Subtype /Widget /Rect [150 620 400 680] /P 3 0 R /DA (/Helv 0 Tf 0 g) /F 4 >>`,
		// 7 check box with its on/off appearances (8, 9)
		`<< /FT /Btn /T (agree) /V /Off /AS /Off /Type /Annot /Subtype /Widget /Rect [150 580 166 596] /P 3 0 R /AP << /N << /Yes 8 0 R /Off 9 0 R >> >> /F 4 >>`,
		stream("/Type /XObject /Subtype /Form /BBox [0 0 16 16]", "0 0 1 RG 2 w 3 8 m 7 3 l 13 13 l S 1 1 14 14 re S"),
		stream("/Type /XObject /Subtype /Form /BBox [0 0 16 16]", "1 1 14 14 re S"),
		// 10 radio group "gender" with widgets 11 (m) and 12 (f)
		`<< /FT /Btn /Ff 49152 /T (gender) /V /Off /Kids [11 0 R 12 0 R] >>`,
		`<< /Type /Annot /Subtype /Widget /Parent 10 0 R /Rect [150 540 166 556] /P 3 0 R /AS /Off /AP << /N << /m 8 0 R /Off 9 0 R >> >> /F 4 >>`,
		`<< /Type /Annot /Subtype /Widget /Parent 10 0 R /Rect [200 540 216 556] /P 3 0 R /AS /Off /AP << /N << /f 8 0 R /Off 9 0 R >> >> /F 4 >>`,
		// 13 combo box
		`<< /FT /Ch /Ff 131072 /T (country) /Opt [[(ru) (Russia)] [(kz) (Kazakhstan)]] /V (ru) /Type /Annot /Subtype /Widget /Rect [150 500 300 516] /P 3 0 R /DA (/Helv 10 Tf 0 g) /F 4 >>`,
		// 14 font, 15 page content: the labels
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`,
	}
	body := "BT /F1 11 Tf 50 705 Td (Name) Tj 0 -60 Td (City) Tj 0 -60 Td (Agree) Tj 0 -40 Td (Gender) Tj 0 -40 Td (Country) Tj ET"
	objs = append(objs, stream("", body))
	var b strings.Builder
	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f\r\n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n\r\n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	if err := os.WriteFile("forms/form.pdf", []byte(b.String()), 0o644); err != nil {
		panic(err)
	}
}
