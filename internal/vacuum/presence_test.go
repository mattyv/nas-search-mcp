package vacuum

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type presenceFake struct {
	mu       sync.Mutex
	state    string
	docked   bool
	battery  int
	commands []string
}

func (f *presenceFake) client(t *testing.T) *Client {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]any{"name": "Roberto", "connected": true, "state": f.state, "battery": f.battery, "docked": f.docked, "error": ""})
		case "/command":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.commands = append(f.commands, body["action"].(string))
			w.Write([]byte(`{"accepted":true,"message":"ok"}`))
		}
	})
	return newClient(t, socket)
}

func TestPresenceStartsOnceADayAndDocksOwnClean(t *testing.T) {
	ctx := context.Background()
	fake := &presenceFake{state: "charging_completed", docked: true, battery: 90}
	path := filepath.Join(t.TempDir(), "vacuum-presence.json")
	day := time.Date(2026, 11, 19, 9, 0, 0, 0, time.Local)
	presence := NewPresence(fake.client(t), path, func() time.Time { return day })

	if out, err := presence.Handle(ctx, "away"); err != nil || out.Action != "start" {
		t.Fatalf("leaving: %#v %v", out, err)
	}
	fake.state, fake.docked = "sweeping", false
	if out, _ := presence.Handle(ctx, "home"); out.Action != "dock" {
		t.Fatalf("arriving mid-clean: %#v", out)
	}
	fake.state, fake.docked = "charging", true
	// A restart must not forget today's clean.
	presence = NewPresence(fake.client(t), path, func() time.Time { return day })
	if out, _ := presence.Handle(ctx, "away"); out.Action != "none" || out.Message == "" {
		t.Fatalf("second departure same day: %#v", out)
	}
	day = day.Add(24 * time.Hour)
	if out, _ := presence.Handle(ctx, "away"); out.Action != "start" {
		t.Fatalf("next day: %#v", out)
	}
	if len(fake.commands) != 3 || fake.commands[0] != "start" || fake.commands[1] != "dock" || fake.commands[2] != "start" {
		t.Fatalf("commands = %v", fake.commands)
	}
}

func TestPresenceLeavesManualCleansAlone(t *testing.T) {
	fake := &presenceFake{state: "sweeping", battery: 70}
	presence := NewPresence(fake.client(t), filepath.Join(t.TempDir(), "p.json"), time.Now)
	if out, _ := presence.Handle(context.Background(), "home"); out.Action != "none" || len(fake.commands) != 0 {
		t.Fatalf("docked a clean it did not start: %#v %v", out, fake.commands)
	}
}

func TestPresenceRejectsUnknownSignal(t *testing.T) {
	fake := &presenceFake{state: "charging", docked: true, battery: 90}
	presence := NewPresence(fake.client(t), filepath.Join(t.TempDir(), "p.json"), time.Now)
	if _, err := presence.Handle(context.Background(), "lunch"); err == nil || len(fake.commands) != 0 {
		t.Fatal("unknown signal accepted")
	}
}
