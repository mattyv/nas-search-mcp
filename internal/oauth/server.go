// Package oauth implements the single-user, pre-registered OAuth boundary.
// Its HTTP, cryptographic, and SQLite adapters are not formally verified.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mattyv/nas-search-mcp/internal/policy"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sys/unix"
	"golang.org/x/time/rate"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const scope = "files:read"

type authenticatedKey struct{}

// AuthenticatedKey identifies a verified bearer without exposing the credential.
func AuthenticatedKey(ctx context.Context) string {
	key, _ := ctx.Value(authenticatedKey{}).(string)
	return key
}

type Config struct {
	ServiceUpgrades                                                bool
	Issuer, DatabasePath, PasswordHash, ClientID, ClientSecretHash string
	RedirectURIs                                                   []string
}
type Server struct {
	cfg               Config
	db                *sql.DB
	mu                sync.Mutex
	limiter           *rate.Limiter
	validationGate    chan struct{}
	validationWaiters chan struct{}
}

func New(c Config) (*Server, error) {
	u, e := url.Parse(c.Issuer)
	if e != nil || u.Host == "" || u.Hostname() == "" || u.ForceQuery || strings.Contains(c.Issuer, "#") || !validPort(u) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname()))) {
		return nil, errors.New("OAuth issuer must be an HTTPS origin (HTTP loopback permitted)")
	}
	if c.DatabasePath == "" || c.ClientID == "" || c.ClientID == browserClient {
		return nil, errors.New("OAuth database and client ID required")
	}
	cost, e := bcrypt.Cost([]byte(c.PasswordHash))
	if e != nil || cost < 10 {
		return nil, errors.New("OAuth password must use bcrypt cost at least 10")
	}
	if c.ClientSecretHash != "" {
		if cost, e = bcrypt.Cost([]byte(c.ClientSecretHash)); e != nil || cost < 10 {
			return nil, errors.New("invalid client secret hash")
		}
	}
	c.RedirectURIs = append([]string{"https://claude.ai/api/mcp/auth_callback"}, c.RedirectURIs...)
	for _, redirect := range c.RedirectURIs {
		v, e := url.Parse(redirect)
		if e != nil || v.User != nil || v.Fragment != "" || (redirect != "https://claude.ai/api/mcp/auth_callback" && !(v.Scheme == "http" && loopback(v.Hostname()))) {
			return nil, errors.New("only exact Claude or explicit HTTP loopback redirects allowed")
		}
	}
	if e := privateDatabase(c.DatabasePath); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite3", sqliteURI(c.DatabasePath))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS oauth_grants(hash TEXT PRIMARY KEY, kind TEXT NOT NULL, client TEXT NOT NULL, redirect TEXT NOT NULL, challenge TEXT NOT NULL, resource TEXT NOT NULL, family TEXT NOT NULL, expires INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0); CREATE INDEX IF NOT EXISTS oauth_family ON oauth_grants(family);
 CREATE TRIGGER IF NOT EXISTS oauth_capacity BEFORE INSERT ON oauth_grants
 WHEN (SELECT count(*) FROM oauth_grants)>=4096 OR (NEW.kind='login' AND (SELECT count(*) FROM oauth_grants WHERE kind='login')>=256)
 BEGIN SELECT RAISE(ABORT,'oauth_capacity'); END;
 PRAGMA max_page_count=4096;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Server{cfg: c, db: db, limiter: rate.NewLimiter(rate.Every(time.Second), 10), validationGate: make(chan struct{}, 2), validationWaiters: make(chan struct{}, 60)}, nil
}
func loopback(h string) bool {
	return h == "localhost" || (net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback())
}
func (s *Server) Close() error     { return s.db.Close() }
func (s *Server) RevokeAll() error { _, e := s.db.Exec("DELETE FROM oauth_grants"); return e }
func RevokeAll(path string) error {
	if e := privateDatabase(path); e != nil {
		return e
	}
	db, e := sql.Open("sqlite3", sqliteURI(path))
	if e != nil {
		return e
	}
	defer db.Close()
	_, e = db.Exec("DELETE FROM oauth_grants")
	return e
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}
func hash(v string) string         { b := sha256.Sum256([]byte(v)); return hex.EncodeToString(b[:]) }
func (s *Server) resource() string { return s.cfg.Issuer + "/mcp" }
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func oauthError(w http.ResponseWriter, status int, v string) {
	if status == 429 && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "1")
	}
	jsonReply(w, status, map[string]string{"error": v})
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		methods := []string{"none"}
		if s.cfg.ClientSecretHash != "" {
			methods = []string{"client_secret_basic", "client_secret_post", "none"}
		}
		jsonReply(w, 200, map[string]any{"issuer": s.cfg.Issuer, "authorization_endpoint": s.cfg.Issuer + "/authorize", "token_endpoint": s.cfg.Issuer + "/token", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": methods, "scopes_supported": []string{scope, browserScope}})
	})
	metadata := func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]any{"resource": s.resource(), "authorization_servers": []string{s.cfg.Issuer}, "scopes_supported": []string{scope}, "bearer_methods_supported": []string{"header"}})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", metadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", metadata)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /authorize", s.consent)
	mux.HandleFunc("POST /token", s.token)
	mux.HandleFunc("GET "+browserCallbackPath, s.browserCallback)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.URL.Path == "/authorize" || r.URL.Path == "/token" || r.URL.Path == browserCallbackPath {
			if _, err := s.db.Exec("DELETE FROM oauth_grants WHERE expires <= ?", time.Now().Unix()); err != nil {
				oauthError(w, 500, "server_error")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) validAuthorize(v url.Values) bool {
	for key, values := range v {
		switch key {
		case "client_id", "redirect_uri", "response_type", "code_challenge_method", "code_challenge", "resource", "scope", "state":
		default:
			return false
		}
		if len(values) != 1 {
			return false
		}
	}
	redirectOK := false
	client := v.Get("client_id")
	wantedScope := scope
	if client == browserClient {
		redirectOK = v.Get("redirect_uri") == s.cfg.Issuer+browserCallbackPath
		wantedScope = browserScope
	} else if client == s.cfg.ClientID {
		for _, redirect := range s.cfg.RedirectURIs {
			redirectOK = redirectOK || v.Get("redirect_uri") == redirect
		}
	}
	challenge, e := base64.RawURLEncoding.DecodeString(v.Get("code_challenge"))
	return redirectOK && v.Get("response_type") == "code" && v.Get("code_challenge_method") == "S256" && e == nil && len(challenge) == 32 && v.Get("resource") == s.clientResource(client) && (v.Get("scope") == "" || v.Get("scope") == wantedScope) && len(v.Get("state")) <= 1024
}

var page = template.Must(template.New("login").Parse(`<!doctype html><html><head><title>Authorize NAS search</title></head><body><h1>{{if .Browser}}Sign in to continue{{else if .ServiceUpgrades}}Allow NAS access and service upgrades?{{else}}Allow read-only NAS access?{{end}}</h1>{{if .Browser}}<p>Sign in again to open the requested file or continue to upgrade approval. This sign-in is limited to this browser action.</p>{{else}}<p>This connector can search and read the folders enabled by the NAS owner.</p>{{end}}{{if .ServiceUpgrades}}<p>This connector can also upgrade the search service from main when requested. Original files remain read-only.</p>{{end}}<p>Send authorization to {{.Destination}}.</p>{{if .Loopback}}<p>Warning: another local program could receive this authorization.</p>{{end}}<form method="post" action="/authorize"><input type="hidden" name="session" value="{{.Session}}"><label>NAS connector password <input name="password" type="password" autocomplete="current-password" required maxlength="1024"></label><button name="consent" value="allow">{{if .Browser}}Continue{{else if .ServiceUpgrades}}Allow access and upgrades{{else}}Allow read-only access{{end}}</button></form></body></html>`))

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	parameters, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil || !s.validAuthorize(parameters) {
		oauthError(w, 400, "invalid_request")
		return
	}
	session, e := randomToken()
	if e != nil {
		oauthError(w, 500, "server_error")
		return
	}
	_, e = s.db.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'login', ?, ?, ?, ?, ?, ?)", hash(session), parameters.Get("client_id"), parameters.Encode(), "", parameters.Get("resource"), "", time.Now().Add(5*time.Minute).Unix())
	if e != nil {
		grantError(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "nas_oauth", Value: session, Path: "/authorize", HttpOnly: true, Secure: strings.HasPrefix(s.cfg.Issuer, "https:"), SameSite: http.SameSiteStrictMode, MaxAge: 300})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "strict-origin")
	destination, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
	page.Execute(w, struct {
		Session, Destination               string
		Loopback, ServiceUpgrades, Browser bool
	}{session, destination.Host, loopback(destination.Hostname()), s.cfg.ServiceUpgrades && parameters.Get("client_id") != browserClient, parameters.Get("client_id") == browserClient})
}
func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	if r.ParseForm() != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	cookie, e := r.Cookie("nas_oauth")
	session := r.PostForm.Get("session")
	if e != nil || len(session) != 43 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(session)) != 1 || r.PostForm.Get("consent") != "allow" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.cfg.Issuer) {
		oauthError(w, 400, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	var raw string
	var failures int
	e = tx.QueryRow("SELECT redirect,used FROM oauth_grants WHERE hash=? AND kind='login' AND expires>?", hash(session), time.Now().Unix()).Scan(&raw, &failures)
	if e != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	// The existing bounded, expiring login row also holds this browser's attempt count.
	// An anonymous browser cannot lock other browsers out of the owner's account.
	if !policy.LoginAttemptAllowed(failures) {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	v, e := url.ParseQuery(raw)
	if e != nil || !s.validAuthorize(v) {
		oauthError(w, 400, "invalid_request")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(s.cfg.PasswordHash), []byte(r.PostForm.Get("password"))) != nil {
		if _, e := tx.Exec("UPDATE oauth_grants SET used=? WHERE hash=? AND kind='login'", policy.FailedLoginCount(failures), hash(session)); e != nil || tx.Commit() != nil {
			oauthError(w, 500, "server_error")
			return
		}
		oauthError(w, 403, "access_denied")
		return
	}
	code, e := randomToken()
	if e != nil {
		oauthError(w, 500, "server_error")
		return
	}
	_, e = tx.Exec("DELETE FROM oauth_grants WHERE hash=?", hash(session))
	if e == nil {
		_, e = tx.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'code', ?, ?, ?, ?, ?, ?)", hash(code), v.Get("client_id"), v.Get("redirect_uri"), v.Get("code_challenge"), v.Get("resource"), hash(code), time.Now().Add(time.Minute).Unix())
	}
	if e != nil {
		grantError(w, e)
		return
	}
	if tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	target, _ := url.Parse(v.Get("redirect_uri"))
	q := target.Query()
	q.Set("code", code)
	if v.Get("state") != "" {
		q.Set("state", v.Get("state"))
	}
	target.RawQuery = q.Encode()
	http.SetCookie(w, &http.Cookie{Name: "nas_oauth", Value: "", Path: "/authorize", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(s.cfg.Issuer, "https:"), SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, target.String(), 303)
}
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow() {
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	if r.ParseForm() != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	v := r.PostForm
	for _, values := range v {
		if len(values) != 1 {
			oauthError(w, 400, "invalid_request")
			return
		}
	}
	client, secret, basic := r.BasicAuth()
	if !basic {
		client = v.Get("client_id")
		secret = v.Get("client_secret")
	}
	validClient := client == browserClient && secret == ""
	if client == s.cfg.ClientID {
		validClient = (s.cfg.ClientSecretHash == "" && secret == "") || (s.cfg.ClientSecretHash != "" && bcrypt.CompareHashAndPassword([]byte(s.cfg.ClientSecretHash), []byte(secret)) == nil)
	}
	if !validClient {
		oauthError(w, 401, "invalid_client")
		return
	}
	grant, status, failure := s.exchange(client, v)
	if status != 200 {
		oauthError(w, status, failure)
		return
	}
	jsonReply(w, 200, grant)
}

type tokenGrant struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// The token endpoint and the first-party callback redeem codes through the same checks.
func (s *Server) exchange(client string, v url.Values) (tokenGrant, int, string) {
	if v.Get("resource") != s.clientResource(client) {
		return tokenGrant{}, 400, "invalid_target"
	}
	kind, raw := "code", v.Get("code")
	if v.Get("grant_type") == "refresh_token" && client != browserClient {
		kind = "refresh"
		raw = v.Get("refresh_token")
	} else if v.Get("grant_type") != "authorization_code" {
		return tokenGrant{}, 400, "unsupported_grant_type"
	}
	if len(raw) != 43 {
		return tokenGrant{}, 400, "invalid_grant"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return tokenGrant{}, 500, "server_error"
	}
	defer tx.Rollback()
	var redirect, challenge, resource, family string
	var expires int64
	var used int
	e = tx.QueryRow("SELECT redirect,challenge,resource,family,expires,used FROM oauth_grants WHERE hash=? AND kind=? AND client=?", hash(raw), kind, client).Scan(&redirect, &challenge, &resource, &family, &expires, &used)
	if e != nil {
		return tokenGrant{}, 400, "invalid_grant"
	}
	if used != 0 {
		_, e = tx.Exec("DELETE FROM oauth_grants WHERE family=?", family)
		if e != nil || tx.Commit() != nil {
			return tokenGrant{}, 500, "server_error"
		}
		return tokenGrant{}, 400, "invalid_grant"
	}
	if expires <= time.Now().Unix() || resource != s.clientResource(client) {
		return tokenGrant{}, 400, "invalid_grant"
	}
	if kind == "code" {
		verifier := v.Get("code_verifier")
		if !validVerifier(verifier) || v.Get("redirect_uri") != redirect {
			return tokenGrant{}, 400, "invalid_grant"
		}
		sum := sha256.Sum256([]byte(verifier))
		if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) != 1 {
			return tokenGrant{}, 400, "invalid_grant"
		}
		expires = time.Now().Add(7 * 24 * time.Hour).Unix()
	}
	access, e := randomToken()
	if e != nil {
		return tokenGrant{}, 500, "server_error"
	}
	refresh := ""
	if client != browserClient {
		refresh, e = randomToken()
		if e != nil {
			return tokenGrant{}, 500, "server_error"
		}
	} else {
		expires = time.Now().Add(10 * time.Minute).Unix()
	}
	_, e = tx.Exec("UPDATE oauth_grants SET used=1,expires=? WHERE hash=?", expires, hash(raw))
	if e == nil {
		_, e = tx.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'access', ?, '', '', ?, ?, ?)", hash(access), client, resource, family, time.Now().Add(10*time.Minute).Unix())
	}
	if e == nil && refresh != "" {
		_, e = tx.Exec("INSERT INTO oauth_grants(hash,kind,client,redirect,challenge,resource,family,expires) VALUES(?, 'refresh', ?, '', '', ?, ?, ?)", hash(refresh), client, resource, family, expires)
	}
	if e != nil {
		if strings.Contains(e.Error(), "oauth_capacity") {
			return tokenGrant{}, 503, "temporarily_unavailable"
		}
		return tokenGrant{}, 500, "server_error"
	}
	if tx.Commit() != nil {
		return tokenGrant{}, 500, "server_error"
	}
	issuedScope := scope
	if client == browserClient {
		issuedScope = browserScope
	}
	return tokenGrant{AccessToken: access, TokenType: "Bearer", ExpiresIn: 600, RefreshToken: refresh, Scope: issuedScope}, 200, ""
}
func (s *Server) Protect(next http.Handler) http.Handler {
	misses := rate.NewLimiter(rate.Every(time.Second), 30)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		parts := strings.Fields(header)
		valid := false
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && len(parts[1]) == 43 {
			// Opaque credentials require a database lookup. Bound active lookups
			// instead of allowing unauthenticated requests to queue without limit.
			acquired := false
			select {
			case s.validationGate <- struct{}{}:
				acquired = true
			default:
				select {
				case s.validationWaiters <- struct{}{}:
					timer := time.NewTimer(time.Second)
					select {
					case s.validationGate <- struct{}{}:
						acquired = true
					case <-r.Context().Done():
					case <-timer.C:
					}
					timer.Stop()
					<-s.validationWaiters
				default:
				}
			}
			if acquired {
				var n int
				e := s.db.QueryRowContext(r.Context(), "SELECT count(*) FROM oauth_grants WHERE hash=? AND kind='access' AND resource=? AND expires>? AND used=0", hash(parts[1]), s.resource(), time.Now().Unix()).Scan(&n)
				<-s.validationGate
				valid = e == nil && n == 1
			} else if r.Context().Err() == nil {
				log.Printf("public rejection reason=token_lookup_busy key=%s method=unread active=%d waiters=%d", hash(parts[1])[:12], len(s.validationGate), len(s.validationWaiters))
				w.Header().Set("Retry-After", "1")
				oauthError(w, 429, "temporarily_unavailable")
				return
			}
		}
		if !valid {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=%q, scope=%q", s.cfg.Issuer+"/.well-known/oauth-protected-resource/mcp", scope))
			w.Header().Set("Cache-Control", "no-store")
			if !misses.Allow() {
				key := "none"
				if len(parts) == 2 && len(parts[1]) == 43 {
					key = hash(parts[1])[:12]
				}
				log.Printf("public rejection reason=invalid_bearer_rate key=%s method=unread tokens=%.2f", key, misses.Tokens())
				w.Header().Set("Retry-After", "1")
				oauthError(w, 429, "temporarily_unavailable")
				return
			}
			oauthError(w, 401, "invalid_token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authenticatedKey{}, hash(parts[1]))))
	})
}

// The containing runtime directory must also be private to the service account.
func privateDatabase(path string) error {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("OAuth database must be a regular file")
	}
	return unix.Fchmod(fd, 0600)
}
func sqliteURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?_busy_timeout=5000&_foreign_keys=on"
}

func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func grantError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "oauth_capacity") {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	oauthError(w, 500, "server_error")
}

func validPort(u *url.URL) bool {
	port := u.Port()
	if port == "" {
		return !strings.HasSuffix(u.Host, ":")
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == port
}
