package cos

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const corpus = "../../testdata/corpus"

var wantPages = map[string]int{"chrome.pdf": 2, "cairo.pdf": 2, "quartz.pdf": 1, "big.pdf": 500}

func corpusFiles(t testing.TB) map[string][]byte {
	files, _ := filepath.Glob(filepath.Join(corpus, "*.pdf"))
	if len(files) == 0 {
		t.Skip("no corpus")
	}
	out := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = b
	}
	return out
}

func TestLexer(t *testing.T) {
	l := &Lexer{B: []byte(`(a\(b\)\101\n) <48 65 6C6> /A#20B -.5 +3 /N 12 R [1 2 R] << /K /V >> true null 4.`), Refs: true}
	var got []Object
	for {
		tok := l.Next()
		if tok.Kind == TEOF {
			break
		}
		o, err := l.ReadObject(tok)
		if ke, ok := errors.AsType[*KeywordError](err); ok {
			got = append(got, "kw:"+ke.Keyword)
			continue
		}
		got = append(got, o)
	}
	want := []Object{String("a(b)A\n"), String("Hel`"), Name("A B"), Real(-.5), Int(3), Name("N"), Int(12), "kw:R",
		Array{Ref{1, 2}}, Dict{"K": Name("V")}, Bool(true), nil, Real(4)}
	if len(got) != len(want) {
		t.Fatalf("got %d objects %#v", len(got), got)
	}
	for i := range want {
		if fmt.Sprintf("%#v", got[i]) != fmt.Sprintf("%#v", want[i]) {
			t.Errorf("#%d: got %#v want %#v", i, got[i], want[i])
		}
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	in := Dict{"A": Array{Int(1), Real(2.5), String("x(y)\\z\x00\xff"), Name("a b#c"), Bool(false), Ref{3, 0}}, "B": Real(1.0 / 3)}
	b := AppendObject(nil, in)
	l := &Lexer{B: b, Refs: true}
	out, err := l.ReadObject(l.Next())
	if err != nil {
		t.Fatal(err)
	}
	if string(AppendObject(nil, out)) != string(b) {
		t.Fatalf("round trip:\n%s\n%s", b, AppendObject(nil, out))
	}
}

func TestFilters(t *testing.T) {
	r := resolver{get: func(Ref) Object { return nil }}
	for _, tc := range []struct {
		name Name
		in   string
		want string
	}{
		{"ASCIIHexDecode", "48656c6c6f>", "Hello"},
		{"ASCII85Decode", "<~87cURD]i,\"Ebo80~>", "Hello World!"},
		{"RunLengthDecode", "\x02abc\xfdz\x80", "abczzzz"},
		{"FlateDecode", string(Deflate([]byte("flate data"))), "flate data"},
	} {
		got, err := r.Decode(&Stream{Dict: Dict{"Filter": tc.name}, Data: []byte(tc.in)})
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: %q, %v", tc.name, got, err)
		}
	}
	// LZW sample from §7.4.4.2: "-----A---B" with EarlyChange.
	got := lzwDecode([]byte{0x80, 0x0b, 0x60, 0x50, 0x22, 0x0c, 0x0c, 0x85, 0x01}, 1)
	if string(got) != "-----A---B" {
		t.Errorf("lzw: %q", got)
	}
	// PNG Up predictor, 2 columns.
	p := Dict{"Predictor": Int(12), "Columns": Int(2)}
	got, _ = predict(r, []byte{2, 1, 2, 2, 1, 1}, p)
	if !bytes.Equal(got, []byte{1, 2, 2, 3}) {
		t.Errorf("predictor: %v", got)
	}
}

func checkDoc(t *testing.T, name string, d *Document) {
	t.Helper()
	pages := d.Pages()
	if want, ok := wantPages[name]; ok && len(pages) != want {
		t.Errorf("%s: %d pages, want %d", name, len(pages), want)
	}
	for i, p := range pages {
		if p.MediaBox[2]-p.MediaBox[0] <= 0 || p.Resources == nil {
			t.Errorf("%s: page %d: box %v, resources %v", name, i, p.MediaBox, p.Resources != nil)
		}
	}
	if name == "big.pdf" {
		return // 500 copies of chrome.pdf: objects checked there
	}
	for num := range d.xref {
		o := d.Get(Ref{Num: num})
		if s, ok := o.(*Stream); ok {
			_, err := d.Decode(s)
			var fe *FilterError
			if err != nil && !errors.As(err, &fe) {
				t.Errorf("%s: obj %d: %v", name, num, err)
			}
		}
	}
}

// validate runs qpdf --check when available (it is in the Docker test stage).
func validate(t *testing.T, name string, b []byte, password ...string) {
	t.Helper()
	qpdf, err := exec.LookPath("qpdf")
	if err != nil {
		return
	}
	f := filepath.Join(t.TempDir(), name)
	os.WriteFile(f, b, 0o644)
	args := []string{"--check", f}
	if len(password) > 0 {
		args = append(args, "--password="+password[0])
	}
	if out, err := exec.Command(qpdf, args...).CombinedOutput(); err != nil {
		t.Errorf("%s: qpdf --check: %v\n%s", name, err, out)
	}
}

func TestCorpus(t *testing.T) {
	for name, data := range corpusFiles(t) {
		t.Run(name, func(t *testing.T) {
			d, err := Open(data)
			if err != nil {
				t.Fatal(err)
			}
			if d.Reconstructed {
				t.Error("xref unexpectedly reconstructed")
			}
			checkDoc(t, name, d)

			// Incremental update: change the Info title, everything else stays.
			u := NewUpdate(d)
			info := Dict{"Title": String("Изменено")}
			if old := d.Dict(d.Trailer["Info"]); old != nil {
				info = Copy(old).(Dict)
				info["Title"] = String("Изменено")
			}
			u.Trailer["Info"] = u.Add(info)
			var buf bytes.Buffer
			if err := u.WriteIncremental(&buf); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(buf.Bytes(), data) {
				t.Error("incremental update must keep original bytes")
			}
			d2, err := Open(buf.Bytes())
			if err != nil || d2.Reconstructed {
				t.Fatalf("reopen incremental: %v, reconstructed=%v", err, d2 != nil && d2.Reconstructed)
			}
			if got := d2.Dict(d2.Trailer["Info"])["Title"]; got != String("Изменено") {
				t.Errorf("title after update: %v", got)
			}
			checkDoc(t, name, d2)
			validate(t, "inc-"+name, buf.Bytes())

			// Clean rewrite.
			buf.Reset()
			if err := NewUpdate(d).WriteClean(&buf); err != nil {
				t.Fatal(err)
			}
			d3, err := Open(buf.Bytes())
			if err != nil || d3.Reconstructed {
				t.Fatalf("reopen clean: %v", err)
			}
			checkDoc(t, name, d3)
			validate(t, "clean-"+name, buf.Bytes())
		})
	}
}

func TestReconstruct(t *testing.T) {
	files := corpusFiles(t)
	for _, name := range []string{"chrome.pdf", "cairo.pdf", "quartz.pdf"} {
		data, ok := files[name]
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// Break startxref: point it into the middle of nowhere.
			i := bytes.LastIndex(data, []byte("startxref"))
			bad := append(bytes.Clone(data[:i]), []byte("startxref\n17\n%%EOF\n")...)
			d, err := Open(bad)
			if err != nil {
				t.Fatal(err)
			}
			if !d.Reconstructed {
				t.Error("expected reconstruction")
			}
			checkDoc(t, name, d)

			// Junk before the header shifts every offset.
			d, err = Open(append([]byte("garbage\n"), data...))
			if err != nil {
				t.Fatal(err)
			}
			checkDoc(t, name, d)
		})
	}
}

func TestImport(t *testing.T) {
	files := corpusFiles(t)
	src, err := Open(files["chrome.pdf"])
	if err != nil {
		t.Skip("no chrome.pdf")
	}
	dst, _ := Open(files["quartz.pdf"])
	u := NewUpdate(dst)
	memo := map[Ref]Ref{}
	page := src.Pages()[0]
	res := u.Import(src, page.Resources, memo).(Dict)
	if len(res) == 0 || len(memo) == 0 {
		t.Fatalf("nothing imported: %v", res)
	}
	var buf bytes.Buffer
	if err := u.WriteClean(&buf); err != nil {
		t.Fatal(err)
	}
	// Unreferenced imports are dropped by WriteClean.
	if strings.Contains(buf.String(), "Skia") {
		t.Error("garbage survived clean write")
	}
}

func FuzzOpen(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join(corpus, "*.pdf"))
	for _, name := range files {
		if b, err := os.ReadFile(name); err == nil && len(b) < 200_000 {
			f.Add(b)
		}
	}
	f.Add([]byte("%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj trailer << /Root 1 0 R >>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Open(data)
		if err != nil {
			return
		}
		for _, p := range d.Pages() {
			d.Dict(p.Resources["Font"])
		}
		for num := range d.xref {
			if s, ok := d.Get(Ref{Num: num}).(*Stream); ok {
				d.Decode(s)
			}
		}
		var buf bytes.Buffer
		NewUpdate(d).WriteClean(&buf)
	})
}

func TestTextAndDates(t *testing.T) {
	for _, s := range []string{"Hello", "Привет, мир — “кавычки”", "Ünïcödé 😀"} {
		if got := Text(TextString(s)); got != s {
			t.Errorf("%q -> %q", s, got)
		}
	}
	if got := Text(String("\x80 \x84 caf\xe9")); got != "• — café" {
		t.Errorf("PDFDocEncoding: %q", got)
	}
	d, ok := ParseDate("D:20261009143005+03'00'")
	if !ok || d.Format(time.RFC3339) != "2026-10-09T14:30:05+03:00" {
		t.Errorf("date %v %v", d, ok)
	}
	if got := string(FormatDate(d)); got != "D:20261009143005+03'00'" {
		t.Errorf("format %q", got)
	}
	if d, ok := ParseDate("D:1999"); !ok || d.Year() != 1999 || d.Month() != 1 {
		t.Errorf("short date %v", d)
	}
}

// An update over a file whose xref had to be rebuilt carries a complete
// cross-reference stream, so readers never follow the broken original.
func TestIncrementalOverReconstructed(t *testing.T) {
	src := "%PDF-1.7\n1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj\n2 0 obj <</Type/Pages/Kids[3 0 R]/Count 1>> endobj\n3 0 obj <</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Resources<<>>>> endobj\ntrailer <</Root 1 0 R>>\n"
	d, err := Open([]byte(src))
	if err != nil || !d.Reconstructed {
		t.Fatalf("%v %v", err, d != nil && d.Reconstructed)
	}
	u := NewUpdate(d)
	u.Trailer["Info"] = u.Add(Dict{"Title": String("x")})
	var buf bytes.Buffer
	u.WriteIncremental(&buf)
	d2, err := Open(buf.Bytes())
	if err != nil || d2.Reconstructed || len(d2.Pages()) != 1 || d2.Dict(d2.Trailer["Info"])["Title"] != String("x") {
		t.Fatalf("reopen: %v reconstructed=%v", err, d2 != nil && d2.Reconstructed)
	}
	validate(t, "rebuilt.pdf", buf.Bytes())
}
