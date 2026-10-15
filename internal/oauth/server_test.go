package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	h, e := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), 10)
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(Config{Issuer: "http://127.0.0.1:8080", DatabasePath: filepath.Join(t.TempDir(), "oauth.db"), PasswordHash: string(h), ClientID: "claude"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestRejectUnauthenticated(t *testing.T) {
	s := testServer(t)
	for _, token := range []string{"", "Bearer invalid"} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("protected handler reached") })).ServeHTTP(w, r)
		if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), "resource_metadata=") {
			t.Fatalf("bad rejection: %d", w.Code)
		}
	}
}
func TestRejectUnsafeConfig(t *testing.T) {
	for _, issuer := range []string{"http://public.example", "https://example.com/path", "https://user:pass@example.com"} {
		_, e := New(Config{Issuer: issuer})
		if e == nil {
			t.Fatalf("accepted %s", issuer)
		}
	}
}
func TestAuthorizeRejectsInvalidParameters(t *testing.T) {
	s := testServer(t)
	for _, q := range []string{"", "client_id=claude&redirect_uri=https://evil.example&response_type=code", "client_id=claude&redirect_uri=https://claude.ai/api/mcp/auth_callback&response_type=code&code_challenge_method=plain&code_challenge=x"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+q, nil))
		if w.Code != 400 {
			t.Fatalf("got %d", w.Code)
		}
	}
}

func TestFlowRotationAndRevocation(t *testing.T) {
	s := testServer(t)
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	values := url.Values{"client_id": {"claude"}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {challenge}, "resource": {s.resource()}, "state": {"test-state"}}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+values.Encode(), nil))
	if w.Code != 200 {
		t.Fatalf("authorize: %d", w.Code)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "strict-origin" {
		t.Fatalf("login referrer policy: %q", got)
	}
	cookies := w.Result().Cookies()
	session := cookies[0].Value
	post := url.Values{"session": {session}, "password": {"correct horse battery staple"}, "consent": {"allow"}}
	nullOrigin := httptest.NewRequest("POST", "/authorize", strings.NewReader(post.Encode()))
	nullOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nullOrigin.Header.Set("Origin", "null")
	nullOrigin.AddCookie(cookies[0])
	rejected := httptest.NewRecorder()
	s.Handler().ServeHTTP(rejected, nullOrigin)
	if rejected.Code != 400 {
		t.Fatalf("null origin accepted: %d", rejected.Code)
	}
	req := httptest.NewRequest("POST", "/authorize", strings.NewReader(post.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.cfg.Issuer)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 303 {
		t.Fatalf("consent: %d", w.Code)
	}
	callback, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	if callback.Query().Get("state") != "test-state" {
		t.Fatal("state lost")
	}
	code := callback.Query().Get("code")
	exchange := func(v url.Values) (int, map[string]any) {
		t.Helper()
		r := httptest.NewRequest("POST", "/token", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var body map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		return w.Code, body
	}
	grant := url.Values{"client_id": {"claude"}, "grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {values.Get("redirect_uri")}, "resource": {s.resource()}}
	grant.Set("code_verifier", strings.Repeat("b", 43))
	status, _ := exchange(grant)
	if status != 400 {
		t.Fatal("bad PKCE accepted")
	}
	grant.Set("code_verifier", verifier)
	status, body := exchange(grant)
	if status != 200 {
		t.Fatalf("exchange: %d", status)
	}
	if body["expires_in"] != float64(600) {
		t.Fatal("access lifetime must allow Claude proactive refresh")
	}
	access := body["access_token"].(string)
	refresh := body["refresh_token"].(string)
	check := func(token string) int {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		return w.Code
	}
	if check(access) != 204 {
		t.Fatal("valid access rejected")
	}
	var plaintext int
	if e := s.db.QueryRow("SELECT count(*) FROM oauth_grants WHERE hash IN (?, ?, ?)", code, access, refresh).Scan(&plaintext); e != nil || plaintext != 0 {
		t.Fatal("plaintext token persisted")
	}
	rotation := url.Values{"client_id": {"claude"}, "grant_type": {"refresh_token"}, "refresh_token": {refresh}, "resource": {s.resource()}}
	status, body = exchange(rotation)
	if status != 200 {
		t.Fatalf("refresh: %d", status)
	}
	rotated := body["access_token"].(string)
	if check(rotated) != 204 {
		t.Fatal("rotated access rejected")
	}
	status, _ = exchange(rotation)
	if status != 400 || check(rotated) != 401 || check(access) != 401 {
		t.Fatal("refresh reuse did not revoke family")
	}
	if e := s.RevokeAll(); e != nil {
		t.Fatal(e)
	}
}
func TestCSRFAndLockout(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("POST", "/authorize", strings.NewReader("password=x&consent=allow&session="+strings.Repeat("a", 43)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("missing CSRF cookie accepted")
	}
}

func TestDatabaseIsPrivate(t *testing.T) {
	s := testServer(t)
	info, e := os.Stat(s.cfg.DatabasePath)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("OAuth database permission is %o, want 600", info.Mode().Perm())
	}
}

func TestExpiredWrongAudienceAndRevokedAccess(t *testing.T) {
	s := testServer(t)
	token, _ := randomToken()
	_, e := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'access', ?, '', '', ?, 'family', ?)", hash(token), s.cfg.ClientID, s.resource(), time.Now().Add(time.Minute).Unix())
	if e != nil {
		t.Fatal(e)
	}
	check := func(want int) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d", w.Code, want)
		}
	}
	check(204)
	s.db.Exec("UPDATE oauth_grants SET resource='https://other.example/mcp'")
	check(401)
	s.db.Exec("UPDATE oauth_grants SET resource=?,expires=0", s.resource())
	check(401)
	s.db.Exec("UPDATE oauth_grants SET expires=?", time.Now().Add(time.Minute).Unix())
	check(204)
	if e := s.RevokeAll(); e != nil {
		t.Fatal(e)
	}
	check(401)
}
func TestVerifierGrammar(t *testing.T) {
	for _, v := range []string{strings.Repeat("a", 42), strings.Repeat("a", 129), strings.Repeat("a", 42) + "!", strings.Repeat("a", 42) + "é"} {
		if validVerifier(v) {
			t.Fatal("invalid verifier accepted")
		}
	}
	if !validVerifier(strings.Repeat("a", 43)) {
		t.Fatal("valid verifier rejected")
	}
}
func TestMetadata(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("metadata %s failed", path)
		}
	}
}

func TestLoginLockout(t *testing.T) {
	s := testServer(t)
	session, _ := randomToken()
	_, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'login', ?, ?, '', ?, '', ?)", hash(session), s.cfg.ClientID, authorizeValues(s).Encode(), s.resource(), time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		post := url.Values{"session": {session}, "password": {"wrong"}, "consent": {"allow"}}
		r := httptest.NewRequest("POST", "/authorize", strings.NewReader(post.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "nas_oauth", Value: session})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 403
		if i == 5 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: got %d want %d", i, w.Code, want)
		}
	}
}

// Wrong guesses in one browser must not lock the owner out in a new browser.
func TestFailedLoginDoesNotLockOtherSession(t *testing.T) {
	s := testServer(t)
	h := s.Handler()
	login := func() *http.Cookie {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+authorizeValues(s).Encode(), nil))
		if w.Code != 200 {
			t.Fatalf("login page: %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	submit := func(c *http.Cookie, password string) int {
		form := url.Values{"session": {c.Value}, "password": {password}, "consent": {"allow"}}
		r := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	attacker := login()
	for i := 0; i < 5; i++ {
		if got := submit(attacker, "wrong"); got != 403 {
			t.Fatalf("guess %d: %d", i, got)
		}
	}
	if got := submit(attacker, "correct horse battery staple"); got != 429 {
		t.Fatalf("spent session: %d", got)
	}
	owner := login()
	if got := submit(owner, "correct horse battery staple"); got != 303 {
		t.Fatalf("other session locked out: %d", got)
	}
}

func authorizeValues(s *Server) url.Values {
	sum := sha256.Sum256([]byte(strings.Repeat("a", 43)))
	return url.Values{"client_id": {s.cfg.ClientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "resource": {s.resource()}}
}
func TestAuthorizeRejectsDuplicateAndUnknownParameters(t *testing.T) {
	s := testServer(t)
	for _, key := range []string{"client_id", "resource", "password"} {
		v := authorizeValues(s)
		v.Add(key, "do-not-persist-me")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+v.Encode(), nil))
		if w.Code != 400 {
			t.Errorf("parameter %s accepted: %d", key, w.Code)
		}
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM oauth_grants").Scan(&n)
	if n != 0 {
		t.Fatal("invalid request persisted")
	}
}
func TestConsentShowsDestination(t *testing.T) {
	s := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+authorizeValues(s).Encode(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "claude.ai") {
		t.Fatal("consent omits redirect hostname")
	}
}
func TestLoginSessionsAreBounded(t *testing.T) {
	s := testServer(t)
	_, e := s.db.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<256) INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) SELECT printf('%064d',x), 'login', 'claude', '', '', '', '', ? FROM n`, time.Now().Add(time.Minute).Unix())
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+authorizeValues(s).Encode(), nil))
	if w.Code != 503 {
		t.Fatalf("full login table accepted request: %d", w.Code)
	}
}

func TestInvalidSessionCannotConsumePasswordAttempts(t *testing.T) {
	s := testServer(t)
	session, _ := randomToken()
	post := url.Values{"session": {session}, "password": {"wrong"}, "consent": {"allow"}}
	r := httptest.NewRequest("POST", "/authorize", strings.NewReader(post.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "nas_oauth", Value: session})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("nonexistent login session consumed password attempt")
	}
}
func TestTotalGrantCapacity(t *testing.T) {
	s := testServer(t)
	_, e := s.db.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<4096) INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) SELECT printf('%064d',x), 'refresh', 'claude', '', '', '', 'family', ? FROM n`, time.Now().Add(time.Hour).Unix())
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+authorizeValues(s).Encode(), nil))
	if w.Code != 503 {
		t.Fatalf("full grant table accepted request: %d", w.Code)
	}
}

func TestIssuerRequiresCanonicalOrigin(t *testing.T) {
	baseline := testServer(t)
	for _, issuer := range []string{"https://example.com?", "https://example.com#", "https://:", "https://example.com:0", "https://example.com:99999", "https://example.com:080"} {
		c := baseline.cfg
		c.Issuer = issuer
		c.DatabasePath = filepath.Join(t.TempDir(), "oauth.db")
		s, e := New(c)
		if e == nil {
			s.Close()
			t.Errorf("invalid issuer accepted: %s", issuer)
		}
	}
}

func TestAuthorizationCodeReplayRevokesFamily(t *testing.T) {
	s := testServer(t)
	code, _ := randomToken()
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	redirect := "https://claude.ai/api/mcp/auth_callback"
	_, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'code', ?, ?, ?, ?, ?, ?)", hash(code), s.cfg.ClientID, redirect, base64.RawURLEncoding.EncodeToString(sum[:]), s.resource(), hash(code), time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	v := url.Values{"client_id": {s.cfg.ClientID}, "grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {s.resource()}}
	exchange := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/token", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := exchange()
	if w.Code != 200 {
		t.Fatalf("initial exchange: %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if exchange().Code != 400 {
		t.Fatal("authorization code replay accepted")
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+body["access_token"].(string))
	w = httptest.NewRecorder()
	s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("replayed family remains authorized") })).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("replay did not revoke family")
	}
}

func TestConsentDisclosesEnabledServiceUpgrades(t *testing.T) {
	s := testServer(t)
	s.cfg.ServiceUpgrades = true
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?"+authorizeValues(s).Encode(), nil))
	if !strings.Contains(w.Body.String(), "upgrade the search service") || strings.Contains(w.Body.String(), "Allow read-only access") {
		t.Fatal("consent does not disclose service upgrade permission")
	}
}

// Requests with opaque bearer strings cannot queue unbounded SQLite work.
func TestBearerLookupCapacityAndCancellation(t *testing.T) {
	s := testServer(t)
	token := strings.Repeat("v", 43)
	_, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'access', ?, '', '', ?, '', ?)", hash(token), s.cfg.ClientID, s.resource(), time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	h := s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r := httptest.NewRequest("POST", "/mcp", nil).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			done <- w.Code
		}()
	}
	deadline := time.Now().Add(time.Second)
	for s.db.Stats().WaitCount < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.db.Stats().WaitCount < 2 {
		t.Fatal("lookups did not reach database")
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("unbounded lookup accepted: %d", w.Code)
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancelled lookup did not finish")
		}
	}
	conn.Close()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("lookup capacity not released: %d", w.Code)
	}
	if err := s.RevokeAll(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("revoked token remained authorized: %d", w.Code)
	}
}

func TestBearerLookupWaitsBrieflyForCapacity(t *testing.T) {
	s := testServer(t)
	token := strings.Repeat("w", 43)
	_, err := s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'access', ?, '', '', ?, '', ?)", hash(token), s.cfg.ClientID, s.resource(), time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	h := s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 3)
	request := func() {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		done <- w.Code
	}
	for i := 0; i < 2; i++ {
		go request()
	}
	deadline := time.Now().Add(time.Second)
	for s.db.Stats().WaitCount < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.db.Stats().WaitCount < 2 {
		conn.Close()
		t.Fatal("lookups did not reach database")
	}
	go request()
	select {
	case code := <-done:
		conn.Close()
		t.Fatalf("third lookup did not wait: %d", code)
	case <-time.After(50 * time.Millisecond):
	}
	conn.Close()
	for i := 0; i < 3; i++ {
		select {
		case code := <-done:
			if code != 204 {
				t.Fatalf("lookup returned %d, want 204", code)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("queued lookup did not finish")
		}
	}
}
