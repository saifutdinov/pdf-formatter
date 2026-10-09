package main

import (
	"bytes"
	"cmp"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
	"pdf-formatter/pdf/edit"
	"pdf-formatter/pdf/font"
	"pdf-formatter/pdf/form"
	"pdf-formatter/pdf/meta"
	"pdf-formatter/pdf/nav"
	"pdf-formatter/pdf/svg"
)

// editLog is the persisted operation log of a document.
type editLog struct {
	Rev  int       `json:"rev"` // bumps on every change, also undo/redo
	Ops  []edit.Op `json:"ops"`
	Redo []edit.Op `json:"redo"`
}

type editState struct {
	Rev       int    `json:"rev"`
	CanUndo   bool   `json:"canUndo"`
	CanRedo   bool   `json:"canRedo"`
	Pages     []int  `json:"pages,omitempty"`     // page indices to re-render
	Created   string `json:"created,omitempty"`   // id of an added object
	Structure bool   `json:"structure,omitempty"` // the page list changed: reload it
}

var docOps = map[string]bool{"meta": true, "sanitize": true, "outline": true, "labels": true, "hf": true, "security": true, "field": true, "flatten": true,
	"page.insert": true, "page.delete": true, "page.move": true, "page.rotate": true, "page.margins": true, "toc": true, "page.import": true}

func isPageOp(op edit.Op) bool {
	return strings.HasPrefix(op.Op, "page.") || op.Op == "toc" || op.Op == "table.split"
}

// specs returns the current page list, cached per revision.
func (d *Doc) specs() []edit.PageSpec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.specsLocked()
}

func (d *Doc) specsLocked() []edit.PageSpec {
	if d.specCache == nil || d.specRev != d.log.Rev {
		d.specCache, _ = edit.Pages(d.PDF, d.log.Ops, d.env().Sources())
		d.specRev = d.log.Rev
	}
	return d.specCache
}

func (d *Doc) loadLog() {
	b, err := os.ReadFile(filepath.Join(d.dir, "ops.json"))
	if err == nil {
		json.Unmarshal(b, &d.log)
	}
}

func (d *Doc) saveLogLocked() error {
	b, _ := json.Marshal(d.log)
	tmp := filepath.Join(d.dir, "ops.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(d.dir, "ops.json"))
}

func (d *Doc) stateLocked(pages ...int) editState {
	return editState{Rev: d.log.Rev, CanUndo: len(d.log.Ops) > 0, CanRedo: len(d.log.Redo) > 0, Pages: pages}
}

func (d *Doc) State() editState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stateLocked()
}

// pageIndex maps a page id to its current index.
func (d *Doc) pageIndex(id int) int {
	for i, p := range d.specs() {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// origLinks returns the link annotations a current page has in the file.
func (d *Doc) origLinks(sp edit.PageSpec) []nav.Link {
	if sp.Orig < 0 {
		return nil
	}
	links := nav.ReadLinks(d.PDF, d.PDF.Dict(d.PDF.Trailer["Root"]), d.PDF.Pages()[sp.Orig].Dict)
	if sp.Fit != nil {
		for k := range links {
			links[k].Quad = edit.MapQuad(links[k].Quad, content.Matrix(*sp.Fit))
		}
	}
	return links
}

func (d *Doc) validatePageOp(op edit.Op) error {
	specs := d.specs()
	exists := func(id int) bool { return d.pageIndex(id) >= 0 }
	switch op.Op {
	case "page.insert", "toc", "table.split":
		if op.After != 0 && !exists(op.After) {
			return &userError{"Нет такой страницы"}
		}
		if op.Op == "toc" && op.TOC == nil {
			return &userError{"Нет настроек оглавления"}
		}
		if op.Size[0] < 0 || op.Size[1] < 0 || op.Size[0] > 14400 || op.Size[1] > 14400 {
			return &userError{"Неверный размер страницы"}
		}
	case "page.import":
		src := d.source(op.Asset)
		if src == nil {
			return &userError{"Нет такого PDF"}
		}
		if op.After != 0 && !exists(op.After) {
			return &userError{"Нет такой страницы"}
		}
		for _, i := range op.From {
			if i < 0 || i >= len(src.Pages()) {
				return &userError{"В PDF нет такой страницы"}
			}
		}
	case "page.delete":
		if !exists(op.Page) {
			return &userError{"Нет такой страницы"}
		}
		if len(specs) < 2 {
			return &userError{"Нельзя удалить единственную страницу"}
		}
	case "page.move":
		if !exists(op.Page) || op.After != 0 && !exists(op.After) {
			return &userError{"Нет такой страницы"}
		}
	case "page.rotate":
		if !exists(op.Page) || op.Rotate%90 != 0 {
			return &userError{"Поворот кратен 90°"}
		}
	case "page.margins":
		for _, id := range op.Pages {
			if !exists(id) {
				return &userError{"Нет такой страницы"}
			}
		}
		if op.Mode != "resize" && op.Mode != "fit" {
			return &userError{"Неизвестный режим полей"}
		}
		for _, v := range op.Margins {
			if math.IsNaN(v) || math.Abs(v) > 2000 {
				return &userError{"Неверные поля"}
			}
		}
	case "labels":
		for _, l := range op.Labels {
			if l.Start < 0 || l.Start >= len(specs) || !strings.Contains("DRrAa", l.Style) && l.Style != "" || len(l.Style) > 1 {
				return &userError{"Неверная нумерация страниц"}
			}
		}
	case "hf":
		for _, it := range op.HF {
			if !slices.Contains([]string{"top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right"}, it.Position) {
				return &userError{"Неверное положение колонтитула"}
			}
		}
	}
	return nil
}

// normalizeRuns keeps Text equal to the runs and drops empty runs; a single
// run in the object's own style is plain text.
func normalizeRuns(o *edit.Object) {
	o.Runs = slices.DeleteFunc(slices.Clone(o.Runs), func(r edit.Run) bool { return r.Text == "" })
	if len(o.Runs) == 0 {
		o.Runs = nil
		return
	}
	o.Text = edit.RunsText(o.Runs)
}

// splitTable cuts a table at the bottom margin of its page: the rows that
// fit stay, the rest moves to a new page inserted after it, under the
// header row repeated, at the same left edge below the top margin. Margins
// mirror the table's left margin (at least 1 cm). Merged rows are never cut.
func (d *Doc) splitTable(op edit.Op, id string) (edit.Op, error) {
	i := d.pageIndex(op.Page)
	if i < 0 || len(op.Blocks) != 1 {
		return op, &userError{"Выберите таблицу"}
	}
	d.mu.Lock()
	ed, err := edit.Fold(d.log.Ops)
	rev := d.log.Rev
	d.mu.Unlock()
	if err != nil {
		return op, err
	}
	var t *edit.Object
	if pe := ed.Pages[op.Page]; pe != nil {
		for _, o := range pe.Objects {
			if o.ID == op.Blocks[0] && o.Kind == "table" {
				c := *o
				t = &c
			}
		}
	}
	if t == nil {
		return op, &userError{"Выберите таблицу"}
	}
	if m := t.Matrix; m[0] != 1 || m[1] != 0 || m[2] != 0 || m[3] != 1 {
		return op, &userError{"Повёрнутую таблицу не перенести"}
	}
	face, err := d.env().Face(t.Font)
	if err != nil {
		return op, err
	}
	heights := t.TableLayout(face)
	sp := d.specs()[i]
	box := sp.Box()
	margin := max(28.35, t.Matrix[4]-box[0])
	y, k := t.Matrix[5], 0
	for k < len(heights) && y-heights[k] >= box[1]+margin {
		y -= heights[k]
		k++
	}
	for moved := true; moved; {
		moved = false
		for _, s := range t.Spans {
			if s.Row < k && k < s.Row+s.Rows {
				k, moved = s.Row, true
			}
		}
	}
	header := 0
	if t.HeaderFill != "" {
		header = 1
	}
	switch {
	case k >= len(t.Rows):
		return op, &userError{"Таблица помещается на странице"}
	case k <= header:
		return op, &userError{"На этой странице не помещается ни одной строки таблицы"}
	}
	first, rest := *t, *t
	first.Rows = slices.Clone(t.Rows[:k])
	rest.ID = id
	rest.Rows = append(slices.Clone(t.Rows[:header]), t.Rows[k:]...)
	first.Spans, rest.Spans = nil, nil
	for _, s := range t.Spans {
		if s.Row+s.Rows <= k {
			first.Spans = append(first.Spans, s)
			if s.Row+s.Rows <= header { // the header row is repeated with its merges
				rest.Spans = append(rest.Spans, s)
			}
			continue
		}
		s.Row += header - k
		rest.Spans = append(rest.Spans, s)
	}
	rest.Matrix[5] = box[3] - margin
	op.Object, op.Next = &first, &rest
	op.After, op.NewID = op.Page, -(rev + 1)
	op.Size = [2]float64{sp.Media[2] - sp.Media[0], sp.Media[3] - sp.Media[1]}
	return op, nil
}

// historyEntry describes one step of the log for the history panel.
type historyEntry struct {
	Op   string `json:"op"`
	At   string `json:"at,omitempty"`
	Page int    `json:"page"` // current page index, -1 if none
	Kind string `json:"kind,omitempty"`
	Text string `json:"text,omitempty"` // a hint: object text, field name
	New  bool   `json:"new,omitempty"`  // add without replacing: a new object
}

// History lists the steps done (oldest first) and the steps undone that
// redo would bring back (next first).
func (d *Doc) History() (done, undone []historyEntry) {
	d.mu.Lock()
	ops, redo := slices.Clone(d.log.Ops), slices.Clone(d.log.Redo)
	d.mu.Unlock()
	index := map[int]int{}
	for i, sp := range d.specs() {
		index[sp.ID] = i
	}
	entry := func(op edit.Op) historyEntry {
		e := historyEntry{Op: op.Op, At: op.At, Page: -1, Text: op.Field, New: op.Op == "add" && len(op.Replaces) == 0}
		if i, ok := index[op.Page]; ok && !docOps[op.Op] {
			e.Page = i
		}
		if op.Object != nil {
			e.Kind, e.Text = op.Object.Kind, op.Object.Text
		}
		if r := []rune(e.Text); len(r) > 40 {
			e.Text = string(r[:40]) + "…"
		}
		return e
	}
	done, undone = []historyEntry{}, []historyEntry{}
	for _, op := range ops {
		done = append(done, entry(op))
	}
	for i := len(redo) - 1; i >= 0; i-- {
		undone = append(undone, entry(redo[i]))
	}
	return done, undone
}

// Goto undoes or redoes steps until n of them are done: one change for any
// distance in the history.
func (d *Doc) Goto(n int) (editState, error) {
	if err := d.checkEditable(""); err != nil {
		return editState{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if n < 0 || n > len(d.log.Ops)+len(d.log.Redo) {
		return editState{}, &userError{"Нет такого шага"}
	}
	if n == len(d.log.Ops) {
		return d.stateLocked(), nil
	}
	for len(d.log.Ops) > n {
		k := len(d.log.Ops) - 1
		d.log.Redo = append(d.log.Redo, d.log.Ops[k])
		d.log.Ops = d.log.Ops[:k]
	}
	for len(d.log.Ops) < n {
		k := len(d.log.Redo) - 1
		d.log.Ops = append(d.log.Ops, d.log.Redo[k])
		d.log.Redo = d.log.Redo[:k]
	}
	st, err := d.commitLocked()
	st.Structure = true
	return st, err
}

// lastOp names the operation undo (or redo) would take back.
func (d *Doc) lastOp(redo bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ops := d.log.Ops
	if redo {
		ops = d.log.Redo
	}
	if len(ops) == 0 {
		return ""
	}
	return ops[len(ops)-1].Op
}

// FieldInfo is a form field for the UI, with its widgets on current pages.
type FieldInfo struct {
	form.Field
	Widgets []widgetInfo `json:"widgets"`
}

type widgetInfo struct {
	Page int        `json:"page"` // current page index
	Rect [4]float64 `json:"rect"`
	On   string     `json:"on,omitempty"`
}

// Fields returns the form fields with the values set so far, and whether
// they were flattened.
func (d *Doc) Fields() ([]form.Field, bool) {
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	ed, err := edit.Fold(ops)
	if err != nil {
		return nil, false
	}
	fields := form.Read(d.PDF, d.PDF.Dict(d.PDF.Trailer["Root"]))
	for i := range fields {
		if v, ok := ed.Fields[fields[i].Name]; ok {
			fields[i].Value = v
		}
	}
	return fields, ed.Flatten
}

// FieldsUI lists the fields with widgets placed on the current pages.
func (d *Doc) FieldsUI() ([]FieldInfo, bool) {
	fields, flat := d.Fields()
	index := map[int]int{} // page object number → current index
	orig := d.PDF.Pages()
	for i, sp := range d.specs() {
		if sp.Orig >= 0 {
			index[orig[sp.Orig].Ref.Num] = i
		}
	}
	out := []FieldInfo{}
	for _, f := range fields {
		fi := FieldInfo{Field: f, Widgets: []widgetInfo{}}
		for _, w := range f.Widgets {
			if i, ok := index[w.Page.Num]; ok && w.Page.Num != 0 {
				fi.Widgets = append(fi.Widgets, widgetInfo{Page: i, Rect: w.Rect, On: w.On})
			}
		}
		out = append(out, fi)
	}
	return out, flat
}

// libFontID is the library font used when text in a font of the file is
// edited: the best match, or the default font.
func (d *Doc) libFontID(name string, bold, italic bool) string {
	if lf := d.lib.Match(name, bold, italic); lf != nil {
		return lf.ID
	}
	if def := d.lib.Default(); def != nil {
		// No such family: the default one, in the same weight and slant.
		return d.lib.Variant(def.ID, bold, italic).ID
	}
	return ""
}

// securityInfo is the protection of an encrypted document, for the UI.
type securityInfo struct {
	Method   string `json:"method"`
	Owner    bool   `json:"owner"`    // opened with the owner password
	Password bool   `json:"password"` // a user password is needed to open it
	Editable bool   `json:"editable"`
}

func (d *Doc) Security() *securityInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sec == nil {
		return nil
	}
	return &securityInfo{Method: d.sec.Method, Owner: d.sec.Owner, Password: d.sec.User, Editable: d.sec.Allows(cos.PermModify)}
}

// checkEditable refuses changes the document's permissions forbid until the
// owner password is entered (as Acrobat and Preview do); filling in form
// fields has a permission of its own.
func (d *Doc) checkEditable(opName string) error {
	d.mu.Lock()
	sec := d.sec
	d.mu.Unlock()
	if opName == "field" && sec.Allows(cos.PermFillForm|cos.PermAnnotate) {
		return nil
	}
	if !sec.Allows(cos.PermModify) {
		return &userError{"Документ защищён от изменений: чтобы править, введите пароль владельца"}
	}
	return nil
}

func (d *Doc) validate(op edit.Op) error {
	if err := d.checkEditable(op.Op); err != nil {
		return err
	}
	if op.Op == "field" || op.Op == "flatten" {
		fields, flat := d.Fields()
		switch {
		case flat:
			return &userError{"Поля формы уже зафиксированы"}
		case op.Op == "flatten" && len(fields) == 0:
			return &userError{"В документе нет полей формы"}
		case op.Op == "field":
			f := form.Find(fields, op.Field)
			if f == nil || f.ReadOnly || !f.Valid(op.Value) {
				return &userError{"Нельзя записать это значение в поле"}
			}
		}
		return nil
	}
	if op.Op == "security" {
		if s := d.Security(); s == nil || !s.Owner {
			return &userError{"Снять защиту можно только с паролем владельца"}
		}
		return nil
	}
	if err := d.validatePageOp(op); err != nil {
		return err
	}
	if docOps[op.Op] && op.Op != "outline" && op.Op != "meta" && op.Op != "sanitize" {
		return nil
	}
	if op.Op == "outline" {
		for _, o := range flatten(op.Outline) {
			if o.Page != 0 && d.pageIndex(o.Page) < 0 {
				return &userError{"Закладка ведёт на несуществующую страницу"}
			}
		}
		return nil
	}
	if op.Op == "meta" || op.Op == "sanitize" {
		_, err := edit.Fold([]edit.Op{op})
		if err != nil {
			return &userError{err.Error()}
		}
		return nil
	}
	i := d.pageIndex(op.Page)
	if i < 0 {
		return &userError{"Нет такой страницы"}
	}
	d.mu.Lock()
	edits, err := edit.Fold(d.log.Ops)
	d.mu.Unlock()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	sp := d.specs()[i]
	if sp.Orig >= 0 {
		for _, b := range d.An.Page(sp.Orig) {
			known[b.ID] = true
		}
	}
	if pe := edits.Pages[op.Page]; pe != nil {
		for _, o := range pe.Objects {
			known[o.ID] = true
		}
	}
	links := map[string]bool{}
	for _, l := range d.origLinks(sp) {
		links[l.ID] = true
	}
	if pe := edits.Pages[op.Page]; pe != nil {
		for _, l := range pe.Links {
			links[l.ID] = true
		}
	}
	for _, id := range append(slices.Clone(op.Blocks), op.Replaces...) {
		if !known[id] && !(edit.IsLink(id) && links[id]) {
			return &userError{"Неизвестный блок " + id}
		}
	}
	if op.Op == "link" {
		l := op.Link
		switch {
		case l == nil:
			return &userError{"Нет ссылки"}
		case l.Kind == "uri" && !nav.SafeURI(l.URI):
			return &userError{"Ссылка должна начинаться с http://, https://, mailto: или tel:"}
		case l.Kind == "page" && d.pageIndex(l.Page) < 0:
			return &userError{"Нет такой страницы"}
		case l.Kind != "uri" && l.Kind != "page":
			return &userError{"Неизвестный тип ссылки"}
		case l.Attach != "" && !known[l.Attach]:
			return &userError{"Неизвестный блок " + l.Attach}
		}
	}
	switch op.Op {
	case "transform", "recolor", "delete":
		if len(op.Blocks) == 0 {
			return &userError{"Не выбраны блоки"}
		}
	case "add", "update":
		o := op.Object
		if o == nil {
			return &userError{"Нет объекта"}
		}
		if op.Op == "update" && !known[o.ID] {
			return &userError{"Неизвестный объект " + o.ID}
		}
		if !slices.Contains([]string{"text", "rect", "ellipse", "line", "image", "svg", "table"}, o.Kind) || o.W < 0 || o.H < 0 || o.W > 1e5 || o.H > 1e5 {
			return &userError{"Неверный объект"}
		}
		if o.Kind == "table" {
			if len(o.Cols) == 0 || len(o.Cols) > 50 || len(o.Rows) == 0 || len(o.Rows) > 1000 {
				return &userError{"Таблица: от 1 до 50 столбцов и до 1000 строк"}
			}
			for _, w := range o.Cols {
				if w < 4 || w > 1e4 || math.IsNaN(w) {
					return &userError{"Неверная ширина столбца"}
				}
			}
		}
		if (o.Kind == "image" || o.Kind == "svg") && !validAsset(o.Asset) {
			return &userError{"Неверное изображение"}
		}
	}
	if op.Matrix != nil {
		for _, v := range op.Matrix {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return &userError{"Неверная матрица"}
			}
		}
	}
	if _, err := edit.Fold([]edit.Op{op}); err != nil {
		return &userError{err.Error()}
	}
	return nil
}

func validAsset(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.HasPrefix(name, ".")
}

// resolve turns intent operations into log entries: adopt and replaceImage
// become "add" with the object computed from the original block.
// resolve completes an op from the client and records the items of the
// original blocks it names (edit.Op.Members).
func (d *Doc) resolve(op edit.Op) (edit.Op, error) {
	op, err := d.complete(op)
	if err != nil {
		return op, err
	}
	var ids []string
	if op.Op == "transform" || op.Op == "recolor" || op.Op == "delete" {
		ids = op.Blocks
	}
	ids = append(slices.Clone(ids), op.Replaces...)
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == "" || id[0] < '0' || id[0] > '9' })
	if len(ids) == 0 {
		return op, nil
	}
	i := d.pageIndex(op.Page)
	if i < 0 {
		return op, nil
	}
	blocks, err := d.Blocks(i)
	if err != nil {
		return op, err
	}
	op.Members = map[string][]string{}
	for _, b := range blocks {
		if slices.Contains(ids, b.ID) && len(b.Items) > 0 {
			op.Members[b.ID] = b.Items
		}
	}
	return op, nil
}

func (d *Doc) complete(op edit.Op) (edit.Op, error) {
	d.mu.Lock()
	id := fmt.Sprintf("o%d", d.log.Rev+1)
	d.mu.Unlock()
	if op.Op == "table.split" {
		return d.splitTable(op, id)
	}
	if op.Op == "add" && op.Object != nil && op.Object.ID == "" {
		o := *op.Object
		o.ID = id
		op.Object = &o
	}
	if (op.Op == "add" || op.Op == "update") && op.Object != nil && len(op.Object.Runs) > 0 {
		o := *op.Object
		normalizeRuns(&o)
		op.Object = &o
	}
	if (op.Op == "page.insert" || op.Op == "toc") && op.NewID == 0 {
		d.mu.Lock()
		op.NewID = -(d.log.Rev + 1)
		d.mu.Unlock()
	}
	if op.Op == "page.import" && len(op.NewIDs) == 0 {
		src := d.source(op.Asset)
		if src == nil {
			return op, &userError{"Нет такого PDF"}
		}
		n := len(op.From)
		if n == 0 {
			n = len(src.Pages())
		}
		d.mu.Lock()
		base := (d.log.Rev + 1) * 100000
		d.mu.Unlock()
		for k := range n {
			op.NewIDs = append(op.NewIDs, -(base + k))
		}
	}
	if op.Op == "link" && op.Link != nil && op.Link.ID == "" {
		l := *op.Link
		l.ID = "l" + id[1:]
		op.Link = &l
	}
	if op.Op != "adopt" && op.Op != "replaceImage" {
		return op, nil
	}
	i := d.pageIndex(op.Page)
	if i < 0 || len(op.Blocks) != 1 {
		return op, &userError{"Выберите один блок"}
	}
	// Current state: the block may have been moved or recoloured already.
	blocks, err := d.Blocks(i)
	if err != nil {
		return op, err
	}
	var b *blockJSON
	for _, x := range blocks {
		if x.ID == op.Blocks[0] && x.Object == nil {
			b = &x
		}
	}
	if b == nil {
		return op, &userError{"Неизвестный блок"}
	}
	m, w, h := edit.QuadMatrix(b.Quad)
	obj := edit.Object{ID: id, Matrix: m, W: w, H: h, Opacity: 1}
	if op.Op == "replaceImage" {
		if op.Object == nil || !validAsset(op.Object.Asset) {
			return op, &userError{"Нет изображения"}
		}
		obj.Kind, obj.Asset = "image", op.Object.Asset
		if strings.HasSuffix(obj.Asset, ".svg") {
			obj.Kind = "svg"
		}
		return edit.Op{Op: "add", Page: op.Page, Object: &obj, Replaces: op.Blocks}, nil
	}
	if b.Kind != "text" {
		return op, &userError{"Править можно только текстовый блок"}
	}
	lf := d.lib.Match(b.Font, b.Bold, b.Italic)
	if lf == nil {
		lf = d.lib.Default()
	}
	if lf == nil {
		return op, &userError{"В библиотеке нет шрифтов"}
	}
	face, err := d.lib.Face(lf.ID)
	if err != nil {
		return op, err
	}
	lines := strings.Split(b.Text, "\n")
	obj.Kind, obj.Font, obj.Size, obj.Color, obj.Align, obj.AutoHeight = "text", lf.ID, b.Size, b.Color, "left", true
	if obj.Size <= 0 {
		obj.Size = 12
	}
	// Wide enough for every original line in the new font: rewrapping the
	// joined paragraph then gives the same line breaks.
	for _, l := range lines {
		obj.W = max(obj.W, face.TextWidth(strings.TrimRight(l, " \t"))/1000*obj.Size+1)
	}
	// A single line (heading, label) may grow to the right margin, taken
	// as the mirror of its left margin, instead of wrapping at its width.
	if box := d.specs()[i].Box(); len(lines) == 1 && math.Abs(b.Angle) < 0.5 {
		left := m[4]
		right := box[2] - (left - box[0])
		obj.W = max(obj.W, right-left)
	}
	asc, desc, _, _, _ := face.Metrics()
	if n := len(lines); n > 1 {
		obj.LineHeight = (h - (asc-desc)/1000*obj.Size) / float64(n-1) / obj.Size
		if obj.LineHeight < 0.8 || obj.LineHeight > 3 {
			obj.LineHeight = 1.2
		}
	}
	obj.Text = cmp.Or(b.Flow, b.Text) // every character as extracted
	obj.Runs = b.Runs                 // and every word in its style
	if op.Object != nil && (op.Object.Text != "" || len(op.Object.Runs) > 0) {
		obj.Text, obj.Runs = op.Object.Text, op.Object.Runs
	}
	normalizeRuns(&obj)
	// Styles change widths (a bold word is wider): widen until the text
	// wraps into no more lines than it had.
	for k := 0; len(obj.Runs) > 0 && k < 25; k++ {
		if l, err := obj.Lines(d.env().Face); err != nil || len(l) <= len(lines) {
			break
		}
		obj.W *= 1.02
	}
	return edit.Op{Op: "add", Page: op.Page, Object: &obj, Replaces: op.Blocks}, nil
}

func (d *Doc) commitLocked(pages ...int) (editState, error) {
	d.log.Rev++
	d.Modified = time.Now().UTC()
	d.Pages = len(d.specsLocked())
	if err := d.saveLogLocked(); err != nil {
		return editState{}, err
	}
	b, _ := json.MarshalIndent(d.Meta, "", "  ")
	os.WriteFile(filepath.Join(d.dir, "meta.json"), b, 0o644)
	return d.stateLocked(pages...), nil
}

func (d *Doc) Do(op edit.Op) (editState, error) {
	op, err := d.resolve(op)
	if err != nil {
		return editState{}, err
	}
	if err := d.validate(op); err != nil {
		return editState{}, err
	}
	op.At = time.Now().UTC().Format(time.RFC3339)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log.Ops = append(d.log.Ops, op)
	d.log.Redo = nil
	st, err := d.commitLocked(d.pagesOfLocked(op)...)
	st.Structure = isPageOp(op) || op.Op == "labels"
	switch op.Op {
	case "add":
		st.Created = op.Object.ID
	case "link":
		st.Created = op.Link.ID
	}
	return st, err
}

func (d *Doc) Undo() (editState, error) {
	if err := d.checkEditable(d.lastOp(false)); err != nil {
		return editState{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.log.Ops)
	if n == 0 {
		return d.stateLocked(), nil
	}
	op := d.log.Ops[n-1]
	d.log.Ops = d.log.Ops[:n-1]
	d.log.Redo = append(d.log.Redo, op)
	st, err := d.commitLocked(d.pagesOfLocked(op)...)
	st.Structure = isPageOp(op) || op.Op == "labels"
	return st, err
}

func (d *Doc) Redo() (editState, error) {
	if err := d.checkEditable(d.lastOp(true)); err != nil {
		return editState{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.log.Redo)
	if n == 0 {
		return d.stateLocked(), nil
	}
	op := d.log.Redo[n-1]
	d.log.Redo = d.log.Redo[:n-1]
	d.log.Ops = append(d.log.Ops, op)
	st, err := d.commitLocked(d.pagesOfLocked(op)...)
	st.Structure = isPageOp(op) || op.Op == "labels"
	return st, err
}

// pagesOfLocked lists the page indices an operation changes (none for
// document-level operations).
func (d *Doc) pagesOfLocked(op edit.Op) []int {
	if docOps[op.Op] {
		return nil
	}
	for i, p := range d.specsLocked() {
		if p.ID == op.Page {
			return []int{i}
		}
	}
	return nil
}

func flatten(items []nav.Outline) []nav.Outline {
	var out []nav.Outline
	for _, o := range items {
		out = append(out, o)
		out = append(out, flatten(o.Items)...)
	}
	return out
}

// Links returns the links of page i after the edits. blocks is the
// current state of the page, for links that follow a block.
func (d *Doc) Links(i int, blocks []blockJSON) ([]nav.Link, error) {
	sp := d.specs()[i]
	links := d.origLinks(sp)
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	ed, err := edit.Fold(ops)
	if err != nil {
		return nil, err
	}
	pe := ed.Pages[sp.ID]
	if pe == nil {
		return links, nil
	}
	quads := map[string][8]float64{}
	for _, b := range blocks {
		quads[b.ID] = b.Quad
	}
	replaced := map[string]bool{}
	for _, l := range pe.Links {
		replaced[l.ID] = true
	}
	out := []nav.Link{}
	for _, l := range links {
		if !pe.DelLinks[l.ID] && !replaced[l.ID] {
			out = append(out, l)
		}
	}
	for _, l := range pe.Links {
		l := *l
		if l.Attach != "" {
			q, ok := quads[l.Attach]
			if !ok {
				continue
			}
			l.Quad = q
		}
		out = append(out, l)
	}
	return out, nil
}

// Outline returns the bookmarks after the edits.
func (d *Doc) Outline() ([]nav.Outline, error) {
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	ed, err := edit.Fold(ops)
	if err != nil {
		return nil, err
	}
	if ed.Outline != nil {
		return *ed.Outline, nil
	}
	return nav.ReadOutline(d.PDF, d.PDF.Dict(d.PDF.Trailer["Root"])), nil
}

// SuggestOutline proposes bookmarks from headings: text noticeably larger
// than the body text, or short bold lines, levels by size.
func (d *Doc) SuggestOutline() []nav.Outline {
	type cand struct {
		index int
		page  int
		top   float64
		size  float64
		title string
	}
	weight := map[float64]int{}
	var all []cand
	for i, sp := range d.specs() {
		if sp.Orig < 0 {
			continue
		}
		blocks := d.An.Page(sp.Orig)
		for _, b := range blocks {
			if b.Kind != "text" || b.Invisible || b.Artifact || b.Size <= 0 {
				continue
			}
			// Headings stand alone on their line: skip table cells and rows.
			alone := math.Abs(b.Angle) < 0.5
			for _, o := range blocks {
				if o.ID != b.ID && o.Kind == "text" && math.Abs(o.Quad[1]-b.Quad[1]) < 2 {
					alone = false
				}
			}
			sz := math.Round(b.Size*2) / 2
			weight[sz] += len([]rune(b.Text))
			text := strings.Join(strings.Fields(b.Text), " ")
			if n := len([]rune(text)); n == 0 || n > 150 || strings.Count(b.Text, "\n") > 2 {
				continue
			}
			if !alone {
				continue
			}
			top := max(b.Quad[5], b.Quad[7])
			key := sz
			if b.Bold {
				key += 0.25 // bold of the same size ranks above regular
			}
			all = append(all, cand{i, sp.ID, top, key, text})
		}
	}
	body, best := 0.0, -1
	for sz, w := range weight {
		if w > best {
			body, best = sz, w
		}
	}
	var heads []cand
	sizes := map[float64]bool{}
	for _, c := range all {
		if c.size >= body*1.12 || (c.size == body+0.25 && len([]rune(c.title)) <= 80) {
			if _, err := strconv.Atoi(strings.TrimSpace(c.title)); err == nil {
				continue // page numbers
			}
			heads = append(heads, c)
			sizes[c.size] = true
		}
	}
	slices.SortStableFunc(heads, func(a, b cand) int {
		if a.index != b.index {
			return a.index - b.index
		}
		return cmp.Compare(b.top, a.top)
	})
	order := slices.Sorted(maps.Keys(sizes))
	slices.Reverse(order)
	level := func(sz float64) int { return min(slices.Index(order, sz), 2) }
	var root []nav.Outline
	type frame struct {
		list  *[]nav.Outline
		level int
	}
	stack := []frame{{&root, -1}}
	for _, c := range heads {
		lv := level(c.size)
		for len(stack) > 1 && stack[len(stack)-1].level >= lv {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].list
		top := c.top
		*parent = append(*parent, nav.Outline{Title: c.title, Page: c.page, Top: &top, Open: true})
		last := &(*parent)[len(*parent)-1]
		stack = append(stack, frame{&last.Items, lv})
	}
	return root
}

// Metadata returns the document metadata with the edits applied.
func (d *Doc) Metadata() (map[string]any, error) {
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	ed, err := edit.Fold(ops)
	if err != nil {
		return nil, err
	}
	m := meta.Read(d.PDF, d.PDF.Trailer)
	if ed.Meta != nil {
		m = *ed.Meta
	}
	return map[string]any{"meta": m, "sanitize": ed.Sanitize, "security": d.Security(), "decrypt": ed.Decrypt}, nil
}

// tailLocked returns the incremental update for the current log, cached
// for the last few revisions.
func (d *Doc) tailLocked() ([]byte, error) {
	if t, ok := d.tails[d.log.Rev]; ok {
		return t, nil
	}
	var tail []byte
	if len(d.log.Ops) > 0 {
		u, err := edit.Apply(d.PDF, d.log.Ops, d.env())
		if err != nil {
			return nil, err
		}
		tail = u.Tail()
	}
	if d.tails == nil {
		d.tails = map[int][]byte{}
	}
	for r := range d.tails {
		if r < d.log.Rev-3 {
			delete(d.tails, r)
		}
	}
	d.tails[d.log.Rev] = tail
	return tail, nil
}

// File returns revision v of the document (the current one if v < 0):
// original bytes + update, without copying the original. A viewer reads a
// revision in ranges over time, so a recent one stays exactly as it was
// while edits go on; an older one is gone (the viewer has a newer one).
func (d *Doc) File(v int) (io.ReadSeeker, int, error) {
	d.mu.Lock()
	if v < 0 {
		v = d.log.Rev
	}
	tail, ok := d.tails[v]
	var err error
	if v == d.log.Rev {
		tail, err = d.tailLocked()
	} else if !ok {
		err = errGone
	}
	d.mu.Unlock()
	if err != nil {
		return nil, 0, err
	}
	orig := d.PDF.Data()
	return io.NewSectionReader(concat{orig, tail}, 0, int64(len(orig)+len(tail))), v, nil
}

// WriteClean writes the current document as a fresh file.
func (d *Doc) WriteClean(w io.Writer) error {
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	u, err := edit.Apply(d.PDF, ops, d.env())
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := u.WriteClean(&buf); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

func fitBlock(b blockJSON, m [6]float64) blockJSON {
	b.Quad = edit.MapQuad(b.Quad, content.Matrix(m))
	for k := range b.Quad {
		b.Quad[k] = round2(b.Quad[k])
	}
	b.Size = round2(b.Size * m[0])
	b.styles = slices.Clone(b.styles)
	for k := range b.styles {
		b.styles[k].Size = round2(b.styles[k].Size * m[0])
	}
	return b
}

// Layout returns page labels and headers/footers after the edits.
func (d *Doc) Layout() (map[string]any, error) {
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	ed, err := edit.Fold(ops)
	if err != nil {
		return nil, err
	}
	labels := edit.ReadLabels(d.PDF, d.PDF.Dict(d.PDF.Trailer["Root"]))
	if ed.Labels != nil {
		labels = *ed.Labels
	}
	return map[string]any{"labels": labels, "hf": ed.HF}, nil
}

// source opens an uploaded PDF once.
func (d *Doc) source(asset string) *cos.Document {
	if !validAsset(asset) || !strings.HasSuffix(asset, ".pdf") {
		return nil
	}
	d.srcMu.Lock()
	defer d.srcMu.Unlock()
	if s, ok := d.sources[asset]; ok {
		return s
	}
	var s *cos.Document
	if data, err := os.ReadFile(filepath.Join(d.dir, "assets", asset)); err == nil {
		s, _ = cos.Open(data)
	}
	if d.sources == nil {
		d.sources = map[string]*cos.Document{}
	}
	d.sources[asset] = s
	return s
}

func (d *Doc) env() edit.Env {
	return edit.Env{
		PDF:   d.source,
		Fonts: d.An.fonts,
		Face: func(id string) (*font.Face, error) {
			if id == "" || d.lib.Get(id) == nil {
				if def := d.lib.Default(); def != nil {
					id = def.ID
				}
			}
			return d.lib.Face(id)
		},
		Asset: func(name string) ([]byte, error) {
			if !validAsset(name) {
				return nil, errNotFound
			}
			return os.ReadFile(filepath.Join(d.dir, "assets", name))
		},
	}
}

// SaveAsset stores an uploaded image and returns its name and pixel size.
func (d *Doc) SaveAsset(name string, r io.Reader) (map[string]any, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	res := map[string]any{"kind": "image"}
	if bytes.HasPrefix(bytes.TrimLeft(data[:min(len(data), 1024)], "\x00\r\n\t "), []byte("%PDF-")) {
		src, err := cos.Open(data)
		if errors.Is(err, cos.ErrPassword) {
			return nil, &userError{"PDF защищён паролем: снимите защиту или загрузите его как отдельный документ"}
		}
		if err != nil {
			return nil, &userError{"Не удалось открыть PDF: " + err.Error()}
		}
		res["kind"], res["pages"] = "pdf", len(src.Pages())
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".pdf"
	} else if strings.EqualFold(filepath.Ext(name), ".svg") || bytes.Contains(data[:min(len(data), 1024)], []byte("<svg")) {
		conv, err := svg.Convert(data)
		if err != nil {
			return nil, &userError{"Не удалось прочитать SVG: " + err.Error()}
		}
		res["kind"], res["width"], res["height"], res["warnings"] = "svg", conv.Width, conv.Height, conv.Warnings
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".svg"
	} else {
		w, h, err := edit.ImageSize(data)
		if err != nil {
			return nil, &userError{"Поддерживаются JPEG, PNG, GIF, WebP и SVG"}
		}
		res["width"], res["height"] = w, h
	}
	sum := sha1.Sum(data)
	asset := hex.EncodeToString(sum[:8]) + strings.ToLower(filepath.Ext(name))
	dir := filepath.Join(d.dir, "assets")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, asset), data, 0o644); err != nil {
		return nil, err
	}
	res["asset"] = asset
	return res, nil
}

// Blocks returns the blocks of page i as they look after the edits: the
// analysis of the original with each block's effect applied, plus the
// editor objects.
func (d *Doc) Blocks(i int) ([]blockJSON, error) {
	sp := d.specs()[i]
	var orig []blockJSON
	if sp.Orig >= 0 {
		orig = d.An.Page(sp.Orig)
	}
	d.mu.Lock()
	ops := slices.Clone(d.log.Ops)
	d.mu.Unlock()
	edits, err := edit.Fold(ops)
	if err != nil {
		return nil, err
	}
	pe := edits.Pages[sp.ID]
	out := make([]blockJSON, 0, len(orig))
	for _, b := range orig {
		if sp.Fit != nil { // content scaled into margins
			b = fitBlock(b, *sp.Fit)
		}
		if b.Kind == "text" {
			b.LibFont = d.libFontID(b.Font, b.Bold, b.Italic)
			for _, s := range b.styles {
				b.Runs = append(b.Runs, edit.Run{Text: s.Text, Font: d.libFontID(s.Font, s.Bold, s.Italic), Size: s.Size, Color: s.Color})
			}
		}
		if pe == nil {
			out = append(out, b)
			continue
		}
		e, ok := pe.EffectFor(b.ID, b.Items...)
		if !ok {
			out = append(out, b)
			continue
		}
		if e.Delete {
			continue
		}
		if e.Matrix != content.Identity {
			b.Quad = edit.MapQuad(b.Quad, e.Matrix)
			for k := range b.Quad {
				b.Quad[k] = round2(b.Quad[k])
			}
			b.Angle = round2(math.Mod(b.Angle+math.Atan2(e.Matrix[1], e.Matrix[0])*180/math.Pi+540, 360) - 180)
		}
		if e.Color != nil && b.Kind != "image" {
			b.Color = hexColor(*e.Color)
		}
		out = append(out, b)
	}
	if pe == nil {
		return out, nil
	}
	env := d.env()
	for _, o := range pe.Objects {
		o := *o
		var rowHeights []float64
		if face, err := env.Face(o.Font); err == nil {
			switch {
			case o.Kind == "text" && o.AutoHeight:
				o.H = o.TextHeight(env.Face)
			case o.Kind == "table":
				o.W, o.H = o.TableSize(face)
				rowHeights = o.TableLayout(face)
			}
		}
		q := o.Quad()
		for k := range q {
			q[k] = round2(q[k])
		}
		kind := o.Kind
		switch kind {
		case "svg":
			kind = "image"
		case "text", "image", "table":
		default:
			kind = "path"
		}
		bj := blockJSON{ID: o.ID, Kind: kind, Quad: q, Angle: round2(math.Atan2(o.Matrix[1], o.Matrix[0]) * 180 / math.Pi),
			Text: o.Text, Size: o.Size, Color: o.Color, Object: &o, RowHeights: rowHeights}
		if kind == "path" {
			bj.Color = o.Fill
			if bj.Color == "" {
				bj.Color = o.Stroke
			}
		}
		if lf := d.lib.Get(o.Font); lf != nil {
			bj.Font = lf.Full
		}
		out = append(out, bj)
	}
	return out, nil
}

type concat struct{ a, b []byte }

func (c concat) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		var src []byte
		switch {
		case pos < int64(len(c.a)):
			src = c.a[pos:]
		case pos < int64(len(c.a)+len(c.b)):
			src = c.b[pos-int64(len(c.a)):]
		default:
			return n, io.EOF
		}
		n += copy(p[n:], src)
	}
	return n, nil
}
