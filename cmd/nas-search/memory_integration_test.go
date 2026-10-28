package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/consolidate"
	"github.com/mattyv/nas-search-mcp/internal/memory"
	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The real store behind the real socket, reached by the real client, with the MCP tools on
// top: the cross-client handoff from the design's acceptance test, in miniature.
type liveMemory struct {
	t      *testing.T
	store  *memory.Store
	client *memoryapi.Client
}

func newLiveMemory(t *testing.T, allowed []string) *liveMemory {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.Open(memory.Config{Path: filepath.Join(dir, "memory.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	socket := filepath.Join(dir, "memory.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- memoryapi.ServeSocket(ctx, socket, memoryapi.NewServer(store, allowed)) }()
	t.Cleanup(func() { cancel(); <-done })
	client := &memoryapi.Client{Socket: socket}
	deadline := time.Now().Add(3 * time.Second)
	for client.Ping(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("memory socket did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &liveMemory{t: t, store: store, client: client}
}

// assistant connects one MCP client (one "assistant") to tools backed by the live service.
func (l *liveMemory) assistant(roots []string) func(name string, args map[string]any) (map[string]any, string) {
	server := mcp.NewServer(&mcp.Implementation{Name: "nas-search", Version: "test"}, nil)
	registerMemoryTools(server, l.client, roots, roots[0], "https://nas.tail1234.ts.net")
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "assistant", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { cs.Close() })
	return func(name string, args map[string]any) (map[string]any, string) {
		l.t.Helper()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return nil, err.Error()
		}
		if res.IsError {
			text := ""
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			return nil, text
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out map[string]any
		json.Unmarshal(raw, &out)
		return out, ""
	}
}

func TestHandoffBetweenAssistantsThroughLiveService(t *testing.T) {
	live := newLiveMemory(t, []string{"personal"})
	claude := live.assistant([]string{"personal"})
	chatgpt := live.assistant([]string{"personal"})

	if _, errText := claude("checkpoint_save", map[string]any{"project": "garden", "expected_version": 0, "goal": "Plan the vegetable beds", "summary": "Measured the beds; soil test ordered.", "next": []string{"Choose tomato varieties"}, "client": "claude"}); errText != "" {
		t.Fatal(errText)
	}
	if _, errText := claude("journal_append", map[string]any{"type": "decision", "title": "Raised beds will be 40 cm deep", "project": "garden", "client": "claude", "idempotency_key": "k1"}); errText != "" {
		t.Fatal(errText)
	}
	// A retried write with the same key must not duplicate.
	if out, errText := claude("journal_append", map[string]any{"type": "decision", "title": "Raised beds will be 40 cm deep", "project": "garden", "client": "claude", "idempotency_key": "k1"}); errText != "" || out["created"] != false {
		t.Fatal(out, errText)
	}

	// The other assistant resumes without being told anything.
	pkg, errText := chatgpt("context_open", map[string]any{"project": "garden"})
	if errText != "" {
		t.Fatal(errText)
	}
	raw, _ := json.Marshal(pkg)
	text := string(raw)
	for _, want := range []string{"Plan the vegetable beds", "Choose tomato varieties", "Raised beds will be 40 cm deep"} {
		if !strings.Contains(text, want) {
			t.Fatalf("handoff missing %q: %s", want, text)
		}
	}
	if decisions, _ := pkg["decisions"].([]any); len(decisions) != 0 {
		t.Fatal("an assistant's decision must not be presented as accepted before review", decisions)
	}

	// Consolidation without a classifier leaves the assistant's decision for review.
	if n, err := consolidate.Once(context.Background(), live.client, consolidate.Config{Roots: []string{"personal"}}); err != nil || n == 0 {
		t.Fatal(n, err)
	}
	pkg, _ = chatgpt("context_open", map[string]any{"project": "garden"})
	unconfirmed, _ := pkg["unconfirmed"].([]any)
	if len(unconfirmed) != 1 {
		t.Fatal("expected one unconfirmed memory", pkg)
	}
	id := unconfirmed[0].(map[string]any)["id"].(string)

	// An assistant may only dispute; accepting is the owner's.
	if _, errText := chatgpt("memory_correct", map[string]any{"memory_id": id, "status": "accepted", "reason": "looks right"}); errText == "" {
		t.Fatal("assistant accepted a memory")
	}
	// The owner accepts it in the editor (actor user through the same store).
	if _, err := live.client.CorrectMemory(context.Background(), []string{"personal"}, memoryapi.Correction{MemoryID: id, Status: "accepted", Reason: "confirmed in editor", Client: "editor", Actor: memoryapi.ActorUser}); err != nil {
		t.Fatal(err)
	}
	pkg, _ = chatgpt("context_open", map[string]any{"project": "garden"})
	if decisions, _ := pkg["decisions"].([]any); len(decisions) != 1 {
		t.Fatal("accepted decision missing from handoff", pkg)
	}

	// A stale checkpoint write is refused rather than overwriting the other assistant's work.
	if _, errText := chatgpt("checkpoint_save", map[string]any{"project": "garden", "expected_version": 0, "summary": "stale"}); !strings.Contains(errText, "context_open") {
		t.Fatal("expected a conflict that points at context_open", errText)
	}
}

func TestScopesIsolateThroughLiveService(t *testing.T) {
	live := newLiveMemory(t, []string{"personal", "work/acme"})
	work := live.assistant([]string{"work/acme"})
	personal := live.assistant([]string{"personal"})
	if _, errText := work("journal_append", map[string]any{"type": "observation", "title": "Quarterly numbers are confidential", "project": "acme-q3"}); errText != "" {
		t.Fatal(errText)
	}
	out, errText := personal("journal_search", map[string]any{"text": "confidential"})
	if errText != "" {
		t.Fatal(errText)
	}
	if events, _ := out["events"].([]any); len(events) != 0 {
		t.Fatal("work entry leaked into personal scope", out)
	}
	if _, errText := personal("journal_append", map[string]any{"type": "observation", "title": "x", "scope": "work/acme"}); errText == "" {
		t.Fatal("personal assistant wrote into work scope")
	}
}

func TestMemoryRoutingHelpers(t *testing.T) {
	if roots, err := memoryScopes("personal, work/acme"); err != nil || len(roots) != 2 {
		t.Fatal(roots, err)
	}
	for _, bad := range []string{"", "Personal", "work//acme", "personal,/x"} {
		if _, err := memoryScopes(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if !memoryTool("context_open") || !memoryTool("memory_forget") || memoryTool("search") {
		t.Fatal("fast-lane tool list")
	}
	t.Setenv("NAS_MEMORY_ENABLED", "")
	if m, err := configuredMemory(); m != nil || err != nil {
		t.Fatal("memory must be off by default", m, err)
	}
	t.Setenv("NAS_MEMORY_ENABLED", "true")
	t.Setenv("NAS_MEMORY_SCOPES", "personal,work/acme")
	t.Setenv("NAS_EDITOR_ORIGIN", "https://nas.tail1234.ts.net")
	if m, err := configuredMemory(); err != nil || m.defaultScope != "personal" || len(m.roots) != 2 || m.editorOrigin != "https://nas.tail1234.ts.net" {
		t.Fatal(m, err)
	}
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("base")) })
	extra := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("extra")) })
	h := withRoutes(base, map[string]http.Handler{"/extra": extra})
	for path, want := range map[string]string{"/extra": "extra", "/mcp": "base", "/": "base"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Body.String() != want {
			t.Fatalf("%s routed to %q", path, rec.Body.String())
		}
	}
}

func TestForgetThroughLiveServiceNeverErases(t *testing.T) {
	live := newLiveMemory(t, []string{"personal"})
	claude := live.assistant([]string{"personal"})
	out, errText := claude("journal_append", map[string]any{"type": "observation", "title": "keep me"})
	if errText != "" {
		t.Fatal(errText)
	}
	id := out["event"].(map[string]any)["id"].(string)
	res, errText := claude("memory_forget", map[string]any{"kind": "event", "id": id})
	if errText != "" || res["state"] != "use_editor" || res["editor_url"] != "https://nas.tail1234.ts.net/e/"+id {
		t.Fatal(res, errText)
	}
	found, _ := claude("journal_search", map[string]any{"text": "keep"})
	if events, _ := found["events"].([]any); len(events) != 1 {
		t.Fatal("the entry was erased", found)
	}
}

func TestPasskeyModeDefault(t *testing.T) {
	t.Setenv("NAS_OAUTH_PASSKEYS", "")
	if passkeyMode("https://nas.example.ts.net") != "enroll" || passkeyMode("https://192.0.2.4:8443") != "off" {
		t.Fatal("default passkey mode")
	}
	t.Setenv("NAS_OAUTH_PASSKEYS", "required")
	if passkeyMode("https://192.0.2.4") != "required" {
		t.Fatal("explicit setting must win")
	}
}
