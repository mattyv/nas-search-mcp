package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(root, "allowed.txt"), []byte("hello"), 0600)
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	a := &App{Root: root}
	for _, p := range []string{"../secret.txt", "/etc/passwd", "escape/secret.txt", "a/../allowed.txt", `a\b`, ""} {
		if _, err := a.safeFile(p); err == nil {
			t.Errorf("accepted forbidden path %q", p)
		}
	}
	if _, err := a.safeFile("allowed.txt"); err != nil {
		t.Fatal(err)
	}
}
func TestAuthentication(t *testing.T) {
	handler := Protect("long-test-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		header string
		status int
	}{{"", 401}, {"Bearer wrong", 401}, {"Bearer long-test-token", 204}} {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.Header.Set("Authorization", tc.header)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Fatalf("got %d want %d", rr.Code, tc.status)
		}
	}
}
func TestReadLimit(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("abcdef"), 0600)
	a := &App{Root: root}
	got, err := a.ReadFile("a.txt", 3)
	if err != nil || got != "abc" {
		t.Fatalf("got %q %v", got, err)
	}
}

func scanApp(t *testing.T) *App {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "files")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	python := os.Getenv("NAS_PYTHON")
	if python == "" {
		python = "../../.venv/bin/python"
	}
	python, e := exec.LookPath(python)
	if e == nil {
		python, e = filepath.Abs(python)
	}
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(python); e != nil {
		t.Skip("set NAS_PYTHON for worker integration tests")
	}
	worker, e := filepath.Abs("../../worker/worker.py")
	if e != nil {
		t.Fatal(e)
	}
	a, e := New(root, filepath.Join(base, "data"), python, worker, "", false, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Store.Close() })
	return a
}
func write(t *testing.T, p, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestScanLifecycle(t *testing.T) {
	a := scanApp(t)
	ctx := context.Background()
	write(t, filepath.Join(a.Root, "a.txt"), "renovation kitchen")
	write(t, filepath.Join(a.Root, ".git", "hidden.txt"), "renovation")
	write(t, filepath.Join(a.Root, "node_modules", "package.txt"), "renovation")
	write(t, filepath.Join(a.Root, "backup.zip"), "renovation")
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "renovation")
	if e := os.Symlink(outside, filepath.Join(a.Root, "escape")); e != nil {
		t.Fatal(e)
	}
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := a.Search(ctx, "renovation", "", false, 10)
	if e != nil || len(hits.Hits) != 1 {
		t.Fatal(hits, e)
	}
	firstID := hits.Hits[0].ID
	if e = a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e = a.Search(ctx, "renovation", "", false, 10)
	if e != nil || len(hits.Hits) != 1 || hits.Hits[0].ID != firstID {
		t.Fatal("unchanged scan replaced chunks", hits, e)
	}
	write(t, filepath.Join(a.Root, "a.txt"), "garden flowers")
	if e = a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e = a.Search(ctx, "renovation", "", false, 10)
	if e != nil || len(hits.Hits) != 0 {
		t.Fatal("stale old text", hits, e)
	}
	hits, e = a.Search(ctx, "flowers", "", false, 10)
	if e != nil || len(hits.Hits) != 1 {
		t.Fatal(hits, e)
	}
	if e = os.Remove(filepath.Join(a.Root, "a.txt")); e != nil {
		t.Fatal(e)
	}
	hits, e = a.Search(ctx, "flowers", "", false, 10)
	if e != nil || len(hits.Hits) != 0 {
		t.Fatal("deleted before scan", hits, e)
	}
	if e = a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	stats, e := a.Store.Status()
	if e != nil || stats.Files != 0 || stats.Chunks != 0 {
		t.Fatal(stats, e)
	}
}
func TestOversizedModificationRemovesStaleChunks(t *testing.T) {
	a := scanApp(t)
	ctx := context.Background()
	p := filepath.Join(a.Root, "a.txt")
	write(t, p, "old stale text")
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if e := os.Truncate(p, 32*1024*1024+1); e != nil {
		t.Fatal(e)
	}
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := a.Search(ctx, "stale", "", false, 10)
	if e != nil || len(hits.Hits) != 0 {
		t.Fatal("oversized changed file still searchable", hits, e)
	}
}

func TestFailedModificationDropsOldChunksAndReportsError(t *testing.T) {
	a := scanApp(t)
	ctx := context.Background()
	p := filepath.Join(a.Root, "a.txt")
	write(t, p, "obsolete content")
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	write(t, p, "new content")
	a.Worker = filepath.Join(t.TempDir(), "missing-worker.py")
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := a.Search(ctx, "obsolete", "", false, 10)
	if e != nil || len(hits.Hits) != 0 {
		t.Fatal(hits, e)
	}
	stats, e := a.Store.Status()
	if e != nil || stats.ErrorCount != 1 || len(stats.Errors) != 1 || stats.Errors[0].Path != "a.txt" {
		t.Fatal(stats, e)
	}
}
func TestConfinedReadRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "secret")
	if e := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt")); e != nil {
		t.Fatal(e)
	}
	a := &App{Root: root}
	if _, e := a.ReadFile("escape.txt", 100); e == nil {
		t.Fatal("read escaped root")
	}
	if _, e := a.ReadFile("../secret.txt", 100); e == nil {
		t.Fatal("accepted traversal")
	}
}
func TestSearchReturnsSortedBoundedHits(t *testing.T) {
	a := scanApp(t)
	ctx := context.Background()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(a.Root, name), "needle common content")
	}
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := a.Search(ctx, "needle", "", false, 2)
	if e != nil || len(hits.Hits) != 2 {
		t.Fatal(hits, e)
	}
	for i := 1; i < len(hits.Hits); i++ {
		before, after := hits.Hits[i-1], hits.Hits[i]
		if before.Score < after.Score || (before.Score == after.Score && before.ID > after.ID) {
			t.Fatal("unsorted hits", hits)
		}
	}
}
