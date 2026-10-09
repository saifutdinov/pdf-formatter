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
	"time"
)

// A document edited here, downloaded and uploaded again opens with its
// objects editable, each drawn once.
func TestReopenDownloaded(t *testing.T) {
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
	store, _ := NewStore(dir, lib)
	srv := httptest.NewServer(guard(routes(store, lib, http.Dir(dir))))
	defer srv.Close()
	upload := func(data []byte) Meta {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "doc.pdf")
		fw.Write(data)
		mw.Close()
		res, err := http.Post(srv.URL+"/api/docs", mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m Meta
		json.NewDecoder(res.Body).Decode(&m)
		return m
	}
	call := func(id, method, path string, v, out any) []byte {
		b, _ := json.Marshal(v)
		r, _ := http.NewRequest(method, srv.URL+"/api/docs/"+id+path, bytes.NewReader(b))
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if out != nil {
			json.Unmarshal(data, out)
		}
		return data
	}
	m := upload(pdf)
	var doc struct{ Pages []pageInfo }
	call(m.ID, "GET", "", nil, &doc)
	page := doc.Pages[0].Num
	call(m.ID, "POST", "/ops", map[string]any{"op": "add", "page": page, "object": map[string]any{
		"kind": "text", "matrix": []float64{1, 0, 0, 1, 60, 300}, "w": 200, "h": 30, "text": "Вернётся объектом", "size": 12, "opacity": 1}}, nil)
	file := call(m.ID, "GET", "/file", nil, nil)

	m2 := upload(file)
	var st struct{ State editState }
	call(m2.ID, "GET", "", nil, &st)
	var bl struct{ Blocks []blockJSON }
	call(m2.ID, "GET", "/pages/0/blocks", nil, &bl)
	objects, texts := 0, []string{}
	for _, b := range bl.Blocks {
		if b.Object != nil && b.Object.Text == "Вернётся объектом" {
			objects++
		}
		texts = append(texts, b.Text)
	}
	if objects != 1 || strings.Count(strings.Join(texts, "|"), "Вернётся объектом") != 1 || !st.State.CanUndo {
		t.Errorf("reopened: %d objects, texts %q, state %+v", objects, texts, st.State)
	}

	// History: the restoring step, then back to the file as downloaded.
	var h struct{ Done, Undone []historyEntry }
	call(m2.ID, "GET", "/history", nil, &h)
	if len(h.Done) != 1 || h.Done[0].Op != "add" || h.Done[0].Text != "Вернётся объектом" || h.Done[0].Page != 0 {
		t.Fatalf("history %+v", h)
	}
	call(m2.ID, "POST", "/goto", map[string]int{"n": 0}, nil)
	var bl0 struct{ Blocks []blockJSON }
	call(m2.ID, "GET", "/pages/0/blocks", nil, &bl0)
	baked := 0
	for _, b := range bl0.Blocks {
		if b.Object != nil {
			t.Error("object left at step 0")
		}
		if b.Text == "Вернётся объектом" {
			baked++
		}
	}
	h = struct{ Done, Undone []historyEntry }{}
	call(m2.ID, "GET", "/history", nil, &h)
	if baked != 1 || len(h.Done) != 0 || len(h.Undone) != 1 {
		t.Errorf("step 0: %d baked texts, history %+v", baked, h)
	}
}
