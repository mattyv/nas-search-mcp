package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
)

var _ memoryapi.Store = (*Store)(nil)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	s, err := Open(Config{Path: filepath.Join(t.TempDir(), "memory.db"), Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk
}

var (
	personal  = []string{"personal"}
	personal2 = []string{"personal2"}
	work      = []string{"work"}
	all       = []string{"personal", "personal2", "work"}
	ctx       = context.Background()
)

func ev(scope, title string) memoryapi.Event {
	return memoryapi.Event{Type: "observation", Title: title, Scope: scope, Client: "test", Actor: memoryapi.ActorUser}
}

func mustAppend(t *testing.T, s *Store, roots []string, e memoryapi.Event) memoryapi.Event {
	t.Helper()
	out, created, err := s.AppendEvent(ctx, roots, e)
	if err != nil || !created {
		t.Fatalf("append: %v created=%v", err, created)
	}
	return out
}

func TestScopeIsolation(t *testing.T) {
	s, _ := newStore(t)
	p := mustAppend(t, s, all, ev("personal", "alpha note"))
	p2 := mustAppend(t, s, all, ev("personal2", "alpha other"))
	w := mustAppend(t, s, all, ev("work/acme", "alpha work"))

	got, err := s.SearchEvents(ctx, personal, memoryapi.EventQuery{Text: "alpha"})
	if err != nil || len(got) != 1 || got[0].ID != p.ID {
		t.Fatalf("personal search: %v %v", got, err)
	}
	got, _ = s.SearchEvents(ctx, work, memoryapi.EventQuery{})
	if len(got) != 1 || got[0].ID != w.ID {
		t.Fatalf("work list: %v", got)
	}
	got, _ = s.SearchEvents(ctx, nil, memoryapi.EventQuery{})
	if len(got) != 0 {
		t.Fatal("no roots must see nothing")
	}
	if _, err := s.GetEvent(ctx, personal, p2.ID); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("invisible get: %v", err)
	}
	if _, err := s.GetEvent(ctx, personal, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendEvent(ctx, personal, ev("work/acme", "x")); !errors.Is(err, memoryapi.ErrScope) {
		t.Fatalf("write outside roots: %v", err)
	}
	if _, _, err := s.AppendEvent(ctx, personal, ev("personal2", "x")); !errors.Is(err, memoryapi.ErrScope) {
		t.Fatalf("personal must not write personal2: %v", err)
	}
	m, err := s.ProposeMemory(ctx, all, memoryapi.Memory{Category: "fact", Statement: "secret fact", Scope: "personal2", Probability: -1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMemory(ctx, personal, m.ID); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("invisible memory: %v", err)
	}
	if ms, _ := s.SearchMemories(ctx, personal, memoryapi.MemoryQuery{Text: "secret"}); len(ms) != 0 {
		t.Fatal("memory search leaked")
	}
	if _, err := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "x", Scope: "personal2", Probability: -1}, true); !errors.Is(err, memoryapi.ErrScope) {
		t.Fatalf("memory write: %v", err)
	}
	if ms, _ := s.SearchMemories(ctx, personal2, memoryapi.MemoryQuery{Text: "secret"}); len(ms) != 1 {
		t.Fatal("owner scope must see its memory")
	}
}

func TestIdempotentAppend(t *testing.T) {
	s, _ := newStore(t)
	e := ev("personal", "once")
	e.IdempotencyKey = "k1"
	first, created, err := s.AppendEvent(ctx, personal, e)
	if err != nil || !created {
		t.Fatal(err, created)
	}
	e.Title = "changed"
	again, created, err := s.AppendEvent(ctx, personal, e)
	if err != nil || created || again.ID != first.ID || again.Title != "once" {
		t.Fatalf("repeat: %+v created=%v err=%v", again, created, err)
	}
	got, _ := s.SearchEvents(ctx, personal, memoryapi.EventQuery{})
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	// The same key must not reveal an event in another scope.
	e.Scope = "personal2"
	if _, _, err := s.AppendEvent(ctx, personal2, e); err == nil {
		t.Fatal("key reuse across scopes must fail")
	}
}

func TestSupersedesMarksOldEvent(t *testing.T) {
	s, _ := newStore(t)
	old := mustAppend(t, s, personal, ev("personal", "wrong claim"))
	n := ev("personal", "right claim")
	n.Type = "correction"
	n.Supersedes = old.ID
	mustAppend(t, s, personal, n)
	got, _ := s.GetEvent(ctx, personal, old.ID)
	if got.Status != "superseded" {
		t.Fatalf("status %q", got.Status)
	}
}

func TestAssistantStatusRules(t *testing.T) {
	s, _ := newStore(t)
	a := ev("personal", "assistant note")
	a.Actor = memoryapi.ActorAssistant
	got := mustAppend(t, s, personal, a)
	if got.Status != "unverified" {
		t.Fatalf("assistant default %q", got.Status)
	}
	a.Type, a.Title = "exploration", "idea"
	if got = mustAppend(t, s, personal, a); got.Status != "proposed" {
		t.Fatalf("exploration default %q", got.Status)
	}
	if got = mustAppend(t, s, personal, ev("personal", "user note")); got.Status != "confirmed" {
		t.Fatalf("user default %q", got.Status)
	}
	a.Status, a.Title = "confirmed", "sneaky"
	if _, _, err := s.AppendEvent(ctx, personal, a); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("assistant confirmed: %v", err)
	}
	bad := ev("personal", "x")
	bad.Actor = "robot"
	if _, _, err := s.AppendEvent(ctx, personal, bad); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("actor: %v", err)
	}
}

func TestValidation(t *testing.T) {
	s, _ := newStore(t)
	cases := map[string]func(e *memoryapi.Event){
		"type":      func(e *memoryapi.Event) { e.Type = "chat" },
		"status":    func(e *memoryapi.Event) { e.Status = "accepted" },
		"scope":     func(e *memoryapi.Event) { e.Scope = "Personal//x" },
		"title":     func(e *memoryapi.Event) { e.Title = "" },
		"longtitle": func(e *memoryapi.Event) { e.Title = strings.Repeat("a", 201) },
		"body":      func(e *memoryapi.Event) { e.Body = strings.Repeat("a", 20001) },
		"project":   func(e *memoryapi.Event) { e.Project = "Bad Project" },
		"sources":   func(e *memoryapi.Event) { e.Sources = make([]memoryapi.Source, 11) },
		"excerpt": func(e *memoryapi.Event) {
			e.Sources = []memoryapi.Source{{Kind: "file", Locator: "x", Excerpt: strings.Repeat("a", 2001)}}
		},
		"actions": func(e *memoryapi.Event) { e.NextActions = make([]string, 21) },
		"action":  func(e *memoryapi.Event) { e.NextActions = []string{strings.Repeat("a", 301)} },
		"related": func(e *memoryapi.Event) { e.Related = make([]string, 21) },
		"key":     func(e *memoryapi.Event) { e.IdempotencyKey = strings.Repeat("k", 129) },
	}
	for name, mod := range cases {
		e := ev("personal", "ok")
		mod(&e)
		if _, _, err := s.AppendEvent(ctx, personal, e); !errors.Is(err, memoryapi.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func cp(project, summary string) memoryapi.Checkpoint {
	return memoryapi.Checkpoint{Project: project, Scope: "personal", Goal: "ship it", Summary: summary, Next: []string{"write tests"}, Client: "test", Actor: memoryapi.ActorUser}
}

func TestCheckpointVersions(t *testing.T) {
	s, _ := newStore(t)
	c1, err := s.SaveCheckpoint(ctx, personal, cp("proj", "first"), 0)
	if err != nil || c1.Version != 1 {
		t.Fatalf("%+v %v", c1, err)
	}
	if _, err := s.SaveCheckpoint(ctx, personal, cp("proj", "stale"), 0); !errors.Is(err, memoryapi.ErrConflict) || !strings.Contains(err.Error(), "1") {
		t.Fatalf("conflict: %v", err)
	}
	c2, err := s.SaveCheckpoint(ctx, personal, cp("proj", "second"), 1)
	if err != nil || c2.Version != 2 {
		t.Fatalf("%+v %v", c2, err)
	}
	latest, err := s.LatestCheckpoint(ctx, personal, "proj")
	if err != nil || latest == nil || latest.Version != 2 || latest.Summary != "second" {
		t.Fatalf("%+v %v", latest, err)
	}
	hist, _ := s.CheckpointHistory(ctx, personal, "proj", 10)
	if len(hist) != 2 || hist[0].Version != 2 || hist[1].Version != 1 {
		t.Fatalf("history %+v", hist)
	}
	if l, _ := s.LatestCheckpoint(ctx, personal2, "proj"); l != nil {
		t.Fatal("checkpoint leaked across scope")
	}
	if _, err := s.SaveCheckpoint(ctx, personal2, cp("proj", "x"), 2); !errors.Is(err, memoryapi.ErrScope) {
		t.Fatalf("scope: %v", err)
	}
	evs, _ := s.SearchEvents(ctx, personal, memoryapi.EventQuery{Types: []string{"checkpoint"}})
	if len(evs) != 2 || !evs[0].Classified || !strings.HasPrefix(evs[0].Title, "Checkpoint v") {
		t.Fatalf("checkpoint events %+v", evs)
	}
	if l, _ := s.LatestCheckpoint(ctx, personal, "none"); l != nil {
		t.Fatal("expected nil")
	}
	bad := cp("proj", "")
	if _, err := s.SaveCheckpoint(ctx, personal, bad, 2); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("empty summary: %v", err)
	}
}

func TestInitialMemoryStatusRouting(t *testing.T) {
	s, _ := newStore(t)
	m := memoryapi.Memory{Category: "fact", Statement: "the sky", Scope: "personal", Probability: -1}
	got, err := s.ProposeMemory(ctx, personal, m, false)
	if err != nil || got.Status != "candidate" {
		t.Fatalf("unscored: %+v %v", got, err)
	}
	m.Probability = 950
	if got, _ = s.ProposeMemory(ctx, personal, m, false); got.Status != "accepted" {
		t.Fatalf("high: %q", got.Status)
	}
	m.Probability = 50
	if got, _ = s.ProposeMemory(ctx, personal, m, false); got.Status != "rejected" {
		t.Fatalf("low: %q", got.Status)
	}
	m.Probability = 500
	if got, _ = s.ProposeMemory(ctx, personal, m, false); got.Status != "candidate" {
		t.Fatalf("mid: %q", got.Status)
	}
	m.Probability = -1
	if got, _ = s.ProposeMemory(ctx, personal, m, true); got.Status != "accepted" {
		t.Fatalf("user: %q", got.Status)
	}
	m.Category = "mood"
	if _, err = s.ProposeMemory(ctx, personal, m, true); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("category: %v", err)
	}
}

func TestSearchMemoriesDefaults(t *testing.T) {
	s, _ := newStore(t)
	acc, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "uses sqlite", Scope: "personal", Probability: -1}, true)
	s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "uses sqlite maybe", Scope: "personal", Probability: -1}, false)
	got, _ := s.SearchMemories(ctx, personal, memoryapi.MemoryQuery{Text: "sqlite"})
	if len(got) != 1 || got[0].ID != acc.ID {
		t.Fatalf("%+v", got)
	}
	got, _ = s.SearchMemories(ctx, personal, memoryapi.MemoryQuery{Text: "sqlite", Statuses: []string{"candidate", "accepted"}})
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestCorrectionLifecycle(t *testing.T) {
	s, _ := newStore(t)
	mk := func(user bool) memoryapi.Memory {
		m, err := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "decision", Statement: "use postgres", Scope: "personal", Project: "p", Probability: -1, Evidence: []string{"evt_x"}}, user)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	cor := func(id, to, actor string) (memoryapi.Memory, error) {
		return s.CorrectMemory(ctx, personal, memoryapi.Correction{MemoryID: id, Status: to, Reason: "because", Client: "t", Actor: actor})
	}
	acc := mk(true)
	if _, err := cor(acc.ID, "retracted", memoryapi.ActorAssistant); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("assistant retract: %v", err)
	}
	d, err := cor(acc.ID, "disputed", memoryapi.ActorAssistant)
	if err != nil || d.Status != "disputed" || d.Reason != "because" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := cor(acc.ID, "accepted", memoryapi.ActorAssistant); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("assistant accept: %v", err)
	}
	if _, err := cor(acc.ID, "accepted", memoryapi.ActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := cor(acc.ID, "retracted", memoryapi.ActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := cor(acc.ID, "accepted", memoryapi.ActorUser); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("terminal: %v", err)
	}
	cand := mk(false)
	if _, err := s.CorrectMemory(ctx, personal, memoryapi.Correction{MemoryID: cand.ID, Status: "accepted", Actor: memoryapi.ActorUser}); !errors.Is(err, memoryapi.ErrInvalid) {
		t.Fatalf("reason: %v", err)
	}
	cur := mk(true)
	got, err := s.CorrectMemory(ctx, personal, memoryapi.Correction{MemoryID: cur.ID, Status: "superseded", Statement: "use sqlite", Reason: "changed mind", Actor: memoryapi.ActorUser})
	if err != nil || got.Status != "superseded" || got.ReplacedBy == "" {
		t.Fatalf("%+v %v", got, err)
	}
	rep, err := s.GetMemory(ctx, personal, got.ReplacedBy)
	if err != nil || rep.Status != "accepted" || rep.Statement != "use sqlite" || rep.Project != "p" || rep.Category != "decision" || len(rep.Evidence) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := s.CorrectMemory(ctx, personal2, memoryapi.Correction{MemoryID: cur.ID, Status: "retracted", Reason: "x", Actor: memoryapi.ActorUser}); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
	if _, err := s.CorrectMemory(ctx, personal, memoryapi.Correction{MemoryID: "mem_nope", Status: "retracted", Reason: "x", Actor: memoryapi.ActorUser}); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestForget(t *testing.T) {
	s, _ := newStore(t)
	e1 := mustAppend(t, s, personal, ev("personal", "zebra sighting"))
	e2 := mustAppend(t, s, personal, ev("personal", "giraffe sighting"))
	only, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "zebras exist", Scope: "personal", Probability: -1, Evidence: []string{e1.ID}}, true)
	both, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "animals exist", Scope: "personal", Probability: -1, Evidence: []string{e1.ID, e2.ID}}, true)

	if err := s.ForgetEvent(ctx, personal2, e1.ID, memoryapi.ActorUser); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
	if err := s.ForgetEvent(ctx, personal, e1.ID, memoryapi.ActorUser); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SearchEvents(ctx, personal, memoryapi.EventQuery{Text: "zebra"}); len(got) != 0 {
		t.Fatal("event still searchable")
	}
	if _, err := s.GetEvent(ctx, personal, e1.ID); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.GetMemory(ctx, personal, only.ID); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("derived memory survived: %v", err)
	}
	if got, _ := s.SearchMemories(ctx, personal, memoryapi.MemoryQuery{Text: "zebras"}); len(got) != 0 {
		t.Fatal("memory still searchable")
	}
	m, err := s.GetMemory(ctx, personal, both.ID)
	if err != nil || len(m.Evidence) != 1 || m.Evidence[0] != e2.ID {
		t.Fatalf("%+v %v", m, err)
	}
	if err := s.ForgetMemory(ctx, personal, both.ID, memoryapi.ActorUser); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetMemory(ctx, personal, both.ID, memoryapi.ActorUser); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT kind, target_id, actor, at, extra FROM forget_audit ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var kind, id, actor, extra string
		var at int64
		rows.Scan(&kind, &id, &actor, &at, &extra)
		if strings.Contains(kind+id+actor+extra, "zebra") {
			t.Fatal("audit holds content")
		}
		n++
	}
	if n != 3 { // event, derived memory, memory
		t.Fatalf("audit rows %d", n)
	}
}

func TestForgetCheckpointEventBlanksPayload(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.SaveCheckpoint(ctx, personal, cp("proj", "confidential plan"), 0); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.SearchEvents(ctx, personal, memoryapi.EventQuery{Types: []string{"checkpoint"}})
	if err := s.ForgetEvent(ctx, personal, evs[0].ID, memoryapi.ActorUser); err != nil {
		t.Fatal(err)
	}
	l, _ := s.LatestCheckpoint(ctx, personal, "proj")
	if l == nil || strings.Contains(l.Summary, "confidential") || l.Version != 1 {
		t.Fatalf("%+v", l)
	}
	if _, err := s.SaveCheckpoint(ctx, personal, cp("proj", "next"), 1); err != nil {
		t.Fatalf("version must stay monotonic: %v", err)
	}
}

func TestRedaction(t *testing.T) {
	secrets := []string{
		"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----",
		"sk-abcdefghijklmnopqrstuvwx",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat_11ABCDEFG0abcdefghijkl_mnopqrstuvwxyz",
		"xoxb-1234567890-abcdefghij",
		"AKIAIOSFODNN7EXAMPLE",
		"Bearer abcdefghijklmnopqrstuvwxyz012345",
		strings.Repeat("a1b2c3d4", 6),
	}
	for _, sec := range secrets {
		got := redact("before " + sec + " after")
		if strings.Contains(got, sec) || !strings.Contains(got, "[redacted]") || !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
			t.Errorf("not redacted: %q -> %q", sec, got)
		}
	}
	plain := "We chose sqlite for /home/user/notes because it is small. Version 1.2.3, id 12345."
	if redact(plain) != plain {
		t.Fatalf("over-redacted: %q", redact(plain))
	}
	s, _ := newStore(t)
	e := ev("personal", "key sk-abcdefghijklmnopqrstuvwx here")
	e.Body = "token ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	e.Sources = []memoryapi.Source{{Kind: "file", Locator: "x", Excerpt: "AKIAIOSFODNN7EXAMPLE"}}
	got := mustAppend(t, s, personal, e)
	back, _ := s.GetEvent(ctx, personal, got.ID)
	blob := back.Title + back.Body + back.Sources[0].Excerpt
	if strings.Contains(blob, "sk-abc") || strings.Contains(blob, "ghp_") || strings.Contains(blob, "AKIA") {
		t.Fatalf("stored secret: %q", blob)
	}
	m, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "pw sk-abcdefghijklmnopqrstuvwx", Scope: "personal", Probability: -1}, true)
	if strings.Contains(m.Statement, "sk-") {
		t.Fatal("statement not redacted")
	}
}

func TestOpenContext(t *testing.T) {
	s, clk := newStore(t)
	s.SaveCheckpoint(ctx, personal, cp("proj", "where we are"), 0)
	dec, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "decision", Statement: "use sqlite", Scope: "personal", Project: "proj", Probability: -1}, true)
	fact, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "db is small", Scope: "personal", Project: "proj", Probability: -1}, true)
	pref, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "preference", Statement: "terse answers", Scope: "personal", Probability: -1}, true)
	s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "scope-wide fact", Scope: "personal", Probability: -1}, true)
	cand, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "maybe true", Scope: "personal", Project: "proj", Probability: -1}, false)
	s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "other project", Scope: "personal", Project: "other", Probability: -1}, true)
	old := ev("personal", "ancient")
	old.Project = "proj"
	old.OccurredAt = clk.now().Add(-30 * 24 * time.Hour)
	mustAppend(t, s, personal, old)
	clk.advance(time.Minute)
	recent := ev("personal", "recent work")
	recent.Project = "proj"
	recent.NextActions = []string{"write tests", "deploy"}
	mustAppend(t, s, personal, recent)

	pkg, err := s.OpenContext(ctx, personal, memoryapi.ContextRequest{Project: "proj", Budget: 8000})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Checkpoint == nil || pkg.Checkpoint.Version != 1 {
		t.Fatalf("checkpoint %+v", pkg.Checkpoint)
	}
	if len(pkg.Decisions) != 1 || pkg.Decisions[0].ID != dec.ID {
		t.Fatalf("decisions %+v", pkg.Decisions)
	}
	ids := map[string]bool{}
	for _, m := range pkg.Memories {
		ids[m.ID] = true
	}
	if len(pkg.Memories) != 2 || !ids[fact.ID] || !ids[pref.ID] {
		t.Fatalf("memories %+v", pkg.Memories)
	}
	if len(pkg.Unconfirmed) != 1 || pkg.Unconfirmed[0].ID != cand.ID {
		t.Fatalf("unconfirmed %+v", pkg.Unconfirmed)
	}
	for _, e := range pkg.Recent {
		if e.Title == "ancient" || e.Type == "checkpoint" {
			t.Fatalf("recent %+v", pkg.Recent)
		}
	}
	if len(pkg.Recent) != 1 || pkg.Omitted != 0 || pkg.Used <= 0 || pkg.Used > pkg.Budget {
		t.Fatalf("recent=%d omitted=%d used=%d", len(pkg.Recent), pkg.Omitted, pkg.Used)
	}
	if got := strings.Join(pkg.NextActions, ","); got != "write tests,deploy" {
		t.Fatalf("next actions %q", got)
	}
	if len(pkg.Notes) != 1 || !strings.Contains(pkg.Notes[0], "review in the editor") {
		t.Fatalf("notes %v", pkg.Notes)
	}
	pkg2, _ := s.OpenContext(ctx, personal, memoryapi.ContextRequest{})
	if pkg2.Project != "proj" || pkg2.Budget != 1500 {
		t.Fatalf("%q %d", pkg2.Project, pkg2.Budget)
	}
	pkg3, _ := s.OpenContext(ctx, personal2, memoryapi.ContextRequest{Project: "proj"})
	if pkg3.Checkpoint != nil || len(pkg3.Decisions)+len(pkg3.Memories)+len(pkg3.Recent) != 0 {
		t.Fatalf("leak %+v", pkg3)
	}
	if len(pkg3.Notes) == 0 || pkg3.Notes[0] != "no checkpoint saved yet" {
		t.Fatalf("notes %v", pkg3.Notes)
	}

	big, _ := s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "decision", Statement: strings.Repeat("long ", 190), Scope: "personal", Project: "proj", Probability: -1}, true)
	small, err := s.OpenContext(ctx, personal, memoryapi.ContextRequest{Project: "proj", Budget: 200})
	if err != nil {
		t.Fatal(err)
	}
	if small.Budget != 200 || small.Used > 200 || small.Omitted == 0 || small.Checkpoint == nil {
		t.Fatalf("tight: used=%d omitted=%d", small.Used, small.Omitted)
	}
	for _, d := range small.Decisions {
		if d.ID == big.ID {
			t.Fatal("oversized item kept")
		}
	}
	clamped, _ := s.OpenContext(ctx, personal, memoryapi.ContextRequest{Project: "proj", Budget: 5})
	if clamped.Budget != 200 {
		t.Fatalf("clamp low %d", clamped.Budget)
	}
	clamped, _ = s.OpenContext(ctx, personal, memoryapi.ContextRequest{Project: "proj", Budget: 99999})
	if clamped.Budget != 8000 {
		t.Fatalf("clamp high %d", clamped.Budget)
	}
}

func TestProjectsAndClassification(t *testing.T) {
	s, clk := newStore(t)
	a := ev("personal", "a1")
	a.Project = "alpha"
	mustAppend(t, s, personal, a)
	clk.advance(time.Hour)
	b := ev("personal", "b1")
	b.Project = "beta"
	mustAppend(t, s, personal, b)
	other := ev("personal2", "c1")
	other.Project = "gamma"
	mustAppend(t, s, all, other)
	clk.advance(time.Hour)
	s.SaveCheckpoint(ctx, personal, cp("alpha", "s"), 0)
	s.ProposeMemory(ctx, personal, memoryapi.Memory{Category: "fact", Statement: "x", Scope: "personal", Project: "alpha", Probability: -1}, false)

	ps, err := s.Projects(ctx, personal)
	if err != nil || len(ps) != 2 {
		t.Fatalf("%+v %v", ps, err)
	}
	if ps[0].Project != "alpha" || ps[0].Events != 2 || ps[0].CheckpointVersion != 1 || ps[0].OpenCandidates != 1 || ps[1].Project != "beta" {
		t.Fatalf("%+v", ps)
	}

	un, _ := s.UnclassifiedEvents(ctx, personal, 10)
	if len(un) != 2 || un[0].Title != "a1" {
		t.Fatalf("%+v", un)
	}
	if err := s.MarkClassified(ctx, personal, un[0].ID); err != nil {
		t.Fatal(err)
	}
	un, _ = s.UnclassifiedEvents(ctx, personal, 10)
	if len(un) != 1 || un[0].Title != "b1" {
		t.Fatalf("%+v", un)
	}
	if err := s.MarkClassified(ctx, personal, "evt_missing"); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.MarkClassified(ctx, personal, "evt_missing"); err == nil {
		t.Fatal("expected error")
	}
	if err := s.MarkClassified(ctx, work, un[0].ID); !errors.Is(err, memoryapi.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
}

func TestDatabaseFileIsPrivate(t *testing.T) {
	s, _ := newStore(t)
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info, err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	e := mustAppend(t, s, personal, ev("personal", "persist"))
	s.Close()
	s, err = Open(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.GetEvent(ctx, personal, e.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.ID, "evt_") || len(e.ID) != 30 {
		t.Fatalf("id %q", e.ID)
	}
}
