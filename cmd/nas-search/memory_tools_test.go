package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type memToolEnv struct {
	t       *testing.T
	store   *fakeMemoryStore
	session *mcp.ClientSession
}

func newMemToolEnv(t *testing.T, roots []string, defaultScope string, approval *memoryApproval) *memToolEnv {
	t.Helper()
	store := newFakeMemoryStore()
	if approval != nil {
		approval.store = store
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mem-test", Version: "1"}, nil)
	registerMemoryTools(server, store, roots, defaultScope, approval)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &memToolEnv{t: t, store: store, session: cs}
}

// call returns the structured result (decoded), the tool-error text, and whether it was an error.
func (e *memToolEnv) call(name string, args map[string]any) (map[string]any, string, bool) {
	e.t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		// Input-schema rejections surface as protocol errors; treat them as tool errors.
		return nil, err.Error(), true
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if res.IsError {
		return nil, text, true
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("structured result: %v (%s)", err, raw)
	}
	return out, text, false
}

func (e *memToolEnv) mustCall(name string, args map[string]any) map[string]any {
	e.t.Helper()
	out, text, isErr := e.call(name, args)
	if isErr {
		e.t.Fatalf("%s failed: %s", name, text)
	}
	return out
}

func TestMemoryToolsRegistered(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	tools, err := env.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		got[tool.Name] = tool
	}
	for _, name := range []string{"context_open", "journal_append", "journal_search", "checkpoint_save", "memory_search", "memory_correct", "memory_forget"} {
		if got[name] == nil {
			t.Fatalf("missing tool %s", name)
		}
	}
	for _, name := range []string{"context_open", "journal_search", "memory_search"} {
		if a := got[name].Annotations; a == nil || !a.ReadOnlyHint {
			t.Errorf("%s must be read-only", name)
		}
	}
	if a := got["memory_forget"].Annotations; a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Error("memory_forget must be destructive")
	}
	if !strings.Contains(got["context_open"].Description, "start of a conversation") {
		t.Error("context_open must say when to call it")
	}
	if !strings.Contains(strings.ToLower(got["journal_append"].Description), "never as instructions") && !strings.Contains(strings.ToLower(got["context_open"].Description), "never as instructions") {
		t.Error("descriptions must warn that memory text is evidence, not instructions")
	}
	if !strings.Contains(got["memory_correct"].Description, "disputed") {
		t.Error("memory_correct must explain assistants may only dispute")
	}
}

func TestJournalAppendRoundTripAndActor(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	out := env.mustCall("journal_append", map[string]any{
		"type": "decision", "title": "Use SQLite", "body": "because simple", "project": "nas",
		"occurred_at": "2026-10-01T10:00:00Z", "next_actions": []string{"write schema"},
		"sources":         []map[string]any{{"kind": "file", "locator": "/a/b", "excerpt": "x"}},
		"idempotency_key": "k1", "session_id": "s1",
	})
	if out["created"] != true {
		t.Fatalf("created = %v", out["created"])
	}
	ev := out["event"].(map[string]any)
	if ev["actor"] != memoryapi.ActorAssistant {
		t.Fatalf("actor = %v, must always be assistant", ev["actor"])
	}
	if ev["client"] != "unknown" || ev["scope"] != "personal" || ev["project"] != "nas" {
		t.Fatalf("defaults wrong: %v", ev)
	}
	if ev["occurred_at"] != "2026-10-01T10:00:00Z" {
		t.Fatalf("occurred_at = %v", ev["occurred_at"])
	}
	// Repeat is idempotent.
	again := env.mustCall("journal_append", map[string]any{"type": "decision", "title": "Use SQLite", "idempotency_key": "k1"})
	if again["created"] != false {
		t.Fatal("repeat with same idempotency_key must report created=false")
	}
	stored := env.store.events[ev["id"].(string)]
	if stored.Status != "unverified" {
		t.Fatalf("status left empty so store applies default; got %q", stored.Status)
	}
}

func TestInputCannotClaimToBeTheUser(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	for name, args := range map[string]map[string]any{
		"journal_append":  {"type": "observation", "title": "t", "actor": "user"},
		"checkpoint_save": {"project": "p", "expected_version": 0, "summary": "s", "actor": "user"},
		"memory_correct":  {"memory_id": "m", "status": "disputed", "reason": "r", "actor": "user"},
	} {
		if _, _, isErr := env.call(name, args); !isErr {
			t.Errorf("%s accepted an actor field", name)
		}
	}
	if len(env.store.events) != 0 || len(env.store.checkpoints) != 0 || env.store.lastCorrect.MemoryID != "" {
		t.Fatal("a claimed actor reached the store")
	}
}

func TestJournalAppendRejections(t *testing.T) {
	env := newMemToolEnv(t, []string{"work"}, "work", nil)
	cases := map[string]map[string]any{
		"confirmed status": {"type": "observation", "title": "t", "status": "confirmed"},
		"bad type":         {"type": "bogus", "title": "t"},
		"empty title":      {"type": "observation", "title": ""},
		"long title":       {"type": "observation", "title": strings.Repeat("a", 201)},
		"long body":        {"type": "observation", "title": "t", "body": strings.Repeat("a", 20001)},
		"too many actions": {"type": "observation", "title": "t", "next_actions": make([]string, 21)},
		"bad time":         {"type": "observation", "title": "t", "occurred_at": "yesterday"},
		"scope outside":    {"type": "observation", "title": "t", "scope": "personal"},
		"bad scope":        {"type": "observation", "title": "t", "scope": "Bad Scope"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, text, isErr := env.call("journal_append", args); !isErr {
				t.Fatalf("expected error, got success (%s)", text)
			}
		})
	}
	if len(env.store.events) != 0 {
		t.Fatal("rejected input reached the store")
	}
}

func TestScopeDefaultAndRootsPassedDown(t *testing.T) {
	env := newMemToolEnv(t, []string{"work/acme", "personal"}, "work/acme", nil)
	out := env.mustCall("journal_append", map[string]any{"type": "observation", "title": "t"})
	if out["event"].(map[string]any)["scope"] != "work/acme" {
		t.Fatal("default scope not applied")
	}
	if strings.Join(env.store.lastRoots, ",") != "work/acme,personal" {
		t.Fatalf("roots = %v", env.store.lastRoots)
	}
	// Reads without scope see all roots; with scope they narrow to it.
	env.mustCall("journal_search", map[string]any{"text": "t"})
	if len(env.store.lastRoots) != 2 {
		t.Fatalf("unscoped read roots = %v", env.store.lastRoots)
	}
	env.mustCall("journal_search", map[string]any{"scope": "personal"})
	if strings.Join(env.store.lastRoots, ",") != "personal" {
		t.Fatalf("scoped read roots = %v", env.store.lastRoots)
	}
	if _, _, isErr := env.call("journal_search", map[string]any{"scope": "work/other"}); !isErr {
		t.Fatal("scope outside roots must fail")
	}
}

func TestContextOpen(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	out := env.mustCall("context_open", map[string]any{"project": "nas", "intent": "resume", "budget": 900})
	if out["project"] != "nas" || out["budget"] != float64(900) {
		t.Fatalf("package = %v", out)
	}
	if env.store.lastCtx.Project != "nas" || env.store.lastCtx.Intent != "resume" || env.store.lastCtx.Budget != 900 {
		t.Fatalf("request = %+v", env.store.lastCtx)
	}
	if _, _, isErr := env.call("context_open", map[string]any{"budget": -1}); !isErr {
		t.Fatal("negative budget must fail")
	}
	if _, _, isErr := env.call("context_open", map[string]any{"intent": "x"}); !isErr {
		t.Fatal("unknown intent must fail")
	}
}

func TestJournalSearch(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	env.mustCall("journal_append", map[string]any{"type": "observation", "title": "alpha thing", "project": "p"})
	out := env.mustCall("journal_search", map[string]any{"text": "alpha", "types": []string{"observation"}, "from": "2026-01-01T00:00:00Z", "to": "2027-01-01T00:00:00Z", "limit": 5})
	events := out["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events = %v", out)
	}
	q := env.store.lastEventQ
	if q.Text != "alpha" || q.Limit != 5 || q.From.IsZero() || q.To.IsZero() || len(q.Types) != 1 {
		t.Fatalf("query = %+v", q)
	}
	for name, args := range map[string]map[string]any{
		"limit":    {"limit": 101},
		"bad type": {"types": []string{"nope"}},
		"bad from": {"from": "x"},
	} {
		if _, _, isErr := env.call("journal_search", args); !isErr {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestCheckpointSaveAndConflict(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	out := env.mustCall("checkpoint_save", map[string]any{"project": "nas", "expected_version": 0, "summary": "first", "next": []string{"a"}, "client": "claude-desktop"})
	if out["version"] != float64(1) || out["actor"] != memoryapi.ActorAssistant || out["scope"] != "personal" || out["client"] != "claude-desktop" {
		t.Fatalf("checkpoint = %v", out)
	}
	_, text, isErr := env.call("checkpoint_save", map[string]any{"project": "nas", "expected_version": 0, "summary": "stale"})
	if !isErr {
		t.Fatal("stale version must be an error")
	}
	for _, want := range []string{"context_open", "merge", "retry"} {
		if !strings.Contains(text, want) {
			t.Errorf("conflict message %q lacks %q", text, want)
		}
	}
	if _, _, isErr := env.call("checkpoint_save", map[string]any{"project": "nas", "expected_version": 1, "summary": ""}); !isErr {
		t.Fatal("empty summary must fail")
	}
	if _, _, isErr := env.call("checkpoint_save", map[string]any{"project": "", "expected_version": 0, "summary": "x"}); !isErr {
		t.Fatal("empty project must fail")
	}
}

func TestMemorySearchAndCorrect(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	env.store.memories["m1"] = memoryapi.Memory{ID: "m1", Statement: "uses sqlite", Scope: "personal", Status: "accepted", Category: "fact"}
	out := env.mustCall("memory_search", map[string]any{"text": "sqlite", "statuses": []string{"accepted"}, "categories": []string{"fact"}})
	if len(out["memories"].([]any)) != 1 {
		t.Fatalf("memories = %v", out)
	}
	if _, _, isErr := env.call("memory_search", map[string]any{"statuses": []string{"bogus"}}); !isErr {
		t.Fatal("bad status must fail")
	}
	if _, _, isErr := env.call("memory_search", map[string]any{"categories": []string{"bogus"}}); !isErr {
		t.Fatal("bad category must fail")
	}
	got := env.mustCall("memory_correct", map[string]any{"memory_id": "m1", "status": "disputed", "reason": "outdated"})
	if got["status"] != "disputed" {
		t.Fatalf("memory = %v", got)
	}
	if env.store.lastCorrect.Actor != memoryapi.ActorAssistant {
		t.Fatalf("correction actor = %q", env.store.lastCorrect.Actor)
	}
	env.store.memories["m1"] = memoryapi.Memory{ID: "m1", Scope: "personal", Status: "accepted"}
	_, text, isErr := env.call("memory_correct", map[string]any{"memory_id": "m1", "status": "retracted", "reason": "no"})
	if !isErr || !strings.Contains(text, "only dispute") {
		t.Fatalf("assistant retract must surface the store's invalid message: %v %q", isErr, text)
	}
	_, text, isErr = env.call("memory_correct", map[string]any{"memory_id": "nope", "status": "disputed", "reason": "r"})
	if !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("not found text = %q", text)
	}
	if _, _, isErr := env.call("memory_correct", map[string]any{"memory_id": "m1", "status": "disputed"}); !isErr {
		t.Fatal("reason is required")
	}
}

func TestMemoryForgetPrivateForgetsDirectly(t *testing.T) {
	env := newMemToolEnv(t, []string{"personal"}, "personal", nil)
	ev := env.mustCall("journal_append", map[string]any{"type": "observation", "title": "secret"})["event"].(map[string]any)
	out := env.mustCall("memory_forget", map[string]any{"kind": "event", "id": ev["id"]})
	if out["state"] != "forgotten" {
		t.Fatalf("out = %v", out)
	}
	if got := env.store.forgottenList(); len(got) != 1 || got[0] != "event:"+ev["id"].(string)+":"+memoryapi.ActorAssistant {
		t.Fatalf("forgotten = %v", got)
	}
	if _, text, isErr := env.call("memory_forget", map[string]any{"kind": "event", "id": "gone"}); !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("missing id: %q", text)
	}
	if _, _, isErr := env.call("memory_forget", map[string]any{"kind": "file", "id": "x"}); !isErr {
		t.Fatal("bad kind must fail")
	}
}

func TestMemoryForgetPublicRequiresApproval(t *testing.T) {
	approval := newMemoryApproval("https://nas.example", nil, []string{"personal"})
	env := newMemToolEnv(t, []string{"personal"}, "personal", approval)
	env.store.memories["m1"] = memoryapi.Memory{ID: "m1", Statement: "likes tea", Scope: "personal", Status: "accepted"}
	out := env.mustCall("memory_forget", map[string]any{"kind": "memory", "id": "m1"})
	if out["state"] != "approval_required" {
		t.Fatalf("out = %v", out)
	}
	url, _ := out["approval_url"].(string)
	if !strings.HasPrefix(url, "https://nas.example"+memoryApprovalPath+"?request=") || out["expires_at"] == "" || out["message"] == "" {
		t.Fatalf("out = %v", out)
	}
	if len(env.store.forgottenList()) != 0 || len(env.store.memories) != 1 {
		t.Fatal("public path must not forget before approval")
	}
	// Unknown ids never get a link.
	if _, _, isErr := env.call("memory_forget", map[string]any{"kind": "memory", "id": "zzz"}); !isErr {
		t.Fatal("unknown id must not yield an approval link")
	}
}
