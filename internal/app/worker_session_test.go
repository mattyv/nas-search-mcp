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

func fakeSessionApp(t *testing.T, keep time.Duration) *App {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker.py")
	err = os.WriteFile(worker, []byte(`import json,os,sys

def reply(r):
 print(json.dumps({"metadata":{"pid":os.getpid()}}),flush=True)
if "--serve" in sys.argv:
 for line in sys.stdin:
  r=json.loads(line)
  if r.get("text")=="wait":
   import time; time.sleep(30)
  if r.get("text")=="malformed":
   print("invalid",flush=True); continue
  if r.get("text")=="exit": sys.exit(0)
  if r.get("text")=="secret-error":
   print(json.dumps({"error":"private document contents"}),flush=True); continue
  reply(r)
else:
 reply(json.load(sys.stdin))
`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Python: python, Worker: worker, Data: dir, workerGate: make(chan struct{}, 1), modelKeepalive: keep}
	t.Cleanup(func() { a.Close() })
	return a
}
func sessionPID(t *testing.T, a *App) float64 {
	t.Helper()
	r, e := a.worker(context.Background(), map[string]any{"operation": "embed"})
	if e != nil {
		t.Fatal(e)
	}
	return r.Metadata["pid"].(float64)
}
func TestModelSessionReusesAndExpires(t *testing.T) {
	a := fakeSessionApp(t, 80*time.Millisecond)
	first := sessionPID(t, a)
	if next := sessionPID(t, a); next != first {
		t.Fatal("embedding requests reload worker")
	}
	time.Sleep(150 * time.Millisecond)
	if next := sessionPID(t, a); next == first {
		t.Fatal("idle worker was retained")
	}
}
func TestModelSessionDisabled(t *testing.T) {
	a := fakeSessionApp(t, 0)
	if sessionPID(t, a) == sessionPID(t, a) {
		t.Fatal("zero keepalive reused worker")
	}
}
func TestModelSessionCancellationRestarts(t *testing.T) {
	a := fakeSessionApp(t, time.Minute)
	first := sessionPID(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := a.worker(ctx, map[string]any{"operation": "embed", "text": "wait"}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if sessionPID(t, a) == first {
		t.Fatal("cancelled worker was reused")
	}
}
func TestExtractionReleasesModelSession(t *testing.T) {
	a := fakeSessionApp(t, time.Minute)
	first := sessionPID(t, a)
	if _, err := a.worker(context.Background(), map[string]any{"operation": "extract"}); err != nil {
		t.Fatal(err)
	}
	if sessionPID(t, a) == first {
		t.Fatal("extraction retained resident model")
	}
}

func TestModelKeepaliveConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", 5 * time.Minute, false}, {"0", 0, false}, {"2.5", 150 * time.Second, false},
		{"-1", 0, true}, {"NaN", 0, true}, {"Inf", 0, true}, {"invalid", 0, true}, {"1e100", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("NAS_MODEL_KEEPALIVE_MINUTES", tc.value)
			got, err := modelKeepaliveFromEnv()
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}
func TestModelSessionClose(t *testing.T) {
	a := fakeSessionApp(t, time.Minute)
	sessionPID(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.worker(context.Background(), map[string]any{"operation": "embed"}); err == nil {
		t.Fatal("closed app accepted embedding")
	}
}

func TestModelSessionInvalidResponseRestarts(t *testing.T) {
	for _, text := range []string{"malformed", "exit"} {
		t.Run(text, func(t *testing.T) {
			a := fakeSessionApp(t, time.Minute)
			first := sessionPID(t, a)
			if _, err := a.worker(context.Background(), map[string]any{"operation": "embed", "text": text}); err == nil {
				t.Fatal("invalid response accepted")
			}
			if sessionPID(t, a) == first {
				t.Fatal("broken worker reused")
			}
		})
	}
}

func TestModelSessionHidesWorkerErrorDetails(t *testing.T) {
	a := fakeSessionApp(t, time.Minute)
	_, err := a.worker(context.Background(), map[string]any{"operation": "embed", "text": "secret-error"})
	if err == nil || strings.Contains(err.Error(), "private document contents") {
		t.Fatal("worker details escaped into a public tool error")
	}
}

func TestModelSessionRenewsIdleCountdown(t *testing.T) {
	a := fakeSessionApp(t, 500*time.Millisecond)
	first := sessionPID(t, a)
	time.Sleep(300 * time.Millisecond)
	if sessionPID(t, a) != first {
		t.Fatal("worker expired before idle limit")
	}
	time.Sleep(300 * time.Millisecond)
	if sessionPID(t, a) != first {
		t.Fatal("second request did not renew idle countdown")
	}
}
