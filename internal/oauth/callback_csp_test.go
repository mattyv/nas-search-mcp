package oauth

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func requireCallbackCSP(t *testing.T, w *httptest.ResponseRecorder, origin string) {
	t.Helper()
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self' "+origin+";") {
		t.Errorf("form redirect to %s would be blocked by %q", origin, csp)
	}
	for _, want := range []string{"default-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("missing %s in %q", want, csp)
		}
	}
}

func TestSignInPagesPermitOnlyCurrentCallbackOrigin(t *testing.T) {
	for _, redirect := range []string{"https://claude.ai/api/mcp/auth_callback", "https://chatgpt.com/connector/oauth/test", "http://127.0.0.1:54321/callback"} {
		t.Run(redirect, func(t *testing.T) {
			f := newPasskeyFixture(t, "enroll")
			client := registerClient(t, f.s, redirect)
			values := authorizeValues(f.s)
			values.Set("client_id", client)
			values.Set("redirect_uri", redirect)
			w := f.do("GET", "/authorize?"+values.Encode(), nil)
			if w.Code != 200 {
				t.Fatalf("authorize: %d", w.Code)
			}
			destination, _ := url.Parse(redirect)
			origin := destination.Scheme + "://" + destination.Host
			requireCallbackCSP(t, w, origin)
			session := sessionField.FindStringSubmatch(w.Body.String())[1]
			w = f.password(session)
			requireCallbackCSP(t, w, origin)
			// A failed proof renders a retry form, which must retain the same callback.
			w = f.finish(session, "invalid")
			if w.Code != 403 {
				t.Fatalf("retry: %d", w.Code)
			}
			requireCallbackCSP(t, w, origin)
			for _, other := range []string{"https://claude.ai", "https://chatgpt.com", "http://127.0.0.1:54321"} {
				if other != origin && strings.Contains(w.Header().Get("Content-Security-Policy"), other) {
					t.Errorf("policy allows unrelated callback %s", other)
				}
			}
		})
	}
}

func TestAssertionAndInvitePagesKeepCallbackCSP(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	key := newSoftKey(t)
	f.enroll(key)
	session := f.begin()
	requireCallbackCSP(t, f.password(session), "https://claude.ai")
	w := f.redeem(session, mustInvite(t, f))
	if w.Code != 200 {
		t.Fatalf("invite: %d", w.Code)
	}
	requireCallbackCSP(t, w, "https://claude.ai")
}
