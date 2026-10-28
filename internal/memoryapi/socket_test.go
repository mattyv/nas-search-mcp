package memoryapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memory"
	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
)

func serve(t *testing.T, store memoryapi.Store, allowed []string) (*memoryapi.Client, string) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "m.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- memoryapi.ServeSocket(ctx, sock, memoryapi.NewServer(store, allowed)) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("ServeSocket: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeSocket did not stop")
		}
	})
	c := &memoryapi.Client{Socket: sock}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := c.Ping(context.Background()); err == nil {
			return c, sock
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func realStore(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(memory.Config{Path: filepath.Join(t.TempDir(), "m.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRoundTripOverSocket(t *testing.T) {
	c, _ := serve(t, realStore(t), []string{"personal", "work"})
	ctx := context.Background()
	roots := []string{"personal"}

	e, created, err := c.AppendEvent(ctx, roots, memoryapi.Event{Type: "decision", Title: "use sockets", Project: "p", Scope: "personal", Client: "t", Actor: memoryapi.ActorUser, IdempotencyKey: "k"})
	if err != nil || !created || e.Status == "confirmed" || e.OccurredAt.IsZero() {
		t.Fatalf("%+v %v %v", e, created, err)
	}
	again, created, err := c.AppendEvent(ctx, roots, memoryapi.Event{Type: "decision", Title: "other", Scope: "personal", Client: "t", Actor: memoryapi.ActorUser, IdempotencyKey: "k"})
	if err != nil || created || again.ID != e.ID {
		t.Fatalf("idempotent: %+v %v %v", again, created, err)
	}
	if got, err := c.GetEvent(ctx, roots, e.ID); err != nil || got.Title != "use sockets" {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := c.SearchEvents(ctx, roots, memoryapi.EventQuery{Text: "sockets"}); err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	cp, err := c.SaveCheckpoint(ctx, roots, memoryapi.Checkpoint{Project: "p", Scope: "personal", Summary: "state", Client: "t", Actor: memoryapi.ActorUser}, 0)
	if err != nil || cp.Version != 1 {
		t.Fatalf("%+v %v", cp, err)
	}
	if l, err := c.LatestCheckpoint(ctx, roots, "p"); err != nil || l == nil || l.Version != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	if l, err := c.LatestCheckpoint(ctx, roots, "nope"); err != nil || l != nil {
		t.Fatalf("%+v %v", l, err)
	}
	if h, err := c.CheckpointHistory(ctx, roots, "p", 5); err != nil || len(h) != 1 {
		t.Fatalf("%+v %v", h, err)
	}
	m, err := c.ProposeMemory(ctx, roots, memoryapi.Memory{Category: "decision", Statement: "sockets are private", Scope: "personal", Project: "p", Probability: -1}, true)
	if err != nil || m.Status != "candidate" {
		t.Fatalf("%+v %v", m, err)
	}
	if got, err := c.GetMemory(ctx, roots, m.ID); err != nil || got.ID != m.ID {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := c.SearchMemories(ctx, roots, memoryapi.MemoryQuery{Text: "private", Statuses: []string{"candidate"}}); err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := c.CorrectMemory(ctx, roots, memoryapi.Correction{MemoryID: m.ID, Status: "disputed", Reason: "check", Actor: memoryapi.ActorAssistant}); err != nil || got.Status != "disputed" {
		t.Fatalf("%+v %v", got, err)
	}
	pkg, err := c.OpenContext(ctx, roots, memoryapi.ContextRequest{Project: "p"})
	if err != nil || pkg.Checkpoint == nil || pkg.Budget != 1500 || len(pkg.Unconfirmed) != 1 {
		t.Fatalf("%+v %v", pkg, err)
	}
	if ps, err := c.Projects(ctx, roots); err != nil || len(ps) != 1 || ps[0].Project != "p" {
		t.Fatalf("%+v %v", ps, err)
	}
	if un, err := c.UnclassifiedEvents(ctx, roots, 10); err != nil || len(un) != 1 {
		t.Fatalf("%+v %v", un, err)
	}
	if err := c.MarkClassified(ctx, roots, e.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSocketCallersAreNeverTheOwner(t *testing.T) {
	store := realStore(t)
	c, _ := serve(t, store, []string{"personal"})
	ctx := context.Background()
	roots := []string{"personal"}

	// Claiming to be the owner does not make an event confirmed or a memory accepted.
	e, _, err := c.AppendEvent(ctx, roots, memoryapi.Event{Type: "decision", Title: "claimed", Scope: "personal", Client: "t", Actor: memoryapi.ActorUser})
	if err != nil || e.Actor != memoryapi.ActorAssistant || e.Status == "confirmed" {
		t.Fatalf("event: %+v %v", e, err)
	}
	if _, _, err := c.AppendEvent(ctx, roots, memoryapi.Event{Type: "decision", Title: "claimed", Status: "confirmed", Scope: "personal", Client: "t", Actor: memoryapi.ActorUser}); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("confirmed over socket: %v", err)
	}
	cp, err := c.SaveCheckpoint(ctx, roots, memoryapi.Checkpoint{Project: "p", Scope: "personal", Summary: "s", Client: "t", Actor: memoryapi.ActorUser}, 0)
	if err != nil || cp.Actor != memoryapi.ActorAssistant {
		t.Fatalf("checkpoint: %+v %v", cp, err)
	}
	m, err := c.ProposeMemory(ctx, roots, memoryapi.Memory{Category: "fact", Statement: "claimed", Scope: "personal", Probability: -1}, true)
	if err != nil || m.Status != "candidate" {
		t.Fatalf("attested over socket: %+v %v", m, err)
	}
	// An "owner" correction is treated as the assistant's: it may only dispute.
	if _, err := c.CorrectMemory(ctx, roots, memoryapi.Correction{MemoryID: m.ID, Status: "accepted", Reason: "r", Actor: memoryapi.ActorUser}); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("accept over socket: %v", err)
	}
	// Forget is editor-only.
	err = c.ForgetMemory(ctx, roots, m.ID, memoryapi.ActorUser)
	if !errors.Is(err, memoryapi.ErrInvalid) || !strings.Contains(err.Error(), "forget is only available in the editor") {
		t.Fatalf("forget memory: %v", err)
	}
	if err := c.ForgetEvent(ctx, roots, e.ID, memoryapi.ActorUser); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("forget event: %v", err)
	}
	if _, err := store.GetEvent(ctx, roots, e.ID); err != nil {
		t.Fatalf("event must survive: %v", err)
	}
	if _, err := store.GetMemory(ctx, roots, m.ID); err != nil {
		t.Fatalf("memory must survive: %v", err)
	}
}

func TestSentinelErrorsSurviveSocket(t *testing.T) {
	c, _ := serve(t, realStore(t), []string{"personal"})
	ctx := context.Background()
	roots := []string{"personal"}

	_, err := c.GetEvent(ctx, roots, "evt_missing")
	if !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	_, _, err = c.AppendEvent(ctx, roots, memoryapi.Event{Type: "chat", Title: "x", Scope: "personal", Actor: memoryapi.ActorUser})
	if !errors.Is(err, memoryapi.ErrInvalid) || strings.Count(err.Error(), "invalid request") != 1 {
		t.Fatalf("invalid: %v", err)
	}
	cp := memoryapi.Checkpoint{Project: "p", Scope: "personal", Summary: "s", Actor: memoryapi.ActorUser}
	if _, err = c.SaveCheckpoint(ctx, roots, cp, 5); !errors.Is(err, memoryapi.ErrConflict) || !strings.Contains(err.Error(), "current version is 0") {
		t.Fatalf("conflict: %v", err)
	}
	// Allowed by the server but outside the caller's own roots: the store answers.
	_, _, err = c.AppendEvent(ctx, roots, memoryapi.Event{Type: "observation", Title: "x", Scope: "personal2", Actor: memoryapi.ActorUser})
	if !errors.Is(err, memoryapi.ErrScope) {
		t.Fatalf("store scope: %v", err)
	}
}

func TestServerRejectsRootsOutsideAllowed(t *testing.T) {
	store := realStore(t)
	c, _ := serve(t, store, []string{"personal"})
	ctx := context.Background()
	for _, roots := range [][]string{{"work"}, {"personal2"}, {"personal", "work/acme"}, {""}, {"Bad Root"}, {"per"}} {
		if _, err := c.SearchEvents(ctx, roots, memoryapi.EventQuery{}); !errors.Is(err, memoryapi.ErrScope) {
			t.Errorf("%v: %v", roots, err)
		}
	}
	if _, err := c.SearchEvents(ctx, []string{"personal/sub"}, memoryapi.EventQuery{}); err != nil {
		t.Fatalf("narrower root must pass: %v", err)
	}
	if _, err := c.SearchEvents(ctx, []string{"personal"}, memoryapi.EventQuery{}); err != nil {
		t.Fatal(err)
	}
}

type failingStore struct{ memoryapi.Store }

func (failingStore) GetEvent(context.Context, []string, string) (memoryapi.Event, error) {
	return memoryapi.Event{}, errors.New("sqlite: /secret/path/db is corrupt")
}

func TestInternalErrorsAreGeneric(t *testing.T) {
	c, _ := serve(t, failingStore{}, []string{"personal"})
	_, err := c.GetEvent(context.Background(), []string{"personal"}, "evt_x")
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("leaked: %v", err)
	}
	for _, sentinel := range []error{memoryapi.ErrNotFound, memoryapi.ErrConflict, memoryapi.ErrInvalid, memoryapi.ErrScope} {
		if errors.Is(err, sentinel) {
			t.Fatalf("internal error mapped to %v", sentinel)
		}
	}
}

func rawPost(t *testing.T, c *memoryapi.Client, sock string, body []byte) (int, memoryapi.CallResponse) {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err := hc.Post("http://memory"+memoryapi.CallPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out memoryapi.CallResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestServerRejectsBadRequests(t *testing.T) {
	c, sock := serve(t, realStore(t), []string{"personal"})
	body, _ := json.Marshal(memoryapi.CallRequest{Method: "DropEverything", Roots: []string{"personal"}, Params: json.RawMessage(`{}`)})
	if code, out := rawPost(t, c, sock, body); code != 400 || out.Error == nil || out.Error.Code != "invalid" {
		t.Fatalf("unknown method: %d %+v", code, out)
	}
	huge := bytes.Repeat([]byte("a"), 1<<20+10)
	body, _ = json.Marshal(memoryapi.CallRequest{Method: "GetEvent", Roots: []string{"personal"}, Params: json.RawMessage(`{"id":"` + string(huge) + `"}`)})
	if code, out := rawPost(t, c, sock, body); code < 400 || code >= 500 || out.Error == nil || out.Error.Code != "invalid" {
		t.Fatalf("oversize: %d %+v", code, out)
	}
	if code, out := rawPost(t, c, sock, []byte(`not json`)); code != 400 || out.Error == nil {
		t.Fatalf("garbage: %d %+v", code, out)
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err := hc.Get("http://memory" + memoryapi.CallPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET call: %d", resp.StatusCode)
	}
}

func TestServeSocketFileHandling(t *testing.T) {
	store := realStore(t)
	dir := t.TempDir()
	sock := filepath.Join(dir, "m.sock")

	// A regular file at the path is never deleted.
	if err := os.WriteFile(sock, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := memoryapi.ServeSocket(context.Background(), sock, memoryapi.NewServer(store, nil)); err == nil {
		t.Fatal("expected refusal")
	}
	if b, _ := os.ReadFile(sock); string(b) != "keep" {
		t.Fatal("regular file was removed")
	}
	os.Remove(sock)

	// A stale socket left by a crash is replaced.
	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatal("stale socket should remain", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- memoryapi.ServeSocket(ctx, sock, memoryapi.NewServer(store, nil)) }()
	c := &memoryapi.Client{Socket: sock}
	deadline := time.Now().Add(3 * time.Second)
	for c.Ping(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("not serving")
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, err := os.Stat(sock)
	if err != nil || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode %v %v", info.Mode(), err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no graceful shutdown")
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("still serving after shutdown")
	}
}
