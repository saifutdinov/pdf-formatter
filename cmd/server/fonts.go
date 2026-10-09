package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"pdf-formatter/pdf/font"
)

// LibFont is one face in the font library. Metadata only: the font program
// is loaded on demand.
type LibFont struct {
	ID       string `json:"id"`
	PSName   string `json:"psName"`
	Family   string `json:"family"`
	Style    string `json:"style"`
	Full     string `json:"full"`
	Bold     bool   `json:"bold"`
	Italic   bool   `json:"italic"`
	Cyrillic bool   `json:"cyrillic"`
	Format   string `json:"format"` // ttf | otf
	Source   string `json:"source"` // builtin | licensed | system | upload
	path     string
	index    int // face index within a collection
}

type Settings struct {
	DefaultFont string `json:"defaultFont"`
}

// Library indexes builtin, system (mounted read-only) and uploaded fonts.
type Library struct {
	uploadDir    string
	settingsPath string
	dirs         [][2]string // path, source

	mu       sync.Mutex
	fonts    []*LibFont
	byID     map[string]*LibFont
	faces    map[string]*font.Face
	settings Settings
	ready    bool
	onChange []func()
	matches  map[string]*LibFont // Match memo, reset on change
}

func NewLibrary(dataDir string, builtin, system []string) *Library {
	l := &Library{
		uploadDir:    filepath.Join(dataDir, "fonts"),
		settingsPath: filepath.Join(dataDir, "settings.json"),
		byID:         map[string]*LibFont{},
		faces:        map[string]*font.Face{},
	}
	os.MkdirAll(l.uploadDir, 0o755)
	for _, d := range builtin {
		l.dirs = append(l.dirs, [2]string{d, "builtin"})
	}
	for _, d := range system {
		l.dirs = append(l.dirs, [2]string{d, "system"})
	}
	l.dirs = append(l.dirs, [2]string{l.uploadDir, "upload"})
	if b, err := os.ReadFile(l.settingsPath); err == nil {
		json.Unmarshal(b, &l.settings)
	}
	go l.scan()
	return l
}

func fontID(path, ps string) string {
	h := sha1.Sum([]byte(path + "#" + ps))
	return hex.EncodeToString(h[:6])
}

func isFontFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc", ".otc":
		return true
	}
	return false
}

// indexFile parses a font file and returns its faces; ids derive from
// idPath and the PostScript name.
func indexFile(path, idPath, source string) []*LibFont {
	st, err := os.Stat(path)
	if err != nil || st.Size() > 64<<20 { // emoji and CJK giants are useless here
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	faces, err := font.ParseFaces(data)
	if err != nil {
		return nil
	}
	var out []*LibFont
	for i, f := range faces {
		if f.PSName == "" || strings.HasPrefix(f.PSName, ".") || strings.HasPrefix(f.Family, ".") { // hidden system UI fonts
			continue
		}
		format := "ttf"
		if f.CFF {
			format = "otf"
		}
		out = append(out, &LibFont{
			ID: fontID(idPath, f.PSName), PSName: f.PSName, Family: f.Family, Style: f.Style, Full: f.Full,
			Bold: f.Bold, Italic: f.Italic, Cyrillic: f.Has("АЖЯажя"), Format: format, Source: source, path: path, index: i,
		})
	}
	return out
}

func (l *Library) scan() {
	var all []*LibFont
	for _, d := range l.dirs {
		filepath.WalkDir(d[0], func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !isFontFile(p) {
				return nil
			}
			source, idPath := d[1], p
			if source == "builtin" {
				// Builtin fonts sit in free/ or licensed/ (commercial, for the
				// license holder only); ids ignore the subfolder, so documents
				// keep their fonts when the folders change.
				idPath = filepath.Join(d[0], filepath.Base(p))
				if slices.Contains(strings.Split(filepath.ToSlash(p), "/"), "licensed") {
					source = "licensed"
				}
			}
			all = append(all, indexFile(p, idPath, source)...)
			return nil
		})
	}
	l.mu.Lock()
	seen := map[string]bool{}
	for _, f := range all {
		// Same face in several places (builtin PT Sans is also a macOS font):
		// the first source wins, scan order is builtin, system, upload.
		if !seen[f.PSName] {
			seen[f.PSName] = true
			l.byID[f.ID] = f
			l.fonts = append(l.fonts, f)
		}
	}
	l.sortLocked()
	l.ready = true
	l.mu.Unlock()
	log.Printf("font library: %d faces", len(all))
	l.changed()
}

func (l *Library) sortLocked() {
	slices.SortFunc(l.fonts, func(a, b *LibFont) int {
		if c := strings.Compare(strings.ToLower(a.Family), strings.ToLower(b.Family)); c != 0 {
			return c
		}
		return strings.Compare(a.Style, b.Style)
	})
}

func (l *Library) changed() {
	l.mu.Lock()
	l.matches = nil
	fns := slices.Clone(l.onChange)
	l.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// OnChange registers a callback for library changes (uploads, scan done).
func (l *Library) OnChange(fn func()) {
	l.mu.Lock()
	l.onChange = append(l.onChange, fn)
	l.mu.Unlock()
}

func (l *Library) List() ([]LibFont, string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LibFont, len(l.fonts))
	for i, f := range l.fonts {
		out[i] = *f
	}
	return out, l.defaultIDLocked(), l.ready
}

func (l *Library) defaultIDLocked() string {
	if _, ok := l.byID[l.settings.DefaultFont]; ok {
		return l.settings.DefaultFont
	}
	for _, f := range l.fonts {
		if f.PSName == "PTSans-Regular" {
			return f.ID
		}
	}
	for _, f := range l.fonts {
		if f.Cyrillic && !f.Bold && !f.Italic {
			return f.ID
		}
	}
	return ""
}

// Default returns the default font, or nil if the library is empty.
func (l *Library) Default() *LibFont {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byID[l.defaultIDLocked()]
}

// Variant returns the face of the same family closest to bold and italic
// (the font itself when the family has nothing better).
func (l *Library) Variant(id string, bold, italic bool) *LibFont {
	l.mu.Lock()
	defer l.mu.Unlock()
	base := l.byID[id]
	if base == nil {
		return nil
	}
	best, score := base, -1
	for _, f := range l.fonts {
		if f.Family != base.Family {
			continue
		}
		s := 0
		if f.Bold == bold {
			s += 2
		}
		if f.Italic == italic {
			s++
		}
		if s > score || s == score && f == base {
			best, score = f, s
		}
	}
	return best
}

func (l *Library) SetDefault(id string) error {
	l.mu.Lock()
	if _, ok := l.byID[id]; !ok {
		l.mu.Unlock()
		return errNotFound
	}
	l.settings.DefaultFont = id
	b, _ := json.MarshalIndent(l.settings, "", "  ")
	l.mu.Unlock()
	return os.WriteFile(l.settingsPath, b, 0o644)
}

func (l *Library) Get(id string) *LibFont {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byID[id]
}

// Face loads the font program of a library font.
func (l *Library) Face(id string) (*font.Face, error) {
	l.mu.Lock()
	f, ok := l.byID[id]
	face := l.faces[id]
	l.mu.Unlock()
	if !ok {
		return nil, errNotFound
	}
	if face != nil {
		return face, nil
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	faces, err := font.ParseFaces(data)
	if err != nil || f.index >= len(faces) {
		return nil, &userError{"Не удалось прочитать шрифт " + f.Full}
	}
	face = faces[f.index]
	l.mu.Lock()
	l.faces[id] = face
	l.mu.Unlock()
	return face, nil
}

// Upload stores a font file and indexes its faces.
func (l *Library) Upload(name string, r io.Reader) ([]LibFont, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	if !isFontFile(name) {
		return nil, &userError{"Поддерживаются файлы .ttf, .otf и .ttc"}
	}
	if _, err := font.ParseFaces(data); err != nil {
		return nil, &userError{"Файл не похож на шрифт: " + err.Error()}
	}
	h := sha1.Sum(data)
	path := filepath.Join(l.uploadDir, hex.EncodeToString(h[:8])+strings.ToLower(filepath.Ext(name)))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	added := indexFile(path, path, "upload")
	if len(added) == 0 {
		os.Remove(path)
		return nil, &userError{"В файле нет пригодных начертаний"}
	}
	var out []LibFont
	l.mu.Lock()
	for _, f := range added {
		// An uploaded face replaces a known one with the same name.
		l.fonts = slices.DeleteFunc(l.fonts, func(x *LibFont) bool {
			if x.PSName == f.PSName {
				delete(l.byID, x.ID)
				return true
			}
			return false
		})
		l.byID[f.ID] = f
		l.fonts = append(l.fonts, f)
		out = append(out, *f)
	}
	l.sortLocked()
	l.mu.Unlock()
	l.changed()
	return out, nil
}

func (l *Library) Delete(id string) error {
	l.mu.Lock()
	f, ok := l.byID[id]
	if !ok || f.Source != "upload" {
		l.mu.Unlock()
		return &userError{"Удалять можно только загруженные шрифты"}
	}
	path := f.path
	var keep []*LibFont
	for _, x := range l.fonts {
		if x.path == path {
			delete(l.byID, x.ID)
			delete(l.faces, x.ID)
		} else {
			keep = append(keep, x)
		}
	}
	l.fonts = keep
	l.mu.Unlock()
	os.Remove(path)
	l.changed()
	return nil
}

// normFamily reduces a font name to comparable letters: "Times New Roman"
// and "TimesNewRomanPSMT" both become "timesnewroman".
func normFamily(s string) string {
	s = strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s))
	for _, suf := range []string{"psmt", "mt", "ps", "std", "pro"} {
		if strings.HasSuffix(s, suf) && len(s) > len(suf)+2 {
			s = strings.TrimSuffix(s, suf)
		}
	}
	return s
}

// Match finds a library face for a document font name like "Arial-BoldMT"
// or "TimesNewRomanPS-BoldItalicMT".
func (l *Library) Match(name string, bold, italic bool) *LibFont {
	if name == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := fmt.Sprint(name, bold, italic)
	if f, ok := l.matches[key]; ok {
		return f
	}
	f := l.matchLocked(name, bold, italic)
	if l.matches == nil {
		l.matches = map[string]*LibFont{}
	}
	l.matches[key] = f
	return f
}

func (l *Library) matchLocked(name string, bold, italic bool) *LibFont {
	alt := strings.ReplaceAll(name, ",", "-")
	for _, f := range l.fonts {
		if strings.EqualFold(f.PSName, name) || strings.EqualFold(f.PSName, alt) {
			return f
		}
	}
	fam := name
	if i := strings.IndexAny(name, "-,"); i > 0 {
		fam = name[:i]
	}
	want := normFamily(fam)
	var best *LibFont
	score := -1
	for _, f := range l.fonts {
		psFam := f.PSName
		if i := strings.IndexByte(psFam, '-'); i > 0 {
			psFam = psFam[:i]
		}
		if normFamily(f.Family) != want && normFamily(psFam) != want {
			continue
		}
		s := 0
		if f.Bold == bold {
			s += 2
		}
		if f.Italic == italic {
			s++
		}
		if s > score {
			best, score = f, s
		}
	}
	return best
}
