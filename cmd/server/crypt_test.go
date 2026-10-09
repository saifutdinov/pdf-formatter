package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"pdf-formatter/pdf/cos"
)

// Encrypted documents: a user password is asked before anything opens,
// changes stay encrypted, restrictions need the owner password, and the
// owner may drop the protection in the clean copy.
func TestEncryptedDocuments(t *testing.T) {
	dir := t.TempDir()
	lib := NewLibrary(dir, []string{"../../fonts"}, nil)
	store, err := NewStore(dir, lib)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(guard(routes(store, lib, http.Dir(dir))))
	defer srv.Close()
	req := func(method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		r, _ := http.NewRequest(method, srv.URL+path, rd)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	upload := func(file string) Meta {
		t.Helper()
		data, err := os.ReadFile("../../testdata/encrypted/" + file)
		if err != nil {
			t.Skip(err)
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", file)
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
	type docInfo struct {
		Pages    []pageInfo    `json:"pages"`
		Security *securityInfo `json:"security"`
	}
	info := func(id string) docInfo {
		t.Helper()
		code, b := req("GET", "/api/docs/"+id, nil)
		if code != 200 {
			t.Fatalf("doc %s: %d %s", id, code, b)
		}
		var d docInfo
		json.Unmarshal(b, &d)
		return d
	}
	rotate := func(id string, page int) int {
		code, _ := req("POST", "/api/docs/"+id+"/ops", map[string]any{"op": "page.rotate", "page": page, "rotate": 90})
		return code
	}

	m := upload("aes-256.pdf")
	if !m.Password || m.Pages != 0 {
		t.Fatalf("meta %+v", m)
	}
	if code, _ := req("GET", "/api/docs/"+m.ID, nil); code != http.StatusLocked {
		t.Fatalf("locked document: %d", code)
	}
	if code, b := req("POST", "/api/docs/"+m.ID+"/unlock", map[string]string{"password": "nope"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password: %d %s", code, b)
	}
	if code, b := req("POST", "/api/docs/"+m.ID+"/unlock", map[string]string{"password": "user"}); code != 200 {
		t.Fatalf("unlock: %d %s", code, b)
	}
	d := info(m.ID)
	if s := d.Security; s == nil || s.Method != "AES-256" || s.Owner || !s.Password || !s.Editable {
		t.Fatalf("security %+v", s)
	}
	if code := rotate(m.ID, d.Pages[0].Num); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	_, file := req("GET", "/api/docs/"+m.ID+"/file", nil)
	if _, err := cos.Open(file); !errors.Is(err, cos.ErrPassword) {
		t.Errorf("edited file opens without the password: %v", err)
	}
	if pdf, err := cos.OpenPassword(file, "user"); err != nil || pdf.Pages()[0].Rotate != 90 {
		t.Errorf("edited file: %v", err)
	}
	if code, _ := req("POST", "/api/docs/"+m.ID+"/ops", map[string]any{"op": "security", "decrypt": true}); code != http.StatusUnprocessableEntity {
		t.Errorf("unprotect without the owner password: %d", code)
	}
	req("POST", "/api/docs/"+m.ID+"/unlock", map[string]string{"password": "owner"})
	if code, b := req("POST", "/api/docs/"+m.ID+"/ops", map[string]any{"op": "security", "decrypt": true}); code != 200 {
		t.Fatalf("unprotect: %d %s", code, b)
	}
	_, clean := req("GET", "/api/docs/"+m.ID+"/file?download=clean", nil)
	if pdf, err := cos.Open(clean); err != nil || pdf.Security != nil || pdf.Pages()[0].Rotate != 90 {
		t.Errorf("clean copy without protection: %v", err)
	}

	// Restrictions without a user password: opens, edits need the owner.
	m = upload("owner-only.pdf")
	d = info(m.ID)
	if m.Password || d.Security == nil || d.Security.Editable {
		t.Fatalf("owner-only: %+v %+v", m, d.Security)
	}
	if code := rotate(m.ID, d.Pages[0].Num); code != http.StatusUnprocessableEntity {
		t.Errorf("edit of a protected document: %d", code)
	}
	req("POST", "/api/docs/"+m.ID+"/unlock", map[string]string{"password": "owner"})
	if code := rotate(m.ID, d.Pages[0].Num); code != 200 {
		t.Errorf("edit with the owner password: %d", code)
	}
}
