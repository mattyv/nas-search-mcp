package oauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

const (
	testPassword   = "correct horse battery staple"
	testPasskeyOrg = "http://localhost:8080"
)

// softKey is a software authenticator: it builds the same bytes a phone or
// security key returns, so the tests exercise real signature verification.
type softKey struct {
	priv   *ecdsa.PrivateKey
	credID []byte
	count  uint32
	// Overrides let a test model a phishing page or a cloned authenticator.
	origin, rpID string
	// zeroCount models authenticators that never count.
	zeroCount bool
}

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	rand.Read(id)
	return &softKey{priv: priv, credID: id, origin: testPasskeyOrg, rpID: "localhost"}
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k *softKey) clientData(kind, challenge string) []byte {
	out, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": k.origin, "crossOrigin": false})
	return out
}

func (k *softKey) authData(flags byte, withKey bool) []byte {
	rp := sha256.Sum256([]byte(k.rpID))
	out := append([]byte{}, rp[:]...)
	out = append(out, flags)
	counter := make([]byte, 4)
	if !k.zeroCount {
		binary.BigEndian.PutUint32(counter, k.count)
	}
	out = append(out, counter...)
	if withKey {
		out = append(out, make([]byte, 16)...)
		length := make([]byte, 2)
		binary.BigEndian.PutUint16(length, uint16(len(k.credID)))
		out = append(out, length...)
		out = append(out, k.credID...)
		x := k.priv.PublicKey.X.FillBytes(make([]byte, 32))
		y := k.priv.PublicKey.Y.FillBytes(make([]byte, 32))
		out = append(out, 0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20)
		out = append(out, x...)
		out = append(out, 0x22, 0x58, 0x20)
		out = append(out, y...)
	}
	return out
}

// create answers a registration request with an attestation of format "none".
func (k *softKey) create(t *testing.T, options []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("registration options: %v %s", err, options)
	}
	return k.createFor(o.PublicKey.Challenge)
}

func (k *softKey) createFor(challenge string) string {
	data := k.clientData("webauthn.create", challenge)
	auth := k.authData(0x45, true)
	att := []byte{0xa3, 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e', 0x67, 'a', 't', 't', 'S', 't', 'm', 't', 0xa0, 0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a', 0x59, byte(len(auth) >> 8), byte(len(auth))}
	att = append(att, auth...)
	out, _ := json.Marshal(map[string]any{"id": b64u(k.credID), "rawId": b64u(k.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64u(data), "attestationObject": b64u(att)}, "clientExtensionResults": map[string]any{}})
	return string(out)
}

// assert answers a login request. The counter rises by one unless the test pins it.
func (k *softKey) assert(t *testing.T, options []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RPID      string `json:"rpId"`
			Allow     []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("login options: %v %s", err, options)
	}
	if len(o.PublicKey.Allow) == 0 || o.PublicKey.RPID != "localhost" {
		t.Fatalf("login options omit credentials or use the wrong relying party: %s", options)
	}
	k.count++
	return k.assertFor(o.PublicKey.Challenge)
}

func (k *softKey) assertFor(challenge string) string {
	data := k.clientData("webauthn.get", challenge)
	auth := k.authData(0x05, false)
	digest := sha256.Sum256(data)
	signed := sha256.Sum256(append(append([]byte{}, auth...), digest[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, k.priv, signed[:])
	out, _ := json.Marshal(map[string]any{"id": b64u(k.credID), "rawId": b64u(k.credID), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64u(data), "authenticatorData": b64u(auth), "signature": b64u(sig), "userHandle": b64u([]byte("nas-search-mcp-owner"))},
		"clientExtensionResults": map[string]any{}})
	return string(out)
}

type pkFixture struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	jar http.CookieJar
}

func newPasskeyServerAt(t *testing.T, mode, issuer string) (*Server, error) {
	t.Helper()
	hashed, err := bcrypt.GenerateFromPassword([]byte(testPassword), 10)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Issuer: issuer, DatabasePath: filepath.Join(t.TempDir(), "oauth.db"), PasswordHash: string(hashed), ClientID: "claude", PasskeyMode: mode, ServiceUpgrades: true})
	if err == nil {
		t.Cleanup(func() { s.Close() })
	}
	return s, err
}

func newPasskeyFixture(t *testing.T, mode string) *pkFixture {
	t.Helper()
	s, err := newPasskeyServerAt(t, mode, testPasskeyOrg)
	if err != nil {
		t.Fatal(err)
	}
	s.limiter = rate.NewLimiter(rate.Inf, 1)
	jar, _ := cookiejar.New(nil)
	mux := http.NewServeMux()
	mux.Handle("/passkeys", s.PasskeysHandler())
	mux.Handle("/passkeys/", s.PasskeysHandler())
	mux.Handle("/upgrade/approve", s.BrowserActionHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("approval fixture")) }), func(*http.Request) bool { return true }))
	mux.Handle("/", s.Handler())
	return &pkFixture{t: t, s: s, h: mux, jar: jar}
}

func (f *pkFixture) do(method, target string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	if strings.HasPrefix(target, "/") {
		target = f.s.cfg.Issuer + target
	}
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if method == "POST" {
		r.Header.Set("Origin", f.s.cfg.Issuer)
	}
	for _, c := range f.jar.Cookies(r.URL) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	f.jar.SetCookies(r.URL, w.Result().Cookies())
	return w
}

var sessionField = regexp.MustCompile(`name="session" value="([^"]+)"`)

// begin opens a connector sign-in and returns its session.
func (f *pkFixture) begin() string {
	f.t.Helper()
	return f.beginAt("/authorize?" + authorizeValues(f.s).Encode())
}

func (f *pkFixture) beginAt(target string) string {
	f.t.Helper()
	w := f.do("GET", target, nil)
	m := sessionField.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		f.t.Fatalf("login page: %d %s", w.Code, w.Body.String())
	}
	return m[1]
}

func (f *pkFixture) password(session string) *httptest.ResponseRecorder {
	return f.do("POST", "/authorize", url.Values{"session": {session}, "password": {testPassword}, "consent": {"allow"}})
}

func (f *pkFixture) options(session string) *httptest.ResponseRecorder {
	return f.do("POST", "/authorize/passkey/options", url.Values{"session": {session}})
}

func (f *pkFixture) finish(session, credential string) *httptest.ResponseRecorder {
	return f.do("POST", "/authorize/passkey/finish", url.Values{"session": {session}, "credential": {credential}, "name": {"Test key"}})
}

func (f *pkFixture) skip(session string) *httptest.ResponseRecorder {
	return f.do("POST", "/authorize/passkey/finish", url.Values{"session": {session}, "skip": {"1"}})
}

func completed(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusSeeOther && strings.HasPrefix(w.Header().Get("Location"), "https://claude.ai/api/mcp/auth_callback?code=")
}

// enroll signs in with the password and registers key as the first passkey.
func (f *pkFixture) enroll(key *softKey) {
	f.t.Helper()
	session := f.begin()
	if w := f.password(session); w.Code != 200 {
		f.t.Fatalf("password step: %d %s", w.Code, w.Body.String())
	}
	opts := f.options(session)
	if opts.Code != 200 {
		f.t.Fatalf("enrollment options: %d %s", opts.Code, opts.Body.String())
	}
	if w := f.finish(session, key.create(f.t, opts.Body.Bytes())); !completed(w) {
		f.t.Fatalf("enrollment did not complete sign-in: %d %s", w.Code, w.Body.String())
	}
}

// signIn completes a full connector sign-in with key and returns the final response.
func (f *pkFixture) signIn(key *softKey) *httptest.ResponseRecorder {
	f.t.Helper()
	session := f.begin()
	if w := f.password(session); w.Code != 200 {
		f.t.Fatalf("password step: %d", w.Code)
	}
	opts := f.options(session)
	if opts.Code != 200 {
		f.t.Fatalf("login options: %d %s", opts.Code, opts.Body.String())
	}
	return f.finish(session, key.assert(f.t, opts.Body.Bytes()))
}

func (f *pkFixture) passkeyCount() int {
	var n int
	if err := f.s.db.QueryRow("SELECT count(*) FROM oauth_passkeys").Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestPasskeyModeValidation(t *testing.T) {
	for _, mode := range []string{"", "off", "enroll", "required"} {
		if _, err := newPasskeyServerAt(t, mode, testPasskeyOrg); err != nil {
			t.Errorf("mode %q rejected: %v", mode, err)
		}
	}
	for _, mode := range []string{"Enroll", "on", "true", "required ", "optional"} {
		if s, err := newPasskeyServerAt(t, mode, testPasskeyOrg); err == nil {
			s.Close()
			t.Errorf("accepted unknown mode %q", mode)
		}
	}
	// Passkeys bind to a host name; a bare IP address cannot be a relying party.
	if s, err := newPasskeyServerAt(t, "enroll", "http://127.0.0.1:8080"); err == nil {
		s.Close()
		t.Error("accepted passkeys on an IP address issuer")
	}
	if _, err := newPasskeyServerAt(t, "off", "http://127.0.0.1:8080"); err != nil {
		t.Errorf("mode off must not depend on the issuer host: %v", err)
	}
}

func TestPasskeysOnlyAddTables(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	rows, err := f.s.db.Query("SELECT name FROM pragma_table_info('oauth_grants')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var c string
		rows.Scan(&c)
		columns = append(columns, c)
	}
	if strings.Join(columns, ",") != "hash,kind,client,redirect,challenge,resource,family,expires,used" {
		t.Fatalf("oauth_grants changed: %v", columns)
	}
}

func TestPasskeyOffIsPasswordOnly(t *testing.T) {
	for _, mode := range []string{"off", ""} {
		f := newPasskeyFixture(t, mode)
		if w := f.password(f.begin()); !completed(w) {
			t.Fatalf("mode %q: password alone must complete: %d", mode, w.Code)
		}
		// Even with a registered passkey, off never asks for it.
		if _, err := f.s.db.Exec("INSERT INTO oauth_passkeys(credential_id,name,credential,sign_count,created) VALUES(x'01','x','{}',0,1)"); err != nil {
			t.Fatal(err)
		}
		if w := f.password(f.begin()); !completed(w) {
			t.Fatalf("mode %q with a stored passkey: %d", mode, w.Code)
		}
		if w := f.options(f.begin()); w.Code != 404 && w.Code != 400 {
			t.Fatalf("mode %q exposes passkey options: %d", mode, w.Code)
		}
	}
}

func TestEnrollModeOffersOptionalEnrollmentAndSkipWorks(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	session := f.begin()
	w := f.password(session)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatalf("password alone must show the enrollment offer: %d", w.Code)
	}
	for _, want := range []string{"Add a passkey (recommended)", "Register", "Skip", `src="/authorize/passkey.js"`, "Use your iPhone, Mac or security key", "QR code"} {
		if !strings.Contains(body, want) {
			t.Errorf("enrollment page lacks %q", want)
		}
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("passkey page CSP: %q", csp)
	}
	if regexp.MustCompile(`<script(\s[^>]*)?>[^<]`).MatchString(body) || strings.Contains(body, " onclick=") || strings.Contains(body, " onsubmit=") {
		t.Error("passkey page contains inline script")
	}
	if skipped := f.skip(session); !completed(skipped) {
		t.Fatalf("skip must finish sign-in: %d %s", skipped.Code, skipped.Body.String())
	}
	if f.passkeyCount() != 0 {
		t.Fatal("skip stored a passkey")
	}
}

func TestPasswordPageStillUsesStrictCSP(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	w := f.do("GET", "/authorize?"+authorizeValues(f.s).Encode(), nil)
	if csp := w.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'" {
		t.Fatalf("password page CSP changed: %q", csp)
	}
}

func TestPasskeyScriptIsServedSameOrigin(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	w := f.do("GET", "/authorize/passkey.js", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("script: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"navigator.credentials.create", "navigator.credentials.get", "atob", "btoa"} {
		if !strings.Contains(body, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(body, "eval(") || strings.Contains(body, "innerHTML") {
		t.Error("script uses unsafe sinks")
	}
}

func TestEnrollmentOptionsPreferResidentKeyAndUserVerification(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	session := f.begin()
	f.password(session)
	w := f.options(session)
	var o struct {
		PublicKey struct {
			RP   struct{ ID, Name string } `json:"rp"`
			User struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"user"`
			Selection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			Attestation string `json:"attestation"`
		} `json:"publicKey"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
		t.Fatalf("options: %d %s", w.Code, w.Body.String())
	}
	p := o.PublicKey
	if p.RP.ID != "localhost" || p.User.DisplayName != "NAS owner" || p.Selection.ResidentKey != "preferred" || p.Selection.UserVerification != "preferred" || (p.Attestation != "" && p.Attestation != "none") {
		t.Fatalf("unexpected registration options: %+v", p)
	}
}

func TestEnrollThenPasskeyIsRequired(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)
	if f.passkeyCount() != 1 {
		t.Fatal("passkey not stored")
	}
	// Password alone no longer completes sign-in.
	session := f.begin()
	w := f.password(session)
	if w.Code != 200 || completed(w) || strings.Contains(w.Body.String(), "Skip") || !strings.Contains(w.Body.String(), "Use your iPhone, Mac or security key") {
		t.Fatalf("expected a passkey step, got %d", w.Code)
	}
	// Skip must not bypass an existing passkey.
	if sk := f.skip(session); completed(sk) || sk.Code < 400 {
		t.Fatalf("skip bypassed a registered passkey: %d", sk.Code)
	}
	// A credential posted without a challenge fails.
	if bad := f.finish(session, key.assertFor(strings.Repeat("A", 43))); completed(bad) {
		t.Fatal("assertion without a challenge completed sign-in")
	}
	// A fresh sign-in with a real assertion works.
	if ok := f.signIn(key); !completed(ok) {
		t.Fatalf("passkey sign-in: %d %s", ok.Code, ok.Body.String())
	}
	var count int
	var lastUsed int64
	f.s.db.QueryRow("SELECT sign_count,last_used FROM oauth_passkeys").Scan(&count, &lastUsed)
	if count != int(key.count) || lastUsed == 0 {
		t.Fatalf("sign count and last used not saved: %d %d", count, lastUsed)
	}
}

func TestRequiredModeWithoutPasskeyOffersEnrollmentOnly(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	session := f.begin()
	w := f.password(session)
	if w.Code != 200 || completed(w) || !strings.Contains(w.Body.String(), "Register") || strings.Contains(w.Body.String(), "Skip") {
		t.Fatalf("required mode must force enrollment: %d", w.Code)
	}
	if sk := f.skip(session); completed(sk) {
		t.Fatal("skip completed in required mode")
	}
	key := newSoftKey(t)
	opts := f.options(session)
	if w := f.finish(session, key.create(t, opts.Body.Bytes())); !completed(w) {
		t.Fatalf("enrollment in required mode: %d %s", w.Code, w.Body.String())
	}
	if f.passkeyCount() != 1 {
		t.Fatal("not stored")
	}
	if w := f.password(f.begin()); completed(w) {
		t.Fatal("password alone completed after enrollment")
	}
}

func TestRegistrationRejectsWrongChallengeOriginAndRelyingParty(t *testing.T) {
	for name, tweak := range map[string]func(k *softKey, challenge string) string{
		"wrong challenge": func(k *softKey, c string) string { return k.createFor(b64u([]byte("some other challenge value....."))) },
		"wrong origin":    func(k *softKey, c string) string { k.origin = "http://evil.localhost:8080"; return k.createFor(c) },
		"wrong rp id":     func(k *softKey, c string) string { k.rpID = "evil.example"; return k.createFor(c) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newPasskeyFixture(t, "enroll")
			session := f.begin()
			f.password(session)
			var o struct {
				PublicKey struct {
					Challenge string `json:"challenge"`
				} `json:"publicKey"`
			}
			json.Unmarshal(f.options(session).Body.Bytes(), &o)
			w := f.finish(session, tweak(newSoftKey(t), o.PublicKey.Challenge))
			if completed(w) || w.Code != 403 || f.passkeyCount() != 0 {
				t.Fatalf("accepted bad registration: %d", w.Code)
			}
		})
	}
}

func TestAssertionRejections(t *testing.T) {
	cases := map[string]func(f *pkFixture, key *softKey, challenge string) string{
		"wrong challenge": func(f *pkFixture, key *softKey, c string) string {
			key.count++
			return key.assertFor(b64u([]byte("some other challenge value.....")))
		},
		"wrong origin": func(f *pkFixture, key *softKey, c string) string {
			key.count++
			key.origin = "http://evil.localhost:8080"
			return key.assertFor(c)
		},
		"wrong rp id": func(f *pkFixture, key *softKey, c string) string {
			key.count++
			key.rpID = "evil.example"
			return key.assertFor(c)
		},
		"unknown credential": func(f *pkFixture, key *softKey, c string) string {
			return newSoftKey(t).assertFor(c)
		},
		"garbage": func(f *pkFixture, key *softKey, c string) string { return "{not json" },
		"empty":   func(f *pkFixture, key *softKey, c string) string { return "" },
	}
	for name, attack := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPasskeyFixture(t, "enroll")
			key := newSoftKey(t)
			f.enroll(key)
			session := f.begin()
			f.password(session)
			opts := f.options(session)
			var o struct {
				PublicKey struct {
					Challenge string `json:"challenge"`
				} `json:"publicKey"`
			}
			json.Unmarshal(opts.Body.Bytes(), &o)
			bad := attack(f, key, o.PublicKey.Challenge)
			if w := f.finish(session, bad); completed(w) || w.Code != 403 {
				t.Fatalf("accepted %s: %d", name, w.Code)
			}
			// The failed attempt used up the challenge: even the genuine response for it is refused.
			good := newSoftKey(t)
			*good = *key
			good.origin, good.rpID = testPasskeyOrg, "localhost"
			good.count++
			if w := f.finish(session, good.assertFor(o.PublicKey.Challenge)); completed(w) {
				t.Fatal("a challenge was reused after a failed attempt")
			}
			// A new challenge in the same login still works with the real key.
			opts = f.options(session)
			*good = *key
			good.origin, good.rpID = testPasskeyOrg, "localhost"
			good.count = key.count + 2
			if w := f.finish(session, good.assert(t, opts.Body.Bytes())); !completed(w) {
				t.Fatalf("legitimate retry failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAssertionReplayAndSessionBinding(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)

	// One login session captures an assertion for its challenge.
	a := f.begin()
	f.password(a)
	optsA := f.options(a)
	assertionA := key.assert(t, optsA.Body.Bytes())

	// A second login session must not accept an assertion made for the first one's challenge.
	b := f.begin()
	f.password(b)
	f.options(b)
	if w := f.finish(b, assertionA); completed(w) {
		t.Fatal("assertion for another session's challenge accepted")
	}
	if w := f.finish(a, assertionA); !completed(w) {
		t.Fatalf("original session: %d %s", w.Code, w.Body.String())
	}
	// Replaying the used assertion in a new session fails on challenge and counter.
	c := f.begin()
	f.password(c)
	f.options(c)
	if w := f.finish(c, assertionA); completed(w) {
		t.Fatal("replayed assertion accepted")
	}
	// The finished session is gone.
	if w := f.finish(a, assertionA); completed(w) || w.Code < 400 {
		t.Fatal("finished login session accepted a second assertion")
	}
}

func TestChallengeExpiresAfterFiveMinutes(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)
	session := f.begin()
	f.password(session)
	opts := f.options(session)
	var expires int64
	if err := f.s.db.QueryRow("SELECT challenge_expires FROM oauth_passkey_flows WHERE session_hash=?", hash(session)).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(time.Unix(expires, 0)); d < 4*time.Minute || d > 5*time.Minute+5*time.Second {
		t.Fatalf("challenge lifetime %v, want about 5 minutes", d)
	}
	if _, err := f.s.db.Exec("UPDATE oauth_passkey_flows SET challenge_expires=? WHERE session_hash=?", time.Now().Add(-time.Second).Unix(), hash(session)); err != nil {
		t.Fatal(err)
	}
	if w := f.finish(session, key.assert(t, opts.Body.Bytes())); completed(w) {
		t.Fatal("expired challenge accepted")
	}
}

func TestSignCountMustIncreaseUnlessAuthenticatorNeverCounts(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)                          // registration reports counter 0
	if w := f.signIn(key); !completed(w) { // counter 1
		t.Fatalf("first sign-in: %d", w.Code)
	}
	// A clone presenting counter 1 again is refused.
	session := f.begin()
	f.password(session)
	opts := f.options(session)
	key.count = 1
	data := key.assertFor(challengeOf(t, opts.Body.Bytes()))
	if w := f.finish(session, data); completed(w) {
		t.Fatal("non-increasing counter accepted")
	}
	// A lower counter is refused too.
	opts = f.options(session)
	key.count = 0
	key.zeroCount = false
	if w := f.finish(session, key.assertFor(challengeOf(t, opts.Body.Bytes()))); completed(w) {
		t.Fatal("decreasing counter accepted")
	}
	var stored int
	f.s.db.QueryRow("SELECT sign_count FROM oauth_passkeys").Scan(&stored)
	if stored != 1 {
		t.Fatalf("refused attempts changed the stored counter: %d", stored)
	}

	// Authenticators that never count (0 and 0) remain usable.
	g := newPasskeyFixture(t, "enroll")
	zero := newSoftKey(t)
	zero.zeroCount = true
	g.enroll(zero)
	for i := 0; i < 2; i++ {
		if w := g.signIn(zero); !completed(w) {
			t.Fatalf("counter-less authenticator, sign-in %d: %d %s", i, w.Code, w.Body.String())
		}
	}
}

func challengeOf(t *testing.T, options []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if json.Unmarshal(options, &o) != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("no challenge in %s", options)
	}
	return o.PublicKey.Challenge
}

func TestPasskeyFailuresShareTheLoginAttemptLimit(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)
	session := f.begin()
	// Two wrong passwords and three wrong passkeys use up the five attempts.
	for i := 0; i < 2; i++ {
		if w := f.do("POST", "/authorize", url.Values{"session": {session}, "password": {"wrong"}, "consent": {"allow"}}); w.Code != 403 {
			t.Fatalf("wrong password: %d", w.Code)
		}
	}
	if w := f.password(session); w.Code != 200 {
		t.Fatalf("right password: %d", w.Code)
	}
	for i := 0; i < 3; i++ {
		f.options(session)
		if w := f.finish(session, key.assertFor(b64u([]byte("some other challenge value.....")))); w.Code != 403 {
			t.Fatalf("wrong passkey %d: %d", i, w.Code)
		}
	}
	// Five failures recorded; both steps now refuse.
	opts := f.options(session)
	if opts.Code != 429 {
		t.Fatalf("options after the limit: %d", opts.Code)
	}
	if w := f.finish(session, key.assertFor(b64u([]byte("some other challenge value.....")))); w.Code != 429 {
		t.Fatalf("finish after the limit: %d", w.Code)
	}
	if w := f.password(session); w.Code != 429 {
		t.Fatalf("password after the limit: %d", w.Code)
	}
}

func TestPasskeyStepRequiresPassword(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)
	session := f.begin() // no password yet
	if w := f.options(session); w.Code == 200 {
		t.Fatal("options issued before the password")
	}
	if w := f.finish(session, key.assertFor(strings.Repeat("A", 43))); completed(w) {
		t.Fatal("passkey alone completed sign-in")
	}
}

func TestPasskeyEndpointsRequireSameOriginAndPOST(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	session := f.begin()
	f.password(session)
	r := httptest.NewRequest("POST", f.s.cfg.Issuer+"/authorize/passkey/options", strings.NewReader(url.Values{"session": {session}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	for _, c := range f.jar.Cookies(r.URL) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("cross-origin options: %d", w.Code)
	}
	if w := f.do("GET", "/authorize/passkey/options", nil); w.Code == 200 {
		t.Fatal("GET options")
	}
}

// ---- Management page ----

func (f *pkFixture) signInBrowser(target string, key *softKey) {
	f.t.Helper()
	start := f.do("GET", target, nil)
	if start.Code != 303 || !strings.HasPrefix(start.Header().Get("Location"), f.s.cfg.Issuer+"/authorize?") {
		f.t.Fatalf("%s did not start sign-in: %d", target, start.Code)
	}
	session := f.beginAt(start.Header().Get("Location"))
	w := f.password(session)
	if key == nil && f.passkeyCount() == 0 {
		w = f.skip(session) // enroll mode offers a first passkey; skip it
	}
	if key != nil && f.passkeyCount() > 0 {
		if w.Code != 200 || completed(w) {
			f.t.Fatalf("browser password step must wait for the passkey: %d", w.Code)
		}
		opts := f.options(session)
		w = f.finish(session, key.assert(f.t, opts.Body.Bytes()))
	}
	location := w.Header().Get("Location")
	if w.Code != 303 || !strings.HasPrefix(location, f.s.cfg.Issuer+browserCallbackPath) {
		f.t.Fatalf("browser sign-in did not reach the callback: %d %s", w.Code, location)
	}
	if done := f.do("GET", location, nil); done.Code != 303 {
		f.t.Fatalf("callback: %d", done.Code)
	}
}

func (f *pkFixture) addViaManager(key *softKey, name string) *httptest.ResponseRecorder {
	f.t.Helper()
	opts := f.do("POST", "/passkeys/options", url.Values{})
	if opts.Code != 200 {
		return opts
	}
	return f.do("POST", "/passkeys/add", url.Values{"credential": {key.create(f.t, opts.Body.Bytes())}, "name": {name}})
}

func TestPasskeyManagerRequiresFreshSignIn(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	if w := f.do("GET", "/passkeys", nil); w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), f.s.cfg.Issuer+"/authorize?") {
		t.Fatalf("manager GET without session: %d", w.Code)
	}
	for _, path := range []string{"/passkeys/options", "/passkeys/add", "/passkeys/rename", "/passkeys/remove"} {
		if w := f.do("POST", path, url.Values{"id": {"1"}}); w.Code != 401 {
			t.Errorf("POST %s without session: %d", path, w.Code)
		}
	}
	if w := f.do("GET", "/passkeys?x=1", nil); w.Code != 404 {
		t.Errorf("query string accepted: %d", w.Code)
	}
}

func TestPasskeyManagerAddListRenameRemove(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	f.signInBrowser("/passkeys", nil) // enroll mode with none: password completes
	page := f.do("GET", "/passkeys", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Add a passkey") {
		t.Fatalf("manager page: %d", page.Code)
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("manager CSP: %q", csp)
	}
	key := newSoftKey(t)
	added := f.addViaManager(key, "Matt's iPhone")
	if added.Code != 200 || !strings.Contains(added.Body.String(), "Matt&#39;s iPhone") {
		t.Fatalf("add: %d %s", added.Code, added.Body.String())
	}
	if !strings.Contains(added.Body.String(), "Last used") || !strings.Contains(added.Body.String(), "never") {
		t.Fatal("list omits last used")
	}
	var id int
	f.s.db.QueryRow("SELECT id FROM oauth_passkeys").Scan(&id)
	idv := url.Values{"id": {itoa(id)}}
	rename := f.do("POST", "/passkeys/rename", url.Values{"id": {itoa(id)}, "name": {"Work Mac"}})
	if rename.Code != 200 || !strings.Contains(rename.Body.String(), "Work Mac") {
		t.Fatalf("rename: %d", rename.Code)
	}
	if bad := f.do("POST", "/passkeys/rename", url.Values{"id": {itoa(id)}, "name": {strings.Repeat("x", 200)}}); bad.Code == 200 && strings.Contains(bad.Body.String(), strings.Repeat("x", 200)) {
		t.Fatal("overlong name accepted")
	}
	// Removal asks first.
	ask := f.do("POST", "/passkeys/remove", idv)
	if ask.Code != 200 || !strings.Contains(ask.Body.String(), "Work Mac") || f.passkeyCount() != 1 {
		t.Fatalf("remove without confirmation: %d count=%d", ask.Code, f.passkeyCount())
	}
	idv.Set("confirm", "yes")
	if done := f.do("POST", "/passkeys/remove", idv); done.Code != 200 || f.passkeyCount() != 0 {
		t.Fatalf("remove: %d count=%d", done.Code, f.passkeyCount())
	}
}

func TestPasskeyManagerCapacityIsTen(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	f.signInBrowser("/passkeys", nil)
	for i := 0; i < 10; i++ {
		if w := f.addViaManager(newSoftKey(t), "key"); w.Code != 200 {
			t.Fatalf("add %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if f.passkeyCount() != 10 {
		t.Fatalf("count %d", f.passkeyCount())
	}
	if w := f.do("POST", "/passkeys/options", url.Values{}); w.Code == 200 {
		t.Fatal("options issued beyond capacity")
	}
	// Even a valid ceremony cannot exceed capacity.
	if w := f.do("POST", "/passkeys/add", url.Values{"credential": {newSoftKey(t).createFor(strings.Repeat("A", 43))}}); w.Code == 200 {
		t.Fatal("11th passkey accepted")
	}
	if f.passkeyCount() != 10 {
		t.Fatalf("count %d", f.passkeyCount())
	}
}

func TestRequiredModeCannotRemoveLastPasskey(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	key := newSoftKey(t)
	f.enroll(key)
	f.jar, _ = cookiejar.New(nil)
	f.signInBrowser("/passkeys", key)
	var id int
	f.s.db.QueryRow("SELECT id FROM oauth_passkeys").Scan(&id)
	w := f.do("POST", "/passkeys/remove", url.Values{"id": {itoa(id)}, "confirm": {"yes"}})
	if f.passkeyCount() != 1 || !strings.Contains(w.Body.String(), "at least one passkey") {
		t.Fatalf("last passkey removed in required mode: %d %s", f.passkeyCount(), w.Body.String())
	}
	// With a second passkey, one can go.
	if w := f.addViaManager(newSoftKey(t), "spare"); w.Code != 200 {
		t.Fatalf("add spare: %d", w.Code)
	}
	if w := f.do("POST", "/passkeys/remove", url.Values{"id": {itoa(id)}, "confirm": {"yes"}}); w.Code != 200 || f.passkeyCount() != 1 {
		t.Fatalf("removal with a spare: %d count=%d", w.Code, f.passkeyCount())
	}
}

func TestEnrollModeMayRemoveLastPasskeyAndReturnsToOptionalEnrollment(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	key := newSoftKey(t)
	f.enroll(key)
	f.jar, _ = cookiejar.New(nil)
	f.signInBrowser("/passkeys", key)
	var id int
	f.s.db.QueryRow("SELECT id FROM oauth_passkeys").Scan(&id)
	if w := f.do("POST", "/passkeys/remove", url.Values{"id": {itoa(id)}, "confirm": {"yes"}}); w.Code != 200 || f.passkeyCount() != 0 {
		t.Fatalf("remove: %d", w.Code)
	}
}

func TestManagerRegistrationChallengeIsSingleUseAndSessionBound(t *testing.T) {
	f := newPasskeyFixture(t, "enroll")
	f.signInBrowser("/passkeys", nil)
	opts := f.do("POST", "/passkeys/options", url.Values{})
	key := newSoftKey(t)
	payload := key.create(t, opts.Body.Bytes())
	form := url.Values{"credential": {payload}, "name": {"one"}}
	if w := f.do("POST", "/passkeys/add", form); w.Code != 200 {
		t.Fatalf("add: %d", w.Code)
	}
	other := newSoftKey(t)
	if w := f.do("POST", "/passkeys/add", url.Values{"credential": {other.createFor(challengeOf(t, opts.Body.Bytes()))}, "name": {"replay"}}); w.Code == 200 {
		t.Fatal("registration challenge reused")
	}
	if f.passkeyCount() != 1 {
		t.Fatalf("count %d", f.passkeyCount())
	}
}

func TestBrowserActionsRequirePasskeyWhenRequired(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	key := newSoftKey(t)
	f.enroll(key)
	f.jar, _ = cookiejar.New(nil)
	target := "/upgrade/approve?request=" + strings.Repeat("a", 43)
	start := f.do("GET", target, nil)
	session := f.beginAt(start.Header().Get("Location"))
	if w := f.password(session); w.Code != 200 || strings.Contains(w.Header().Get("Location"), browserCallbackPath) {
		t.Fatalf("password alone moved the upgrade approval forward: %d", w.Code)
	}
	if w := f.do("GET", target, nil); w.Code == 200 {
		t.Fatal("approval page served after password only")
	}
	opts := f.options(session)
	w := f.finish(session, key.assert(t, opts.Body.Bytes()))
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), f.s.cfg.Issuer+browserCallbackPath) {
		t.Fatalf("passkey did not complete the approval sign-in: %d", w.Code)
	}
	f.do("GET", w.Header().Get("Location"), nil)
	if got := f.do("GET", target, nil); got.Code != 200 || got.Body.String() != "approval fixture" {
		t.Fatalf("approval after passkey: %d", got.Code)
	}
}

func TestResetPasskeysRemovesEverything(t *testing.T) {
	f := newPasskeyFixture(t, "required")
	key := newSoftKey(t)
	f.enroll(key)
	session := f.begin()
	f.password(session)
	f.options(session)
	if f.passkeyCount() == 0 {
		t.Fatal("setup")
	}
	if err := ResetPasskeys(f.s.cfg.DatabasePath); err != nil {
		t.Fatal(err)
	}
	var passkeys, flows int
	f.s.db.QueryRow("SELECT count(*) FROM oauth_passkeys").Scan(&passkeys)
	f.s.db.QueryRow("SELECT count(*) FROM oauth_passkey_flows").Scan(&flows)
	if passkeys != 0 || flows != 0 {
		t.Fatalf("reset left passkeys=%d flows=%d", passkeys, flows)
	}
	// Host-only recovery: the owner is offered enrollment again.
	if w := f.password(f.begin()); w.Code != 200 || !strings.Contains(w.Body.String(), "Register") {
		t.Fatalf("after reset: %d", w.Code)
	}
	// It also works on a database that predates passkeys.
	old := filepath.Join(t.TempDir(), "old.db")
	if err := ResetPasskeys(old); err != nil {
		t.Fatalf("reset on a fresh database: %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
