package oauth

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

const managerFlowLifetime = 10 * time.Minute

// PasskeysHandler serves the passkey management page at /passkeys behind a
// fresh OAuth sign-in (the same gate as browser downloads and approvals).
// Mount it at "/passkeys" and "/passkeys/". With passkeys off it answers 404.
func (s *Server) PasskeysHandler() http.Handler {
	gate := s.BrowserActionHandler(http.HandlerFunc(s.managePasskeys), func(*http.Request) bool { return true })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.wa == nil {
			http.NotFound(w, r)
			return
		}
		gate.ServeHTTP(w, r)
	})
}

var managerPage = template.Must(template.New("manager").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>NAS passkeys</title></head><body>
<h1>Passkeys</h1>
<p>{{if eq .Mode "required"}}Sign-in needs a password and a passkey.{{else}}Sign-in asks for a passkey once you add one.{{end}}</p>
{{if .Message}}<p role="alert">{{.Message}}</p>{{end}}
{{if .Confirm}}<h2>Remove "{{.Confirm.Name}}"?</h2><p>You will not be able to sign in with this passkey again.</p>
<form method="post" action="/passkeys/remove"><input type="hidden" name="id" value="{{.Confirm.ID}}"><input type="hidden" name="confirm" value="yes"><button type="submit">Yes, remove it</button></form>
<p><a href="/passkeys">Cancel</a></p>
{{else}}
{{if .Keys}}<table><tr><th>Name</th><th>Created</th><th>Last used</th><th></th></tr>
{{range .Keys}}<tr><td>{{.Name}}</td><td>{{.CreatedText}}</td><td>{{.LastUsedText}}</td><td>
<form method="post" action="/passkeys/rename"><input type="hidden" name="id" value="{{.ID}}"><input name="name" value="{{.Name}}" maxlength="64" aria-label="New name"><button type="submit">Rename</button></form>
<form method="post" action="/passkeys/remove"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">Remove</button></form></td></tr>{{end}}</table>
{{else}}<p>No passkeys yet.</p>{{end}}
{{if .CanAdd}}<h2>Add a passkey</h2><p>Use your iPhone, Mac or security key. On Linux, Chrome shows a QR code: scan it with your phone.</p>
<form method="post" action="/passkeys/add" data-passkey="create" data-options="/passkeys/options"><input type="hidden" name="credential" value=""><label>Name <input name="name" value="My passkey" maxlength="64"></label> <button type="submit">Add a passkey</button></form>
<p id="passkey-status" role="status"></p><noscript><p>Passkeys need JavaScript. Turn it on for this page.</p></noscript>
<script src="/authorize/passkey.js"></script>
{{else}}<p>The NAS holds the most passkeys it allows. Remove one to add another.</p>{{end}}
{{end}}
</body></html>`))

type managerView struct {
	Mode, Message string
	Keys          []storedPasskey
	CanAdd        bool
	Confirm       *storedPasskey
}

func (s *Server) renderManager(w http.ResponseWriter, status int, message string, confirm *storedPasskey) {
	keys, err := listPasskeys(s.db)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", passkeyCSP)
	// Pages with forms must let the browser send their origin on same-site requests:
	// under "no-referrer" a form submission carries Origin "null" and is refused.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(status)
	managerPage.Execute(w, managerView{Mode: s.cfg.PasskeyMode, Message: message, Keys: keys, CanAdd: policy.CanRegisterPasskey(len(keys), passkeyCapacity), Confirm: confirm})
}

func (s *Server) managePasskeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", passkeyCSP)
	// Pages with forms must let the browser send their origin on same-site requests:
	// under "no-referrer" a form submission carries Origin "null" and is refused.
	w.Header().Set("Referrer-Policy", "same-origin")
	posts := map[string]func(http.ResponseWriter, *http.Request){
		"/passkeys/options": s.managerOptions, "/passkeys/add": s.managerAdd,
		"/passkeys/rename": s.managerRename, "/passkeys/remove": s.managerRemove,
	}
	if r.URL.Path == "/passkeys" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.renderManager(w, 200, "", nil)
		return
	}
	handler := posts[r.URL.Path]
	if handler == nil || r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	if r.Header.Get("Origin") != s.cfg.Issuer || r.ParseForm() != nil {
		passkeyRequestError(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Exec("DELETE FROM oauth_passkey_flows WHERE expires <= ?", time.Now().Unix())
	handler(w, r)
}

func managerFlowKey(r *http.Request) string { return hash("manager:" + BrowserActionKey(r.Context())) }

func (s *Server) managerOptions(w http.ResponseWriter, r *http.Request) {
	keys, err := listPasskeys(s.db)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if !policy.CanRegisterPasskey(len(keys), passkeyCapacity) {
		oauthError(w, 409, "passkey_limit")
		return
	}
	creation, session, err := s.beginEnrollment(ownerOf(keys))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	data, err := json.Marshal(session)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	now := time.Now()
	if _, err = s.db.Exec(`INSERT INTO oauth_passkey_flows(session_hash,purpose,session_data,challenge_expires,expires) VALUES(?, 'register', ?, ?, ?)
 ON CONFLICT(session_hash) DO UPDATE SET purpose='register',session_data=excluded.session_data,challenge_expires=excluded.challenge_expires,expires=excluded.expires`,
		managerFlowKey(r), string(data), now.Add(challengeLifetime).Unix(), now.Add(managerFlowLifetime).Unix()); err != nil {
		grantError(w, err)
		return
	}
	jsonReply(w, 200, creation)
}

func (s *Server) managerAdd(w http.ResponseWriter, r *http.Request) {
	fail := func(message string) { s.renderManager(w, http.StatusForbidden, message, nil) }
	var raw string
	var expires int64
	err := s.db.QueryRow("SELECT session_data,challenge_expires FROM oauth_passkey_flows WHERE session_hash=? AND purpose='register'", managerFlowKey(r)).Scan(&raw, &expires)
	credential := r.PostForm.Get("credential")
	var session webauthn.SessionData
	if err != nil || expires <= time.Now().Unix() || credential == "" || len(credential) > maxCredentialForm || json.Unmarshal([]byte(raw), &session) != nil {
		fail("That passkey request expired. Try again.")
		return
	}
	// The challenge is single use whatever the outcome.
	s.db.Exec("DELETE FROM oauth_passkey_flows WHERE session_hash=?", managerFlowKey(r))
	keys, err := listPasskeys(s.db)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if message := s.verifyEnrollment(s.db, keys, session, credential, r.PostForm.Get("name")); message != "" {
		fail(message)
		return
	}
	s.renderManager(w, 200, "Passkey added.", nil)
}

func (s *Server) findPasskey(r *http.Request) (*storedPasskey, []storedPasskey, error) {
	keys, err := listPasskeys(s.db)
	if err != nil {
		return nil, nil, err
	}
	id, parseErr := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)
	if parseErr == nil {
		for i := range keys {
			if keys[i].ID == id {
				return &keys[i], keys, nil
			}
		}
	}
	return nil, keys, nil
}

func (s *Server) managerRename(w http.ResponseWriter, r *http.Request) {
	key, _, err := s.findPasskey(r)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	name, ok := passkeyName(r.PostForm.Get("name"))
	switch {
	case key == nil:
		s.renderManager(w, 404, "That passkey no longer exists.", nil)
	case !ok:
		s.renderManager(w, 400, "Use a name of up to 64 characters.", nil)
	default:
		if _, err = s.db.Exec("UPDATE oauth_passkeys SET name=? WHERE id=?", name, key.ID); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		s.renderManager(w, 200, "Passkey renamed.", nil)
	}
}

func (s *Server) managerRemove(w http.ResponseWriter, r *http.Request) {
	key, keys, err := s.findPasskey(r)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	switch {
	case key == nil:
		s.renderManager(w, 404, "That passkey no longer exists.", nil)
	case !passkeyRemovalAllowed(s.cfg.PasskeyMode, len(keys)):
		s.renderManager(w, 409, "Sign-in needs at least one passkey. Add another before removing this one.", nil)
	case r.PostForm.Get("confirm") != "yes":
		s.renderManager(w, 200, "", key)
	default:
		if _, err = s.db.Exec("DELETE FROM oauth_passkeys WHERE id=?", key.ID); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		s.renderManager(w, 200, "Passkey removed.", nil)
	}
}
