package upgrade

import (
	"fmt"
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

func TestStatusReportsInstalledCommitOnlyAfterSuccessfulUpgrade(t *testing.T) {
	// The container receives only the queue mount; its parent has no current-image.
	queueDir := t.TempDir()
	target := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	write := func(state string, updatedAt int) {
		t.Helper()
		data := fmt.Sprintf(`{"state":%q,"commit":%q,"updated_at":%d}`, state, target, updatedAt)
		if err := os.WriteFile(filepath.Join(queueDir, "status.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	q := Queue{Dir: queueDir}
	write("succeeded", 123)
	status, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status["installed_commit"] != target || status["target_commit"] != target {
		t.Fatalf("status commits = %v, want installed and target %s", status, target)
	}
	if status["finished_at"] != float64(123) {
		t.Fatalf("finished_at = %v, want status update time 123", status["finished_at"])
	}
	if _, ok := status["started_at"]; ok {
		t.Fatalf("reported unsupported started_at: %v", status["started_at"])
	}
	write("failed", 124)
	status, err = q.Status()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := status["installed_commit"]; ok {
		t.Fatalf("claimed installed commit for failed upgrade: %v", status)
	}
}

func TestStatusPreservesNewHostRunnerFields(t *testing.T) {
	q := Queue{Dir: t.TempDir()}
	statusFile := filepath.Join(q.Dir, "status.json")
	data := []byte(`{"state":"failed","installed_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","target_commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","started_at":100,"finished_at":120,"failure_reason":"Invalid settings."}`)
	if err := os.WriteFile(statusFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	status, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status["installed_commit"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || status["target_commit"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || status["started_at"] != float64(100) || status["finished_at"] != float64(120) || status["failure_reason"] != "Invalid settings." {
		t.Fatalf("new host status fields lost: %v", status)
	}
}
