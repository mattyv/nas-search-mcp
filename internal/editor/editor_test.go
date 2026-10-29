package editor

import (
	"bytes"
	"encoding/json"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
)

const (
	testOwner  = "owner@example.com"
	testOrigin = "https://nas.tail1234.ts.net"
	xss        = `<script>alert(1)</script>`
)

func testConfig() Config {
	return Config{Owner: testOwner, Origin: testOrigin, Roots: []string{"personal", "work/acme"}, DefaultScope: "personal"}
}

func seeded(t *testing.T) (http.Handler, *fakeStore) {
	t.Helper()
	f := newFake()
	day := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	f.projects = []memoryapi.ProjectSummary{
		{Project: "alpha", Scope: "personal", LastActivity: day, Events: 2, CheckpointVersion: 2, OpenCandidates: 1},
		{Project: xss, Scope: "personal", LastActivity: day, CheckpointVersion: 0},
	}
	f.events = []memoryapi.Event{
		{ID: "ev1", Type: "decision", Status: "confirmed", Title: xss, Body: "body " + xss, Project: "alpha", Scope: "personal",
			Client: "claude", Actor: memoryapi.ActorAssistant, OccurredAt: day, RecordedAt: day,
			Sources:     []memoryapi.Source{{Kind: "file", Locator: "/notes/a.md", Excerpt: "excerpt " + xss, Available: true}, {Kind: "url", Locator: "javascript:alert(1)", Available: false}},
			Related:     []string{"ev2"},
			NextActions: []string{"call the plumber"}},
		{ID: "ev2", Type: "observation", Status: "proposed", Title: "older", Project: "alpha", Scope: "personal",
			Client: "codex", Actor: memoryapi.ActorAssistant, OccurredAt: day.Add(-48 * time.Hour), RecordedAt: day},
		{ID: "ev3", Type: "manual_note", Status: "confirmed", Title: "other project", Project: "beta", Scope: "personal",
			Client: "editor", Actor: memoryapi.ActorUser, OccurredAt: day.Add(time.Hour), RecordedAt: day},
	}
	f.checkpoints["alpha"] = []memoryapi.Checkpoint{
		{Project: "alpha", Scope: "personal", Version: 1, Summary: "first"},
		{Project: "alpha", Scope: "personal", Version: 2, Summary: "summary " + xss, Goal: "ship", Done: []string{"one", "two"}, Blockers: []string{"none"}, Next: []string{"n1", "n2"}, Decisions: []string{"d1"}, UpdatedAt: day},
	}
	f.checkpoints[xss] = []memoryapi.Checkpoint{}
	f.memories["m-cand"] = memoryapi.Memory{ID: "m-cand", Category: "fact", Statement: "cand " + xss, Scope: "personal", Project: "alpha", Status: "candidate", Evidence: []string{"ev1"}, Probability: 730, Model: "clf-1", CreatedAt: day}
	f.memories["m-disp"] = memoryapi.Memory{ID: "m-disp", Category: "fact", Statement: "disputed one", Scope: "personal", Project: "alpha", Status: "disputed", Evidence: []string{"ev2"}, Probability: -1, CreatedAt: day}
	f.memories["m-acc"] = memoryapi.Memory{ID: "m-acc", Category: "decision", Statement: "accepted one", Scope: "personal", Project: "alpha", Status: "accepted", Probability: 900, CreatedAt: day}
	f.memories["m-rej"] = memoryapi.Memory{ID: "m-rej", Category: "fact", Statement: "rejected one", Scope: "personal", Project: "alpha", Status: "rejected", CreatedAt: day}
	h, err := New(f, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

type reqOpt func(*http.Request)

func noLogin() reqOpt { return func(r *http.Request) { r.Header.Del("Tailscale-User-Login") } }
func login(l string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Tailscale-User-Login", l) }
}
func origin(o string) reqOpt {
	return func(r *http.Request) {
		if o == "" {
			r.Header.Del("Origin")
		} else {
			r.Header.Set("Origin", o)
		}
	}
}
func ctype(c string) reqOpt { return func(r *http.Request) { r.Header.Set("Content-Type", c) } }

func do(h http.Handler, method, target, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Tailscale-User-Login", testOwner)
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func form(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v.Encode()
}

func mustContain(t *testing.T, w *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	b := w.Body.String()
	for _, s := range want {
		if !strings.Contains(b, s) {
			t.Errorf("body missing %q\n%s", s, b)
		}
	}
}

func mustNotContain(t *testing.T, w *httptest.ResponseRecorder, bad ...string) {
	t.Helper()
	b := w.Body.String()
	for _, s := range bad {
		if strings.Contains(b, s) {
			t.Errorf("body unexpectedly contains %q", s)
		}
	}
}

func status(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, want, w.Body.String())
	}
}

func TestNewValidatesConfig(t *testing.T) {
	good := testConfig()
	bad := map[string]func(*Config){
		"no owner":         func(c *Config) { c.Owner = "" },
		"http origin":      func(c *Config) { c.Origin = "http://nas.ts.net" },
		"origin path":      func(c *Config) { c.Origin = "https://nas.ts.net/x" },
		"origin slash":     func(c *Config) { c.Origin = "https://nas.ts.net/" },
		"origin no host":   func(c *Config) { c.Origin = "https://" },
		"origin empty":     func(c *Config) { c.Origin = "" },
		"origin query":     func(c *Config) { c.Origin = "https://nas.ts.net?x=1" },
		"origin userinfo":  func(c *Config) { c.Origin = "https://u@nas.ts.net" },
		"no roots":         func(c *Config) { c.Roots = nil },
		"invalid root":     func(c *Config) { c.Roots = []string{"Bad Root"} },
		"default outside":  func(c *Config) { c.DefaultScope = "family" },
		"default empty":    func(c *Config) { c.DefaultScope = "" },
		"default prefixed": func(c *Config) { c.DefaultScope = "personalx" },
	}
	for name, mut := range bad {
		c := good
		c.Roots = append([]string(nil), good.Roots...)
		mut(&c)
		if _, err := New(newFake(), c); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := New(newFake(), good); err != nil {
		t.Fatal(err)
	}
	c := good
	c.Origin = "https://nas.ts.net:8443"
	c.DefaultScope = "work/acme/sub"
	if _, err := New(newFake(), c); err != nil {
		t.Fatalf("port origin and nested default scope should be valid: %v", err)
	}
}

func TestOwnerGate(t *testing.T) {
	h, f := seeded(t)
	paths := []string{"/", "/p/alpha", "/timeline", "/new", "/memories", "/review", "/e/ev1", "/manifest.webmanifest", "/sw.js", "/static/app.css"}
	for _, p := range paths {
		for name, opt := range map[string]reqOpt{"missing": noLogin(), "other": login("mallory@example.com"), "case": login("OWNER@example.com")} {
			w := do(h, "GET", p, "", opt)
			if w.Code != 403 {
				t.Errorf("%s %s: status %d, want 403", name, p, w.Code)
			}
			if strings.Contains(w.Body.String(), "alpha") {
				t.Errorf("%s %s leaked data", name, p)
			}
		}
	}
	w := do(h, "POST", "/new", form("title", "t", "type", "manual_note", "scope", "personal"), login("mallory@example.com"))
	status(t, w, 403)
	if n := f.count("AppendEvent"); n != 0 {
		t.Fatalf("non-owner wrote %d events", n)
	}
	if len(f.calls) != 0 {
		t.Fatalf("store touched for forbidden requests: %v", f.calls)
	}
}

func TestOriginCheckOnWrites(t *testing.T) {
	h, f := seeded(t)
	body := form("title", "hello", "type", "manual_note", "scope", "personal")
	for name, o := range map[string]string{"missing": "", "http": "http://nas.tail1234.ts.net", "other host": "https://evil.example", "slash": testOrigin + "/", "null": "null"} {
		w := do(h, "POST", "/new", body, origin(o))
		if w.Code != 403 {
			t.Errorf("%s: status %d, want 403", name, w.Code)
		}
	}
	if f.count("AppendEvent") != 0 {
		t.Fatal("write happened despite bad Origin")
	}
	status(t, do(h, "POST", "/new", body), 303)
	if f.count("AppendEvent") != 1 {
		t.Fatal("good Origin should write")
	}
	// Other methods are gated too.
	status(t, do(h, "DELETE", "/e/ev1", "", origin("")), 403)
}

func TestBodyRules(t *testing.T) {
	h, f := seeded(t)
	status(t, do(h, "POST", "/new", "title=x", ctype("text/plain")), 415)
	status(t, do(h, "POST", "/new", "x=y", ctype("multipart/form-data; boundary=x")), 415)
	big := form("title", "t", "type", "manual_note", "scope", "personal", "body", strings.Repeat("a", 70*1024))
	status(t, do(h, "POST", "/new", big), 413)
	jbig, _ := json.Marshal(map[string]string{"title": "t", "type": "manual_note", "scope": "personal", "body": strings.Repeat("a", 70*1024)})
	status(t, do(h, "POST", "/new", string(jbig), ctype("application/json")), 413)
	if f.count("AppendEvent") != 0 {
		t.Fatal("rejected bodies must not write")
	}
	status(t, do(h, "POST", "/new", form("title", "t", "type", "manual_note", "scope", "personal"), ctype("application/x-www-form-urlencoded; charset=UTF-8")), 303)
}

func TestSecurityHeadersAndNoInlineCode(t *testing.T) {
	h, _ := seeded(t)
	const csp = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	inline := regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>\s*[^<\s]|\sstyle\s*=|<style|\son[a-z]+\s*=`)
	for _, p := range []string{"/", "/p/alpha", "/p/alpha/checkpoint", "/timeline", "/e/ev1", "/e/ev1/correct", "/e/ev1/forget", "/new", "/memories?status=all", "/review", "/nope"} {
		w := do(h, "GET", p, "")
		hd := w.Header()
		if hd.Get("Content-Security-Policy") != csp {
			t.Errorf("%s: CSP = %q", p, hd.Get("Content-Security-Policy"))
		}
		if hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s: missing nosniff/referrer headers", p)
		}
		if hd.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", p, hd.Get("Cache-Control"))
		}
		if m := inline.FindString(w.Body.String()); m != "" {
			t.Errorf("%s: inline script/style found: %q", p, m)
		}
	}
	w := do(h, "GET", "/", "", noLogin())
	if w.Header().Get("Content-Security-Policy") != csp {
		t.Error("403 response lacks CSP")
	}
	w = do(h, "GET", "/static/app.css", "")
	if w.Header().Get("Content-Security-Policy") != csp || strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Cache-Control") == "" {
		t.Errorf("static headers wrong: %v", w.Header())
	}
}

func TestPagesRenderAndEscape(t *testing.T) {
	h, _ := seeded(t)
	escaped := "&lt;script&gt;alert(1)&lt;/script&gt;"
	pages := []string{"/", "/p/alpha", "/p/alpha/checkpoint", "/timeline?day=2026-10-27", "/e/ev1", "/e/ev1/correct", "/e/ev1/forget", "/new", "/memories?status=all", "/review", "/memories/m-cand/forget"}
	for _, p := range pages {
		w := do(h, "GET", p, "")
		status(t, w, 200)
		mustNotContain(t, w, xss)
		mustContain(t, w, `rel="manifest"`, `apple-touch-icon`, `apple-mobile-web-app-capable`, `/static/app.js`, "Projects", "Timeline", "New", "Review")
		if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: content type %q", p, w.Header().Get("Content-Type"))
		}
	}
	for _, p := range []string{"/", "/p/alpha", "/e/ev1", "/review", "/memories?status=all", "/p/alpha/checkpoint"} {
		mustContain(t, do(h, "GET", p, ""), escaped)
	}
	// Project names are escaped in paths, too.
	w := do(h, "GET", "/", "")
	mustContain(t, w, "/p/"+url.PathEscape(xss))
}

func TestProjectsPage(t *testing.T) {
	h, _ := seeded(t)
	w := do(h, "GET", "/", "")
	mustContain(t, w, "alpha", `datetime="2026-10-27T10:00:00Z"`, "v2", "1 to review")
}

func TestProjectPage(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/p/alpha", "")
	status(t, w, 200)
	if f.lastCtx.Project != "alpha" || f.lastCtx.Budget != 8000 {
		t.Fatalf("OpenContext request = %+v", f.lastCtx)
	}
	mustContain(t, w, "ship", "one", "none", "n1", "d1", "accepted one", "not confirmed", "cand ", "disputed one", "Edit checkpoint", "/p/alpha/checkpoint", "/e/ev1")
	status(t, do(h, "GET", "/p/unknown", ""), 404)
}

func TestCheckpointEditAndSave(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/p/alpha/checkpoint", "")
	status(t, w, 200)
	mustContain(t, w, `name="expected_version"`, `value="2"`, "ship", "n1\nn2")
	w = do(h, "POST", "/p/alpha/checkpoint", form("expected_version", "2", "summary", "new summary", "goal", "g", "done", "a\r\nb\r\n\r\n", "next", "x", "blockers", "", "decisions", "", "branch", "main", "commit", "abc"))
	status(t, w, 303)
	cp := f.latest("alpha")
	if cp.Version != 3 || cp.Summary != "new summary" || len(cp.Done) != 2 || cp.Done[1] != "b" || cp.Actor != memoryapi.ActorUser || cp.Client != "editor" || cp.Scope != "personal" || cp.Branch != "main" {
		t.Fatalf("checkpoint = %+v", cp)
	}
	if !strings.Contains(strings.Join(f.calls, "|"), "SaveCheckpoint expected=2 actor=user client=editor") {
		t.Fatalf("calls: %v", f.calls)
	}
	status(t, do(h, "POST", "/p/alpha/checkpoint", form("expected_version", "x", "summary", "s")), 400)
	status(t, do(h, "POST", "/p/alpha/checkpoint", form("expected_version", "3", "summary", "  ")), 400)
}

func TestCheckpointConflictKeepsText(t *testing.T) {
	h, f := seeded(t)
	// Someone else saved version 3 after the form was opened at version 2.
	f.checkpoints["alpha"] = append(f.checkpoints["alpha"], memoryapi.Checkpoint{Project: "alpha", Scope: "personal", Version: 3, Summary: "from the laptop"})
	w := do(h, "POST", "/p/alpha/checkpoint", form("expected_version", "2", "summary", "my precious text", "next", "my next step"))
	status(t, w, 409)
	mustContain(t, w, "changed elsewhere", "my precious text", "my next step", `name="expected_version" value="3"`, "from the laptop")
	if len(f.checkpoints["alpha"]) != 3 {
		t.Fatal("conflicting save must not store")
	}
}

func TestTimeline(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/timeline?day=2026-10-27", "")
	status(t, w, 200)
	if !f.lastQuery.From.Equal(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) || !f.lastQuery.To.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("query window = %v..%v", f.lastQuery.From, f.lastQuery.To)
	}
	mustContain(t, w, "/e/ev1", "/e/ev3", "2026-10-27", "day=2026-10-26", "day=2026-10-28")
	mustNotContain(t, w, "/e/ev2")
	w = do(h, "GET", "/timeline?day=2026-10-27&project=alpha", "")
	mustContain(t, w, "/e/ev1")
	mustNotContain(t, w, "/e/ev3")
	if f.lastQuery.Project != "alpha" {
		t.Fatal("project filter not passed")
	}
	mustContain(t, w, "project=alpha")
	status(t, do(h, "GET", "/timeline?day=garbage", ""), 400)
	status(t, do(h, "GET", "/timeline", ""), 200)
}

func TestEventDetail(t *testing.T) {
	h, _ := seeded(t)
	w := do(h, "GET", "/e/ev1", "")
	status(t, w, 200)
	mustContain(t, w, "/notes/a.md", "call the plumber", `/e/ev2`, "confirmed", "Correct", "Forget", "/e/ev1/correct", "/e/ev1/forget", "no longer available")
	mustNotContain(t, w, `href="javascript:`)
	status(t, do(h, "GET", "/e/missing", ""), 404)
}

func TestCorrectEvent(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/e/ev1/correct", "")
	status(t, w, 200)
	w = do(h, "POST", "/e/ev1/correct", form("title", "Fixed title", "body", "fixed", "next_actions", "a\nb", "idempotency_key", "k-1"))
	status(t, w, 303)
	got := f.events[len(f.events)-1]
	if got.Type != "correction" || got.Supersedes != "ev1" || got.Actor != memoryapi.ActorUser || got.Client != "editor" || got.Title != "Fixed title" || got.Project != "alpha" || got.Scope != "personal" || len(got.NextActions) != 2 {
		t.Fatalf("event = %+v", got)
	}
	status(t, do(h, "POST", "/e/ev1/correct", form("title", "")), 400)
}

func TestForgetEventNeedsConfirmation(t *testing.T) {
	h, f := seeded(t)
	mustContain(t, do(h, "GET", "/e/ev1/forget", ""), `name="confirm"`, "cannot be undone")
	w := do(h, "POST", "/e/ev1/forget", form())
	status(t, w, 400)
	mustContain(t, w, "Confirm")
	if f.count("ForgetEvent") != 0 {
		t.Fatal("forgot without confirmation")
	}
	status(t, do(h, "POST", "/e/ev1/forget", form("confirm", "yes")), 303)
	if len(f.forgotten) != 1 || f.forgotten[0] != "event:ev1" || f.forgetActor[0] != memoryapi.ActorUser {
		t.Fatalf("forgotten = %v %v", f.forgotten, f.forgetActor)
	}
}

func TestNewNoteForm(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/new", "")
	status(t, w, 200)
	mustContain(t, w, `name="idempotency_key"`, `value="work/acme"`, `value="personal" selected`, "manual_note", "exploration", `id="note-form"`)
	mustNotContain(t, w, `value="checkpoint"`, `value="discussion"`)
	w = do(h, "POST", "/new", form("type", "decision", "project", "alpha", "scope", "work/acme", "title", "Use SQLite", "body", "because", "next_actions", "one\r\n\r\n two \r\n", "idempotency_key", "abc-1"))
	status(t, w, 303)
	e := f.events[len(f.events)-1]
	if e.Type != "decision" || e.Project != "alpha" || e.Scope != "work/acme" || e.Title != "Use SQLite" || e.Actor != memoryapi.ActorUser || e.Client != "editor" || e.IdempotencyKey != "abc-1" || len(e.NextActions) != 2 || e.NextActions[1] != "two" || e.OccurredAt.IsZero() {
		t.Fatalf("event = %+v", e)
	}
	if strings.Join(f.lastRoots, ",") != "personal,work/acme" {
		t.Fatalf("roots = %v", f.lastRoots)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/e/") {
		t.Fatalf("location = %q", loc)
	}
	before := len(f.events)
	for name, body := range map[string]string{
		"scope outside": form("type", "decision", "scope", "family", "title", "t"),
		"bad type":      form("type", "checkpoint", "scope", "personal", "title", "t"),
		"no title":      form("type", "decision", "scope", "personal", "title", " "),
		"long title":    form("type", "decision", "scope", "personal", "title", strings.Repeat("x", 201)),
	} {
		if w := do(h, "POST", "/new", body); w.Code != 400 {
			t.Errorf("%s: status %d", name, w.Code)
		}
	}
	if len(f.events) != before {
		t.Fatal("invalid notes were stored")
	}
}

func TestJSONNoteIsIdempotent(t *testing.T) {
	h, f := seeded(t)
	payload := `{"type":"observation","project":"alpha","scope":"personal","title":"Offline thought","body":"b","next_actions":["x","y"],"idempotency_key":"offline-123","occurred_at":"2026-10-27T08:30:00Z"}`
	var ids []string
	var created []bool
	for i := 0; i < 2; i++ {
		w := do(h, "POST", "/new", payload, ctype("application/json"))
		status(t, w, 200)
		if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("content type %q", w.Header().Get("Content-Type"))
		}
		var resp struct {
			ID      string `json:"id"`
			Created bool   `json:"created"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.ID == "" {
			t.Fatalf("bad response %q: %v", w.Body.String(), err)
		}
		ids = append(ids, resp.ID)
		created = append(created, resp.Created)
	}
	if ids[0] != ids[1] || !created[0] || created[1] {
		t.Fatalf("ids=%v created=%v", ids, created)
	}
	n := 0
	for _, e := range f.events {
		if e.IdempotencyKey == "offline-123" {
			n++
			if e.Actor != memoryapi.ActorUser || e.Client != "editor" || !e.OccurredAt.Equal(time.Date(2026, 10, 27, 8, 30, 0, 0, time.UTC)) || len(e.NextActions) != 2 {
				t.Fatalf("event = %+v", e)
			}
		}
	}
	if n != 1 {
		t.Fatalf("stored %d copies", n)
	}
	w := do(h, "POST", "/new", `{"type":"observation","scope":"personal","title":""}`, ctype("application/json"))
	status(t, w, 400)
	mustContain(t, w, `"error"`)
	status(t, do(h, "POST", "/new", `{not json`, ctype("application/json")), 400)
}

func hasToValue(w *httptest.ResponseRecorder, v string) bool {
	return strings.Contains(w.Body.String(), `name="to" value="`+v+`"`)
}

func TestMemoriesOfferOnlyValidTransitions(t *testing.T) {
	h, _ := seeded(t)
	cases := map[string]struct{ want, not []string }{
		"candidate": {[]string{"accepted", "rejected", "disputed"}, []string{"retracted", "superseded"}},
		"accepted":  {[]string{"superseded", "retracted", "disputed"}, []string{"accepted", "rejected"}},
		"disputed":  {[]string{"accepted", "rejected", "retracted"}, []string{"superseded", "disputed"}},
		"rejected":  {nil, []string{"accepted", "rejected", "retracted", "superseded", "disputed"}},
	}
	for st, c := range cases {
		w := do(h, "GET", "/memories?status="+st, "")
		status(t, w, 200)
		for _, v := range c.want {
			if !hasToValue(w, v) {
				t.Errorf("%s: missing action %s", st, v)
			}
		}
		for _, v := range c.not {
			if hasToValue(w, v) {
				t.Errorf("%s: offered invalid action %s", st, v)
			}
		}
		mustContain(t, w, "/memories/", "/forget")
	}
	w := do(h, "GET", "/memories", "")
	mustContain(t, w, "accepted one")
	mustNotContain(t, w, "rejected one")
	w = do(h, "GET", "/memories?status=all&project=alpha", "")
	mustContain(t, w, "rejected one", "accepted one", "disputed one")
}

func TestMemoryTransitions(t *testing.T) {
	h, f := seeded(t)
	// Valid: retract an accepted memory with a reason.
	status(t, do(h, "POST", "/memories/m-acc/transition", form("to", "retracted", "reason", "no longer true")), 303)
	c := f.corrections[len(f.corrections)-1]
	if c.MemoryID != "m-acc" || c.Status != "retracted" || c.Reason != "no longer true" || c.Actor != memoryapi.ActorUser || c.Client != "editor" {
		t.Fatalf("correction = %+v", c)
	}
	n := len(f.corrections)
	// Terminal state: nothing more is allowed.
	status(t, do(h, "POST", "/memories/m-acc/transition", form("to", "accepted", "reason", "oops")), 400)
	// Invalid from the start state.
	status(t, do(h, "POST", "/memories/m-rej/transition", form("to", "accepted", "reason", "x")), 400)
	// Reason required.
	status(t, do(h, "POST", "/memories/m-cand/transition", form("to", "accepted", "reason", "  ")), 400)
	// Unknown target.
	status(t, do(h, "POST", "/memories/m-cand/transition", form("to", "deleted", "reason", "x")), 400)
	// Supersede needs a new statement.
	f.memories["m-acc2"] = memoryapi.Memory{ID: "m-acc2", Category: "fact", Statement: "old", Scope: "personal", Status: "accepted"}
	status(t, do(h, "POST", "/memories/m-acc2/transition", form("to", "superseded", "reason", "changed")), 400)
	status(t, do(h, "POST", "/memories/m-acc2/transition", form("to", "superseded", "reason", "changed", "statement", "new text")), 303)
	last := f.corrections[len(f.corrections)-1]
	if last.Status != "superseded" || last.Statement != "new text" {
		t.Fatalf("correction = %+v", last)
	}
	if len(f.corrections) != n+1 {
		t.Fatalf("invalid transitions reached the store: %d", len(f.corrections)-n)
	}
	status(t, do(h, "POST", "/memories/nope/transition", form("to", "accepted", "reason", "x")), 404)
}

func TestForgetMemoryNeedsConfirmation(t *testing.T) {
	h, f := seeded(t)
	mustContain(t, do(h, "GET", "/memories/m-acc/forget", ""), `name="confirm"`)
	status(t, do(h, "POST", "/memories/m-acc/forget", form()), 400)
	if f.count("ForgetMemory") != 0 {
		t.Fatal("forgot without confirmation")
	}
	status(t, do(h, "POST", "/memories/m-acc/forget", form("confirm", "yes")), 303)
	if len(f.forgotten) != 1 || f.forgotten[0] != "memory:m-acc" || f.forgetActor[0] != memoryapi.ActorUser {
		t.Fatalf("forgotten = %v", f.forgotten)
	}
}

func TestReviewQueue(t *testing.T) {
	h, f := seeded(t)
	w := do(h, "GET", "/review", "")
	status(t, w, 200)
	if strings.Join(f.lastMemQ.Statuses, ",") != "candidate,disputed" {
		t.Fatalf("statuses = %v", f.lastMemQ.Statuses)
	}
	mustContain(t, w, "73", "clf-1", "unscored", "/e/ev1", "/e/ev2", "disputed one", "Accept", "Reject", "Edit &amp; accept", "/review/m-cand")
	mustNotContain(t, w, "accepted one", "rejected one")
}

func TestReviewActions(t *testing.T) {
	h, f := seeded(t)
	status(t, do(h, "POST", "/review/m-cand", form("action", "accept", "statement", "ignored", "reason", "")), 303)
	c := f.corrections[0]
	if c.MemoryID != "m-cand" || c.Status != "accepted" || c.Actor != memoryapi.ActorUser || c.Client != "editor" || c.Reason == "" {
		t.Fatalf("accept = %+v", c)
	}
	if f.memories["m-cand"].Statement != "cand "+xss {
		t.Fatal("plain accept must not change the statement")
	}
	status(t, do(h, "POST", "/review/m-disp", form("action", "reject", "reason", "wrong")), 303)
	c = f.corrections[1]
	if c.MemoryID != "m-disp" || c.Status != "rejected" || c.Reason != "wrong" {
		t.Fatalf("reject = %+v", c)
	}
	// Already decided: no second decision.
	status(t, do(h, "POST", "/review/m-cand", form("action", "reject")), 400)
	status(t, do(h, "POST", "/review/m-cand", form("action", "bogus")), 400)
	if len(f.corrections) != 2 {
		t.Fatalf("corrections = %d", len(f.corrections))
	}
}

func TestReviewEditAndAccept(t *testing.T) {
	h, f := seeded(t)
	f.memories["m-edit"] = memoryapi.Memory{ID: "m-edit", Category: "preference", Statement: "orig", Scope: "work/acme", Project: "alpha", Status: "candidate", Evidence: []string{"ev1", "ev2"}, Probability: 500}
	before := len(f.calls)
	status(t, do(h, "POST", "/review/m-edit", form("action", "edit", "statement", "edited text")), 303)
	if len(f.proposed) != 1 {
		t.Fatalf("proposed = %d", len(f.proposed))
	}
	p := f.proposed[0]
	if p.Statement != "edited text" || p.Category != "preference" || p.Scope != "work/acme" || p.Project != "alpha" || strings.Join(p.Evidence, ",") != "ev1,ev2" || !f.proposedAtt[0] {
		t.Fatalf("proposed = %+v att=%v", p, f.proposedAtt)
	}
	c := f.corrections[len(f.corrections)-1]
	if c.MemoryID != "m-edit" || c.Status != "rejected" || c.Reason != "replaced by edited version" || c.Actor != memoryapi.ActorUser {
		t.Fatalf("correction = %+v", c)
	}
	var order []string
	for _, call := range f.calls[before:] {
		if strings.HasPrefix(call, "ProposeMemory") || strings.HasPrefix(call, "CorrectMemory") {
			order = append(order, strings.Fields(call)[0])
		}
	}
	if strings.Join(order, ",") != "ProposeMemory,CorrectMemory" {
		t.Fatalf("order = %v", order)
	}
	status(t, do(h, "POST", "/review/m-edit", form("action", "edit", "statement", "again")), 400)
	f.memories["m-x"] = memoryapi.Memory{ID: "m-x", Category: "fact", Statement: "s", Scope: "personal", Status: "candidate"}
	n := len(f.proposed)
	status(t, do(h, "POST", "/review/m-x", form("action", "edit", "statement", "  ")), 400)
	if len(f.proposed) != n {
		t.Fatal("blank edit must not propose")
	}
}

func TestPWAAssets(t *testing.T) {
	h, _ := seeded(t)
	w := do(h, "GET", "/manifest.webmanifest", "")
	status(t, w, 200)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Fatalf("manifest type %q", ct)
	}
	var m struct {
		Name, ShortName, StartURL, Display, ThemeColor, BackgroundColor string
		Icons                                                           []struct{ Src, Sizes, Type, Purpose string }
	}
	raw := strings.NewReplacer("short_name", "ShortName", "start_url", "StartURL", "theme_color", "ThemeColor", "background_color", "BackgroundColor").Replace(w.Body.String())
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "NAS Memory" || m.ShortName != "Memory" || m.StartURL != "/" || m.Display != "standalone" || m.ThemeColor == "" || m.BackgroundColor == "" {
		t.Fatalf("manifest = %+v", m)
	}
	sizes := map[string]bool{}
	for _, ic := range m.Icons {
		sizes[ic.Sizes] = true
		if ic.Type != "image/png" || !strings.Contains(ic.Purpose, "any") || !strings.Contains(ic.Purpose, "maskable") {
			t.Errorf("icon %+v", ic)
		}
		iw := do(h, "GET", ic.Src, "")
		status(t, iw, 200)
		if iw.Header().Get("Content-Type") != "image/png" {
			t.Errorf("%s type %q", ic.Src, iw.Header().Get("Content-Type"))
		}
		img, err := png.Decode(bytes.NewReader(iw.Body.Bytes()))
		if err != nil {
			t.Fatalf("%s: %v", ic.Src, err)
		}
		if want := strings.Split(ic.Sizes, "x")[0]; want != itoa(img.Bounds().Dx()) {
			t.Errorf("%s declared %s, is %d", ic.Src, ic.Sizes, img.Bounds().Dx())
		}
	}
	if !sizes["192x192"] || !sizes["512x512"] {
		t.Fatalf("icons = %v", sizes)
	}
	aw := do(h, "GET", "/apple-touch-icon.png", "")
	status(t, aw, 200)
	if img, err := png.Decode(bytes.NewReader(aw.Body.Bytes())); err != nil || img.Bounds().Dx() != 180 {
		t.Fatalf("apple icon: %v", err)
	}

	sw := do(h, "GET", "/sw.js", "")
	status(t, sw, 200)
	if ct := sw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") && !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("sw type %q", ct)
	}
	if sw.Header().Get("Service-Worker-Allowed") != "/" || sw.Header().Get("Cache-Control") == "" {
		t.Fatalf("sw headers %v", sw.Header())
	}
	mustContain(t, sw, "fetch", "caches")
	mustNotContain(t, sw, "__VERSION__")
	for _, p := range []string{"/static/app.js", "/static/app.css"} {
		w := do(h, "GET", p, "")
		status(t, w, 200)
	}
	if ct := do(h, "GET", "/static/app.css", "").Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("css type %q", ct)
	}
	if w := do(h, "GET", "/static/../editor.go", ""); w.Code == 200 || strings.Contains(w.Body.String(), "package editor") {
		t.Fatalf("path traversal served a file: %d", w.Code)
	}
	status(t, do(h, "GET", "/static/sw.js", ""), 404)
	status(t, do(h, "GET", "/static/", ""), 404)
	status(t, do(h, "POST", "/static/app.css", ""), 405)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestEmbeddedAssetsStaySmall(t *testing.T) {
	total := 0
	for _, root := range []fs.FS{staticFS, templateFS} {
		_ = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				info, _ := d.Info()
				total += int(info.Size())
			}
			return nil
		})
	}
	if total == 0 || total > 300*1024 {
		t.Fatalf("embedded size %d bytes", total)
	}
}

func TestStoreErrorsMapToStatus(t *testing.T) {
	h, _ := seeded(t)
	status(t, do(h, "GET", "/e/missing/correct", ""), 404)
	status(t, do(h, "GET", "/nope", ""), 404)
}

// A home-screen app has no browser reload, so every page carries its own Refresh control.
func TestEveryPageHasRefresh(t *testing.T) {
	h, _ := seeded(t)
	for _, p := range []string{"/", "/p/alpha", "/timeline", "/e/ev1", "/new", "/review", "/memories?status=all"} {
		mustContain(t, do(h, "GET", p, ""), `id="refresh"`, ">Refresh<")
	}
	js := do(h, "GET", "/static/app.js", "")
	status(t, js, 200)
	mustContain(t, js, `getElementById("refresh")`, "location.reload")
}
