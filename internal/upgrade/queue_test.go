package upgrade

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestQueueExclusiveAndStatus(t *testing.T) {
	q := Queue{Dir: t.TempDir()}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := q.Request(); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent upgrades", accepted)
	}
	b, err := os.ReadFile(filepath.Join(q.Dir, "request.json"))
	if err != nil || string(b) != "{\"action\":\"upgrade_main\"}\n" {
		t.Fatalf("request %q %v", b, err)
	}
	status, err := q.Status()
	if err != nil || status["state"] != "queued" {
		t.Fatalf("status %v %v", status, err)
	}
	if err = os.WriteFile(filepath.Join(q.Dir, "status.json"), []byte(`{"state":"running","commit":"abc"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(q.Dir, "request.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Request(); err == nil {
		t.Fatal("allowed concurrent running upgrade")
	}
	if err = os.WriteFile(filepath.Join(q.Dir, "status.json"), []byte(`{"state":"succeeded","commit":"abc"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Request(); err != nil {
		t.Fatal(err)
	}
	status, err = q.Status()
	if err != nil || status["state"] != "queued" {
		t.Fatalf("stale status %v %v", status, err)
	}
}

func TestQueueDisabled(t *testing.T) {
	q := Queue{}
	if _, err := q.Request(); err == nil {
		t.Fatal("disabled queue accepted upgrade")
	}
	status, err := q.Status()
	if err != nil || status["state"] != "disabled" {
		t.Fatal(status, err)
	}
}
