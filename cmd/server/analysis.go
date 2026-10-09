package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/edit"
	"pdf-formatter/pdf/font"
	"pdf-formatter/pdf/layout"
)

type blockJSON struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	Quad       [8]float64   `json:"quad"`
	Angle      float64      `json:"angle,omitempty"`
	Text       string       `json:"text,omitempty"`
	Flow       string       `json:"flow,omitempty"` // text for editing, when it differs (see layout.Block)
	Font       string       `json:"font,omitempty"`
	FontRef    int          `json:"fontRef,omitempty"`
	Size       float64      `json:"size,omitempty"`
	Color      string       `json:"color,omitempty"`
	Bold       bool         `json:"bold,omitempty"`
	Italic     bool         `json:"italic,omitempty"`
	Invisible  bool         `json:"invisible,omitempty"`
	Artifact   bool         `json:"artifact,omitempty"`
	InForm     bool         `json:"inForm,omitempty"`
	LibFont    string       `json:"libFont,omitempty"`    // library face used when the text is edited
	Object     *edit.Object `json:"object,omitempty"`     // editor objects only
	RowHeights []float64    `json:"rowHeights,omitempty"` // tables: computed row heights
	Items      []string     `json:"-"`                    // original blocks: layout.ItemID of the items
	Runs       []edit.Run   `json:"runs,omitempty"`       // text in several styles, with library fonts
	styles     []textStyle  // the same as found in the file (Blocks matches the fonts)
}

// textStyle is a part of a block's text in one style of the file.
type textStyle struct {
	Text, Font, Color string
	Bold, Italic      bool
	Size              float64
}

func (s textStyle) same(o textStyle) bool {
	return s.Font == o.Font && s.Bold == o.Bold && s.Italic == o.Italic && s.Size == o.Size && s.Color == o.Color
}

type fontJSON struct {
	Refs     []int    `json:"refs"` // font objects with this name and type (merged files repeat them)
	Name     string   `json:"name"`
	BaseFont string   `json:"baseFont"`
	Subtype  string   `json:"subtype"`
	Status   string   `json:"status"`
	HasText  bool     `json:"hasText"`
	Pages    []int    `json:"pages"`
	Chars    string   `json:"chars"` // distinct characters used, sorted
	Problem  string   `json:"problem,omitempty"`
	Match    *LibFont `json:"match,omitempty"` // library face used for editing
	Blocking bool     `json:"blocking"`        // display or editing really suffers
}

type event struct {
	Type string
	Data any
}

func hexColor(c [3]float64) string {
	b := func(v float64) int { return int(max(0, min(1, v))*255 + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", b(c[0]), b(c[1]), b(c[2]))
}

// Analysis segments every page in the background and aggregates fonts.
// Results derive from the immutable original, so they never go stale.
type Analysis struct {
	doc   *cos.Document
	lib   *Library
	fonts *content.FontCache

	mu        sync.Mutex
	pages     [][]blockJSON
	done      int
	finished  bool
	fontPages map[cos.Ref]map[int]bool
	fontChars map[cos.Ref]map[rune]bool
	fontObj   map[cos.Ref]*font.Font
	subs      map[chan event]bool
	lastProg  time.Time
}

func newAnalysis(doc *cos.Document, lib *Library) *Analysis {
	a := &Analysis{
		doc:       doc,
		lib:       lib,
		fonts:     content.NewFontCache(),
		pages:     make([][]blockJSON, len(doc.Pages())),
		fontPages: map[cos.Ref]map[int]bool{},
		fontChars: map[cos.Ref]map[rune]bool{},
		fontObj:   map[cos.Ref]*font.Font{},
		subs:      map[chan event]bool{},
	}
	go a.run()
	return a
}

func (a *Analysis) run() {
	n := len(a.pages)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() {
			for i := range jobs {
				a.Page(i)
			}
		})
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	a.mu.Lock()
	a.finished = true
	a.emit(event{"progress", a.progressLocked()})
	a.mu.Unlock()
}

// Page returns the blocks of page i, analysing it now if needed.
func (a *Analysis) Page(i int) []blockJSON {
	a.mu.Lock()
	if b := a.pages[i]; b != nil {
		a.mu.Unlock()
		return b
	}
	a.mu.Unlock()

	pg := layout.Analyze(a.doc, a.doc.Pages()[i], a.fonts)
	blocks := pageBlocks(pg)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pages[i] != nil { // computed concurrently
		return a.pages[i]
	}
	a.pages[i] = blocks
	a.done++
	for ref, u := range pg.FontUse {
		isNew := a.fontPages[ref] == nil
		if isNew {
			a.fontPages[ref] = map[int]bool{}
			a.fontChars[ref] = map[rune]bool{}
			a.fontObj[ref] = u.Font
		}
		a.fontPages[ref][i] = true
		maps.Copy(a.fontChars[ref], u.Chars)
		if f := a.fontInfoLocked(ref); isNew && f.Blocking {
			a.emit(event{"font", f})
		}
	}
	if time.Since(a.lastProg) > 150*time.Millisecond {
		a.lastProg = time.Now()
		a.emit(event{"progress", a.progressLocked()})
	}
	return blocks
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (a *Analysis) progressLocked() map[string]any {
	return map[string]any{"done": a.done, "total": len(a.pages), "finished": a.finished}
}

func (a *Analysis) fontInfoLocked(ref cos.Ref) fontJSON {
	f := a.fontObj[ref]
	chars := slices.Sorted(maps.Keys(a.fontChars[ref]))
	pages := slices.Sorted(maps.Keys(a.fontPages[ref]))
	fj := fontJSON{Refs: []int{ref.Num}, Name: f.Name, BaseFont: f.BaseFont, Subtype: f.Subtype, Status: f.Status,
		HasText: f.HasText(), Pages: pages, Chars: string(chars)}
	if fj.Name == "" {
		fj.Name = fmt.Sprintf("Без имени (%s)", f.Subtype)
	}
	fj.Match = a.lib.Match(f.Name, f.Bold, f.Italic)
	have := ""
	if fj.Match != nil {
		have = fmt.Sprintf(" В библиотеке есть «%s» — он будет использован при правке.", fj.Match.Full)
	}
	switch {
	case f.Subtype == "Type3":
		fj.Problem = "Шрифт Type3: блоки можно двигать и красить, но не править текст."
		fj.Blocking = true
	case !f.HasText():
		fj.Problem = "Нет таблицы ToUnicode: текст нельзя надёжно извлечь и править."
		fj.Blocking = true
	case f.Status == font.Missing && fj.Match == nil:
		fj.Problem = "Шрифт не встроен и не найден в системе: вид может отличаться, при правке подставится шрифт по умолчанию. Загрузите этот шрифт."
		fj.Blocking = true
	case f.Status == font.Missing:
		fj.Problem = "Шрифт не встроен." + have
	case f.Status == font.Subset && fj.Match == nil:
		fj.Problem = "Встроено подмножество, полного шрифта в системе нет: новые символы наберутся шрифтом по умолчанию."
	case f.Status == font.Subset:
		fj.Problem = "Встроено подмножество." + have
	case f.Status == font.Standard && strings.ContainsFunc(string(chars), func(r rune) bool { return r > 0x24f }):
		fj.Problem = "Стандартный шрифт без нужных символов."
	}
	return fj
}

// Fonts returns the font report collected so far, one entry per distinct
// font (same name, type and status).
func (a *Analysis) Fonts() []fontJSON {
	a.mu.Lock()
	defer a.mu.Unlock()
	groups := map[string]*fontJSON{}
	var keys []string
	for ref, f := range a.fontObj {
		key := f.Name + "|" + f.Subtype + "|" + f.Status
		g := groups[key]
		if g == nil {
			fj := a.fontInfoLocked(ref)
			groups[key] = &fj
			keys = append(keys, key)
			continue
		}
		g.Refs = append(g.Refs, ref.Num)
		for p := range a.fontPages[ref] {
			g.Pages = append(g.Pages, p)
		}
		g.Chars += string(slices.Collect(maps.Keys(a.fontChars[ref])))
	}
	out := []fontJSON{}
	for _, k := range keys {
		g := groups[k]
		slices.Sort(g.Refs)
		slices.Sort(g.Pages)
		g.Pages = slices.Compact(g.Pages)
		chars := []rune(g.Chars)
		slices.Sort(chars)
		g.Chars = string(slices.Compact(chars))
		out = append(out, *g)
	}
	slices.SortFunc(out, func(x, y fontJSON) int { return strings.Compare(x.Name, y.Name) })
	return out
}

func (a *Analysis) Progress() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.progressLocked()
}

func (a *Analysis) emit(e event) {
	for ch := range a.subs {
		select {
		case ch <- e:
		default: // slow client: drop, it will catch up on the next progress
		}
	}
}

func (a *Analysis) subscribe() chan event {
	ch := make(chan event, 256)
	a.mu.Lock()
	a.subs[ch] = true
	ch <- event{"progress", a.progressLocked()}
	a.mu.Unlock()
	return ch
}

func (a *Analysis) unsubscribe(ch chan event) {
	a.mu.Lock()
	delete(a.subs, ch)
	a.mu.Unlock()
}

func sseEncode(e event) []byte {
	data, _ := json.Marshal(e.Data)
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", e.Type, data)
}

// pageBlocks turns the blocks of an analysed page into their JSON form.
func pageBlocks(pg *layout.Page) []blockJSON {
	blocks := make([]blockJSON, 0, len(pg.Blocks))
	for _, b := range pg.Blocks {
		for k := range b.Quad {
			b.Quad[k] = round2(b.Quad[k])
		}
		bj := blockJSON{ID: b.ID, Kind: b.Kind, Quad: b.Quad, Angle: round2(b.Angle), Text: b.Text, Size: round2(b.Size),
			Invisible: b.Invisible, Artifact: b.Artifact, InForm: b.InForm}
		if b.Flow != b.Text {
			bj.Flow = b.Flow
		}
		for _, k := range b.Items {
			bj.Items = append(bj.Items, layout.ItemID(pg.Items[k]))
		}
		if b.Kind == "text" || b.Kind == "path" {
			bj.Color = hexColor(b.Color)
		}
		if b.Font != nil {
			bj.Font, bj.Bold, bj.Italic = b.Font.Name, b.Font.Bold, b.Font.Italic
			bj.FontRef = pg.Items[b.Items[0]].State.FontRef.Num
		}
		for _, r := range b.Runs {
			it := pg.Items[r.Item]
			s := textStyle{Text: r.Text, Size: round2(it.Size), Color: hexColor(it.State.Fill)}
			if f := it.State.Font; f != nil {
				s.Font, s.Bold, s.Italic = f.Name, f.Bold, f.Italic
			}
			if n := len(bj.styles); n > 0 && bj.styles[n-1].same(s) {
				bj.styles[n-1].Text += s.Text
			} else {
				bj.styles = append(bj.styles, s)
			}
		}
		if len(bj.styles) == 1 { // one style: the block's own fields say it all
			bj.styles = nil
		}
		blocks = append(blocks, bj)
	}
	return blocks
}
