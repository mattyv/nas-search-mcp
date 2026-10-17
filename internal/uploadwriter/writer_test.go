package uploadwriter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattyv/nas-search-mcp/internal/upload"
)

func TestHandlerWritesOnlyAuthorizedDescendants(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []string{"docs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(dest, name, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/save", strings.NewReader(body))
		r.Header.Set("X-Destination", dest)
		r.Header.Set("X-Filename", name)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("docs", "note.txt", "hello"); w.Code != 201 {
		t.Fatalf("save status %d: %s", w.Code, w.Body)
	}
	got, err := os.ReadFile(filepath.Join(root, "docs", "note.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("saved bytes %q, %v", got, err)
	}
	for _, tc := range []struct{ dest, name string }{
		{"", "note.txt"}, {"..", "note.txt"}, {"docs/../", "note.txt"}, {"docs", ".hidden"}, {"docs", "../escape.txt"},
	} {
		if w := request(tc.dest, tc.name, "no"); w.Code < 400 {
			t.Errorf("accepted %q/%q", tc.dest, tc.name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped root: %v", err)
	}
}

func TestHandlerRejectsExcludedAndTraversalOnlyDirectories(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"docs/public", "docs/node_modules", "docs/@eaDir", "docss"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	h, err := NewHandler(root, []string{"docs/public"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"docs", "docs/node_modules", "docs/@eaDir", "docss"} {
		r := httptest.NewRequest("POST", "/save", strings.NewReader("no"))
		r.Header.Set("X-Destination", dest)
		r.Header.Set("X-Filename", "a.txt")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatalf("upload allowed into %q", dest)
		}
	}
	h2, err := NewHandler(root, []string{"docs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"docs/node_modules", "docs/@eaDir"} {
		r := httptest.NewRequest("POST", "/save", strings.NewReader("no"))
		r.Header.Set("X-Destination", dest)
		r.Header.Set("X-Filename", "a.txt")
		w := httptest.NewRecorder()
		h2.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatalf("upload allowed into excluded %q", dest)
		}
	}
}

func TestHandlerAllowsOnlyOneActiveUpload(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []string{"docs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		r := httptest.NewRequest("POST", "/save", reader)
		r.Header.Set("X-Destination", "docs")
		r.Header.Set("X-Filename", "first.txt")
		close(started)
		h.ServeHTTP(httptest.NewRecorder(), r)
		close(done)
	}()
	<-started
	// Wait until the first request has started reading its body.
	if _, err := writer.Write([]byte("begin")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/save", strings.NewReader("second"))
	r.Header.Set("X-Destination", "docs")
	r.Header.Set("X-Filename", "second.txt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("parallel upload status=%d retry=%q", w.Code, w.Header().Get("Retry-After"))
	}
	writer.Close()
	<-done
}

func TestWriterHealthDoesNotRequireOrStartUpload(t *testing.T) {
	root := t.TempDir()
	h, err := NewHandler(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health status=%d", w.Code)
	}
}

func TestHandlerRejectsDeniedAndSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "link")); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []string{"docs"}, []string{"docs/private"})
	if err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"docs/private", "docs/link"} {
		r := httptest.NewRequest("POST", "/save", strings.NewReader("safe"))
		r.Header.Set("X-Destination", dest)
		r.Header.Set("X-Filename", "a.txt")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Errorf("accepted destination %q", dest)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote through symlink: %v", err)
	}
}

func TestHandlerEnforcesBodyLimitAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []string{"docs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/save", bytes.NewReader(body))
		r.Header.Set("X-Destination", "docs")
		r.Header.Set("X-Filename", "a.txt")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call([]byte("first")); w.Code != 201 {
		t.Fatalf("first write: %d %s", w.Code, w.Body)
	}
	if w := call([]byte("second")); w.Code < 400 {
		t.Fatalf("overwrote existing file: %d", w.Code)
	}
	if w := call(bytes.Repeat([]byte{'x'}, int(upload.MaxSize+1))); w.Code < 400 {
		t.Fatalf("accepted oversized body: %d", w.Code)
	}
}

func TestClientUsesUnixSocket(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp(".", ".uploadwriter-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "writer.sock")
	ln, err := net.Listen("unix", socket)
	if errors.Is(err, os.ErrPermission) {
		t.Skip("sandbox does not permit Unix socket creation")
	}
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []string{"docs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = h.(*handler).root.Close() })
	if err := (Client{Socket: socket}).Ping(context.Background()); err != nil {
		t.Fatalf("writer health: %v", err)
	}
	got, err := (Client{Socket: socket}).Save(context.Background(), "docs", "client.md", strings.NewReader("hello"))
	if err != nil || got != (upload.Result{Name: "client.md", Size: 5}) {
		t.Fatalf("client save: %+v, %v", got, err)
	}
	if _, err := (Client{Socket: socket}).Save(context.Background(), "docs", "client.md", strings.NewReader("other")); !errors.Is(err, upload.ErrExists) {
		t.Fatalf("duplicate save: %v", err)
	}
}
