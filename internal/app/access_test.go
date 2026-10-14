package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattyv/nas-search-mcp/internal/store"
)

func TestAccessDefaultDenyAndStaleIndex(t *testing.T) {
	a := accessApp(t)
	write(t, filepath.Join(a.Root, "Documents/open.txt"), "needle")
	write(t, filepath.Join(a.Root, "Documents/private/passport.txt"), "needle secret")
	for _, p := range []string{"Documents/open.txt", "Documents/private/passport.txt"} {
		if err := a.Store.Replace(store.File{Path: p, Kind: "document", Fingerprint: "old", Metadata: "secret"}, []store.Chunk{{Location: "text", Text: "needle secret"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Store.RecordError("Documents/private/passport.txt", "secret document content"); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureAccess(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadFile("Documents/open.txt", 100); err == nil {
		t.Fatal("empty allowlist granted access")
	}
	hits, err := a.Search(context.Background(), "needle", "", false, 20)
	if err != nil || len(hits.Hits) != 0 {
		t.Fatal(hits, err)
	}
	if err := a.ConfigureAccess([]string{"Documents"}, []string{"Documents/private"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../Documents/open.txt", "Documents/../Documents/open.txt", "Documents/private/passport.txt"} {
		if _, err := a.ReadFile(p, 100); err == nil {
			t.Fatal("granted", p)
		}
	}
	hits, err = a.Search(context.Background(), "needle", "", false, 20)
	if err != nil || len(hits.Hits) != 1 || hits.Hits[0].Path != "Documents/open.txt" {
		t.Fatal(hits, err)
	}
	files, err := a.Browse("", 20)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	found, err := a.FindFiles(context.Background(), "txt", "", 20)
	if err != nil || len(found.Files) != 1 {
		t.Fatal(found, err)
	}
	status, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "passport") || strings.Contains(string(encoded), "secret") {
		t.Fatal("status exposed denied data")
	}
}

func TestAccessRejectsSymlinksAndInvalidFolders(t *testing.T) {
	a := accessApp(t)
	write(t, filepath.Join(a.Root, "Documents/private/secret.txt"), "secret")
	write(t, filepath.Join(a.Root, "Documents/public/ok.txt"), "ok")
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "outside")
	for _, pair := range [][2]string{{"Documents/public/link.txt", filepath.Join(a.Root, "Documents/private/secret.txt")}, {"Documents/public/dir", filepath.Join(a.Root, "Documents/private")}, {"Documents/public/outside", outside}} {
		if err := os.Symlink(pair[1], filepath.Join(a.Root, pair[0])); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ConfigureAccess([]string{"Documents/public"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Documents/public/link.txt", "Documents/public/dir/secret.txt", "Documents/public/outside/secret.txt"} {
		if _, err := a.ReadFile(p, 100); err == nil {
			t.Fatal("symlink granted", p)
		}
		if _, err := a.ReadDocument(context.Background(), p, 0, 100); err == nil {
			t.Fatal("worker symlink granted", p)
		}
	}
	for _, p := range []string{"../Documents", "/Documents", "Documents/../Documents", "Documents/public/dir", "missing", "Documents/public/ok.txt"} {
		if err := a.ConfigureAccess([]string{p}, nil); err == nil {
			t.Fatal("invalid allow", p)
		}
	}
	if _, err := a.ReadFile("Documents/public/ok.txt", 100); err != nil {
		t.Fatal("failed config changed previous access", err)
	}
}

func accessApp(t *testing.T) *App {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "files")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(root, filepath.Join(base, "data"), "missing-python", "missing-worker", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Store.Close() })
	return a
}

func TestAccessSnapshotPinsAuthorizedBytes(t *testing.T) {
	a := accessApp(t)
	write(t, filepath.Join(a.Root, "Documents/public/ok.txt"), "allowed bytes")
	write(t, filepath.Join(a.Root, "Documents/private/secret.txt"), "secret bytes")
	if err := a.ConfigureAccess([]string{"Documents/public"}, []string{"Documents/private"}); err != nil {
		t.Fatal(err)
	}
	snapshot, _, cleanup, err := a.snapshot("Documents/public/ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasPrefix(snapshot, a.Data+string(os.PathSeparator)) {
		t.Fatal("snapshot outside private data")
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("snapshot permissions", info, err)
	}
	source := filepath.Join(a.Root, "Documents/public/ok.txt")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(a.Root, "Documents/private/secret.txt"), source); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(snapshot)
	if err != nil || string(content) != "allowed bytes" {
		t.Fatal("worker source was not pinned", string(content), err)
	}
	if _, err := a.ReadFile("Documents/public/ok.txt", 100); err == nil {
		t.Fatal("raced source symlink accepted")
	}
	cleanup()
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatal("snapshot not removed")
	}
}

func TestAccessScanIndexesOnlyAllowedFolders(t *testing.T) {
	a := accessApp(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	a.Python = python
	a.Worker, err = filepath.Abs("../../worker/worker.py")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.Root, "Documents/open.txt"), "needle")
	write(t, filepath.Join(a.Root, "Documents/private/secret.txt"), "needle secret")
	if err := a.ConfigureAccess([]string{"Documents"}, []string{"Documents/private"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := a.Store.Fingerprints()
	if err != nil || len(entries) != 1 || entries["Documents/open.txt"] == "" {
		t.Fatal(entries, err)
	}
	if err := a.ConfigureAccess(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err = a.Store.Fingerprints()
	if err != nil || len(entries) != 0 {
		t.Fatal("default deny left index entries", entries, err)
	}
	snapshots, err := filepath.Glob(filepath.Join(a.Data, ".source-*"))
	if err != nil || len(snapshots) != 0 {
		t.Fatal("scan left snapshots", snapshots, err)
	}
}

func TestHiddenFilesAndExcludedFoldersCannotBeReadDirectly(t *testing.T) {
	a := accessApp(t)
	write(t, filepath.Join(a.Root, "Code/.env"), "test-only secret")
	write(t, filepath.Join(a.Root, "Code/build/secret.txt"), "test-only secret")
	write(t, filepath.Join(a.Root, "Code/visible.txt"), "safe")
	if err := a.ConfigureAccess([]string{"Code"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Code/.env", "Code/build/secret.txt"} {
		if _, err := a.ReadFile(path, 100); err == nil {
			t.Fatal("private or excluded source readable", path)
		}
		if _, err := a.FetchFile(path, 0, 100, ""); err == nil {
			t.Fatal("private or excluded source downloadable", path)
		}
	}
	if _, err := a.ReadFile("Code/visible.txt", 100); err != nil {
		t.Fatal(err)
	}
}
