package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPhotoSettingChangeKeepsExistingIndex(t *testing.T) {
	base := t.TempDir()
	root, data := filepath.Join(base, "files"), filepath.Join(base, "data")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	open := func(semantic, photos bool) error {
		a, e := New(root, data, "python3", "worker.py", "", semantic, photos)
		if e == nil {
			a.Close()
		}
		return e
	}
	if e := open(true, false); e != nil {
		t.Fatal(e)
	}
	if e := open(true, true); e != nil {
		t.Fatal("turning photos on must not demand a new index:", e)
	}
	if e := open(true, false); e != nil {
		t.Fatal("turning photos off must not demand a new index:", e)
	}
	if e := open(false, false); e == nil {
		t.Fatal("changing text embeddings must still demand a new index")
	}
}

func TestLegacyPipelineSignatureUpgradesWithoutRebuild(t *testing.T) {
	base := t.TempDir()
	root, data := filepath.Join(base, "files"), filepath.Join(base, "data")
	for _, dir := range []string{root, data} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	resolved, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	legacy := "v1;bge-small-en-v1.5;clip-ViT-B-32;semantic=true;photos=false;root=" + resolved
	if e = os.WriteFile(filepath.Join(data, "pipeline.json"), []byte(legacy), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := New(root, data, "python3", "worker.py", "", true, true)
	if e != nil {
		t.Fatal("legacy index rejected:", e)
	}
	a.Close()
	upgraded, e := os.ReadFile(filepath.Join(data, "pipeline.json"))
	if e != nil || strings.Contains(string(upgraded), "photos=") {
		t.Fatal("signature not upgraded:", string(upgraded), e)
	}
	moved := "v1;bge-small-en-v1.5;clip-ViT-B-32;semantic=true;photos=false;root=/elsewhere"
	if e = os.WriteFile(filepath.Join(data, "pipeline.json"), []byte(moved), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = New(root, data, "python3", "worker.py", "", true, true); e == nil {
		t.Fatal("legacy index from another root accepted")
	}
}

// fakeWorker records each operation and path, and returns fixed-size vectors.
const fakeWorker = `import json, os, sys
request = json.load(sys.stdin)
op = request['operation']
with open(os.environ['FAKE_WORKER_LOG'], 'a') as log:
    log.write(op + ' ' + os.path.splitext(request.get('path', ''))[1] + '\n')
if op == 'extract':
    photo = request['path'].endswith('.jpg')
    out = {'kind': 'photo' if photo else 'document', 'sections': [] if photo else [{'location': 'text', 'text': 'kitchen plans'}], 'metadata': {}}
elif op == 'image_embed':
    out = {'kind': 'photo', 'metadata': {'width': 4}, 'vectors': [[0.1] * 512]}
else:
    out = {'vectors': [[0.1] * 384 for _ in request['texts']]}
json.dump(out, sys.stdout)
`

func TestPhotoSettingChangeReindexesOnlyPhotos(t *testing.T) {
	extension := os.Getenv("NAS_VEC_EXTENSION")
	if extension == "" {
		t.Skip("set NAS_VEC_EXTENSION for vector integration tests")
	}
	t.Setenv("NAS_MODEL_KEEPALIVE_MINUTES", "0")
	base := t.TempDir()
	root := filepath.Join(base, "files")
	write(t, filepath.Join(root, "notes.txt"), "kitchen plans")
	write(t, filepath.Join(root, "beach.jpg"), "jpeg bytes")
	worker := filepath.Join(base, "fake_worker.py")
	write(t, worker, fakeWorker)
	logPath := filepath.Join(base, "worker.log")
	t.Setenv("FAKE_WORKER_LOG", logPath)
	a, e := New(root, filepath.Join(base, "data"), "python3", worker, extension, true, false)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
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
		// Files are indexed from private snapshots, so only the extension identifies them.
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		sort.Strings(lines)
		return lines
	}
	first := scan()
	if strings.Join(first, "|") != "embed|extract .jpg|extract .txt" {
		t.Fatal("unexpected first scan:", first)
	}
	if again := scan(); again != nil {
		t.Fatal("unchanged files reindexed:", again)
	}
	a.Photos = true
	enabled := scan()
	// One model call per photo: a separate extract step would stop the warm model.
	if strings.Join(enabled, "|") != "image_embed .jpg" {
		t.Fatal("enabling photos must reindex only photos, in one worker call:", enabled)
	}
	if again := scan(); again != nil {
		t.Fatal("photos reindexed repeatedly:", again)
	}
	a.Photos = false
	if disabled := scan(); strings.Join(disabled, "|") != "extract .jpg" {
		t.Fatal("disabling photos must drop photo vectors only:", disabled)
	}
}
