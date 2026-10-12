package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/mattyv/nas-search-mcp/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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

func TestReadBinaryIndependentOfRequestedLimit(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "project.cfg"), []byte{'A', 'B', 0, 1, 2}, 0600)
	a := &App{Root: root}
	for _, limit := range []int{-1, 1, 2, 16, 65536} {
		if text, err := a.ReadFile("project.cfg", limit); err == nil {
			t.Fatalf("limit %d exposed binary content %q", limit, text)
		}
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

func TestFetchOriginalBinaryRanges(t *testing.T) {
	root := t.TempDir()
	a := &App{Root: root}
	content := []byte("%PDF-1.7\noriginal\x00binary\xffpayload")
	p := filepath.Join(root, "original.pdf")
	if e := os.WriteFile(p, content, 0600); e != nil {
		t.Fatal(e)
	}
	var combined []byte
	var offset int64
	version := ""
	for {
		part, e := a.FetchFile("original.pdf", offset, 7, version)
		if e != nil {
			t.Fatal(e)
		}
		if part.Size != int64(len(content)) || part.MIME != "application/pdf" || part.Offset != offset {
			t.Fatal(part)
		}
		if version == "" {
			version = part.Version
		}
		decoded, e := base64.StdEncoding.DecodeString(part.Data)
		if e != nil {
			t.Fatal(e)
		}
		combined = append(combined, decoded...)
		if part.NextOffset == nil {
			if !part.Complete {
				t.Fatal("missing completion")
			}
			break
		}
		offset = *part.NextOffset
	}
	if !bytes.Equal(combined, content) {
		t.Fatal("original bytes changed", combined)
	}
	for _, offset := range []int64{-1, int64(len(content) + 1)} {
		if _, e := a.FetchFile("original.pdf", offset, 7, ""); e == nil {
			t.Fatal("accepted invalid offset", offset)
		}
	}
	if _, e := a.FetchFile("../original.pdf", 0, 7, ""); e == nil {
		t.Fatal("accepted traversal")
	}
	if e := os.WriteFile(p, append(content, '!'), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := a.FetchFile("original.pdf", 0, 7, version); e == nil {
		t.Fatal("accepted changed file version")
	}
}
func TestFetchBoundsAndEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	a := &App{Root: root}
	write(t, filepath.Join(outside, "secret"), "secret")
	if e := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e := a.FetchFile("escape", 0, 1, ""); e == nil {
		t.Fatal("escaped root")
	}
	p := filepath.Join(root, "large.bin")
	if e := os.WriteFile(p, make([]byte, 300000), 0600); e != nil {
		t.Fatal(e)
	}
	out, e := a.FetchFile("large.bin", 0, 1000000, "")
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := base64.StdEncoding.DecodeString(out.Data)
	if e != nil || len(decoded) != 262144 {
		t.Fatal(len(decoded), e)
	}
	if e := os.Truncate(p, 32*1024*1024+1); e != nil {
		t.Fatal(e)
	}
	if _, e := a.FetchFile("large.bin", 0, 1, ""); e == nil {
		t.Fatal("accepted >32MiB source")
	}
}
func TestReadDocumentBeyondIndexCap(t *testing.T) {
	a := scanApp(t)
	text := strings.Repeat("α", 200005) + "TAIL"
	write(t, filepath.Join(a.Root, "long.txt"), text)
	out, e := a.ReadDocument(context.Background(), "long.txt", 200000, 9)
	if e != nil {
		t.Fatal(e)
	}
	if out.Text != "αααααTAIL" || out.Offset != 200000 || !out.Complete || out.NextOffset != nil {
		t.Fatal(out)
	}
	if _, e := a.ReadDocument(context.Background(), "long.txt", -1, 9); e == nil {
		t.Fatal("negative offset")
	}
	if _, e := a.ReadDocument(context.Background(), "../long.txt", 0, 9); e == nil {
		t.Fatal("traversal")
	}
}
func TestExtractionRevisionReindexesOnce(t *testing.T) {
	a := scanApp(t)
	ctx := context.Background()
	write(t, filepath.Join(a.Root, "a.txt"), "needle")
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	first, e := a.Search(ctx, "needle", "", false, 10)
	if e != nil || len(first.Hits) != 1 {
		t.Fatal(first, e)
	}
	if e := os.WriteFile(filepath.Join(a.Data, "extraction-revision"), []byte("old-version"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	second, e := a.Search(ctx, "needle", "", false, 10)
	if e != nil || len(second.Hits) != 1 || second.Hits[0].ID == first.Hits[0].ID {
		t.Fatal("did not refresh extractor revision", second, e)
	}
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	third, e := a.Search(ctx, "needle", "", false, 10)
	if e != nil || len(third.Hits) != 1 || third.Hits[0].ID != second.Hits[0].ID {
		t.Fatal("revision refreshed repeatedly", third, e)
	}
}

func TestCanceledScanDoesNotPromoteExtractorRevision(t *testing.T) {
	a := scanApp(t)
	marker := filepath.Join(a.Data, "extraction-revision")
	if e := os.WriteFile(marker, []byte("previous"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := a.Scan(ctx); e == nil {
		t.Fatal("cancelled scan succeeded")
	}
	revision, e := os.ReadFile(marker)
	if e != nil || string(revision) != "previous" {
		t.Fatal(string(revision), e)
	}
}
func TestReadDocumentPaginationAndCharacterCap(t *testing.T) {
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "a.txt"), "αβγδεζηθ")
	var text strings.Builder
	var offset int64
	for {
		out, e := a.ReadDocument(context.Background(), "a.txt", offset, 3)
		if e != nil {
			t.Fatal(e)
		}
		text.WriteString(out.Text)
		if out.NextOffset == nil {
			if !out.Complete {
				t.Fatal(out)
			}
			break
		}
		if *out.NextOffset <= offset {
			t.Fatal("no progress", out)
		}
		offset = *out.NextOffset
	}
	if text.String() != "αβγδεζηθ" {
		t.Fatal(text.String())
	}
	write(t, filepath.Join(a.Root, "cap.txt"), strings.Repeat("x", 21000))
	out, e := a.ReadDocument(context.Background(), "cap.txt", 0, 50000)
	if e != nil || len(out.Text) != 20000 || out.Complete || out.NextOffset == nil || *out.NextOffset != 20000 {
		t.Fatal(len(out.Text), out, e)
	}
}

func TestReadFileRejectsNegativeLimit(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "abcdef")
	a := &App{Root: root}
	if _, e := a.ReadFile("a.txt", -1); e == nil {
		t.Fatal("negative max_bytes accepted")
	}
	got, e := a.ReadFile("a.txt", 3)
	if e != nil || got != "abc" {
		t.Fatal(got, e)
	}
}
func TestBrowseOmitsMissingAndEscapingFiles(t *testing.T) {
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "present.txt"), "present")
	for _, path := range []string{"missing.txt", "present.txt", "escape.txt"} {
		if e := a.Store.Replace(store.File{Path: path, Kind: "text"}, nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "secret")
	if e := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(a.Root, "escape.txt")); e != nil {
		t.Fatal(e)
	}
	files, e := a.Browse("", 100)
	if e != nil || len(files) != 1 || files[0].Path != "present.txt" {
		t.Fatal(files, e)
	}
	files, e = a.Browse("missing", 100)
	if e != nil || len(files) != 0 {
		t.Fatal(files, e)
	}
}

func TestOpenFileRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pipe.txt")
	if e := syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	a := &App{Root: root}
	done := make(chan error, 1)
	go func() {
		f, e := a.openFile("pipe.txt")
		if f != nil {
			f.Close()
		}
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("FIFO accepted as regular source")
		}
	case <-time.After(250 * time.Millisecond):
		// Unblock the old implementation before failing, so no goroutine is left behind.
		fd, e := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if e != nil {
			t.Fatalf("FIFO open blocked; cleanup failed: %v", e)
		}
		syscall.Close(fd)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("FIFO cleanup did not release reader")
		}
		t.Fatal("FIFO read open blocked before regular-file validation")
	}
}

func TestReadDocumentForwardsForcedOCRAndDefaults(t *testing.T) {
	t.Setenv("NAS_OCR", "true")
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "a.PDF"), "source")
	worker := filepath.Join(t.TempDir(), "capture.py")
	script := `import json,sys
request=json.load(sys.stdin)
force=request['force_ocr']
assert isinstance(force,bool)
json.dump({'kind':'document','text':'forced' if force else 'automatic','sections':[],'offset':request['offset'],'next_offset':None,'complete':True,'metadata':{'received_force_ocr':force}},sys.stdout)
`
	if e := os.WriteFile(worker, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	a.Worker = worker
	out, e := a.ReadDocumentWithOCR(context.Background(), "a.PDF", 0, 20, true)
	if e != nil || out.Text != "forced" || out.Metadata["received_force_ocr"] != true {
		t.Fatal(out, e)
	}
	out, e = a.ReadDocument(context.Background(), "a.PDF", 0, 20)
	if e != nil || out.Text != "automatic" || out.Metadata["received_force_ocr"] != false {
		t.Fatal(out, e)
	}
}
func TestOCRStatusAndRevisionStayConsistent(t *testing.T) {
	a := scanApp(t)
	for _, tc := range []struct {
		setting string
		enabled bool
	}{{"", true}, {"true", true}, {"TRUE", true}, {"false", false}} {
		t.Setenv("NAS_OCR", tc.setting)
		status, e := a.Status()
		if e != nil || status["ocr"] != tc.enabled {
			t.Fatal(status, e)
		}
		want := fmt.Sprintf("v3;ocr=%t", tc.enabled)
		if revision := extractionRevision(); revision != want {
			t.Fatalf("revision=%s want %s", revision, want)
		}
	}
}

func TestForcedOCRRejectsDisabledAndNonPDFBeforeWorker(t *testing.T) {
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "a.pdf"), "source")
	write(t, filepath.Join(a.Root, "a.txt"), "source")
	a.Worker = filepath.Join(t.TempDir(), "missing-worker.py")
	t.Setenv("NAS_OCR", "false")
	if _, e := a.ReadDocumentWithOCR(context.Background(), "a.pdf", 0, 20, true); e == nil || !strings.Contains(e.Error(), "NAS_OCR") {
		t.Fatalf("expected OCR configuration error, got %v", e)
	}
	t.Setenv("NAS_OCR", "true")
	if _, e := a.ReadDocumentWithOCR(context.Background(), "a.txt", 0, 20, true); e == nil || !strings.Contains(e.Error(), "PDF-only") {
		t.Fatalf("expected PDF-only error, got %v", e)
	}
}

func TestScanStatusIdentifiesActiveFileAndStage(t *testing.T) {
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "a.txt"), "needle")
	marker := filepath.Join(t.TempDir(), "started")
	worker := filepath.Join(t.TempDir(), "slow.py")
	script := fmt.Sprintf(`import json,sys,time
from pathlib import Path
request=json.load(sys.stdin)
Path(%q).write_text('started')
time.sleep(0.3)
json.dump({'kind':'document','sections':[{'location':'text','text':'needle'}],'metadata':{}},sys.stdout)
`, marker)
	if e := os.WriteFile(worker, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	a.Worker = worker
	done := make(chan error, 1)
	go func() { done <- a.Scan(context.Background()) }()
	// Always let the scan finish, including when assertions fail.
	t.Cleanup(func() {
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("scan did not finish")
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, e := os.Stat(marker); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	status, e := a.Status()
	if e != nil {
		t.Fatal(e)
	}
	if status["scanning"] != true || status["current_file"] != "a.txt" || status["scan_stage"] != "extracting" {
		t.Fatal(status)
	}
	for _, field := range []string{"scan_started_at", "file_started_at"} {
		value, ok := status[field].(string)
		if !ok || value == "" {
			t.Fatal(field, status)
		}
		if _, e := time.Parse(time.RFC3339Nano, value); e != nil {
			t.Fatal(field, value, e)
		}
	}
	// Read final state after completion without consuming the cleanup's channel.
	deadline = time.Now().Add(2 * time.Second)
	for {
		status, e = a.Status()
		if e != nil {
			t.Fatal(e)
		}
		if status["scanning"] == false {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan stayed active")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if status["current_file"] != "" || status["scan_stage"] != "" || status["file_started_at"] != "" {
		t.Fatal("active progress retained after scan", status)
	}
}

func TestFindFilesLivePathsWithoutIndexOrWorker(t *testing.T) {
	root := t.TempDir()
	a := &App{Root: root, workerGate: make(chan struct{}, 1)}
	a.workerGate <- struct{}{}
	write(t, filepath.Join(root, "Berklee", "MÉLODY [Final].pdf"), "invalid PDF")
	write(t, filepath.Join(root, "Berklee", "unsupported.backup"), "binary archive")
	write(t, filepath.Join(root, "Other", "MÉLODY.pdf"), "invalid PDF")
	out, e := a.FindFiles(context.Background(), "berklee mélody [final]", "", 20)
	if e != nil || len(out.Files) != 1 || out.Files[0].Path != "Berklee/MÉLODY [Final].pdf" {
		t.Fatal(out, e)
	}
	if out.Files[0].Size != 11 || out.Files[0].ModifiedAt == "" {
		t.Fatal(out)
	}
	out, e = a.FindFiles(context.Background(), "unsupported", "Berklee/", 20)
	if e != nil || len(out.Files) != 1 {
		t.Fatal(out, e)
	}
	out, e = a.FindFiles(context.Background(), ".*", "", 20)
	if e != nil || len(out.Files) != 0 {
		t.Fatal("query must be literal", out, e)
	}
}
func TestFindFilesExclusionsAndValidation(t *testing.T) {
	root := t.TempDir()
	a := &App{Root: root}
	for _, name := range []string{".hidden/Passport.pdf", "node_modules/Passport.pdf", "@Recycle/Passport.pdf", "Passport.pdf"} {
		write(t, filepath.Join(root, name), "data")
	}
	outside := t.TempDir()
	write(t, filepath.Join(outside, "Passport.pdf"), "secret")
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(outside, "Passport.pdf"), filepath.Join(root, "Passport-link.pdf")); e != nil {
		t.Fatal(e)
	}
	out, e := a.FindFiles(context.Background(), "passport", "", 20)
	if e != nil || len(out.Files) != 1 || out.Files[0].Path != "Passport.pdf" {
		t.Fatal(out, e)
	}
	for _, prefix := range []string{"/", "/etc", "../", "escape/../", "a//b"} {
		if _, e = a.FindFiles(context.Background(), "passport", prefix, 20); e == nil {
			t.Fatal("accepted invalid prefix", prefix)
		}
	}
	for _, query := range []string{"", "   ", strings.Repeat("x", 257), strings.Repeat("x ", 17)} {
		if _, e = a.FindFiles(context.Background(), query, "", 20); e == nil {
			t.Fatal("accepted invalid query", query)
		}
	}
	if _, e = a.FindFiles(context.Background(), "passport", "", -1); e == nil {
		t.Fatal("accepted negative limit")
	}
}
func TestFindFilesBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	a := &App{Root: root}
	for i := 0; i < 4; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("match-%d.bin", i)), "data")
	}
	out, e := a.FindFiles(context.Background(), "match", "", 1)
	if e != nil || len(out.Files) != 1 || !out.Truncated || out.Warning == "" {
		t.Fatal(out, e)
	}
	out, e = a.findFiles(context.Background(), "match", "", 20, 2, time.Second)
	if e != nil || !out.Truncated || out.Warning == "" || out.Visited > 2 {
		t.Fatal(out, e)
	}
	out, e = a.findFiles(context.Background(), "match", "", 20, 100, time.Nanosecond)
	if e != nil || !out.Truncated || out.Warning == "" {
		t.Fatal(out, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = a.FindFiles(ctx, "match", "", 20); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestFindFilesUnreadableCandidateReportsIncomplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	root := t.TempDir()
	path := filepath.Join(root, "target.bin")
	write(t, path, "private")
	if e := os.Chmod(path, 0); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	out, e := (&App{Root: root}).FindFiles(context.Background(), "target", "", 20)
	if e != nil || len(out.Files) != 0 || !out.Truncated || out.Warning == "" {
		t.Fatal("unreadable matching file must not appear as a complete empty search", out, e)
	}
}
func TestFindFilesSkipsFIFOWithoutWarning(t *testing.T) {
	root := t.TempDir()
	if e := syscall.Mkfifo(filepath.Join(root, "target.fifo"), 0600); e != nil {
		t.Fatal(e)
	}
	out, e := (&App{Root: root}).FindFiles(context.Background(), "target", "", 20)
	if e != nil || len(out.Files) != 0 || out.Truncated || out.Warning != "" {
		t.Fatal(out, e)
	}
}
