package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// A table running off its page moves the rest to a new page after it,
// under the repeated header row; undo brings everything back.
func TestTableSplit(t *testing.T) {
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
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "doc.pdf")
	fw.Write(pdf)
	mw.Close()
	res, _ := http.Post(srv.URL+"/api/docs", mw.FormDataContentType(), &body)
	var m Meta
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	call := func(method, path string, v, out any) int {
		t.Helper()
		b, _ := json.Marshal(v)
		r, _ := http.NewRequest(method, srv.URL+"/api/docs/"+m.ID+path, bytes.NewReader(b))
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if out != nil {
			json.Unmarshal(data, out)
		}
		if res.StatusCode != 200 {
			t.Logf("%s %s: %s", method, path, data)
		}
		return res.StatusCode
	}
	var doc struct{ Pages []pageInfo }
	call("GET", "", nil, &doc)
	page := doc.Pages[0].Num
	rows := []map[string]any{{"cells": []string{"№", "Позиция"}}}
	for i := 1; i < 40; i++ {
		rows = append(rows, map[string]any{"cells": []string{fmt.Sprint(i), fmt.Sprintf("Строка %d", i)}})
	}
	table := map[string]any{"kind": "table", "matrix": []float64{1, 0, 0, 1, 60, 400}, "cols": []float64{40, 200}, "rows": rows,
		"headerFill": "#eeeeee", "stroke": "#000000", "strokeWidth": 0.5, "cellPad": 3, "size": 10, "opacity": 1}
	var st editState
	call("POST", "/ops", map[string]any{"op": "add", "page": page, "object": table}, &st)
	if code := call("POST", "/ops", map[string]any{"op": "table.split", "page": page, "blocks": []string{st.Created}}, &st); code != 200 || !st.Structure {
		t.Fatalf("split: %d %+v", code, st)
	}
	call("GET", "", nil, &doc)
	if len(doc.Pages) != 3 || doc.Pages[1].Num >= 0 {
		t.Fatalf("pages %+v", doc.Pages)
	}
	tableRows := func(i int) [][]string {
		var bl struct{ Blocks []blockJSON }
		call("GET", fmt.Sprintf("/pages/%d/blocks", i), nil, &bl)
		for _, b := range bl.Blocks {
			if b.Object != nil && b.Object.Kind == "table" {
				var out [][]string
				for _, r := range b.Object.Rows {
					out = append(out, r.Cells)
				}
				return out
			}
		}
		return nil
	}
	first, rest := tableRows(0), tableRows(1)
	if len(first) < 2 || len(rest) < 2 || rest[0][0] != "№" || len(first)+len(rest)-1 != 40 || rest[1][0] != fmt.Sprint(len(first)) {
		t.Errorf("split rows: %d on the page, %d on the next (%v)", len(first), len(rest), rest[:min(2, len(rest))])
	}
	if code := call("POST", "/ops", map[string]any{"op": "table.split", "page": page, "blocks": []string{st.Created}}, nil); code == 200 {
		t.Error("splitting a table that fits")
	}
	call("POST", "/undo", nil, &st)
	call("GET", "", nil, &doc)
	if len(doc.Pages) != 2 || len(tableRows(0)) != 40 {
		t.Errorf("undo: %d pages, %d rows", len(doc.Pages), len(tableRows(0)))
	}
}
