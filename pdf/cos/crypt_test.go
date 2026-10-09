package cos

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func pageContent(t *testing.T, d *Document) []byte {
	t.Helper()
	p := d.Pages()[0]
	s := d.Stream(p.Dict["Contents"])
	if s == nil {
		t.Fatal("no page content")
	}
	b, err := d.Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Every revision of the standard security handler opens with the user or
// the owner password, and reads back the same content as the plain file
// (testdata/make-encrypted.sh).
func TestDecrypt(t *testing.T) {
	plain, err := os.ReadFile("../../testdata/corpus/quartz.pdf")
	if err != nil {
		t.Skip(err)
	}
	pd, _ := Open(plain)
	want := pageContent(t, pd)
	for _, c := range []struct{ file, user, owner, method string }{
		{"rc4-40", "user", "owner", "RC4-40"},
		{"rc4-128", "user", "owner", "RC4-128"},
		{"aes-128", "user", "owner", "AES-128"},
		{"aes-256", "user", "owner", "AES-256"},
		{"aes-256-r5", "user", "owner", "AES-256"},
		{"aes-256-utf8", "пароль", "владелец", "AES-256"},
		{"owner-only", "", "owner", "AES-256"},
		{"owner-only-rc4", "", "owner", "RC4-128"},
	} {
		data, err := os.ReadFile("../../testdata/encrypted/" + c.file + ".pdf")
		if err != nil {
			t.Fatal(err)
		}
		if c.user != "" {
			if _, err := Open(data); !errors.Is(err, ErrPassword) {
				t.Errorf("%s: no password: %v, want ErrPassword", c.file, err)
			}
		}
		if _, err := OpenPassword(data, "wrong"); !errors.Is(err, ErrPassword) {
			t.Errorf("%s: wrong password: %v", c.file, err)
		}
		for _, pw := range []string{c.user, c.owner} {
			d, err := OpenPassword(data, pw)
			if err != nil {
				t.Errorf("%s/%q: %v", c.file, pw, err)
				continue
			}
			sec := d.Security
			if sec.Method != c.method || sec.Owner != (pw == c.owner) || sec.User != (c.user != "") {
				t.Errorf("%s/%q: security %+v", c.file, pw, sec)
			}
			if got := pageContent(t, d); !bytes.Equal(got, want) {
				t.Errorf("%s/%q: content differs:\n%.80q", c.file, pw, got)
			}
		}
		if c.user == "" {
			d, _ := Open(data)
			if d.Security.Allows(PermModify) {
				t.Errorf("%s: changes should need the owner password", c.file)
			}
		}
	}
}

// Changes to an encrypted file are encrypted the same way: an incremental
// update and a clean copy open with the same password; a clean copy can
// also drop the encryption.
func TestEncryptOnWrite(t *testing.T) {
	for _, file := range []string{"rc4-40", "rc4-128", "aes-128", "aes-256"} {
		data, err := os.ReadFile("../../testdata/encrypted/" + file + ".pdf")
		if err != nil {
			t.Skip(err)
		}
		d, err := OpenPassword(data, "user")
		if err != nil {
			t.Fatal(err)
		}
		u := NewUpdate(d)
		info := u.Add(Dict{"Title": TextString("Зашифрованный (тест)")})
		u.Trailer["Info"] = info
		note := u.Add(&Stream{Dict: Dict{}, Data: []byte("BT /F1 12 Tf (secret) Tj ET")})
		cat := Copy(d.Dict(d.Trailer["Root"])).(Dict)
		cat["Note"] = note
		u.Set(d.Trailer["Root"].(Ref), cat)

		var inc, clean, open bytes.Buffer
		u.WriteIncremental(&inc)
		u.WriteClean(&clean)
		u.Decrypt = true
		u.WriteClean(&open)
		if bytes.Contains(inc.Bytes(), []byte("secret")) || bytes.Contains(clean.Bytes(), []byte("secret")) {
			t.Errorf("%s: plain text in an encrypted file", file)
		}
		for name, b := range map[string][]byte{"incremental": inc.Bytes(), "clean": clean.Bytes(), "decrypted": open.Bytes()} {
			pw := "user"
			if name == "decrypted" {
				pw = ""
			} else if _, err := Open(b); !errors.Is(err, ErrPassword) {
				t.Errorf("%s/%s: opens without a password", file, name)
			}
			d2, err := OpenPassword(b, pw)
			if err != nil {
				t.Errorf("%s/%s: %v", file, name, err)
				continue
			}
			if name == "decrypted" && d2.Security != nil {
				t.Errorf("%s: still encrypted", file)
			}
			title, _ := d2.Resolve(d2.Dict(d2.Trailer["Info"])["Title"]).(String)
			body, _ := d2.Decode(d2.Stream(d2.Dict(d2.Trailer["Root"])["Note"]))
			if Text(title) != "Зашифрованный (тест)" || string(body) != "BT /F1 12 Tf (secret) Tj ET" {
				t.Errorf("%s/%s: read back %q, %q", file, name, Text(title), body)
			}
			validate(t, file+"-"+name+".pdf", b, pw)
		}
	}
}
