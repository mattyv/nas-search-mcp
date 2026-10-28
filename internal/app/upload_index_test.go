package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/store"
)

func TestScanFailurePreservesConcurrentUploadedIndex(t *testing.T) {
	a := pacingTestApp(t)
	path := filepath.Join(a.Root, "new.txt")
	write(t, path, "uploaded content")
	hash, err := fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(t.TempDir(), "worker.sh")
	if err := os.WriteFile(worker, []byte("sleep 0.3\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a.Python, a.Worker = "/bin/sh", worker
	a.modelKeepalive = 0
	done := make(chan error, 1)
	go func() { done <- a.Scan(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := a.Status()
		if err != nil {
			t.Fatal(err)
		}
		if status["scan_stage"] == "extracting" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not start extracting")
		}
		time.Sleep(time.Millisecond)
	}
	if err := a.Store.Replace(store.File{Path: "new.txt", Fingerprint: a.indexedFingerprint("new.txt", hash), Kind: "document"}, []store.Chunk{{Location: "text", Text: "uploaded content"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	fingerprints, err := a.Store.Fingerprints()
	if err != nil || fingerprints["new.txt"] != a.indexedFingerprint("new.txt", hash) {
		t.Fatalf("concurrent index lost: %v, %v", fingerprints, err)
	}
}

func TestIndexUploadedSkipsFullScanPacing(t *testing.T) {
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "Documents", "Uploads", "new.txt"), "fresh upload phrase")
	if err := a.ConfigureAccess([]string{"Documents"}, nil); err != nil {
		t.Fatal(err)
	}
	a.SetScanFileDelay(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.IndexUploaded(ctx, "Documents/Uploads/new.txt"); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Search(ctx, "fresh upload phrase", "", false, 10)
	if err != nil || len(hits.Hits) != 1 {
		t.Fatalf("new upload not searchable: %+v, %v", hits, err)
	}
	if err := a.IndexUploaded(ctx, "Documents/Uploads/new.txt"); err != nil {
		t.Fatal(err)
	}
	hits, err = a.Search(ctx, "fresh upload phrase", "", false, 10)
	if err != nil || len(hits.Hits) != 1 {
		t.Fatalf("second index changed search: %+v, %v", hits, err)
	}
}

func TestIndexUploadedRejectsEscapesAndDeniedFiles(t *testing.T) {
	a := scanApp(t)
	write(t, filepath.Join(a.Root, "Documents", "Uploads", "ok.txt"), "allowed")
	write(t, filepath.Join(a.Root, "Documents", "Private", "secret.txt"), "secret")
	if err := a.ConfigureAccess([]string{"Documents"}, []string{"Documents/Private"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(a.Root, "Documents", "Private", "secret.txt"), filepath.Join(a.Root, "Documents", "Uploads", "link.txt")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../Documents/Uploads/ok.txt", "Documents/Private/secret.txt", "Documents/Uploads/link.txt"} {
		if err := a.IndexUploaded(context.Background(), path); err == nil {
			t.Fatalf("indexed forbidden path %q", path)
		}
	}
}
