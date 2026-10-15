// Package upgrade adapts authenticated MCP requests to a fixed host-side operation.
// The service has no Docker socket, shell access, or caller-selected upgrade target.
package upgrade

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Queue struct{ Dir string }

func (q Queue) Status() (map[string]any, error) {
	if q.Dir == "" {
		return map[string]any{"state": "disabled"}, nil
	}
	pending := false
	if _, err := os.Stat(filepath.Join(q.Dir, "request.json")); err == nil {
		pending = true
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.Open(filepath.Join(q.Dir, "status.json"))
	if os.IsNotExist(err) {
		if pending {
			return map[string]any{"state": "queued"}, nil
		}
		return map[string]any{"state": "idle"}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out map[string]any
	if err = json.NewDecoder(f).Decode(&out); err != nil {
		return nil, errors.New("upgrade status is unreadable; inspect the host upgrade runner")
	}
	if pending && out["state"] != "running" {
		return map[string]any{"state": "queued"}, nil
	}
	if target, ok := out["commit"]; ok {
		out["target_commit"] = target
		delete(out, "commit")
		if out["state"] == "succeeded" {
			if targetCommit, ok := target.(string); ok && targetCommit != "" {
				out["installed_commit"] = targetCommit
			}
		}
	}
	if out["state"] == "succeeded" || out["state"] == "failed" {
		if updatedAt, ok := out["updated_at"]; ok {
			out["finished_at"] = updatedAt
		}
	}
	return out, nil
}

func (q Queue) Request() (map[string]any, error) {
	if q.Dir == "" {
		return nil, errors.New("upgrades are disabled; configure the NAS host upgrade runner first")
	}
	if err := os.MkdirAll(q.Dir, 0700); err != nil {
		return nil, err
	}
	// A fixed exclusive request file serializes callers. The runner keeps it until
	// its terminal status has been published, including across service replacement.
	// Publish a complete request with an exclusive hard link so the host never
	// observes a partially written JSON document.
	f, err := os.CreateTemp(q.Dir, ".request-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString("{\"action\":\"upgrade_main\"}\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	status, err := q.Status()
	if err != nil {
		return nil, err
	}
	if status["state"] == "running" {
		return nil, errors.New("an upgrade is running; call upgrade_status")
	}
	err = os.Link(f.Name(), filepath.Join(q.Dir, "request.json"))
	if os.IsExist(err) {
		return nil, errors.New("an upgrade is already queued or running; call upgrade_status")
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"state": "queued", "message": "Upgrade to checked main requested. Search may disconnect briefly. Call upgrade_status after reconnecting."}, nil
}
