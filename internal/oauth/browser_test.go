package oauth

import (
	"encoding/json"
	"golang.org/x/time/rate"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDownloadStartsOAuthInsteadOfPasswordSession(t *testing.T) {
	s := testServer(t)
	served := false
	h := s.DownloadHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = true }), func(*http.Request) bool { return true })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", s.cfg.Issuer+"/download/test-token/file.pdf", nil))
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), s.cfg.Issuer+"/authorize?") || served {
		t.Fatalf("download must start a browser OAuth authorization: status=%d served=%v", w.Code, served)
	}
}

func TestPasswordAloneCannotDownload(t *testing.T) {
	s := testServer(t)
	served := false
	h := s.DownloadHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = true }), func(*http.Request) bool { return true })
	r := httptest.NewRequest("POST", s.cfg.Issuer+"/download/test-token/file.pdf", strings.NewReader("password=correct+horse+battery+staple"))
	r.Header.Set("Origin", s.cfg.Issuer)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || served || len(w.Result().Cookies()) != 0 {
		t.Fatalf("password alone granted download: status=%d served=%v", w.Code, served)
	}
}

type browserFixture struct {
	s       *Server
	handler http.Handler
	jar     http.CookieJar
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	s := testServer(t)
	s.limiter = rate.NewLimiter(rate.Inf, 1)
	jar, _ := cookiejar.New(nil)
	mux := http.NewServeMux()
	mux.Handle("/download/", s.DownloadHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("private fixture")) }), func(*http.Request) bool { return true }))
	mux.Handle("/", s.Handler())
	return &browserFixture{s: s, handler: mux, jar: jar}
}

func (b *browserFixture) request(t *testing.T, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if strings.HasPrefix(target, "/") {
		target = b.s.cfg.Issuer + target
	}
	r := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	r.Header.Set("Origin", b.s.cfg.Issuer)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range b.jar.Cookies(r.URL) {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	b.handler.ServeHTTP(w, r)
	b.jar.SetCookies(r.URL, w.Result().Cookies())
	return w
}

func (b *browserFixture) authorize(t *testing.T, target string) string {
	t.Helper()
	start := b.request(t, "GET", target, nil)
	if start.Code != 303 {
		t.Fatalf("authorization start: %d", start.Code)
	}
	page := b.request(t, "GET", start.Header().Get("Location"), nil)
	if page.Code != 200 || page.Header().Get("Referrer-Policy") != "strict-origin" {
		t.Fatalf("authorization page: %d", page.Code)
	}
	var session string
	for _, c := range page.Result().Cookies() {
		if c.Name == "nas_oauth" {
			session = c.Value
		}
	}
	consent := b.request(t, "POST", "/authorize", url.Values{"session": {session}, "password": {"correct horse battery staple"}, "consent": {"allow"}})
	if consent.Code != 303 {
		t.Fatalf("authorization consent: %d", consent.Code)
	}
	return consent.Header().Get("Location")
}

func TestBrowserDownloadOAuthFlowAndRevocation(t *testing.T) {
	b := newBrowserFixture(t)
	b.s.cfg.Issuer = "https://nas.test"
	target := "/download/one/file.pdf"
	callback := b.authorize(t, target)
	completed := b.request(t, "GET", callback, nil)
	if completed.Code != 303 || completed.Header().Get("Location") != target {
		t.Fatalf("callback: %d", completed.Code)
	}
	for _, c := range completed.Result().Cookies() {
		if c.MaxAge > 0 && (!c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge > 600) {
			t.Fatal("unsafe browser session cookie")
		}
	}
	file := b.request(t, "GET", target, nil)
	if file.Code != 200 || file.Body.String() != "private fixture" {
		t.Fatalf("download after OAuth: %d", file.Code)
	}
	replay := b.request(t, "GET", callback, nil)
	if replay.Code != 400 {
		t.Fatalf("callback replay accepted: %d", replay.Code)
	}
	other := b.request(t, "GET", "/download/two/file.pdf", nil)
	if other.Code != 303 {
		t.Fatalf("other link skipped fresh login: %d", other.Code)
	}
	if err := b.s.RevokeAll(); err != nil {
		t.Fatal(err)
	}
	revoked := b.request(t, "GET", target, nil)
	if revoked.Code != 303 || strings.Contains(revoked.Body.String(), "private fixture") {
		t.Fatalf("revoked session downloaded: %d", revoked.Code)
	}
}

func TestBrowserCallbackRejectsUnboundAndSwappedResponses(t *testing.T) {
	for _, attack := range []string{"missing browser cookie", "wrong state", "swapped code", "expired transaction"} {
		t.Run(attack, func(t *testing.T) {
			b := newBrowserFixture(t)
			callback := b.authorize(t, "/download/one/file.pdf")
			u, _ := url.Parse(callback)
			q := u.Query()
			switch attack {
			case "missing browser cookie":
				b.jar, _ = cookiejar.New(nil)
			case "wrong state":
				q.Set("state", strings.Repeat("x", 43))
			case "swapped code":
				other, _ := url.Parse(b.authorize(t, "/download/two/file.pdf"))
				q.Set("code", other.Query().Get("code"))
			case "expired transaction":
				if _, err := b.s.db.Exec("UPDATE oauth_grants SET expires=? WHERE kind='browser_pending'", time.Now().Add(-time.Second).Unix()); err != nil {
					t.Fatal(err)
				}
			}
			u.RawQuery = q.Encode()
			result := b.request(t, "GET", u.String(), nil)
			if result.Code != 400 {
				t.Fatalf("accepted %s: %d", attack, result.Code)
			}
			file := b.request(t, "GET", "/download/one/file.pdf", nil)
			if file.Code != 303 {
				t.Fatalf("failed callback granted download: %d", file.Code)
			}
		})
	}
}

func TestBrowserClientCannotUseClaudeRedirectOrAudience(t *testing.T) {
	b := newBrowserFixture(t)
	start := b.request(t, "GET", "/download/one/file.pdf", nil)
	u, _ := url.Parse(start.Header().Get("Location"))
	for _, key := range []string{"redirect_uri", "resource", "client_id"} {
		q := u.Query()
		switch key {
		case "redirect_uri":
			q.Set(key, "https://claude.ai/api/mcp/auth_callback")
		case "resource":
			q.Set(key, b.s.resource())
		case "client_id":
			q.Set(key, b.s.cfg.ClientID)
		}
		u2 := *u
		u2.RawQuery = q.Encode()
		w := b.request(t, "GET", u2.String(), nil)
		if w.Code != 400 {
			t.Fatalf("accepted browser request with wrong %s: %d", key, w.Code)
		}
	}
}

func TestBrowserGrantCannotAccessMCPOrRefresh(t *testing.T) {
	b := newBrowserFixture(t)
	callback, _ := url.Parse(b.authorize(t, "/download/one/file.pdf"))
	var verifier string
	if err := b.s.db.QueryRow("SELECT challenge FROM oauth_grants WHERE hash=? AND kind='browser_pending'", hash(callback.Query().Get("state"))).Scan(&verifier); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {browserClient}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}, "resource": {b.s.clientResource(browserClient)}, "redirect_uri": {b.s.cfg.Issuer + browserCallbackPath}}
	for _, key := range []string{"redirect_uri", "resource", "client_id"} {
		original := form.Get(key)
		switch key {
		case "redirect_uri":
			form.Set(key, "https://claude.ai/api/mcp/auth_callback")
		case "resource":
			form.Set(key, b.s.resource())
		case "client_id":
			form.Set(key, b.s.cfg.ClientID)
		}
		denied := b.request(t, "POST", "/token", form)
		if denied.Code != 400 {
			t.Fatalf("browser code accepted wrong %s: %d", key, denied.Code)
		}
		form.Set(key, original)
	}
	response := b.request(t, "POST", "/token", form)
	var grant tokenGrant
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &grant) != nil || grant.AccessToken == "" || grant.RefreshToken != "" || grant.Scope != browserScope {
		t.Fatal("browser token response invalid")
	}
	reached := false
	r := httptest.NewRequest("POST", b.s.cfg.Issuer+"/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+grant.AccessToken)
	w := httptest.NewRecorder()
	b.s.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })).ServeHTTP(w, r)
	if w.Code != 401 || reached {
		t.Fatal("browser grant authorized MCP")
	}
	form = url.Values{"grant_type": {"refresh_token"}, "client_id": {browserClient}, "refresh_token": {grant.AccessToken}, "resource": {b.s.clientResource(browserClient)}}
	if refreshed := b.request(t, "POST", "/token", form); refreshed.Code != 400 {
		t.Fatal("browser grant allowed refresh")
	}
}

func TestBrowserSessionsExpireAndPendingStateIsBounded(t *testing.T) {
	for _, kind := range []string{"access", "browser_session"} {
		t.Run(kind, func(t *testing.T) {
			b := newBrowserFixture(t)
			callback := b.authorize(t, "/download/one/file.pdf")
			if w := b.request(t, "GET", callback, nil); w.Code != 303 {
				t.Fatal("callback failed")
			}
			if _, err := b.s.db.Exec("UPDATE oauth_grants SET expires=? WHERE kind=?", time.Now().Add(-time.Second).Unix(), kind); err != nil {
				t.Fatal(err)
			}
			if w := b.request(t, "GET", "/download/one/file.pdf", nil); w.Code != 303 {
				t.Fatal("expired browser authority downloaded")
			}
		})
	}
	b := newBrowserFixture(t)
	for i := 0; i < 128; i++ {
		if w := b.request(t, "GET", "/download/one/file.pdf", nil); w.Code != 303 {
			t.Fatalf("pending flow %d: %d", i, w.Code)
		}
	}
	if w := b.request(t, "GET", "/download/one/file.pdf", nil); w.Code != 503 {
		t.Fatalf("unbounded browser state: %d", w.Code)
	}
}
