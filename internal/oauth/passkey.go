package oauth

// Passkeys (WebAuthn) as a second sign-in factor. The ceremony checks are done
// by github.com/go-webauthn/webauthn; the decisions about when a passkey is
// required, whether a counter is acceptable and how many fit come from
// internal/policy (verified) or passkey_rules.go (stubs awaiting the core).
// The HTTP, SQLite and browser glue here is not formally verified.

import (
	"bytes"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// The passkey tables are new; existing tables are never altered, so the
// previous release still runs on this database after a rollback.
const passkeySchema = `CREATE TABLE IF NOT EXISTS oauth_passkeys(
 id INTEGER PRIMARY KEY AUTOINCREMENT, credential_id BLOB NOT NULL UNIQUE, name TEXT NOT NULL,
 credential TEXT NOT NULL, sign_count INTEGER NOT NULL, created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS oauth_passkey_flows(
 session_hash TEXT PRIMARY KEY, purpose TEXT NOT NULL, session_data TEXT NOT NULL,
 challenge_expires INTEGER NOT NULL, expires INTEGER NOT NULL);`

//go:embed passkey.js
var passkeyScript []byte

const (
	// Passkey pages may load the same-origin script and call same-origin endpoints; nothing else.
	passkeyCSP        = "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; script-src 'self'; connect-src 'self'"
	challengeLifetime = 5 * time.Minute
	maxCredentialForm = 6000
)

var passkeyUserHandle = []byte("nas-search-mcp-owner")

type dbQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}

type storedPasskey struct {
	ID                int64
	Name              string
	Credential        webauthn.Credential
	SignCount         int64
	Created, LastUsed int64
}

func (p storedPasskey) CreatedText() string  { return stamp(p.Created) }
func (p storedPasskey) LastUsedText() string { return stamp(p.LastUsed) }

func stamp(unix int64) string {
	if unix == 0 {
		return "never"
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC")
}

// passkeyOwner is the single owner account that every passkey belongs to.
type passkeyOwner struct{ credentials []webauthn.Credential }

func (passkeyOwner) WebAuthnID() []byte                           { return passkeyUserHandle }
func (passkeyOwner) WebAuthnName() string                         { return "owner" }
func (passkeyOwner) WebAuthnDisplayName() string                  { return "NAS owner" }
func (o passkeyOwner) WebAuthnCredentials() []webauthn.Credential { return o.credentials }

func newRelyingParty(c Config) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(c.Issuer)
	if err != nil {
		return nil, err
	}
	required := false
	limits := webauthn.TimeoutConfig{Enforce: true, Timeout: challengeLifetime, TimeoutUVD: challengeLifetime}
	wa, err := webauthn.New(&webauthn.Config{
		RPID: u.Hostname(), RPDisplayName: "NAS search", RPOrigins: []string{c.Issuer},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: &required, ResidentKey: protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: limits, Registration: limits},
	})
	if err != nil {
		return nil, errors.New("passkeys need an issuer with a host name (not an IP address): " + err.Error())
	}
	return wa, nil
}

func listPasskeys(q dbQuerier) ([]storedPasskey, error) {
	rows, err := q.Query("SELECT id,name,credential,sign_count,created,last_used FROM oauth_passkeys ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedPasskey
	for rows.Next() {
		var p storedPasskey
		var raw string
		if err = rows.Scan(&p.ID, &p.Name, &raw, &p.SignCount, &p.Created, &p.LastUsed); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p.Credential); err != nil {
			return nil, err
		}
		if p.SignCount >= 0 && p.SignCount <= 1<<32-1 {
			p.Credential.Authenticator.SignCount = uint32(p.SignCount)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func ownerOf(keys []storedPasskey) passkeyOwner {
	o := passkeyOwner{}
	for _, k := range keys {
		o.credentials = append(o.credentials, k.Credential)
	}
	return o
}

func insertPasskey(q dbQuerier, name string, c *webauthn.Credential) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = q.Exec("INSERT INTO oauth_passkeys(credential_id,name,credential,sign_count,created) VALUES(?,?,?,?,?)", c.ID, name, string(raw), int64(c.Authenticator.SignCount), time.Now().Unix())
	return err
}

// ResetPasskeys deletes every passkey and pending challenge. It is the
// host-only recovery for a lost authenticator; it never touches OAuth grants.
func ResetPasskeys(dbPath string) error {
	if err := privateDatabase(dbPath); err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", sqliteURI(dbPath))
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{passkeySchema, "DELETE FROM oauth_passkeys", "DELETE FROM oauth_passkey_flows"} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- sign-in ceremony ----

var passkeyPage = template.Must(template.New("passkey").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>NAS passkey</title></head><body>
{{if eq .Step "assert"}}<h1>Confirm with your passkey</h1>{{else}}<h1>Add a passkey{{if eq .Step "enroll_optional"}} (recommended){{end}}</h1><p>A passkey replaces typing a second code. Your device keeps it; the NAS never sees a secret.</p>{{end}}
<p>Use your iPhone, Mac or security key. On Linux, Chrome shows a QR code: scan it with your phone.</p>
{{if .Message}}<p role="alert">{{.Message}}</p>{{end}}
<form method="post" action="/authorize/passkey/finish" data-passkey="{{if eq .Step "assert"}}get{{else}}create{{end}}" data-options="/authorize/passkey/options"><input type="hidden" name="session" value="{{.Session}}"><input type="hidden" name="credential" value="">{{if ne .Step "assert"}}<label>Name this passkey <input name="name" value="My passkey" maxlength="64"></label> {{end}}<button type="submit">{{if eq .Step "assert"}}Use passkey{{else}}Register{{end}}</button></form>
{{if eq .Step "enroll_optional"}}<form method="post" action="/authorize/passkey/finish"><input type="hidden" name="session" value="{{.Session}}"><input type="hidden" name="skip" value="1"><button type="submit">Skip</button></form>{{end}}
<p id="passkey-status" role="status"></p><noscript><p>Passkeys need JavaScript. Turn it on for this page.</p></noscript>
<script src="/authorize/passkey.js"></script></body></html>`))

func renderPasskeyStep(w http.ResponseWriter, status int, session, step, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", passkeyCSP)
	// Pages with forms must let the browser send their origin on same-site requests:
	// under "no-referrer" a form submission carries Origin "null" and is refused.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(status)
	passkeyPage.Execute(w, struct{ Session, Step, Message string }{session, step, message})
}

func (s *Server) passkeyScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write(passkeyScript)
}

// beginPasskeyStep runs inside consent after a correct password. It reports
// true when it has answered the request (passkey step shown or an error sent).
func (s *Server) beginPasskeyStep(w http.ResponseWriter, tx *sql.Tx, session string) bool {
	if s.wa == nil {
		return false
	}
	var registered int
	if err := tx.QueryRow("SELECT count(*) FROM oauth_passkeys").Scan(&registered); err != nil {
		oauthError(w, 500, "server_error")
		return true
	}
	step := passkeyStep(s.cfg.PasskeyMode, registered)
	if step == stepComplete {
		return false
	}
	// The password is now verified for this login session; the flow row records that.
	_, err := tx.Exec(`INSERT INTO oauth_passkey_flows(session_hash,purpose,session_data,challenge_expires,expires)
 SELECT hash,'ready','',0,expires FROM oauth_grants WHERE hash=? AND kind='login'
 ON CONFLICT(session_hash) DO UPDATE SET purpose='ready',session_data='',challenge_expires=0`, hash(session))
	if err != nil || tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return true
	}
	renderPasskeyStep(w, 200, session, step, "")
	return true
}

type loginState struct {
	s         *Server
	tx        *sql.Tx
	session   string
	values    url.Values
	failures  int
	purpose   string
	data      string
	challenge int64
}

func (l *loginState) close() {
	l.tx.Rollback()
	l.s.mu.Unlock()
}

// openLogin applies the checks every passkey request shares with the password
// step: same-origin POST, login cookie, attempt limit, and a verified password.
// On success the caller holds the server lock and must call close.
func (s *Server) openLogin(w http.ResponseWriter, r *http.Request) *loginState {
	if s.wa == nil {
		http.NotFound(w, r)
		return nil
	}
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return nil
	}
	if r.ParseForm() != nil {
		passkeyRequestError(w)
		return nil
	}
	session := r.PostForm.Get("session")
	cookie, err := r.Cookie(loginCookieName(session))
	if err != nil || len(session) != 43 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(session)) != 1 || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.cfg.Issuer) {
		passkeyRequestError(w)
		return nil
	}
	s.mu.Lock()
	tx, err := s.db.Begin()
	if err != nil {
		s.mu.Unlock()
		oauthError(w, 500, "server_error")
		return nil
	}
	l := &loginState{s: s, tx: tx, session: session}
	now := time.Now().Unix()
	var raw string
	if err = tx.QueryRow("SELECT redirect,used FROM oauth_grants WHERE hash=? AND kind='login' AND expires>?", hash(session), now).Scan(&raw, &l.failures); err != nil {
		l.close()
		passkeyRequestError(w)
		return nil
	}
	if !policy.LoginAttemptAllowed(l.failures) {
		l.close()
		oauthError(w, 429, "temporarily_unavailable")
		return nil
	}
	if l.values, err = url.ParseQuery(raw); err != nil || !s.validAuthorizeWith(l.values, tx) {
		l.close()
		passkeyRequestError(w)
		return nil
	}
	if err = tx.QueryRow("SELECT purpose,session_data,challenge_expires FROM oauth_passkey_flows WHERE session_hash=? AND expires>?", hash(session), now).Scan(&l.purpose, &l.data, &l.challenge); err != nil {
		l.close()
		passkeyRequestError(w)
		return nil
	}
	return l
}

func (s *Server) passkeyOptions(w http.ResponseWriter, r *http.Request) {
	l := s.openLogin(w, r)
	if l == nil {
		return
	}
	defer l.close()
	keys, err := listPasskeys(l.tx)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	owner := ownerOf(keys)
	var payload any
	var session *webauthn.SessionData
	var purpose string
	switch passkeyStep(s.cfg.PasskeyMode, len(keys)) {
	case stepAssert:
		payload, session, err = s.beginAssertion(owner)
		purpose = "assert"
	case stepEnrollOptional, stepEnrollRequired:
		payload, session, err = s.beginEnrollment(owner)
		purpose = "register"
	default:
		oauthError(w, 400, "invalid_request")
		return
	}
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	data, err := json.Marshal(session)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if _, err = l.tx.Exec("UPDATE oauth_passkey_flows SET purpose=?,session_data=?,challenge_expires=? WHERE session_hash=?", purpose, string(data), time.Now().Add(challengeLifetime).Unix(), hash(l.session)); err != nil || l.tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	jsonReply(w, 200, payload)
}

func (s *Server) beginAssertion(owner passkeyOwner) (any, *webauthn.SessionData, error) {
	assertion, session, err := s.wa.BeginLogin(owner, webauthn.WithUserVerification(protocol.VerificationPreferred))
	return assertion, session, err
}

func (s *Server) beginEnrollment(owner passkeyOwner) (any, *webauthn.SessionData, error) {
	creation, session, err := s.wa.BeginRegistration(owner, webauthn.WithExclusions(webauthn.Credentials(owner.credentials).CredentialDescriptors()))
	return creation, session, err
}

// reject counts a failed attempt against the login session, spends any
// outstanding challenge, and shows the passkey step again.
func (l *loginState) reject(w http.ResponseWriter, step, message string) {
	_, e1 := l.tx.Exec("UPDATE oauth_grants SET used=? WHERE hash=? AND kind='login'", policy.FailedLoginCount(l.failures), hash(l.session))
	_, e2 := l.tx.Exec("UPDATE oauth_passkey_flows SET purpose='ready',session_data='',challenge_expires=0 WHERE session_hash=?", hash(l.session))
	if e1 != nil || e2 != nil || l.tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	renderPasskeyStep(w, 403, l.session, step, message)
}

func (s *Server) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	l := s.openLogin(w, r)
	if l == nil {
		return
	}
	defer l.close()
	keys, err := listPasskeys(l.tx)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	step := passkeyStep(s.cfg.PasskeyMode, len(keys))
	if step == stepComplete {
		passkeyRequestError(w)
		return
	}
	if r.PostForm.Get("skip") == "1" {
		if step != stepEnrollOptional {
			l.reject(w, step, "A passkey is required to sign in.")
			return
		}
		s.issueCode(w, r, l.tx, l.session, l.values)
		return
	}
	credential := r.PostForm.Get("credential")
	want := "register"
	if step == stepAssert {
		want = "assert"
	}
	var session webauthn.SessionData
	if l.purpose != want || l.challenge <= time.Now().Unix() || credential == "" || len(credential) > maxCredentialForm || json.Unmarshal([]byte(l.data), &session) != nil {
		l.reject(w, step, "That passkey request expired. Try again.")
		return
	}
	// Spend the challenge before verifying: it is single use whatever the outcome.
	if _, err = l.tx.Exec("UPDATE oauth_passkey_flows SET purpose='ready',session_data='',challenge_expires=0 WHERE session_hash=?", hash(l.session)); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if want == "assert" {
		if message := s.verifyAssertion(l.tx, keys, session, credential); message != "" {
			l.reject(w, step, message)
			return
		}
	} else if message := s.verifyEnrollment(l.tx, keys, session, credential, r.PostForm.Get("name")); message != "" {
		l.reject(w, step, message)
		return
	}
	s.issueCode(w, r, l.tx, l.session, l.values)
}

// verifyAssertion checks a login assertion and records its counter; it returns a user message on failure.
func (s *Server) verifyAssertion(tx *sql.Tx, keys []storedPasskey, session webauthn.SessionData, credential string) string {
	const failed = "That passkey did not work. Try again."
	parsed, err := protocol.ParseCredentialRequestResponseBytes([]byte(credential))
	if err != nil {
		return failed
	}
	verified, err := s.wa.ValidateLogin(ownerOf(keys), session, parsed)
	if err != nil {
		return failed
	}
	for _, k := range keys {
		if !bytes.Equal(k.Credential.ID, verified.ID) {
			continue
		}
		if !policy.PasskeySignCountValid(k.SignCount, int64(verified.Authenticator.SignCount)) {
			return "That passkey looks copied, so the NAS refused it."
		}
		raw, err := json.Marshal(verified)
		if err != nil {
			return failed
		}
		if _, err = tx.Exec("UPDATE oauth_passkeys SET sign_count=?,last_used=?,credential=? WHERE id=?", int64(verified.Authenticator.SignCount), time.Now().Unix(), string(raw), k.ID); err != nil {
			return failed
		}
		return ""
	}
	return failed
}

// verifyEnrollment checks a registration and stores the new passkey; it returns a user message on failure.
func (s *Server) verifyEnrollment(tx dbQuerier, keys []storedPasskey, session webauthn.SessionData, credential, label string) string {
	const failed = "The NAS could not add that passkey. Try again."
	name, ok := passkeyName(label)
	if !ok {
		return "Use a name of up to 64 characters."
	}
	if !policy.CanRegisterPasskey(len(keys), passkeyCapacity) {
		return "The NAS already holds the most passkeys it allows."
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes([]byte(credential))
	if err != nil {
		return failed
	}
	created, err := s.wa.CreateCredential(ownerOf(keys), session, parsed)
	if err != nil {
		return failed
	}
	if err = insertPasskey(tx, name, created); err != nil {
		return failed
	}
	return ""
}

var passkeyErrorPage = template.Must(template.New("passkey-error").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign-in expired</title></head><body><h1>This sign-in has expired</h1><p>The NAS could not match this step to a sign-in in progress. Sign-ins last 5 minutes. Close this tab and start again from Claude, ChatGPT or the NAS page you were opening.</p></body></html>`))

// passkeyRequestError answers a browser that posted a passkey step the NAS cannot match
// to a live sign-in. People see these pages, so the answer is a page, not JSON.
func passkeyRequestError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", passkeyCSP)
	w.WriteHeader(http.StatusBadRequest)
	passkeyErrorPage.Execute(w, nil)
}
