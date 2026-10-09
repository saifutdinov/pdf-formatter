package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Form fields over HTTP: listed with their widgets, filled, checked,
// flattened.
func TestFormFields(t *testing.T) {
	pdf, err := os.ReadFile("../../testdata/forms/form.pdf")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	lib := NewLibrary(dir, []string{"../../fonts"}, nil)
	store, _ := NewStore(dir, lib)
	srv := httptest.NewServer(guard(routes(store, lib, http.Dir(dir))))
	defer srv.Close()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "form.pdf")
	fw.Write(pdf)
	mw.Close()
	res, _ := http.Post(srv.URL+"/api/docs", mw.FormDataContentType(), &body)
	var m Meta
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	req := func(method, path string, v any) (int, []byte) {
		b, _ := json.Marshal(v)
		r, _ := http.NewRequest(method, srv.URL+"/api/docs/"+m.ID+path, bytes.NewReader(b))
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, out
	}
	type list struct {
		Fields []struct {
			Name, Type, Value string
			Widgets           []widgetInfo
		}
		Flattened bool
	}
	fields := func() list {
		_, b := req("GET", "/fields", nil)
		var l list
		json.Unmarshal(b, &l)
		return l
	}
	l := fields()
	if len(l.Fields) != 5 || l.Fields[0].Name != "name" || len(l.Fields[0].Widgets) != 1 || l.Fields[0].Widgets[0].Page != 0 {
		t.Fatalf("fields %+v", l)
	}
	if code, b := req("POST", "/ops", map[string]any{"op": "field", "page": 0, "field": "name", "value": "Иван"}); code != 200 {
		t.Fatalf("fill: %d %s", code, b)
	}
	if code, _ := req("POST", "/ops", map[string]any{"op": "field", "page": 0, "field": "gender", "value": "x"}); code != http.StatusUnprocessableEntity {
		t.Errorf("bad state accepted: %d", code)
	}
	if l := fields(); l.Fields[0].Value != "Иван" {
		t.Errorf("value %+v", l.Fields[0])
	}
	if code, b := req("POST", "/ops", map[string]any{"op": "flatten", "page": 0}); code != 200 {
		t.Fatalf("flatten: %d %s", code, b)
	}
	if !fields().Flattened {
		t.Error("not flattened")
	}
	if code, _ := req("POST", "/ops", map[string]any{"op": "field", "page": 0, "field": "name", "value": "Пётр"}); code != http.StatusUnprocessableEntity {
		t.Errorf("fill after flatten: %d", code)
	}
}
