package main

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"golang.org/x/crypto/bcrypt"
)

const memIssuer = "http://127.0.0.1:8080"

type memApprovalHarness struct {
	t        *testing.T
	store    *fakeMemoryStore
	approval *memoryApproval
	auth     *oauth.Server
	mux      *http.ServeMux
	jar      *cookiejar.Jar
}

func newMemApprovalHarness(t *testing.T) *memApprovalHarness {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("owner-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := oauth.New(oauth.Config{Issuer: memIssuer, DatabasePath: filepath.Join(t.TempDir(), "oauth.db"), PasswordHash: string(hash), ClientID: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auth.Close() })
	store := newFakeMemoryStore()
	approval := newMemoryApproval(memIssuer, store, []string{"personal"})
	mux := http.NewServeMux()
	mux.Handle(memoryApprovalPath, auth.BrowserActionHandler(approval, approval.known))
	mux.Handle("/", auth.Handler())
	jar, _ := cookiejar.New(nil)
	return &memApprovalHarness{t: t, store: store, approval: approval, auth: auth, mux: mux, jar: jar}
}

func (h *memApprovalHarness) request(method, destination, origin string, form url.Values) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(method, destination, strings.NewReader(form.Encode()))
	r.Header.Set("Origin", origin)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range h.jar.Cookies(r.URL) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	h.jar.SetCookies(r.URL, w.Result().Cookies())
	return w
}

// signIn runs the fresh OAuth browser sign-in that BrowserActionHandler demands.
func (h *memApprovalHarness) signIn(target string) {
	h.t.Helper()
	start := h.request("GET", target, memIssuer, nil)
	if start.Code != 303 {
		h.t.Fatalf("approval must start OAuth: %d", start.Code)
	}
	login := h.request("GET", start.Header().Get("Location"), memIssuer, nil)
	var session string
	for _, c := range login.Result().Cookies() {
		if strings.HasPrefix(c.Name, "nas_oauth_") {
			session = c.Value
		}
	}
	consent := h.request("POST", memIssuer+"/authorize", memIssuer, url.Values{"session": {session}, "password": {"owner-password"}, "consent": {"allow"}})
	if consent.Code != 303 {
		h.t.Fatalf("OAuth consent: %d", consent.Code)
	}
	if cb := h.request("GET", consent.Header().Get("Location"), memIssuer, nil); cb.Code != 303 {
		h.t.Fatalf("OAuth callback: %d", cb.Code)
	}
}

func (h *memApprovalHarness) prepare(kind, id string) (string, string) {
	h.t.Helper()
	out, err := h.approval.prepare(context.Background(), kind, id)
	if err != nil {
		h.t.Fatal(err)
	}
	link := out["approval_url"].(string)
	return link, link[strings.Index(link, "request=")+len("request="):]
}

func csrfFor(w *httptest.ResponseRecorder, nonce string) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == memoryCookieName(nonce) {
			return c.Value
		}
	}
	return ""
}

func TestMemoryApprovalShowsExactlyWhatIsForgottenAndEscapes(t *testing.T) {
	h := newMemApprovalHarness(t)
	h.store.memories["m1"] = memoryapi.Memory{ID: "m1", Category: "fact", Statement: `<script>alert("x")</script> & more`, Scope: "personal", Project: `<b>proj</b>`, Status: "accepted"}
	link, nonce := h.prepare("memory", "m1")
	target := link
	h.signIn(target)
	page := h.request("GET", target, memIssuer, nil)
	if page.Code != 200 {
		t.Fatalf("page: %d", page.Code)
	}
	body := page.Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>proj</b>") {
		t.Fatalf("page did not escape stored text: %s", body)
	}
	for _, want := range []string{"&lt;script&gt;", "&amp; more", "&lt;b&gt;proj&lt;/b&gt;", "personal", "fact", "Approve"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", page.Header())
	}
	if csrfFor(page, nonce) == "" {
		t.Fatal("no csrf cookie")
	}
	if len(h.store.forgottenList()) != 0 {
		t.Fatal("GET forgot something")
	}
}

func TestMemoryApprovalForgetsOnPostOnceAsUser(t *testing.T) {
	for _, kind := range []string{"event", "memory"} {
		t.Run(kind, func(t *testing.T) {
			h := newMemApprovalHarness(t)
			h.store.events["e1"] = memoryapi.Event{ID: "e1", Type: "decision", Title: "Pick DB", Scope: "personal", Project: "nas"}
			h.store.memories["e1"] = memoryapi.Memory{ID: "e1", Statement: "uses sqlite", Scope: "personal", Status: "accepted"}
			link, nonce := h.prepare(kind, "e1")
			h.signIn(link)
			page := h.request("GET", link, memIssuer, nil)
			if kind == "event" && !strings.Contains(page.Body.String(), "Pick DB") {
				t.Fatal("event page must show the title")
			}
			form := url.Values{"csrf": {csrfFor(page, nonce)}, "consent": {"approve"}}
			if w := h.request("POST", link, memIssuer, form); w.Code != 200 {
				t.Fatalf("approve: %d %s", w.Code, w.Body.String())
			}
			if got := h.store.forgottenList(); len(got) != 1 || got[0] != kind+":e1:"+memoryapi.ActorUser {
				t.Fatalf("forgotten = %v", got)
			}
			if w := h.request("POST", link, memIssuer, form); w.Code != 404 {
				t.Fatalf("replay: %d", w.Code)
			}
			if len(h.store.forgottenList()) != 1 {
				t.Fatal("replay forgot again")
			}
		})
	}
}

func TestMemoryApprovalRejections(t *testing.T) {
	for _, attack := range []string{"cross origin", "missing origin", "missing csrf", "wrong consent", "expired", "unknown token", "missing OAuth"} {
		t.Run(attack, func(t *testing.T) {
			h := newMemApprovalHarness(t)
			h.store.memories["m1"] = memoryapi.Memory{ID: "m1", Statement: "s", Scope: "personal", Status: "accepted"}
			link, nonce := h.prepare("memory", "m1")
			h.signIn(link)
			page := h.request("GET", link, memIssuer, nil)
			form := url.Values{"csrf": {csrfFor(page, nonce)}, "consent": {"approve"}}
			origin, want, target := memIssuer, 403, link
			switch attack {
			case "cross origin":
				origin = "https://evil.test"
			case "missing origin":
				origin = ""
			case "missing csrf":
				form.Del("csrf")
			case "wrong consent":
				form.Set("consent", "deny")
			case "expired":
				h.approval.pending[sha256.Sum256([]byte(nonce))].expires = time.Now().Add(-time.Second)
				want = 404
			case "unknown token":
				target = memIssuer + memoryApprovalPath + "?request=" + strings.Repeat("u", 43)
				want = 404
			case "missing OAuth":
				h.jar, _ = cookiejar.New(nil)
				want = 401
			}
			if w := h.request("POST", target, origin, form); w.Code != want {
				t.Fatalf("got %d, want %d", w.Code, want)
			}
			if len(h.store.forgottenList()) != 0 {
				t.Fatal("rejected approval forgot content")
			}
		})
	}
}

func TestMemoryApprovalKnownAndPrepareLimits(t *testing.T) {
	h := newMemApprovalHarness(t)
	h.store.memories["m1"] = memoryapi.Memory{ID: "m1", Statement: "s", Scope: "personal", Status: "accepted"}
	link, nonce := h.prepare("memory", "m1")
	if !h.approval.known(httptest.NewRequest("GET", link, nil)) {
		t.Fatal("fresh token must be known")
	}
	if h.approval.known(httptest.NewRequest("GET", memoryApprovalPath+"?request=short", nil)) {
		t.Fatal("malformed token must be unknown")
	}
	h.approval.pending[sha256.Sum256([]byte(nonce))].expires = time.Now().Add(-time.Second)
	if h.approval.known(httptest.NewRequest("GET", link, nil)) {
		t.Fatal("expired token must be unknown")
	}
	if _, err := h.approval.prepare(context.Background(), "memory", "missing"); err == nil {
		t.Fatal("unknown id must not prepare")
	}
	if _, err := h.approval.prepare(context.Background(), "weird", "m1"); err == nil {
		t.Fatal("unknown kind must not prepare")
	}
}
