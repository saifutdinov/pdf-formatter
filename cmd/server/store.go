package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"pdf-formatter/pdf/content"
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
	Password bool      `json:"password,omitempty"` // encrypted with a user password: Unlock first
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
	sec       *cos.Security // nil unless encrypted; Owner after the owner password
	log       editLog
	tails     map[int][]byte // incremental update per recent revision
	specRev   int
	specCache []edit.PageSpec
	srcMu     sync.Mutex
	sources   map[string]*cos.Document // uploaded PDFs for page.import
	imported  map[string][]blockJSON   // blocks of their pages (asset#index)
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
	errLocked   = errors.New("документ защищён паролем")
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
	locked := errors.Is(err, cos.ErrPassword)
	switch {
	case locked:
		// Stored as is; it opens once the password is entered (Unlock).
	case errors.Is(err, cos.ErrEncrypted):
		return nil, &userError{"Этот вид шифрования не поддерживается (например, по сертификату)"}
	case err != nil:
		return nil, &userError{"Это не PDF или файл повреждён: " + err.Error()}
	}
	if !locked && (d.Reconstructed || !bytes.HasPrefix(data, []byte("%PDF-"))) {
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
		Size:     len(data),
		Created:  now,
		Modified: now,
		Password: locked,
	}
	if !locked {
		m.Pages = len(d.Pages())
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
	if !locked {
		if err := s.restorePieces(dir, d); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.open[m.ID] = s.newDoc(*m, d)
		s.mu.Unlock()
	}
	return m, nil
}

// restorePieces turns the editor objects stored in a file (a document
// edited here and downloaded) back into objects: each one replaces the
// items that show it. Fonts are found again by PostScript name, images and
// SVG come back as uploads. Undoing these first steps shows the file as it
// was downloaded.
func (s *Store) restorePieces(dir string, d *cos.Document) error {
	pieces := edit.ReadPieces(d, content.NewFontCache())
	if len(pieces) == 0 {
		return nil
	}
	font := func(p edit.Piece, id string) string {
		if lf := s.lib.ByPSName(p.Fonts[id]); lf != nil {
			return lf.ID
		}
		return "" // the default font
	}
	var log editLog
	for _, p := range pieces {
		for name, data := range p.Assets {
			if validAsset(name) {
				os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
				if err := os.WriteFile(filepath.Join(dir, "assets", name), data, 0o644); err != nil {
					return err
				}
			}
		}
		for _, o := range p.Objects {
			members := p.Members[o.ID]
			o.ID = fmt.Sprintf("o%d", len(log.Ops)+1) // ids of this document's log
			o.Font = font(p, o.Font)
			o.Runs = slices.Clone(o.Runs)
			for k := range o.Runs {
				if o.Runs[k].Font != "" {
					o.Runs[k].Font = font(p, o.Runs[k].Font)
				}
			}
			op := edit.Op{Op: "add", Page: p.Page.Num, Object: &o}
			if len(members) > 0 {
				op.Replaces = []string{members[0]}
				op.Members = map[string][]string{members[0]: members}
			}
			log.Ops = append(log.Ops, op)
		}
	}
	log.Rev = len(log.Ops)
	b, _ := json.Marshal(log)
	return os.WriteFile(filepath.Join(dir, "ops.json"), b, 0o644)
}

func (s *Store) newDoc(m Meta, pdf *cos.Document) *Doc {
	d := &Doc{Meta: m, PDF: pdf, An: newAnalysis(pdf, s.lib), dir: s.docDir(m.ID), lib: s.lib, sec: pdf.Security}
	d.loadLog()
	return d
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
	if errors.Is(err, cos.ErrPassword) {
		return nil, errLocked
	}
	if err != nil {
		return nil, err
	}
	d := s.newDoc(m, pdf)
	s.open[id] = d
	return d, nil
}

// Unlock opens an encrypted document with its user or owner password. The
// password stays in memory only: after a restart it is asked again. The
// owner password lifts the restrictions of an already open document.
func (s *Store) Unlock(id, password string) error {
	if !validID(id) {
		return errNotFound
	}
	data, err := os.ReadFile(filepath.Join(s.docDir(id), "original.pdf"))
	if err != nil {
		return errNotFound
	}
	pdf, err := cos.OpenPassword(data, password)
	if errors.Is(err, cos.ErrPassword) {
		return &userError{"Неверный пароль"}
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.open[id]; ok {
		d.mu.Lock()
		if pdf.Security != nil && pdf.Security.Owner {
			d.sec = pdf.Security
		}
		d.mu.Unlock()
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.docDir(id), "meta.json"))
	if err != nil {
		return errNotFound
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Pages == 0 { // stored locked: now the pages can be counted
		m.Pages = len(pdf.Pages())
		s.saveMeta(&m)
	}
	s.open[id] = s.newDoc(m, pdf)
	return nil
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
