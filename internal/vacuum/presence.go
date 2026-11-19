package vacuum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// PresenceResult says what a leave or arrive signal did.
type PresenceResult struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

// Presence turns phone leave/arrive signals into at most one clean a day.
// The verified core decides; this adapter remembers the day it last started
// a clean, in a small file so restarts do not forget.
type Presence struct {
	client *Client
	path   string
	now    func() time.Time
	mu     sync.Mutex
}

func NewPresence(client *Client, path string, now func() time.Time) *Presence {
	return &Presence{client: client, path: path, now: now}
}

type presenceRecord struct {
	StartedOn string `json:"started_on"`
}

func (p *Presence) read() presenceRecord {
	var record presenceRecord
	if data, err := os.ReadFile(p.path); err == nil {
		json.Unmarshal(data, &record)
	}
	return record
}

func (p *Presence) write(record presenceRecord) error {
	data, _ := json.Marshal(record)
	temp, err := os.CreateTemp(filepath.Dir(p.path), ".vacuum-presence-*")
	if err != nil {
		return errors.New("could not record the clean")
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err == nil {
		err = temp.Chmod(0o600)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), p.path)
	}
	if err != nil {
		return errors.New("could not record the clean")
	}
	return nil
}

// Handle acts on "away" or "home".
func (p *Presence) Handle(ctx context.Context, presence string) (PresenceResult, error) {
	if presence != "away" && presence != "home" {
		return PresenceResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	status, err := p.client.Status(ctx)
	if err != nil {
		return PresenceResult{}, err
	}
	today := p.now().Format("2006-01-02")
	record := p.read()
	startedToday := record.StartedOn == today
	switch policy.VacuumPresenceAction(presence, status.State, status.Docked, status.Error, status.Battery, startedToday) {
	case "start":
		// Record first, so a failed write can never cause a second clean today.
		if err := p.write(presenceRecord{StartedOn: today}); err != nil {
			return PresenceResult{}, err
		}
		if _, err := p.client.Command(ctx, CommandInput{Action: "start"}); err != nil {
			return PresenceResult{}, err
		}
		return PresenceResult{Action: "start", Message: "Started cleaning while you're out."}, nil
	case "dock":
		if _, err := p.client.Command(ctx, CommandInput{Action: "dock"}); err != nil {
			return PresenceResult{}, err
		}
		return PresenceResult{Action: "dock", Message: "Welcome back. Sent the vacuum to its dock."}, nil
	}
	return PresenceResult{Action: "none", Message: p.reason(presence, status, startedToday)}, nil
}

func (p *Presence) reason(presence string, status Status, startedToday bool) string {
	if presence == "home" {
		return "Nothing to do: no clean from leaving home is running."
	}
	switch {
	case startedToday:
		return "Already cleaned after you left today."
	case status.Error != "":
		return "Not cleaning: the vacuum needs help (" + words(status.Error) + ")."
	case !status.Docked:
		return "Not cleaning: the vacuum is not on its dock."
	case status.Battery < int(policy.VacuumAwayMinBatteryValue()):
		return fmt.Sprintf("Not cleaning: battery is %d%%.", status.Battery)
	}
	return "Not cleaning: the vacuum is busy."
}
