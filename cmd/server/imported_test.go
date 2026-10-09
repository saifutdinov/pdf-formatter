package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Pages copied from another PDF show their original blocks, which can be
// edited.
func TestImportedPageBlocks(t *testing.T) {
	base, err := os.ReadFile("../../testdata/corpus/quartz.pdf")
	other, err2 := os.ReadFile("../../testdata/corpus/chrome.pdf")
	if err != nil || err2 != nil {
		t.Skip(err, err2)
	}
	dir := t.TempDir()
	lib := NewLibrary(dir, []string{"../../fonts"}, nil)
	store, _ := NewStore(dir, lib)
	srv := httptest.NewServer(guard(routes(store, lib, http.Dir(dir))))
	defer srv.Close()
	post := func(path, name string, data []byte, out any) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(data)
		mw.Close()
		res, err := http.Post(srv.URL+path, mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		json.NewDecoder(res.Body).Decode(out)
	}
	var m Meta
	post("/api/docs", "base.pdf", base, &m)
	var asset struct{ Asset string }
	post("/api/docs/"+m.ID+"/assets", "other.pdf", other, &asset)
	call := func(method, path string, v, out any) {
		b, _ := json.Marshal(v)
		r, _ := http.NewRequest(method, srv.URL+"/api/docs/"+m.ID+path, bytes.NewReader(b))
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Logf("%s %s: %s", method, path, data)
		}
		if out != nil {
			json.Unmarshal(data, out)
		}
	}
	var doc struct{ Pages []pageInfo }
	call("GET", "", nil, &doc)
	call("POST", "/ops", map[string]any{"op": "page.import", "page": 0, "asset": asset.Asset, "after": doc.Pages[0].Num, "from": []int{0}}, nil)
	var bl struct{ Blocks []blockJSON }
	call("GET", "/pages/1/blocks", nil, &bl)
	var site string
	for _, b := range bl.Blocks {
		if strings.HasPrefix(b.Text, "Сайт исполнителя") {
			site = b.ID
		}
	}
	if site == "" {
		t.Fatalf("no original blocks on the imported page: %d blocks", len(bl.Blocks))
	}
	call("GET", "", nil, &doc)
	var st editState
	call("POST", "/ops", map[string]any{"op": "delete", "page": doc.Pages[1].Num, "blocks": []string{site}}, &st)
	var after struct{ Blocks []blockJSON }
	call("GET", "/pages/1/blocks", nil, &after)
	for _, b := range after.Blocks {
		if b.ID == site {
			t.Error("deleted block still listed")
		}
	}
	if st.Rev != 2 {
		t.Errorf("state %+v", st)
	}
}
