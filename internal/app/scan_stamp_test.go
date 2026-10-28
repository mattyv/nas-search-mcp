package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type stampRig struct {
	a    *App
	scan func() []string
}

func newStampRig(t *testing.T, files map[string]string) stampRig {
	t.Helper()
	extension := os.Getenv("NAS_VEC_EXTENSION")
	if extension == "" {
		t.Skip("set NAS_VEC_EXTENSION for vector integration tests")
	}
	t.Setenv("NAS_MODEL_KEEPALIVE_MINUTES", "0")
	base := t.TempDir()
	root := filepath.Join(base, "files")
	for name, text := range files {
		write(t, filepath.Join(root, name), text)
	}
	worker := filepath.Join(base, "fake_worker.py")
	write(t, worker, fakeWorker)
	logPath := filepath.Join(base, "worker.log")
	t.Setenv("FAKE_WORKER_LOG", logPath)
	a, e := New(root, filepath.Join(base, "data"), "python3", worker, extension, true, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	scan := func() []string {
		t.Helper()
		os.Remove(logPath)
		if e := a.Scan(context.Background()); e != nil {
			t.Fatal(e)
		}
		b, _ := os.ReadFile(logPath)
		if len(b) == 0 {
			return nil
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		sort.Strings(lines)
		return lines
	}
	return stampRig{a, scan}
}

func TestUnchangedFileIsNotCopiedOrExtracted(t *testing.T) {
	r := newStampRig(t, map[string]string{"notes.txt": "kitchen plans"})
	if first := r.scan(); len(first) == 0 {
		t.Fatal("first scan did not extract")
	}
	// Watch the data dir for snapshot temp files while the second scan runs.
	stop, seen := make(chan struct{}), make(chan string, 1)
	go func() {
		defer close(seen)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if m, _ := filepath.Glob(filepath.Join(r.a.Data, ".source-*")); len(m) > 0 {
				seen <- m[0]
				return
			}
		}
	}()
	again := r.scan()
	close(stop)
	if name, ok := <-seen; ok && name != "" {
		t.Fatal("unchanged file was copied to", name)
	}
	if again != nil {
		t.Fatal("unchanged file reached the worker:", again)
	}
}

func TestContentChangeWithNewMtimeReindexes(t *testing.T) {
	r := newStampRig(t, map[string]string{"notes.txt": "kitchen plans"})
	r.scan()
	p := filepath.Join(r.a.Root, "notes.txt")
	write(t, p, "bathroom plans")
	future := time.Now().Add(time.Hour)
	if e := os.Chtimes(p, future, future); e != nil {
		t.Fatal(e)
	}
	if got := r.scan(); strings.Join(got, "|") != "embed|extract .txt" {
		t.Fatal("changed content was not reindexed:", got)
	}
}

func TestMtimeOnlyChangeUpdatesStampWithoutExtraction(t *testing.T) {
	r := newStampRig(t, map[string]string{"notes.txt": "kitchen plans"})
	r.scan()
	p := filepath.Join(r.a.Root, "notes.txt")
	info, _ := os.Stat(p)
	future := time.Now().Add(time.Hour)
	if e := os.Chtimes(p, future, future); e != nil {
		t.Fatal(e)
	}
	if got := r.scan(); got != nil {
		t.Fatal("identical content was extracted again:", got)
	}
	idx, e := r.a.Store.Indexed()
	if e != nil || idx["notes.txt"].Stamp == "" || idx["notes.txt"].Stamp == fileVersion(info) {
		t.Fatal("stamp was not refreshed", idx, e)
	}
	// Same size and mtime but different bytes: only a skipped (unhashed) scan ignores this.
	write(t, p, "kitchen plAns")
	if e := os.Chtimes(p, future, future); e != nil {
		t.Fatal(e)
	}
	if got := r.scan(); got != nil {
		t.Fatal("third scan hashed or extracted a file with unchanged stamp:", got)
	}
}

func TestPhotoSettingStillReindexesOnlyPhotosWithStamps(t *testing.T) {
	r := newStampRig(t, map[string]string{"notes.txt": "kitchen plans", "beach.jpg": "jpeg bytes"})
	r.scan()
	if again := r.scan(); again != nil {
		t.Fatal("unchanged files reindexed:", again)
	}
	r.a.Photos = true
	if got := r.scan(); strings.Join(got, "|") != "image_embed .jpg" {
		t.Fatal("enabling photos must reindex only photos:", got)
	}
	if again := r.scan(); again != nil {
		t.Fatal("photos reindexed repeatedly:", again)
	}
	r.a.Photos = false
	if got := r.scan(); strings.Join(got, "|") != "extract .jpg" {
		t.Fatal("disabling photos must reindex only photos:", got)
	}
}

func TestSearchDropsDeletedHitAndFillsFromNextRank(t *testing.T) {
	a := scanApp(t)
	ctx := context.Background()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(a.Root, name), "needle common content")
	}
	if e := a.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	top, e := a.Search(ctx, "needle", "", false, 1)
	if e != nil || len(top.Hits) != 1 {
		t.Fatal(top, e)
	}
	if e = os.Remove(filepath.Join(a.Root, top.Hits[0].Path)); e != nil {
		t.Fatal(e)
	}
	got, e := a.Search(ctx, "needle", "", false, 2)
	if e != nil || len(got.Hits) != 2 {
		t.Fatal(got, e)
	}
	for _, h := range got.Hits {
		if h.Path == top.Hits[0].Path {
			t.Fatal("deleted file returned", got)
		}
	}
	if got.Hits[0].Score < got.Hits[1].Score {
		t.Fatal("unsorted", got)
	}
}
