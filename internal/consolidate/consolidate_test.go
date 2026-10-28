package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/jev"
	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
)

// fakeStore implements memoryapi.Store; unused methods panic via the embedded nil interface.
type fakeStore struct {
	memoryapi.Store
	mu         sync.Mutex
	events     []memoryapi.Event
	classified map[string]bool
	proposed   []memoryapi.Memory
	attested   []bool
	existing   []memoryapi.Memory
	lastQuery  memoryapi.MemoryQuery
	unclCalls  int
}

func newStore(events ...memoryapi.Event) *fakeStore {
	return &fakeStore{events: events, classified: map[string]bool{}}
}

func (f *fakeStore) UnclassifiedEvents(_ context.Context, _ []string, limit int) ([]memoryapi.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unclCalls++
	var out []memoryapi.Event
	for _, e := range f.events {
		if !f.classified[e.ID] && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) MarkClassified(_ context.Context, _ []string, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.classified[id] = true
	return nil
}

func (f *fakeStore) SearchMemories(_ context.Context, _ []string, q memoryapi.MemoryQuery) ([]memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastQuery = q
	var out []memoryapi.Memory
	for _, m := range f.existing {
		if m.Project == q.Project {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) ProposeMemory(_ context.Context, _ []string, m memoryapi.Memory, attested bool) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proposed = append(f.proposed, m)
	f.attested = append(f.attested, attested)
	return m, nil
}

type fakeClassifier struct {
	calls    int
	category string
	pm       int
	model    string
	err      error
	seen     []memoryapi.Event
}

func (c *fakeClassifier) Classify(_ context.Context, e memoryapi.Event) (string, int, string, error) {
	c.calls++
	c.seen = append(c.seen, e)
	return c.category, c.pm, c.model, c.err
}

func ev(id, typ, actor, scope string) memoryapi.Event {
	return memoryapi.Event{ID: id, Type: typ, Title: "Use SQLite for the index", Project: "nas", Scope: scope, Actor: actor}
}

var roots = []string{"personal"}

func cfg(c Classifier) Config {
	return Config{Roots: roots, ExternalScopes: []string{"personal"}, Classifier: c}
}

func TestNonProposingTypesOnlyMarked(t *testing.T) {
	s := newStore(ev("1", "checkpoint", memoryapi.ActorAssistant, "personal"), ev("2", "action", memoryapi.ActorAssistant, "personal"))
	cl := &fakeClassifier{}
	n, err := Once(context.Background(), s, cfg(cl))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(s.proposed) != 0 || cl.calls != 0 {
		t.Fatalf("proposed=%d calls=%d", len(s.proposed), cl.calls)
	}
	if !s.classified["1"] || !s.classified["2"] {
		t.Fatal("not marked")
	}
}

func TestUserEntryProposedAttestedWithoutExternalCall(t *testing.T) {
	e := ev("1", "decision", memoryapi.ActorUser, "personal")
	s := newStore(e)
	cl := &fakeClassifier{category: "fact", pm: 900}
	if _, err := Once(context.Background(), s, cfg(cl)); err != nil {
		t.Fatal(err)
	}
	if cl.calls != 0 {
		t.Fatal("classifier called for user entry")
	}
	if len(s.proposed) != 1 || !s.attested[0] {
		t.Fatalf("%+v %v", s.proposed, s.attested)
	}
	m := s.proposed[0]
	if m.Probability != -1 || m.Category != "decision" || m.Project != "nas" || m.Scope != "personal" ||
		len(m.Evidence) != 1 || m.Evidence[0] != "1" || m.Statement != "Use SQLite for the index" {
		t.Fatalf("%+v", m)
	}
	if !s.classified["1"] {
		t.Fatal("not marked")
	}
}

func TestStatementIncludesUsefulBody(t *testing.T) {
	e := ev("1", "decision", memoryapi.ActorUser, "personal")
	e.Body = strings.Repeat("x", 600)
	s := newStore(e)
	Once(context.Background(), s, cfg(nil))
	want := "Use SQLite for the index: " + strings.Repeat("x", 400)
	if s.proposed[0].Statement != want {
		t.Fatalf("got %q", s.proposed[0].Statement)
	}

	// Body that only repeats the title adds nothing.
	e2 := ev("2", "decision", memoryapi.ActorUser, "personal")
	e2.Body = "  use sqlite for the index. "
	s2 := newStore(e2)
	Once(context.Background(), s2, cfg(nil))
	if s2.proposed[0].Statement != "Use SQLite for the index" {
		t.Fatalf("got %q", s2.proposed[0].Statement)
	}
}

func TestStatementCapped(t *testing.T) {
	e := ev("1", "decision", memoryapi.ActorUser, "personal")
	e.Title = strings.Repeat("t", 800)
	e.Body = strings.Repeat("b", 600)
	s := newStore(e)
	Once(context.Background(), s, cfg(nil))
	if n := len([]rune(s.proposed[0].Statement)); n != 1000 {
		t.Fatalf("len %d", n)
	}
}

func TestOutOfScopeNeverReachesClassifier(t *testing.T) {
	s := newStore(ev("1", "decision", memoryapi.ActorAssistant, "work/acme"))
	cl := &fakeClassifier{category: "fact", pm: 990, model: "m"}
	if _, err := Once(context.Background(), s, cfg(cl)); err != nil {
		t.Fatal(err)
	}
	if cl.calls != 0 {
		t.Fatal("private-scope entry sent to classifier")
	}
	if len(s.proposed) != 1 || s.proposed[0].Probability != -1 || s.attested[0] {
		t.Fatalf("%+v", s.proposed)
	}
}

func TestNilClassifierKeepsUnscored(t *testing.T) {
	s := newStore(ev("1", "decision", memoryapi.ActorAssistant, "personal"))
	if _, err := Once(context.Background(), s, Config{Roots: roots, ExternalScopes: []string{"personal"}}); err != nil {
		t.Fatal(err)
	}
	if len(s.proposed) != 1 || s.proposed[0].Probability != -1 {
		t.Fatalf("%+v", s.proposed)
	}
}

func TestClassifierResultApplied(t *testing.T) {
	s := newStore(ev("1", "observation", memoryapi.ActorAssistant, "personal"))
	cl := &fakeClassifier{category: "preference", pm: 870, model: "jev-1"}
	if _, err := Once(context.Background(), s, cfg(cl)); err != nil {
		t.Fatal(err)
	}
	m := s.proposed[0]
	if cl.calls != 1 || m.Category != "preference" || m.Probability != 870 || m.Model != "jev-1" || s.attested[0] {
		t.Fatalf("%+v calls=%d", m, cl.calls)
	}
}

func TestInvalidCategoryFallsBack(t *testing.T) {
	s := newStore(ev("1", "decision", memoryapi.ActorAssistant, "personal"))
	cl := &fakeClassifier{category: "gossip", pm: 500, model: "m"}
	Once(context.Background(), s, cfg(cl))
	if s.proposed[0].Category != "decision" {
		t.Fatalf("%q", s.proposed[0].Category)
	}
}

func TestProbabilityClamped(t *testing.T) {
	s := newStore(ev("1", "decision", memoryapi.ActorAssistant, "personal"), ev("2", "decision", memoryapi.ActorAssistant, "personal"))
	s.events[1].Title = "Second thing"
	cl := &fakeClassifier{category: "fact", pm: 5000, model: "m"}
	Once(context.Background(), s, cfg(cl))
	if s.proposed[0].Probability != 1000 {
		t.Fatalf("%d", s.proposed[0].Probability)
	}
	cl.pm = -7
	s2 := newStore(ev("3", "decision", memoryapi.ActorAssistant, "personal"))
	Once(context.Background(), s2, cfg(cl))
	if s2.proposed[0].Probability != 0 {
		t.Fatalf("%d", s2.proposed[0].Probability)
	}
}

func TestDuplicateSkipped(t *testing.T) {
	s := newStore(ev("1", "decision", memoryapi.ActorUser, "personal"))
	s.existing = []memoryapi.Memory{{ID: "m1", Project: "nas", Statement: "  use SQLite   for the index. ", Status: "accepted"}}
	cl := &fakeClassifier{}
	n, err := Once(context.Background(), s, cfg(cl))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(s.proposed) != 0 || cl.calls != 0 || !s.classified["1"] {
		t.Fatalf("proposed=%d calls=%d classified=%v", len(s.proposed), cl.calls, s.classified)
	}
	got := strings.Join(s.lastQuery.Statuses, ",")
	for _, want := range []string{"accepted", "candidate", "disputed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("statuses %q lack %s", got, want)
		}
	}
	if s.lastQuery.Project != "nas" {
		t.Fatalf("project %q", s.lastQuery.Project)
	}
}

func TestDifferentStatementNotDuplicate(t *testing.T) {
	s := newStore(ev("1", "decision", memoryapi.ActorUser, "personal"))
	s.existing = []memoryapi.Memory{{ID: "m1", Project: "nas", Statement: "Use Postgres", Status: "accepted"}}
	Once(context.Background(), s, cfg(nil))
	if len(s.proposed) != 1 {
		t.Fatal("should propose")
	}
}

func TestCorrectionStillProposed(t *testing.T) {
	e := ev("2", "correction", memoryapi.ActorUser, "personal")
	e.Supersedes = "1"
	s := newStore(e)
	Once(context.Background(), s, cfg(nil))
	if len(s.proposed) != 1 {
		t.Fatalf("correction types that propose must be proposed: %d", len(s.proposed))
	}
}

// retryableErr produces a genuine retryable jev error via a 503 server.
func retryableErr(t *testing.T) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c := &jev.Client{BaseURL: srv.URL, APIKey: "k"}
	_, err := c.Decide(context.Background(), "s", map[string]jev.Question{"q": {Type: "noul", Instructions: "i"}})
	if !jev.IsRetryable(err) {
		t.Fatalf("setup: %v", err)
	}
	return err
}

func TestRetryableErrorStopsRound(t *testing.T) {
	e2 := ev("2", "decision", memoryapi.ActorAssistant, "personal")
	e2.Title = "Second"
	s := newStore(ev("1", "decision", memoryapi.ActorAssistant, "personal"), e2)
	cl := &fakeClassifier{err: retryableErr(t)}
	n, err := Once(context.Background(), s, cfg(cl))
	if err != nil {
		t.Fatalf("a retryable classifier error is not a failure of the round: %v", err)
	}
	if n != 0 || cl.calls != 1 || len(s.proposed) != 0 || len(s.classified) != 0 {
		t.Fatalf("n=%d calls=%d proposed=%d classified=%v", n, cl.calls, len(s.proposed), s.classified)
	}
}

func TestRetryableAfterProgressKeepsEarlierWork(t *testing.T) {
	e1 := ev("1", "checkpoint", memoryapi.ActorAssistant, "personal")
	e2 := ev("2", "decision", memoryapi.ActorAssistant, "personal")
	s := newStore(e1, e2)
	cl := &fakeClassifier{err: retryableErr(t)}
	n, _ := Once(context.Background(), s, cfg(cl))
	if n != 1 || !s.classified["1"] || s.classified["2"] {
		t.Fatalf("n=%d %v", n, s.classified)
	}
}

func TestNonRetryableContinuesUnscored(t *testing.T) {
	e2 := ev("2", "decision", memoryapi.ActorAssistant, "personal")
	e2.Title = "Second"
	s := newStore(ev("1", "decision", memoryapi.ActorAssistant, "personal"), e2)
	cl := &fakeClassifier{err: errors.New("bad request")}
	n, err := Once(context.Background(), s, cfg(cl))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(s.proposed) != 2 || s.proposed[0].Probability != -1 || s.proposed[1].Probability != -1 || s.proposed[0].Model != "" {
		t.Fatalf("%+v", s.proposed)
	}
	if !s.classified["1"] || !s.classified["2"] {
		t.Fatal("not marked")
	}
}

func TestBatchBounds(t *testing.T) {
	var evs []memoryapi.Event
	for i := 0; i < 150; i++ {
		evs = append(evs, ev(string(rune('a'+i%26))+strings.Repeat("x", i/26), "checkpoint", memoryapi.ActorUser, "personal"))
	}
	s := newStore(evs...)
	n, _ := Once(context.Background(), s, Config{Roots: roots})
	if n != 20 {
		t.Fatalf("default batch: %d", n)
	}
	s2 := newStore(evs...)
	n, _ = Once(context.Background(), s2, Config{Roots: roots, Batch: 1000})
	if n != 100 {
		t.Fatalf("max batch: %d", n)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	s := newStore()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, s, Config{Roots: roots, Interval: 10 * time.Millisecond}) }()
	deadline := time.After(2 * time.Second)
	for {
		s.mu.Lock()
		calls := s.unclCalls
		s.mu.Unlock()
		if calls >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not loop")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestJevClassifier(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"model":"jev-9","answers":{
		 "decided":{"type":"noul","noul":0.8764},
		 "category":{"type":"choice","choice":"lesson","probabilities":{"fact":0.1,"preference":0.1,"decision":0.1,"constraint":0.1,"lesson":0.6},"confidence":0.6}}}`)
	}))
	defer srv.Close()
	cl := NewJevClassifier(&jev.Client{BaseURL: srv.URL, APIKey: "k"})
	e := ev("SECRET-ID", "observation", memoryapi.ActorAssistant, "personal")
	e.Body = "body text"
	e.Sources = []memoryapi.Source{{Kind: "file", Locator: "/secret/path", Excerpt: "SECRET-EXCERPT"}}
	cat, pm, model, err := cl.Classify(context.Background(), e)
	if err != nil || cat != "lesson" || pm != 876 || model != "jev-9" {
		t.Fatalf("%q %d %q %v", cat, pm, model, err)
	}
	raw, _ := json.Marshal(body)
	for _, bad := range []string{"SECRET-ID", "/secret/path", "SECRET-EXCERPT"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("request leaked %q: %s", bad, raw)
		}
	}
	for _, want := range []string{"Use SQLite for the index", "body text", "nas", "observation"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("state lacks %q: %s", want, raw)
		}
	}
	qs := body["questions"].(map[string]any)
	if qs["decided"].(map[string]any)["type"] != "noul" || qs["category"].(map[string]any)["type"] != "choice" {
		t.Fatalf("%v", qs)
	}
	crit := qs["category"].(map[string]any)["criteria"].(map[string]any)
	for _, k := range []string{"fact", "preference", "decision", "constraint", "lesson"} {
		if _, ok := crit[k]; !ok {
			t.Fatalf("missing option %s", k)
		}
	}
}

func TestJevClassifierPropagatesRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer srv.Close()
	cl := NewJevClassifier(&jev.Client{BaseURL: srv.URL, APIKey: "k"})
	_, _, _, err := cl.Classify(context.Background(), ev("1", "decision", memoryapi.ActorAssistant, "personal"))
	if !jev.IsRetryable(err) {
		t.Fatalf("%v", err)
	}
}
