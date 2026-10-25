package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func registrationServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	s.limiter = rate.NewLimiter(rate.Inf, 1)
	return s
}

func registrationDocument(redirect string) map[string]any {
	return map[string]any{"redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"scope": scope, "client_name": "Local test client"}
}

func registerRequest(s *Server, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func registerClient(t *testing.T, s *Server, redirect string) string {
	t.Helper()
	body, _ := json.Marshal(registrationDocument(redirect))
	w := registerRequest(s, string(body))
	if w.Code != http.StatusCreated {
		t.Fatalf("registration: %d %s", w.Code, w.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &registered); err != nil || registered.ClientID == "" || registered.ClientID == s.cfg.ClientID || registered.ClientID == browserClient {
		t.Fatalf("bad registered client: %s %v", w.Body.String(), err)
	}
	return registered.ClientID
}

func registeredCode(t *testing.T, s *Server, client, redirect, scopeValue string) (string, string) {
	t.Helper()
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	values := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"resource": {s.resource()}, "scope": {scopeValue}, "state": {"registered-state"}}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+values.Encode(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "type=\"password\"") {
		t.Fatalf("registered authorization: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	form := url.Values{"session": {cookie.Value}, "password": {"correct horse battery staple"}, "consent": {"allow"}}
	r := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", s.cfg.Issuer)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("registered consent: %d %s", w.Code, w.Body.String())
	}
	callback, err := url.Parse(w.Header().Get("Location"))
	if err != nil || callback.Query().Get("state") != "registered-state" {
		t.Fatalf("bad callback: %s", w.Header().Get("Location"))
	}
	return callback.Query().Get("code"), verifier
}

func registeredToken(s *Server, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/token", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestRegistrationDiscoveryAndCompletePKCEFlow(t *testing.T) {
	s := registrationServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil))
	var metadata map[string]any
	json.Unmarshal(w.Body.Bytes(), &metadata)
	if metadata["registration_endpoint"] != s.cfg.Issuer+"/register" {
		t.Fatalf("registration missing from discovery: %s", w.Body.String())
	}
	if got := metadata["scopes_supported"].([]any); len(got) != 1 || got[0] != scope {
		t.Fatalf("first-party scope exposed in external discovery: %v", got)
	}
	redirect := "https://chatgpt.com/connector/oauth/new-registration"
	client := registerClient(t, s, redirect)
	code, verifier := registeredCode(t, s, client, redirect, scope)
	grant := url.Values{"client_id": {client}, "grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {s.resource()}}
	w = registeredToken(s, grant)
	if w.Code != 200 {
		t.Fatalf("registered exchange: %d %s", w.Code, w.Body.String())
	}
	var token tokenGrant
	json.Unmarshal(w.Body.Bytes(), &token)
	if token.Scope != scope || token.RefreshToken == "" {
		t.Fatalf("wrong dynamic grant: %+v", token)
	}
	protected := s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, map[string]bool{"ok": true}) }))
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+token.AccessToken)
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("registered protected request failed: %d", w.Code)
	}
	w = registeredToken(s, url.Values{"client_id": {client}, "grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "resource": {s.resource()}})
	if w.Code != 200 {
		t.Fatalf("registered refresh: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistrationRejectsUnsafeMetadata(t *testing.T) {
	for _, redirect := range []string{"https://evil.example/callback", "https://chatgpt.com.evil.example/connector/oauth/a", "https://chatgpt.com:443/connector/oauth/a",
		"https://chatgpt.com/connector/oauth/a?x=1", "https://chatgpt.com/connector/oauth/a#", "https://user@chatgpt.com/connector/oauth/a",
		"https://chatgpt.com/connector/oauth/a%2fb", "http://localhost:1234/callback", "http://127.0.0.2:1234/callback",
		"http://127.0.0.1/callback", "http://127.0.0.1:0/callback", "http://127.0.0.1:1234/callback?x=1", "http://[::1]/callback"} {
		t.Run(redirect, func(t *testing.T) {
			s := registrationServer(t)
			body, _ := json.Marshal(registrationDocument(redirect))
			w := registerRequest(s, string(body))
			if w.Code != 400 {
				t.Fatalf("unsafe redirect accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for key, value := range map[string]any{"token_endpoint_auth_method": "client_secret_post", "grant_types": []string{"client_credentials"}, "response_types": []string{"token"}, "scope": browserScope, "client_name": strings.Repeat("x", 129), "redirect_uris": []string{}} {
		t.Run(key, func(t *testing.T) {
			s := registrationServer(t)
			metadata := registrationDocument("https://claude.ai/api/mcp/auth_callback")
			metadata[key] = value
			body, _ := json.Marshal(metadata)
			if w := registerRequest(s, string(body)); w.Code != 400 {
				t.Fatalf("unsafe metadata accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRegistrationExactClientBindingAndBrowserIsolation(t *testing.T) {
	s := registrationServer(t)
	a := "https://chatgpt.com/connector/oauth/a"
	b := "https://chatgpt.com/connector/oauth/b"
	clientA := registerClient(t, s, a)
	clientB := registerClient(t, s, b)
	values := url.Values{"client_id": {clientA}, "redirect_uri": {b}, "response_type": {"code"}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(make([]byte, 32))}, "resource": {s.resource()}, "scope": {scope}}
	if s.validAuthorize(values) {
		t.Fatal("client used another registered callback")
	}
	values.Set("redirect_uri", a)
	values.Set("scope", browserScope)
	if s.validAuthorize(values) {
		t.Fatal("registered client obtained browser scope")
	}
	code, verifier := registeredCode(t, s, clientA, a, scope)
	grant := url.Values{"client_id": {clientB}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {a}, "code_verifier": {verifier}, "resource": {s.resource()}}
	if w := registeredToken(s, grant); w.Code != 400 {
		t.Fatalf("wrong client redeemed code: %d", w.Code)
	}
	grant.Set("client_id", clientA)
	w := registeredToken(s, grant)
	var token tokenGrant
	json.Unmarshal(w.Body.Bytes(), &token)
	if w.Code != 200 || token.RefreshToken == "" {
		t.Fatalf("right client code was consumed: %d %s", w.Code, w.Body.String())
	}
	if w := registeredToken(s, url.Values{"client_id": {clientB}, "grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "resource": {s.resource()}}); w.Code != 400 {
		t.Fatalf("wrong client redeemed refresh: %d", w.Code)
	}
}

func TestRegistrationPendingExpiryAndActiveGrantPinning(t *testing.T) {
	s := registrationServer(t)
	redirect := "http://127.0.0.1:12345/callback"
	pending := registerClient(t, s, redirect)
	active := registerClient(t, s, redirect)
	code, verifier := registeredCode(t, s, active, redirect, scope)
	w := registeredToken(s, url.Values{"client_id": {active}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {s.resource()}})
	var token tokenGrant
	json.Unmarshal(w.Body.Bytes(), &token)
	if w.Code != 200 {
		t.Fatalf("exchange: %d", w.Code)
	}
	if _, err := s.db.Exec("UPDATE oauth_clients SET expires=?", time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'login', ?, '', '', ?, '', ?)", hash("pending-login"), pending, s.resource(), time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	registerClient(t, s, "http://[::1]:12345/callback")
	var n int
	s.db.QueryRow("SELECT count(*) FROM oauth_clients WHERE client=?", pending).Scan(&n)
	if n != 0 {
		t.Fatal("pre-consent login pinned expired pending registration")
	}
	s.db.QueryRow("SELECT count(*) FROM oauth_clients WHERE client=?", active).Scan(&n)
	if n != 1 {
		t.Fatal("active grants lost their registration")
	}
	w = registeredToken(s, url.Values{"client_id": {active}, "grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "resource": {s.resource()}})
	if w.Code != 200 {
		t.Fatalf("active refresh failed: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistrationOwnerAuthorizedClientSurvivesExpiredGrants(t *testing.T) {
	s := registrationServer(t)
	redirect := "https://chatgpt.com/connector/oauth/persistent-connection"
	client := registerClient(t, s, redirect)
	registeredCode(t, s, client, redirect, scope)
	// A client that received owner consent is retained independently of its
	// current tokens: fresh password sign-in must not require re-registration.
	if _, err := s.db.Exec("DELETE FROM oauth_grants"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE oauth_clients SET expires=1 WHERE expires>0"); err != nil {
		t.Fatal(err)
	}
	registerClient(t, s, "http://127.0.0.1:12345/other")
	code, _ := registeredCode(t, s, client, redirect, scope)
	if code == "" {
		t.Fatal("owner-authorized client could not reconnect")
	}
}

func TestRegistrationAuthorizationCodeOnlyClientCanSignInAgainAfterExpiry(t *testing.T) {
	s := registrationServer(t)
	redirect := "http://127.0.0.1:12345/callback"
	metadata := registrationDocument(redirect)
	metadata["grant_types"] = []string{"authorization_code"}
	body, _ := json.Marshal(metadata)
	w := registerRequest(s, string(body))
	var client struct {
		ClientID string `json:"client_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &client)
	if w.Code != 201 || client.ClientID == "" {
		t.Fatalf("code-only registration: %d %s", w.Code, w.Body.String())
	}
	code, verifier := registeredCode(t, s, client.ClientID, redirect, scope)
	w = registeredToken(s, url.Values{"client_id": {client.ClientID}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {s.resource()}})
	var token tokenGrant
	json.Unmarshal(w.Body.Bytes(), &token)
	if w.Code != 200 || token.AccessToken == "" || token.RefreshToken != "" {
		t.Fatalf("code-only grant was widened or failed: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.db.Exec("UPDATE oauth_grants SET expires=?", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	registerClient(t, s, "http://[::1]:23456/callback")
	if code, _ = registeredCode(t, s, client.ClientID, redirect, scope); code == "" {
		t.Fatal("code-only connection was lost after token expiry")
	}
}

func TestRegistrationFailedPasswordDoesNotMakePendingClientPermanent(t *testing.T) {
	s := registrationServer(t)
	redirect := "http://127.0.0.1:12345/callback"
	client := registerClient(t, s, redirect)
	sum := sha256.Sum256([]byte(strings.Repeat("a", 43)))
	values := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"resource": {s.resource()}, "scope": {scope}}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+values.Encode(), nil))
	if w.Code != 200 {
		t.Fatalf("pending client authorization: %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	form := url.Values{"session": {cookie.Value}, "password": {"incorrect"}, "consent": {"allow"}}
	r := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", s.cfg.Issuer)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("wrong password: %d", w.Code)
	}
	var expires int64
	var count int
	if err := s.db.QueryRow("SELECT expires FROM oauth_clients WHERE client=?", client).Scan(&expires); err != nil || expires <= time.Now().Unix() {
		t.Fatalf("failed consent promoted or removed pending registration: %d %v", expires, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM oauth_grants WHERE client=? AND kind='code'", client).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed password issued a grant: %d %v", count, err)
	}
	if _, err := s.db.Exec("UPDATE oauth_clients SET expires=1 WHERE client=?", client); err != nil {
		t.Fatal(err)
	}
	registerClient(t, s, "http://[::1]:23456/callback")
	if err := s.db.QueryRow("SELECT count(*) FROM oauth_clients WHERE client=?", client).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed-password pending client was not reclaimed: %d %v", count, err)
	}
}

func TestRegistrationDynamicClientCannotUseBrowserAudienceOrCallback(t *testing.T) {
	s := registrationServer(t)
	redirect := "http://127.0.0.1:12345/callback"
	client := registerClient(t, s, redirect)
	values := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
		"resource": {s.cfg.Issuer + "/browser"}, "scope": {scope}}
	if s.validAuthorize(values) {
		t.Fatal("dynamic client obtained first-party browser audience")
	}
	values.Set("resource", s.resource())
	values.Set("redirect_uri", s.cfg.Issuer+browserCallbackPath)
	if s.validAuthorize(values) {
		t.Fatal("dynamic client used first-party browser callback")
	}
	code, verifier := registeredCode(t, s, client, redirect, scope)
	w := registeredToken(s, url.Values{"client_id": {client}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {s.resource()}})
	var token tokenGrant
	json.Unmarshal(w.Body.Bytes(), &token)
	if w.Code != 200 || token.AccessToken == "" {
		t.Fatalf("dynamic MCP token: %d", w.Code)
	}
	called := false
	gate := s.BrowserActionHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }), func(*http.Request) bool { return true })
	r := httptest.NewRequest("POST", "/upload/"+strings.Repeat("x", 43), nil)
	r.Header.Set("Authorization", "Bearer "+token.AccessToken)
	w = httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || called {
		t.Fatalf("MCP token bypassed separate browser sign-in: %d %v", w.Code, called)
	}
}

func TestRegistrationCapacityIsAtomicAndDoesNotBlockStaticClient(t *testing.T) {
	s := registrationServer(t)
	redirect := "https://claude.ai/api/mcp/auth_callback"
	for index := 0; index < 127; index++ {
		_, err := s.db.Exec("INSERT INTO oauth_clients(client,redirects,refresh,expires) VALUES(?, ?, 1, ?)", "fixture-"+strings.Repeat("x", index+1), `["`+redirect+`"]`, time.Now().Add(time.Hour).Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	statuses := make(chan int, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			body, _ := json.Marshal(registrationDocument(redirect))
			statuses <- registerRequest(s, string(body)).Code
		}()
	}
	workers.Wait()
	close(statuses)
	created, full := 0, 0
	for status := range statuses {
		if status == 201 {
			created++
		}
		if status == 503 {
			full++
		}
	}
	if created != 1 || full != 1 {
		t.Fatalf("capacity race: created=%d full=%d", created, full)
	}
	code, _ := registeredCode(t, s, s.cfg.ClientID, redirect, scope)
	if code == "" {
		t.Fatal("full dynamic registry blocked static owner login")
	}
}

func TestRegistrationIndependentRateAndBoundedBody(t *testing.T) {
	s := registrationServer(t)
	for i := 0; i < 4; i++ {
		registerClient(t, s, "https://claude.ai/api/mcp/auth_callback")
	}
	body, _ := json.Marshal(registrationDocument("https://claude.ai/api/mcp/auth_callback"))
	w := registerRequest(s, string(body))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("unbounded registration rate: %d", w.Code)
	}
	s = registrationServer(t)
	w = registerRequest(s, `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"`+strings.Repeat("x", 8192)+`"}`)
	if w.Code != 413 {
		t.Fatalf("oversized registration: %d", w.Code)
	}
	w = registerRequest(s, string(body)+` {}`)
	if w.Code != 400 {
		t.Fatalf("trailing registration JSON: %d", w.Code)
	}
}

func TestRegistrationRevocationRemovesClientsAndGrants(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		s := registrationServer(t)
		registerClient(t, s, "http://127.0.0.1:12345/callback")
		var err error
		if standalone {
			err = RevokeAll(s.cfg.DatabasePath)
		} else {
			err = s.RevokeAll()
		}
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err = s.db.QueryRow("SELECT count(*) FROM oauth_clients").Scan(&n); err != nil || n != 0 {
			t.Fatalf("registrations survived revoke: %d %v", n, err)
		}
	}
}

func TestRegistrationStandaloneRevocationSupportsLegacyDatabase(t *testing.T) {
	s := registrationServer(t)
	if _, err := s.db.Exec("DROP TABLE oauth_clients"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES('legacy','access','claude','','',?,'legacy',?)", s.resource(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := RevokeAll(s.cfg.DatabasePath); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM oauth_grants").Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy revocation: %d %v", count, err)
	}
}

func TestRegistrationRevocationIsAtomicOnFailure(t *testing.T) {
	s := registrationServer(t)
	client := registerClient(t, s, "http://127.0.0.1:12345/callback")
	if _, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES('retained','access',?,'','',?,'retained',?)", client, s.resource(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER refuse_client_delete BEFORE DELETE ON oauth_clients BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAll(); err == nil {
		t.Fatal("expected simulated revocation failure")
	}
	var grants, clients int
	s.db.QueryRow("SELECT count(*) FROM oauth_grants").Scan(&grants)
	s.db.QueryRow("SELECT count(*) FROM oauth_clients").Scan(&clients)
	if grants != 1 || clients != 1 {
		t.Fatalf("partial revocation: grants=%d clients=%d", grants, clients)
	}
}

func TestRegistrationPublicClientCannotUseBasicOrSecretAuthentication(t *testing.T) {
	s := registrationServer(t)
	client := registerClient(t, s, "http://127.0.0.1:12345/callback")
	for _, basic := range []bool{true, false} {
		values := url.Values{"client_id": {client}, "grant_type": {"authorization_code"}, "code": {strings.Repeat("x", 43)}, "resource": {s.resource()}}
		if !basic {
			values.Set("client_secret", "not-a-public-secret")
		}
		r := httptest.NewRequest("POST", "/token", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if basic {
			r.SetBasicAuth(client, "")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected registered-client authentication accepted: %d", w.Code)
		}
	}
}

func TestRegistrationRealHTTPMetadataReturnsPublicClient(t *testing.T) {
	s := registrationServer(t)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	body, _ := json.Marshal(registrationDocument("http://[::1]:23456/callback"))
	r, err := http.Post(server.URL+"/register", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	if r.StatusCode != 201 || strings.Contains(string(data), "client_secret") {
		t.Fatalf("public HTTP registration: %d %s", r.StatusCode, data)
	}
}
