package main

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// browserOAuthClient follows the same authorization-code flow as a browser without exporting tokens.
func browserOAuthClient(t *testing.T, base *http.Client, issuer, target, password string) *http.Client {
	t.Helper()
	browser := *base
	browser.Jar, _ = cookiejar.New(nil)
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	start, err := browser.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	if start.StatusCode != 303 || !strings.HasPrefix(start.Header.Get("Location"), issuer+"/authorize?") {
		t.Fatalf("browser OAuth start: %d", start.StatusCode)
	}
	page, err := browser.Get(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != 200 || page.Header.Get("Referrer-Policy") != "strict-origin" {
		t.Fatalf("browser OAuth page: %d", page.StatusCode)
	}
	var session string
	for _, c := range page.Cookies() {
		if c.Name == "nas_oauth" {
			session = c.Value
		}
	}
	form := url.Values{"session": {session}, "password": {password}, "consent": {"allow"}}
	r, _ := http.NewRequest("POST", issuer+"/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Origin", issuer)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	consent, err := browser.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	consent.Body.Close()
	if consent.StatusCode != 303 || !strings.HasPrefix(consent.Header.Get("Location"), issuer+"/oauth/browser/callback?") {
		t.Fatalf("browser OAuth consent: %d", consent.StatusCode)
	}
	callback, err := browser.Get(consent.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callback.Body.Close()
	if callback.StatusCode != 303 || callback.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("browser OAuth callback: %d", callback.StatusCode)
	}
	return &browser
}
