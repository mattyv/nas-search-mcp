package main

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"golang.org/x/crypto/bcrypt"
)

func TestUpgradeApprovalBoundary(t *testing.T) {
	const issuer = "http://127.0.0.1:8080"
	for _, attack := range []string{"owner", "query consent", "cross origin", "missing origin", "missing csrf", "expired", "revoked OAuth", "missing OAuth"} {
		t.Run(attack, func(t *testing.T) {
			directory := t.TempDir()
			hash, err := bcrypt.GenerateFromPassword([]byte("owner-password"), 10)
			if err != nil {
				t.Fatal(err)
			}
			auth, err := oauth.New(oauth.Config{Issuer: issuer, DatabasePath: filepath.Join(t.TempDir(), "oauth.db"), PasswordHash: string(hash), ClientID: "claude"})
			if err != nil {
				t.Fatal(err)
			}
			defer auth.Close()
			a := newUpgradeApproval(issuer, directory)
			nonce := strings.Repeat("n", 43)
			key := sha256.Sum256([]byte(nonce))
			a.pending[key] = &pendingUpgrade{expires: time.Now().Add(5 * time.Minute)}
			target := issuer + "/upgrade/approve?request=" + nonce
			mux := http.NewServeMux()
			mux.Handle("/upgrade/approve", auth.BrowserActionHandler(a, a.known))
			mux.Handle("/", auth.Handler())
			jar, _ := cookiejar.New(nil)
			request := func(method, destination, origin string, form url.Values) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, destination, strings.NewReader(form.Encode()))
				r.Header.Set("Origin", origin)
				if form != nil {
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				for _, cookie := range jar.Cookies(r.URL) {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				jar.SetCookies(r.URL, w.Result().Cookies())
				return w
			}
			start := request("GET", target, issuer, nil)
			if start.Code != 303 {
				t.Fatalf("approval must start OAuth: %d", start.Code)
			}
			login := request("GET", start.Header().Get("Location"), issuer, nil)
			if login.Code != 200 {
				t.Fatalf("OAuth login: %d", login.Code)
			}
			var session string
			for _, c := range login.Result().Cookies() {
				if strings.HasPrefix(c.Name, "nas_oauth_") {
					session = c.Value
				}
			}
			consent := request("POST", issuer+"/authorize", issuer, url.Values{"session": {session}, "password": {"owner-password"}, "consent": {"allow"}})
			if consent.Code != 303 {
				t.Fatalf("OAuth consent: %d", consent.Code)
			}
			callback := request("GET", consent.Header().Get("Location"), issuer, nil)
			if callback.Code != 303 {
				t.Fatalf("OAuth callback: %d", callback.Code)
			}
			page := request("GET", target, issuer, nil)
			if page.Code != 200 || page.Header().Get("Referrer-Policy") != "strict-origin" || strings.Contains(page.Body.String(), "type=\"password\"") {
				t.Fatalf("approval after OAuth: %d", page.Code)
			}
			if _, err := os.Stat(filepath.Join(directory, "request.json")); !os.IsNotExist(err) {
				t.Fatal("GET approval queued upgrade")
			}
			var csrf string
			for _, c := range page.Result().Cookies() {
				if c.Name == upgradeCookieName(nonce) {
					csrf = c.Value
				}
			}
			origin := issuer
			form := url.Values{"csrf": {csrf}, "consent": {"approve"}}
			want := 403
			switch attack {
			case "owner":
				want = 200
			case "query consent":
				target += "&consent=approve"
				form.Del("consent")
				want = 404
			case "cross origin":
				origin = "https://evil.test"
			case "missing origin":
				origin = ""
			case "missing csrf":
				form.Del("csrf")
			case "expired":
				a.pending[key].expires = time.Now().Add(-time.Second)
				want = 404
			case "revoked OAuth":
				if err := auth.RevokeAll(); err != nil {
					t.Fatal(err)
				}
				want = 401
			case "missing OAuth":
				jar, _ = cookiejar.New(nil)
				want = 401
			}
			w := request("POST", target, origin, form)
			if w.Code != want {
				t.Fatalf("approval: got %d, want %d", w.Code, want)
			}
			_, err = os.Stat(filepath.Join(directory, "request.json"))
			if attack == "owner" {
				if err != nil {
					t.Fatal("approved upgrade not queued", err)
				}
				replay := request("POST", target, origin, form)
				if replay.Code != 404 {
					t.Fatalf("approval replay: %d", replay.Code)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("rejected approval queued: %v", err)
			}
		})
	}
}

func TestUpgradePreparationRequiresOAuth(t *testing.T) {
	a := newUpgradeApproval("https://nas.test", t.TempDir())
	if _, err := a.prepare(context.Background()); err == nil || len(a.pending) != 0 {
		t.Fatal("unauthenticated caller prepared an upgrade")
	}
}

func TestUpgradePasswordCannotBypassOAuth(t *testing.T) {
	a := newUpgradeApproval("https://nas.test", t.TempDir())
	nonce := strings.Repeat("n", 43)
	a.pending[sha256.Sum256([]byte(nonce))] = &pendingUpgrade{expires: time.Now().Add(time.Minute)}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "https://nas.test/upgrade/approve?request="+nonce, strings.NewReader("password=owner-password")))
	if w.Code != 401 {
		t.Fatalf("password-only approval bypassed OAuth: %d", w.Code)
	}
}
