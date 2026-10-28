package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	browserClient       = "nas-first-party-browser"
	browserScope        = "browser:action"
	browserCallbackPath = "/oauth/browser/callback"
)

type browserActionKey struct{}

// BrowserActionKey is present only after validating a browser session and its OAuth grant.
func BrowserActionKey(ctx context.Context) string {
	key, _ := ctx.Value(browserActionKey{}).(string)
	return key
}

func (s *Server) clientResource(client string) string {
	if client == browserClient {
		return s.cfg.Issuer + "/browser"
	}
	return s.resource()
}

func browserTarget(r *http.Request) (string, bool) {
	if strings.HasPrefix(r.URL.Path, "/download/") && r.URL.RawQuery == "" {
		return r.URL.EscapedPath(), true
	}
	if strings.HasPrefix(r.URL.Path, "/upload/") && r.URL.RawQuery == "" && !r.URL.ForceQuery {
		path := r.URL.EscapedPath()
		token := strings.TrimPrefix(path, "/upload/")
		if len(token) == 43 && strings.IndexFunc(token, func(c rune) bool {
			return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_')
		}) == -1 {
			return path, true
		}
	}
	switch r.URL.Path {
	case "/passkeys", "/passkeys/options", "/passkeys/add", "/passkeys/rename", "/passkeys/remove":
		// One fresh sign-in covers the page and its actions.
		if r.URL.RawQuery == "" && !r.URL.ForceQuery {
			return "/passkeys", true
		}
	}
	if r.URL.Path == "/upgrade/approve" || r.URL.Path == "/memory/approve" {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err == nil && len(q) == 1 && len(q["request"]) == 1 && len(q.Get("request")) == 43 {
			return r.URL.Path + "?" + q.Encode(), true
		}
	}
	return "", false
}

func browserCookieName(target string) string { return "nas_browser_" + hash(target)[:16] }
func flowCookieName(state string) string     { return "nas_flow_" + hash(state)[:16] }

// DownloadHandler requires a fresh OAuth sign-in for each link; its short session permits PDF range requests.
func (s *Server) DownloadHandler(next http.Handler, known func(*http.Request) bool) http.Handler {
	gate := s.BrowserActionHandler(next, known)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		gate.ServeHTTP(w, r)
	})
}

// BrowserActionHandler binds a browser session to one action and checks OAuth revocation on every request.
func (s *Server) BrowserActionHandler(next http.Handler, known func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		target, ok := browserTarget(r)
		if !ok || !known(r) {
			http.NotFound(w, r)
			return
		}
		cookie, err := r.Cookie(browserCookieName(target))
		if err == nil && len(cookie.Value) == 43 {
			var active int
			err = s.db.QueryRowContext(r.Context(), `SELECT count(*) FROM oauth_grants session
                JOIN oauth_grants access ON access.hash=session.family
                WHERE session.hash=? AND session.kind='browser_session' AND session.redirect=? AND session.expires>?
                AND access.kind='access' AND access.client=? AND access.resource=? AND access.used=0 AND access.expires>?`,
				hash(cookie.Value), target, time.Now().Unix(), browserClient, s.clientResource(browserClient), time.Now().Unix()).Scan(&active)
			if err != nil {
				oauthError(w, 500, "server_error")
				return
			}
			if active == 1 {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), browserActionKey{}, hash(cookie.Value))))
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			oauthError(w, http.StatusUnauthorized, "invalid_token")
			return
		}
		s.beginBrowser(w, r, target)
	})
}

func (s *Server) beginBrowser(w http.ResponseWriter, r *http.Request, target string) {
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	state, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	binding, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	verifier, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if _, err = s.db.Exec("DELETE FROM oauth_grants WHERE expires <= ?", time.Now().Unix()); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	result, err := s.db.Exec(`INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires)
        SELECT ?, 'browser_pending', ?, ?, ?, ?, ?, ? WHERE (SELECT count(*) FROM oauth_grants WHERE kind='browser_pending')<128`,
		hash(state), browserClient, target, verifier, s.clientResource(browserClient), hash(binding), time.Now().Add(5*time.Minute).Unix())
	if err != nil {
		grantError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookieName(state), Value: binding, Path: browserCallbackPath, HttpOnly: true,
		Secure: strings.HasPrefix(s.cfg.Issuer, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: 300})
	digest := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {browserClient}, "redirect_uri": {s.cfg.Issuer + browserCallbackPath}, "response_type": {"code"},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])},
		"state": {state}, "resource": {s.clientResource(browserClient)}, "scope": {browserScope}}
	http.Redirect(w, r, s.cfg.Issuer+"/authorize?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) browserCallback(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["state"]) != 1 || len(q["code"]) != 1 || len(q.Get("state")) != 43 || len(q.Get("code")) != 43 {
		oauthError(w, 400, "invalid_request")
		return
	}
	state := q.Get("state")
	cookie, err := r.Cookie(flowCookieName(state))
	if err != nil || len(cookie.Value) != 43 {
		oauthError(w, 400, "invalid_request")
		return
	}
	var target, verifier, binding string
	err = s.db.QueryRowContext(r.Context(), "SELECT redirect,challenge,family FROM oauth_grants WHERE hash=? AND kind='browser_pending' AND expires>?", hash(state), time.Now().Unix()).Scan(&target, &verifier, &binding)
	if err != nil || subtle.ConstantTimeCompare([]byte(hash(cookie.Value)), []byte(binding)) != 1 {
		oauthError(w, 400, "invalid_request")
		return
	}
	// Consume state before exchanging the code so concurrent callbacks cannot create two sessions.
	result, err := s.db.Exec("DELETE FROM oauth_grants WHERE hash=? AND kind='browser_pending' AND expires>?", hash(state), time.Now().Unix())
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		oauthError(w, 400, "invalid_request")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookieName(state), Value: "", Path: browserCallbackPath, HttpOnly: true,
		Secure: strings.HasPrefix(s.cfg.Issuer, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	grant, status, failure := s.exchange(browserClient, url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")},
		"redirect_uri": {s.cfg.Issuer + browserCallbackPath}, "code_verifier": {verifier}, "resource": {s.clientResource(browserClient)}})
	if status != 200 {
		oauthError(w, status, failure)
		return
	}
	session, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	_, err = s.db.Exec(`INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires)
        VALUES(?, 'browser_session', ?, ?, '', ?, ?, ?)`, hash(session), browserClient, target, s.clientResource(browserClient), hash(grant.AccessToken), time.Now().Add(time.Duration(grant.ExpiresIn)*time.Second).Unix())
	if err != nil {
		grantError(w, err)
		return
	}
	destination, err := url.Parse(target)
	if err != nil || destination.IsAbs() || destination.Host != "" {
		oauthError(w, 500, "server_error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: browserCookieName(target), Value: session, Path: destination.Path, HttpOnly: true,
		Secure: strings.HasPrefix(s.cfg.Issuer, "https:"), SameSite: http.SameSiteStrictMode, MaxAge: grant.ExpiresIn})
	http.Redirect(w, r, target, http.StatusSeeOther)
}
