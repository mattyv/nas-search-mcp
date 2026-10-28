package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeExtractApp's worker echoes its pid and the requested rss_kib. Each request gets a fresh response.
func fakeExtractApp(t *testing.T, keep time.Duration) *App {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker.py")
	err = os.WriteFile(worker, []byte(`import json,os,sys

def reply(r):
 return {"metadata":{"pid":os.getpid()},"rss_kib":r.get("rss",1000),"text":"ok"}
if "--serve" in sys.argv:
 for line in sys.stdin:
  r=json.loads(line)
  if r.get("text")=="wait":
   import time; time.sleep(30)
  if r.get("text")=="huge":
   sys.stdout.write("x"*(17*1024*1024)+"\n"); sys.stdout.flush(); continue
  if r.get("text")=="boom":
   print(json.dumps({"error":"x","rss_kib":1}),flush=True); continue
  print(json.dumps(reply(r)),flush=True)
else:
 print(json.dumps(reply(json.load(sys.stdin))))
`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Python: python, Worker: worker, Data: dir, workerGate: make(chan struct{}, 1), modelKeepalive: keep}
	t.Cleanup(func() { a.Close() })
	return a
}
func callPID(t *testing.T, a *App, request map[string]any) float64 {
	t.Helper()
	r, err := a.worker(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return r.Metadata["pid"].(float64)
}

func TestExtractReusesResidentSession(t *testing.T) {
	a := fakeExtractApp(t, time.Minute)
	first := callPID(t, a, map[string]any{"operation": "extract"})
	if callPID(t, a, map[string]any{"operation": "extract"}) != first || callPID(t, a, map[string]any{"operation": "read_document"}) != first {
		t.Fatal("extract restarted the worker")
	}
}
func TestExtractThenEmbedKeepsProcess(t *testing.T) {
	a := fakeExtractApp(t, time.Minute)
	first := callPID(t, a, map[string]any{"operation": "extract"})
	if callPID(t, a, map[string]any{"operation": "embed"}) != first {
		t.Fatal("embed restarted the worker after extract")
	}
	if callPID(t, a, map[string]any{"operation": "extract"}) != first {
		t.Fatal("extract restarted the worker after embed")
	}
}
func TestHighRSSRecyclesWorkerAfterReply(t *testing.T) {
	t.Setenv("NAS_WORKER_MAX_RSS_MIB", "100")
	a := fakeExtractApp(t, time.Minute)
	first := callPID(t, a, map[string]any{"operation": "extract", "rss": 101 * 1024})
	if callPID(t, a, map[string]any{"operation": "extract"}) == first {
		t.Fatal("oversized worker was reused")
	}
	pid := callPID(t, a, map[string]any{"operation": "extract", "rss": 100 * 1024})
	if callPID(t, a, map[string]any{"operation": "extract"}) != pid {
		t.Fatal("worker at the limit was recycled")
	}
}
func TestExtractWithoutKeepaliveUsesOneShotProcesses(t *testing.T) {
	a := fakeExtractApp(t, 0)
	if callPID(t, a, map[string]any{"operation": "extract", "rss": 1}) == callPID(t, a, map[string]any{"operation": "extract"}) {
		t.Fatal("zero keepalive reused worker")
	}
	if a.session != nil {
		t.Fatal("zero keepalive left a session")
	}
}
func TestExtractCancellationAndOversizedReplyRestart(t *testing.T) {
	a := fakeExtractApp(t, time.Minute)
	first := callPID(t, a, map[string]any{"operation": "extract"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := a.worker(ctx, map[string]any{"operation": "extract", "text": "wait"}); err == nil {
		t.Fatal("cancelled extract succeeded")
	}
	second := callPID(t, a, map[string]any{"operation": "extract"})
	if second == first {
		t.Fatal("cancelled worker was reused")
	}
	if _, err := a.worker(context.Background(), map[string]any{"operation": "extract", "text": "huge"}); err == nil || !strings.Contains(err.Error(), "worker failed") {
		t.Fatalf("oversized reply error: %v", err)
	}
	third := callPID(t, a, map[string]any{"operation": "extract"})
	if third == second {
		t.Fatal("worker survived oversized reply")
	}
	if _, err := a.worker(context.Background(), map[string]any{"operation": "extract", "text": "boom"}); err == nil {
		t.Fatal("worker error not reported")
	}
	if callPID(t, a, map[string]any{"operation": "extract"}) != third {
		t.Fatal("reported error restarted the worker")
	}
}
func TestHighRSSPhotoEmbedKeepsResidentModel(t *testing.T) {
	t.Setenv("NAS_WORKER_MAX_RSS_MIB", "100")
	a := fakeExtractApp(t, time.Minute)
	// CLIP peaks far above the text limit; photo runs must not reload it per photo.
	first := callPID(t, a, map[string]any{"operation": "image_embed", "rss": 700 * 1024})
	if callPID(t, a, map[string]any{"operation": "image_embed", "rss": 700 * 1024}) != first {
		t.Fatal("photo embedding recycled the resident model")
	}
	// The first extraction afterwards recycles once, so text indexing starts from a small process.
	if callPID(t, a, map[string]any{"operation": "extract", "rss": 700 * 1024}) != first {
		t.Fatal("extract reply itself should still be served by the old process")
	}
	if callPID(t, a, map[string]any{"operation": "extract"}) == first {
		t.Fatal("oversized process survived an extraction")
	}
}
