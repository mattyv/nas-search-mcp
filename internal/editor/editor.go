// Package editor is the owner's notes and memory editor: a small server-rendered web app
// that works on a phone and installs to the home screen.
//
// Trust boundary. The listener must be bound to localhost, and only `tailscale serve`
// may reach it. tailscale serve authenticates the caller on the private tailnet and adds
// the Tailscale-User-Login header; the editor admits only the configured owner login
// (policy.EditorAllowed). Anything that can reach the socket directly could forge that
// header, so never expose the listener on a LAN interface or a public address. State-
// changing requests also need an exact Origin match as a second, independent check
// against cross-site request forgery.
//
// Decision rules live in internal/policy (verified Dafny core). The input-format checks in
// rules.go only give friendly messages; the memory service enforces the same limits.
package editor

import (
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

//go:embed static
var staticFS embed.FS

//go:embed templates
var templateFS embed.FS

const (
	contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	maxBody               = 64 << 10
	contextBudget         = 8000
	clientName            = "editor"
)

// Config sets who may use the editor and what it may touch.
type Config struct {
	Owner        string   // Tailscale login of the only permitted user
	Origin       string   // https origin the owner's browser uses, e.g. https://nas.tailnet.ts.net
	Roots        []string // scope roots the editor may read and write
	DefaultScope string   // scope preselected for new notes; must lie within Roots
}

type server struct {
	store   memoryapi.Store
	cfg     Config
	roots   []string
	tmpl    *template.Template
	mux     *http.ServeMux
	swBody  []byte
	statics map[string][]byte
}

// New builds the editor handler.
func New(store memoryapi.Store, cfg Config) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("editor: store is required")
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return nil, errors.New("editor: owner login is required")
	}
	if err := checkOrigin(cfg.Origin); err != nil {
		return nil, err
	}
	if len(cfg.Roots) == 0 {
		return nil, errors.New("editor: at least one scope root is required")
	}
	for _, r := range cfg.Roots {
		if !policy.ValidScope(r) {
			return nil, fmt.Errorf("editor: invalid scope root %q", r)
		}
	}
	if !policy.ValidScope(cfg.DefaultScope) || !policy.ScopeVisible(cfg.DefaultScope, cfg.Roots) {
		return nil, fmt.Errorf("editor: default scope %q is not within the roots", cfg.DefaultScope)
	}
	s := &server{store: store, cfg: cfg, roots: append([]string(nil), cfg.Roots...), statics: map[string][]byte{}}
	if err := s.loadAssets(); err != nil {
		return nil, err
	}
	funcs := template.FuncMap{
		"ts":      timeTag,
		"pe":      url.PathEscape,
		"pct":     percent,
		"current": policy.IsCurrentMemory,
	}
	t, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.tmpl = t
	s.routes()
	return s, nil
}

func checkOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.Path != "" || u.User != nil ||
		strings.ContainsAny(o, "?#") || u.String() != o {
		return fmt.Errorf("editor: origin %q must look like https://host[:port]", o)
	}
	return nil
}

func (s *server) loadAssets() error {
	sum := sha256.New()
	err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(p, "static/")
		if name == "sw.js" {
			return nil
		}
		s.statics[name] = b
		sum.Write([]byte(name))
		sum.Write(b)
		return nil
	})
	if err != nil {
		return err
	}
	sw, err := staticFS.ReadFile("static/sw.js")
	if err != nil {
		return err
	}
	version := hex.EncodeToString(sum.Sum(nil))[:12]
	s.swBody = []byte(strings.ReplaceAll(string(sw), "__VERSION__", version))
	return nil
}

func (s *server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.projects)
	m.HandleFunc("GET /p/{project}", s.project)
	m.HandleFunc("GET /p/{project}/checkpoint", s.checkpointForm)
	m.HandleFunc("POST /p/{project}/checkpoint", s.checkpointSave)
	m.HandleFunc("GET /timeline", s.timeline)
	m.HandleFunc("GET /e/{id}", s.event)
	m.HandleFunc("GET /e/{id}/correct", s.correctForm)
	m.HandleFunc("POST /e/{id}/correct", s.correctSave)
	m.HandleFunc("GET /e/{id}/forget", s.forgetEventForm)
	m.HandleFunc("POST /e/{id}/forget", s.forgetEvent)
	m.HandleFunc("GET /new", s.newForm)
	m.HandleFunc("POST /new", s.newSave)
	m.HandleFunc("GET /memories", s.memories)
	m.HandleFunc("GET /m/{id}", s.memoryPage)
	m.HandleFunc("POST /memories/{id}/transition", s.transition)
	m.HandleFunc("GET /memories/{id}/forget", s.forgetMemoryForm)
	m.HandleFunc("POST /memories/{id}/forget", s.forgetMemory)
	m.HandleFunc("GET /review", s.review)
	m.HandleFunc("POST /review/{id}", s.reviewDecide)

	m.HandleFunc("GET /manifest.webmanifest", s.manifest)
	m.HandleFunc("GET /apple-touch-icon.png", func(w http.ResponseWriter, r *http.Request) { s.asset(w, "apple-touch-icon.png") })
	m.HandleFunc("GET /sw.js", s.serviceWorker)
	m.HandleFunc("GET /static/{name}", func(w http.ResponseWriter, r *http.Request) { s.asset(w, r.PathValue("name")) })
	s.mux = m
}

// ServeHTTP applies the owner gate, the CSRF and body rules, and the security headers.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	// "same-origin", not "no-referrer": under "no-referrer" a browser submits forms with
	// Origin "null", which the Origin check below refuses. Other sites still get nothing.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")

	// The editor is tailnet-only. Funnel puts a service on the public internet, so a request
	// that arrives through it is refused before any other check.
	if r.Header.Get("Tailscale-Funnel-Request") != "" {
		plain(w, http.StatusForbidden, "Forbidden: this editor is not available through Funnel.")
		return
	}
	if !policy.EditorAllowed(r.Header.Get("Tailscale-User-Login"), s.cfg.Owner) {
		plain(w, http.StatusForbidden, "Forbidden: this editor is private to its owner.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("Origin") != s.cfg.Origin {
			plain(w, http.StatusForbidden, "Forbidden: request did not come from the editor.")
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || (mt != "application/x-www-form-urlencoded" && mt != "application/json") {
			plain(w, http.StatusUnsupportedMediaType, "Unsupported content type.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	}
	s.mux.ServeHTTP(w, r)
}

func plain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintln(w, msg)
}

var assetTypes = map[string]string{
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".png":         "image/png",
	".webmanifest": "application/manifest+json",
}

func (s *server) asset(w http.ResponseWriter, name string) {
	b, ok := s.statics[name]
	if !ok || name == "manifest.webmanifest" {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", assetTypes[name[strings.LastIndex(name, "."):]])
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(b)
}

func (s *server) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(s.statics["manifest.webmanifest"])
}

func (s *server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(s.swBody)
}

// page is the data every template receives.
type page struct {
	Title string
	Nav   string
	Msg   string
	Err   string
	Data  map[string]any
}

var doneMessages = map[string]string{
	"saved":     "Saved.",
	"created":   "Note saved.",
	"forgotten": "Forgotten.",
	"updated":   "Memory updated.",
	"reviewed":  "Review saved.",
}

func (s *server) render(w http.ResponseWriter, r *http.Request, code int, name string, p page) {
	if p.Msg == "" {
		p.Msg = doneMessages[r.URL.Query().Get("done")]
	}
	var buf strings.Builder
	if err := s.tmpl.ExecuteTemplate(&buf, name, p); err != nil {
		plain(w, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		fmt.Fprint(w, buf.String())
	}
}

func (s *server) problem(w http.ResponseWriter, r *http.Request, code int, msg string) {
	s.render(w, r, code, "error", page{Title: http.StatusText(code), Data: map[string]any{"Message": msg}})
}

// fail maps a store error to a status and a plain message.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, memoryapi.ErrNotFound):
		s.problem(w, r, http.StatusNotFound, "That item was not found. It may have been forgotten.")
	case errors.Is(err, memoryapi.ErrInvalid):
		s.problem(w, r, http.StatusBadRequest, "The memory service did not accept that change.")
	case errors.Is(err, memoryapi.ErrScope):
		s.problem(w, r, http.StatusForbidden, "That scope is not permitted.")
	case errors.Is(err, memoryapi.ErrConflict):
		s.problem(w, r, http.StatusConflict, "That item changed elsewhere. Reload and try again.")
	default:
		s.problem(w, r, http.StatusInternalServerError, "The memory service is not responding. Try again in a moment.")
	}
}

func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// bodyTooLarge reports whether err came from the 64 KiB request limit.
func bodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
