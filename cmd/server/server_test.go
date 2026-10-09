package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/layout"
)

func TestEndToEnd(t *testing.T) {
	pdf, err := os.ReadFile("../../testdata/corpus/chrome.pdf")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	lib := NewLibrary(dir, []string{"../../fonts"}, nil)
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, _, ready := lib.List(); ready || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	store, err := NewStore(dir, lib)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(guard(routes(store, lib, http.Dir(dir))))
	defer srv.Close()

	call := func(method, path string, body io.Reader, ctype string, out any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
		}
		if out != nil {
			if err := json.Unmarshal(b, out); err != nil {
				t.Fatalf("%s: %v", b, err)
			}
		}
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "договор.pdf")
	fw.Write(pdf)
	mw.Close()
	var meta Meta
	call("POST", "/api/docs", &body, mw.FormDataContentType(), &meta)
	if meta.Pages != 2 || meta.Name != "договор" {
		t.Fatalf("meta %+v", meta)
	}
	var doc struct {
		Pages []pageInfo `json:"pages"`
	}
	call("GET", "/api/docs/"+meta.ID, nil, "", &doc)
	page := doc.Pages[0].Num

	var blocks struct {
		Blocks []blockJSON `json:"blocks"`
	}
	call("GET", "/api/docs/"+meta.ID+"/pages/0/blocks", nil, "", &blocks)
	var heading blockJSON
	for _, b := range blocks.Blocks {
		if strings.HasPrefix(b.Text, "Договор оказания") {
			heading = b
		}
	}
	if heading.ID == "" || heading.LibFont == "" {
		t.Fatalf("heading %+v", heading)
	}

	op := func(v any) editState {
		b, _ := json.Marshal(v)
		var st editState
		call("POST", "/api/docs/"+meta.ID+"/ops", bytes.NewReader(b), "application/json", &st)
		return st
	}
	op(map[string]any{"op": "adopt", "page": page, "blocks": []string{heading.ID}, "object": map[string]any{"text": "Договор № 43"}})
	st := op(map[string]any{"op": "transform", "page": page, "blocks": []string{"001058"}, "matrix": []float64{1, 0, 0, 1, 0, -300}})
	if !st.CanUndo || st.Rev != 2 {
		t.Fatalf("state %+v", st)
	}
	// Edits of original blocks carry the items the blocks were made of.
	if d, _ := store.Get(meta.ID); len(d.log.Ops[0].Members[heading.ID]) == 0 || len(d.log.Ops[1].Members["001058"]) == 0 {
		t.Errorf("members not recorded: %+v %+v", d.log.Ops[0].Members, d.log.Ops[1].Members)
	}
	get := func(path, rng string) (int, []byte) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/docs/"+meta.ID+path, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	_, rev2 := get("/file?v=2", "")
	call("POST", "/api/docs/"+meta.ID+"/undo", nil, "", &st)
	if !st.CanRedo {
		t.Fatalf("after undo %+v", st)
	}
	// A viewer still reading revision 2 in ranges gets revision 2's bytes;
	// one never served is gone.
	if code, b := get("/file?v=2", "bytes=-300"); code != http.StatusPartialContent || !bytes.Equal(b, rev2[len(rev2)-300:]) {
		t.Errorf("old revision changed under a range request: %d", code)
	}
	if code, _ := get("/file?v=1", ""); code != http.StatusGone {
		t.Errorf("unknown old revision: %d, want 410", code)
	}

	res, err := http.Get(srv.URL + "/api/docs/" + meta.ID + "/file?v=" + "3")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.HasPrefix(out, pdf) {
		t.Error("served file must be the original plus an update")
	}
	d, err := cos.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pg := layout.Analyze(d, d.Pages()[0], content.NewFontCache())
	var texts []string
	for _, b := range pg.Blocks {
		texts = append(texts, b.Text)
	}
	all := strings.Join(texts, "|")
	if !strings.Contains(all, "Договор № 43") || strings.Contains(all, "Договор оказания") {
		t.Errorf("adopted heading not applied: %s", all)
	}

	// Insert a page after the first: the page list changes.
	st = op(map[string]any{"op": "page.insert", "page": 0, "after": page})
	if !st.Structure {
		t.Error("insert must report a structure change")
	}
	call("GET", "/api/docs/"+meta.ID, nil, "", &doc)
	if len(doc.Pages) != 3 || doc.Pages[1].Num >= 0 || doc.Pages[1].Label != "2" {
		t.Fatalf("pages after insert: %+v", doc.Pages)
	}
	call("GET", "/api/docs/"+meta.ID+"/pages/1/blocks", nil, "", &blocks)
	if len(blocks.Blocks) != 0 {
		t.Errorf("new page has blocks: %+v", blocks.Blocks)
	}
	op(map[string]any{"op": "add", "page": doc.Pages[1].Num, "object": map[string]any{"kind": "rect", "matrix": []float64{1, 0, 0, 1, 50, 500}, "w": 100, "h": 50, "fill": "#ff0000", "opacity": 1}})
	call("GET", "/api/docs/"+meta.ID+"/pages/1/blocks", nil, "", &blocks)
	if len(blocks.Blocks) != 1 {
		t.Errorf("object on new page: %+v", blocks.Blocks)
	}

	// Editing a block starts from its text as extracted, not re-spaced.
	call("GET", "/api/docs/"+meta.ID+"/pages/0/blocks", nil, "", &blocks)
	var para blockJSON
	for _, b := range blocks.Blocks {
		if b.Object == nil && strings.Contains(b.Text, "\n") {
			para = b
			break
		}
	}
	st = op(map[string]any{"op": "adopt", "page": page, "blocks": []string{para.ID}})
	call("GET", "/api/docs/"+meta.ID+"/pages/0/blocks", nil, "", &blocks)
	found := false
	for _, b := range blocks.Blocks {
		if b.ID == st.Created {
			found = true
			if want := cmp.Or(para.Flow, para.Text); b.Object.Text != want {
				t.Errorf("adopted text %q, want %q", b.Object.Text, want)
			}
		}
	}
	if para.ID == "" || !found {
		t.Errorf("paragraph %q not adopted", para.Text)
	}

	// Clean copy opens too.
	res, _ = http.Get(srv.URL + "/api/docs/" + meta.ID + "/file?download=clean")
	clean, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if d, err := cos.Open(clean); err != nil || len(d.Pages()) != 3 || bytes.HasPrefix(clean, pdf) {
		t.Errorf("clean copy: %v", err)
	}
}

func TestConcatReaderAt(t *testing.T) {
	c := concat{[]byte("hello "), []byte("world")}
	r := io.NewSectionReader(c, 0, 11)
	b, _ := io.ReadAll(r)
	if string(b) != "hello world" {
		t.Errorf("%q", b)
	}
	p := make([]byte, 4)
	if n, _ := c.ReadAt(p, 4); n != 4 || string(p) != "o wo" {
		t.Errorf("%q", p[:n])
	}
}

func TestGuard(t *testing.T) {
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for _, tc := range []struct {
		method, host, origin string
		want                 int
	}{
		{"GET", "127.0.0.1:8080", "", 200},
		{"POST", "127.0.0.1:8080", "http://127.0.0.1:8080", 200},
		{"POST", "localhost:8080", "", 200},
		{"POST", "127.0.0.1:8080", "https://evil.example", 403}, // cross-site write
		{"GET", "evil.example:8080", "", 403},                   // DNS rebinding
	} {
		req := httptest.NewRequest(tc.method, "http://"+tc.host+"/api/x", nil)
		req.Host = tc.host
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s origin=%q: %d, want %d", tc.method, tc.host, tc.origin, rec.Code, tc.want)
		}
	}
}
