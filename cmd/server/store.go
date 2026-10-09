package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/edit"
)

// Meta describes a stored document.
type Meta struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Pages    int       `json:"pages"`
	Size     int       `json:"size"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
}

// Doc is an opened document. The original is immutable; edits live in the
// operation log (later phases).
type Doc struct {
	Meta
	PDF *cos.Document
	An  *Analysis
	dir string
	lib *Library

	mu        sync.Mutex
	log       editLog
	tails     map[int][]byte // incremental update per recent revision
	specRev   int
	specCache []edit.PageSpec
	srcMu     sync.Mutex
	sources   map[string]*cos.Document // uploaded PDFs for page.import
}

// Store keeps documents under dir/docs/<id>/: original.pdf + meta.json.
type Store struct {
	dir  string
	lib  *Library
	mu   sync.Mutex
	open map[string]*Doc
}

func NewStore(dir string, lib *Library) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, lib: lib, open: map[string]*Doc{}}
	// Font reports depend on the library: tell open documents to refresh.
	lib.OnChange(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, d := range s.open {
			d.An.mu.Lock()
			d.An.emit(event{"fonts", nil})
			d.An.mu.Unlock()
		}
	})
	return s, nil
}

var (
	errNotFound = errors.New("документ не найден")
	errGone     = errors.New("эта версия документа уже заменена")
)

// userError is shown to the user as is.
type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

func (s *Store) docDir(id string) string { return filepath.Join(s.dir, "docs", id) }

func validID(id string) bool {
	_, err := hex.DecodeString(id)
	return err == nil && len(id) == 16
}

// Import validates and stores an uploaded file. Broken files (rebuilt xref,
// junk before the header) are normalised once with a clean rewrite, so that
// incremental updates later sit on a sound base.
func (s *Store) Import(name string, r io.Reader) (*Meta, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	d, err := cos.Open(data)
	switch {
	case errors.Is(err, cos.ErrEncrypted):
		return nil, &userError{"Документ зашифрован — такие файлы не поддерживаются"}
	case err != nil:
		return nil, &userError{"Это не PDF или файл повреждён: " + err.Error()}
	}
	if d.Reconstructed || !bytes.HasPrefix(data, []byte("%PDF-")) {
		var buf bytes.Buffer
		if err := cos.NewUpdate(d).WriteClean(&buf); err != nil {
			return nil, err
		}
		data = buf.Bytes()
		if d, err = cos.Open(data); err != nil {
			return nil, err
		}
	}
	idb := make([]byte, 8)
	rand.Read(idb)
	now := time.Now().UTC()
	m := &Meta{
		ID:       hex.EncodeToString(idb),
		Name:     strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)),
		Pages:    len(d.Pages()),
		Size:     len(data),
		Created:  now,
		Modified: now,
	}
	dir := s.docDir(m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "original.pdf"), data, 0o644); err != nil {
		return nil, err
	}
	if err := s.saveMeta(m); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.open[m.ID] = &Doc{Meta: *m, PDF: d, An: newAnalysis(d, s.lib), dir: dir, lib: s.lib}
	s.mu.Unlock()
	return m, nil
}

func (s *Store) saveMeta(m *Meta) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := filepath.Join(s.docDir(m.ID), "meta.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.docDir(m.ID), "meta.json"))
}

func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "docs"))
	if err != nil {
		return nil, err
	}
	out := []Meta{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(s.docDir(e.Name()), "meta.json"))
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Meta) int { return b.Modified.Compare(a.Modified) })
	return out, nil
}

// Get opens a document, keeping it in memory for later requests.
// ponytail: every opened document stays in memory; add LRU eviction when
// many large documents are opened in one run.
func (s *Store) Get(id string) (*Doc, error) {
	if !validID(id) {
		return nil, errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.open[id]; ok {
		return d, nil
	}
	b, err := os.ReadFile(filepath.Join(s.docDir(id), "meta.json"))
	if err != nil {
		return nil, errNotFound
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.docDir(id), "original.pdf"))
	if err != nil {
		return nil, err
	}
	pdf, err := cos.Open(data)
	if err != nil {
		return nil, err
	}
	d := &Doc{Meta: m, PDF: pdf, An: newAnalysis(pdf, s.lib), dir: s.docDir(id), lib: s.lib}
	d.loadLog()
	s.open[id] = d
	return d, nil
}

func (s *Store) Delete(id string) error {
	if !validID(id) {
		return errNotFound
	}
	s.mu.Lock()
	delete(s.open, id)
	s.mu.Unlock()
	return os.RemoveAll(s.docDir(id))
}
