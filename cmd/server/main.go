package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	pdfformatter "pdf-formatter"
	"pdf-formatter/pdf/edit"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code, msg := http.StatusInternalServerError, "Внутренняя ошибка: "+err.Error()
	var ue *userError
	switch {
	case errors.Is(err, errNotFound):
		code, msg = http.StatusNotFound, err.Error()
	case errors.Is(err, errGone):
		code, msg = http.StatusGone, err.Error()
	case errors.Is(err, errLocked):
		code, msg = http.StatusLocked, err.Error()
	case errors.As(err, &ue):
		code, msg = http.StatusUnprocessableEntity, ue.msg
	default:
		log.Print(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type pageInfo struct {
	Num    int        `json:"num"` // page id: object number, negative for inserted pages
	Box    [4]float64 `json:"box"` // visible area, PDF units
	Rotate int        `json:"rotate"`
	Label  string     `json:"label"`
}

func main() {
	addr := env("ADDR", "127.0.0.1:8080")
	if pdfformatter.Embedded && os.Getenv("ADDR") == "" {
		// Its own port: never mistaken for a Docker or development server on 8080.
		addr = "127.0.0.1:47321"
	}
	dataDir := env("DATA_DIR", "data")
	var web http.FileSystem = http.Dir(env("WEB_DIR", "web/dist"))
	builtin := filepath.SplitList(env("BUILTIN_FONTS", "fonts"))
	if pdfformatter.Embedded {
		// Standalone build: everything is inside the binary, documents live
		// in the user's application data folder.
		if os.Getenv("DATA_DIR") == "" {
			cfg, err := os.UserConfigDir()
			if err != nil {
				log.Fatal(err)
			}
			dataDir = filepath.Join(cfg, "PDF Formatter")
		}
		sub, err := fs.Sub(pdfformatter.Web, "web/dist")
		if err != nil {
			log.Fatal(err)
		}
		web = http.FS(sub)
		dir, err := extractFonts(dataDir)
		if err != nil {
			log.Fatal(err)
		}
		builtin = []string{dir}
	}
	lib := NewLibrary(dataDir, builtin, systemFontDirs())
	store, err := NewStore(dataDir, lib)
	if err != nil {
		log.Fatal(err)
	}
	ln, link := listen(addr)
	if pdfformatter.Embedded {
		fmt.Printf("\nPDF Formatter работает: %s\nДокументы и шрифты хранятся в: %s\n\nВыключить: кнопка «Выключить программу» на главном экране редактора (или Ctrl+C в терминале).\n\n", link, dataDir)
		openBrowser(link)
	} else {
		log.Printf("listening on %s", ln.Addr())
	}
	log.Fatal(http.Serve(ln, guard(routes(store, lib, web))))
}

// guard keeps other websites out: the app listens on localhost, but any
// page in the browser can send requests to it. Unknown Host headers (DNS
// rebinding) and cross-origin writes are refused.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request refused", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

const appName = "pdf-formatter"

// listen takes the requested address. The standalone build falls back to
// a free port, and when the address is held by another copy of the app it
// just opens that one in the browser and exits.
func listen(addr string) (net.Listener, string) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, "http://" + ln.Addr().String()
	}
	if !pdfformatter.Embedded {
		log.Fatal(err)
	}
	link := "http://" + addr
	c := http.Client{Timeout: 2 * time.Second}
	if res, err := c.Get(link + "/api/health"); err == nil {
		var h struct {
			App        string
			Standalone bool
		}
		json.NewDecoder(res.Body).Decode(&h)
		res.Body.Close()
		// Only another installed copy counts: a Docker or development server
		// has other documents.
		if h.App == appName && h.Standalone {
			fmt.Printf("PDF Formatter уже запущен: %s\n", link)
			openBrowser(link)
			os.Exit(0)
		}
	}
	host, _, _ := net.SplitHostPort(addr)
	ln, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		log.Fatal(err)
	}
	return ln, "http://" + ln.Addr().String()
}

func openBrowser(link string) {
	if os.Getenv("NO_BROWSER") != "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Откройте в браузере: %s\n", link)
	}
}

// systemFontDirs lists the OS font folders (SYSTEM_FONTS overrides).
func systemFontDirs() []string {
	if v := os.Getenv("SYSTEM_FONTS"); v != "" {
		return filepath.SplitList(v)
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library/Fonts")}
	case "windows":
		return []string{filepath.Join(env("WINDIR", `C:\Windows`), "Fonts"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")}
	}
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".local/share/fonts"), filepath.Join(home, ".fonts")}
}

// extractFonts writes the builtin fonts (free/, licensed/) next to the data
// so the library indexes them like any folder. Unchanged files are kept;
// files this build does not carry (an older layout, the licensed fonts of
// another build) are removed.
func extractFonts(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "builtin-fonts")
	keep := map[string]bool{}
	for _, fsys := range pdfformatter.Fonts {
		err := fs.WalkDir(fsys, "fonts", func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			dst := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "fonts/")))
			keep[dst] = true
			info, _ := e.Info()
			if st, err := os.Stat(dst); err == nil && info != nil && st.Size() == info.Size() {
				return nil
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dst, data, 0o644)
		})
		if err != nil {
			return "", err
		}
	}
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && !keep[p] {
			os.Remove(p)
		}
		return nil
	})
	return dir, nil
}

func routes(store *Store, lib *Library, web http.FileSystem) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "app": appName, "standalone": pdfformatter.Embedded})
	})

	if pdfformatter.Embedded {
		// The standalone app has no window: the UI switches it off. Every
		// change is already on disk, so exiting is safe.
		mux.HandleFunc("POST /api/quit", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]bool{"ok": true})
			go func() {
				time.Sleep(300 * time.Millisecond)
				os.Exit(0)
			}()
		})
	}

	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.List()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, list)
	})

	mux.HandleFunc("POST /api/docs", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, &userError{"Файл не получен: " + err.Error()})
			return
		}
		defer f.Close()
		m, err := store.Import(hdr.Filename, f)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("GET /api/docs/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		pages := []pageInfo{}
		lay, err := d.Layout()
		if err != nil {
			writeErr(w, err)
			return
		}
		labels := lay["labels"].([]edit.Label)
		for i, sp := range d.specs() {
			pages = append(pages, pageInfo{Num: sp.ID, Box: sp.Box(), Rotate: sp.Rotate, Label: edit.LabelOf(labels, i)})
		}
		writeJSON(w, map[string]any{"meta": d.Meta, "pages": pages, "state": d.State(), "security": d.Security()})
	})

	mux.HandleFunc("GET /api/docs/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		done, undone := d.History()
		writeJSON(w, map[string]any{"done": done, "undone": undone})
	})

	mux.HandleFunc("POST /api/docs/{id}/goto", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct{ N int }
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil {
			writeErr(w, &userError{"Нет номера шага"})
			return
		}
		st, err := d.Goto(body.N)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, st)
	})

	mux.HandleFunc("GET /api/docs/{id}/fields", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		fields, flat := d.FieldsUI()
		writeJSON(w, map[string]any{"fields": fields, "flattened": flat})
	})

	mux.HandleFunc("POST /api/docs/{id}/unlock", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Password string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			writeErr(w, &userError{"Нет пароля"})
			return
		}
		if err := store.Unlock(r.PathValue("id"), body.Password); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("DELETE /api/docs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/docs/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		name := url.PathEscape(d.Name) + ".pdf"
		if r.URL.Query().Get("download") == "clean" {
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+name)
			if err := d.WriteClean(w); err != nil {
				log.Print(err)
			}
			return
		}
		if r.URL.Query().Has("download") {
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+name)
		}
		v := -1
		if s := r.URL.Query().Get("v"); s != "" {
			if v, err = strconv.Atoi(s); err != nil || v < 0 {
				writeErr(w, &userError{"Неверная версия"})
				return
			}
		}
		f, rev, err := d.File(v)
		if err != nil {
			writeErr(w, err)
			return
		}
		// A revision never changes: cacheable under ?v=rev, and the ETag keeps
		// range requests (If-Range) to the same bytes.
		w.Header().Set("ETag", fmt.Sprintf(`"%s-%d"`, d.ID, rev))
		if v >= 0 {
			w.Header().Set("Cache-Control", "private, max-age=3600")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, "", time.Time{}, f)
	})

	mux.HandleFunc("POST /api/docs/{id}/ops", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var op edit.Op
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			writeErr(w, &userError{"Неверная операция: " + err.Error()})
			return
		}
		st, err := d.Do(op)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, st)
	})

	mux.HandleFunc("GET /api/docs/{id}/layout", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		lay, err := d.Layout()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, lay)
	})

	mux.HandleFunc("GET /api/docs/{id}/outline", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if r.URL.Query().Has("suggest") {
			writeJSON(w, map[string]any{"outline": d.SuggestOutline()})
			return
		}
		o, err := d.Outline()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"outline": o})
	})

	mux.HandleFunc("GET /api/docs/{id}/meta", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		m, err := d.Metadata()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("POST /api/docs/{id}/assets", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, &userError{"Файл не получен"})
			return
		}
		defer f.Close()
		res, err := d.SaveAsset(hdr.Filename, f)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("POST /api/docs/{id}/undo", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		st, err := d.Undo()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, st)
	})

	mux.HandleFunc("POST /api/docs/{id}/redo", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		st, err := d.Redo()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, st)
	})

	mux.HandleFunc("GET /api/docs/{id}/objects/{num}", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		num := r.PathValue("num")
		if num == "trailer" {
			writeJSON(w, inspect(d.PDF, "trailer", d.PDF.Trailer))
			return
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			writeErr(w, errNotFound)
			return
		}
		writeJSON(w, inspectRef(d.PDF, n))
	})

	mux.HandleFunc("GET /api/docs/{id}/pages/{n}/blocks", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 0 || n >= len(d.specs()) {
			writeErr(w, errNotFound)
			return
		}
		blocks, err := d.Blocks(n)
		if err != nil {
			writeErr(w, err)
			return
		}
		links, err := d.Links(n, blocks)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"blocks": blocks, "links": links})
	})

	mux.HandleFunc("GET /api/docs/{id}/fonts", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"fonts": d.An.Fonts(), "progress": d.An.Progress()})
	})

	mux.HandleFunc("GET /api/docs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		d, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ch := d.An.subscribe()
		defer d.An.unsubscribe(ch)
		rc := http.NewResponseController(w)
		for {
			select {
			case e := <-ch:
				w.Write(sseEncode(e))
				rc.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	mux.HandleFunc("GET /api/fonts", func(w http.ResponseWriter, r *http.Request) {
		fonts, def, ready := lib.List()
		writeJSON(w, map[string]any{"fonts": fonts, "default": def, "ready": ready})
	})

	mux.HandleFunc("POST /api/fonts", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, &userError{"Файлы не получены: " + err.Error()})
			return
		}
		var added []LibFont
		for _, fh := range r.MultipartForm.File["file"] {
			f, err := fh.Open()
			if err != nil {
				writeErr(w, err)
				return
			}
			got, err := lib.Upload(fh.Filename, f)
			f.Close()
			if err != nil {
				writeErr(w, err)
				return
			}
			added = append(added, got...)
		}
		writeJSON(w, map[string]any{"added": added})
	})

	mux.HandleFunc("DELETE /api/fonts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := lib.Delete(r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/fonts/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		lf := lib.Get(r.PathValue("id"))
		face, err := lib.Face(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "font/"+lf.Format)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(face.Data)
	})

	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var s Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeErr(w, &userError{"Неверный запрос"})
			return
		}
		if err := lib.SetDefault(s.DefaultFont); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.Handle("GET /", http.FileServer(web))
	return mux
}
