package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/store"
)

func pacingTestApp(t *testing.T) *App {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "files")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.New(filepath.Join(base, "index.sqlite3"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &App{Root: root, Data: base, Store: db, workerGate: make(chan struct{}, 1)}
}

func TestScanDelayCancellationStopsBeforeTouchingFile(t *testing.T) {
	a := pacingTestApp(t)
	if err := a.Store.Replace(store.File{Path: "removed.txt", Fingerprint: "old", Kind: "document"}, []store.Chunk{{Location: "text", Text: "preserve me"}}, nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.Root, "a.txt"), "new content")
	if err := os.WriteFile(filepath.Join(a.Data, "extraction-revision"), []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	a.SetScanFileDelay(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Scan(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		status, err := a.Status()
		if err != nil {
			t.Fatal(err)
		}
		if status["scan_stage"] == "pacing" {
			cancel()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("scan never entered pacing stage: %v", status)
		}
		time.Sleep(time.Millisecond)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan error = %v, want context cancellation", err)
	}
	stats, err := a.Store.Status()
	if err != nil || stats.Files != 1 || stats.ErrorCount != 0 {
		t.Fatalf("index changed during pacing cancellation: %+v, %v", stats, err)
	}
	if revision, err := os.ReadFile(filepath.Join(a.Data, "extraction-revision")); err != nil || string(revision) != "previous" {
		t.Fatalf("extraction revision = %q, %v", revision, err)
	}
}

func TestScanCancellationDuringIndexingIsNotRecordedAsDocumentError(t *testing.T) {
	a := pacingTestApp(t)
	if err := a.Store.Replace(store.File{Path: "a.txt", Fingerprint: "old", Kind: "document"}, []store.Chunk{{Location: "text", Text: "old content"}}, nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.Root, "a.txt"), "changed content")
	worker := filepath.Join(t.TempDir(), "worker.sh")
	if err := os.WriteFile(worker, []byte("sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a.Python = "/bin/sh"
	a.Worker = worker
	a.modelKeepalive = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Scan(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := a.Status()
		if err != nil {
			t.Fatal(err)
		}
		if status["scan_stage"] == "extracting" {
			cancel()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("worker never started: %v", status)
		}
		time.Sleep(time.Millisecond)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan error = %v, want context cancellation", err)
	}
	stats, err := a.Store.Status()
	if err != nil || stats.ErrorCount != 0 || stats.Files != 0 {
		t.Fatalf("cancellation recorded as document error: %+v, %v", stats, err)
	}
}
