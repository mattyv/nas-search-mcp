// Package uploadwriter exposes a narrow local socket for append-only uploads.
// It is an unverified operating-system boundary; path checks are regression tested.
package uploadwriter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
	"github.com/mattyv/nas-search-mcp/internal/upload"
)

var ErrRejected = errors.New("upload rejected")

type Client struct{ Socket string }

func (c Client) Ping(ctx context.Context) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", c.Socket)
	}}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://uploadwriter/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrRejected
	}
	return nil
}

// Save sends one file to the isolated writer over its Unix socket.
func (c Client) Save(ctx context.Context, dest, filename string, src io.Reader) (upload.Result, error) {
	if src == nil {
		return upload.Result{}, upload.ErrInvalid
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		_, err := io.Copy(pw, io.LimitReader(src, upload.MaxSize+1))
		_ = pw.CloseWithError(err)
	}()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.Socket)
	}}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://uploadwriter/save", pr)
	if err != nil {
		pr.Close()
		return upload.Result{}, err
	}
	req.Header.Set("X-Destination", dest)
	req.Header.Set("X-Filename", filename)
	resp, err := (&http.Client{Transport: tr, Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return upload.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		if resp.StatusCode == http.StatusConflict {
			return upload.Result{}, upload.ErrExists
		}
		if resp.StatusCode == http.StatusRequestEntityTooLarge {
			return upload.Result{}, upload.ErrTooLarge
		}
		return upload.Result{}, ErrRejected
	}
	var result upload.Result
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&result); err != nil {
		return upload.Result{}, err
	}
	return result, nil
}

// NewHandler creates the writer's only HTTP endpoint. Allow and deny entries
// are normalized directory prefixes relative to root; an accepted file must
// be a strict descendant of an allow entry and no descendant of a deny entry.
func NewHandler(root string, allow, deny []string) (http.Handler, error) {
	if !filepath.IsAbs(root) {
		return nil, upload.ErrInvalid
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, upload.ErrInvalid
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, upload.ErrInvalid
	}
	a, err := normalizeRules(allow)
	if err != nil {
		r.Close()
		return nil, err
	}
	d, err := normalizeRules(deny)
	if err != nil {
		r.Close()
		return nil, err
	}
	return &handler{root: r, allow: a, deny: d, active: make(chan struct{}, 1)}, nil
}

type handler struct {
	root        *os.Root
	allow, deny []string
	active      chan struct{}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/save" {
		http.NotFound(w, r)
		return
	}
	select {
	case h.active <- struct{}{}:
		defer func() { <-h.active }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "writer busy", http.StatusTooManyRequests)
		return
	}
	dest, name := r.Header.Get("X-Destination"), r.Header.Get("X-Filename")
	if !validRelativeDir(dest) || !validFilename(name) {
		http.Error(w, "invalid upload", 400)
		return
	}
	target := path.Join(dest, name)
	if !strictlyAllowed(target, h.allow) || matchesPrefix(target, h.deny) {
		http.Error(w, "upload path is not allowed", 403)
		return
	}
	dir, err := h.openDirectory(dest)
	if err != nil {
		http.Error(w, "invalid upload directory", 400)
		return
	}
	defer dir.Close()
	limited := http.MaxBytesReader(w, r.Body, upload.MaxSize)
	result, err := upload.SaveRoot(dir, name, limited, upload.MaxSize)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.Is(err, upload.ErrTooLarge) || errors.As(err, &maxErr) {
			http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
			return
		}
		if errors.Is(err, upload.ErrExists) {
			http.Error(w, "file already exists", http.StatusConflict)
			return
		}
		if errors.Is(err, upload.ErrInvalid) {
			http.Error(w, "invalid upload", http.StatusBadRequest)
			return
		}
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(result)
}

func (h *handler) openDirectory(rel string) (*os.Root, error) {
	// Re-open from the configured anchor on each request, checking each entry
	// through the current anchored fd before descending one component at a time.
	cur, err := h.root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" {
			cur.Close()
			return nil, upload.ErrInvalid
		}
		info, e := cur.Lstat(part)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			cur.Close()
			return nil, upload.ErrInvalid
		}
		next, e := cur.OpenRoot(part)
		if e != nil {
			cur.Close()
			return nil, upload.ErrInvalid
		}
		opened, e := next.Stat(".")
		cur.Close()
		if e != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, upload.ErrInvalid
		}
		cur = next
	}
	return cur, nil
}

// Run serves requests until the process is interrupted. It removes a stale
// socket only when the path is itself a Unix socket, then owns the new socket.
func Run(socket, root string, allow, deny []string) error {
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("socket path must be absolute")
	}
	if _, err := os.Lstat(socket); err == nil {
		info, err := os.Lstat(socket)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("socket path occupied")
		}
		conn, dialErr := net.DialTimeout("unix", socket, 250*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return fmt.Errorf("writer socket already active")
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	h, err := NewHandler(root, allow, deny)
	if err != nil {
		return err
	}
	defer h.(*handler).root.Close()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	created, err := os.Lstat(socket)
	if err != nil {
		ln.Close()
		return err
	}
	defer func() {
		_ = ln.Close()
		if current, statErr := os.Lstat(socket); statErr == nil && os.SameFile(created, current) {
			_ = os.Remove(socket)
		}
	}()
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	return srv.Serve(ln)
}

func normalizeRules(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, rule := range in {
		if !validRelativeDir(rule) {
			return nil, upload.ErrInvalid
		}
		out = append(out, rule)
	}
	return out, nil
}
func validRelativeDir(s string) bool {
	if len(s) > 4096 || !policy.ValidRelative(s) || path.Clean(s) != s {
		return false
	}
	for _, c := range strings.Split(s, "/") {
		if c == "" || c == "." || c == ".." || policy.SourceComponentExcluded(c) {
			return false
		}
	}
	return true
}
func validFilename(s string) bool {
	return s != "" && path.Base(s) == s && !strings.ContainsAny(s, "/\\") && !strings.HasPrefix(s, ".") && s != ".."
}
func strictlyAllowed(target string, rules []string) bool {
	for _, r := range rules {
		if strings.HasPrefix(target, r+"/") {
			return true
		}
	}
	return false
}
func matchesPrefix(target string, rules []string) bool {
	for _, r := range rules {
		if target == r || strings.HasPrefix(target, r+"/") {
			return true
		}
	}
	return false
}
