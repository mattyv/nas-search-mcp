package oauth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

const dynamicClientPrefix = "nas-dcr-"
const registeredClientCapacity = 128
const pendingClientLifetime = 15 * time.Minute

const registrationSchema = `CREATE TABLE IF NOT EXISTS oauth_clients(
 client TEXT PRIMARY KEY, redirects TEXT NOT NULL, refresh INTEGER NOT NULL, expires INTEGER NOT NULL);
 CREATE TRIGGER IF NOT EXISTS oauth_client_capacity BEFORE INSERT ON oauth_clients
 WHEN (SELECT count(*) FROM oauth_clients)>=128
 BEGIN SELECT RAISE(ABORT,'oauth_client_capacity'); END;`

type registrationMetadata struct {
	RedirectURIs []string
	GrantTypes   []string
	Refresh      bool
}

type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

type rowQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

type registeredClient struct {
	RedirectURIs []string
	Refresh      bool
}

func dynamicClientID(client string) bool {
	return strings.HasPrefix(client, dynamicClientPrefix) && len(client) == len(dynamicClientPrefix)+43 && validVerifier(strings.TrimPrefix(client, dynamicClientPrefix))
}

func (s *Server) registeredClient(q rowQuerier, client string) (*registeredClient, error) {
	if !dynamicClientID(client) {
		return nil, nil
	}
	var raw string
	var refresh bool
	now := time.Now().Unix()
	err := q.QueryRow(`SELECT redirects,refresh FROM oauth_clients WHERE client=? AND
 (expires=0 OR expires>? OR EXISTS(SELECT 1 FROM oauth_grants WHERE client=oauth_clients.client
 AND kind IN ('code','access','refresh') AND used=0 AND expires>?))`, client, now, now).Scan(&raw, &refresh)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var redirects []string
	if err = json.Unmarshal([]byte(raw), &redirects); err != nil || len(redirects) < 1 || len(redirects) > 4 {
		return nil, errors.New("invalid registered client")
	}
	return &registeredClient{RedirectURIs: redirects, Refresh: refresh}, nil
}

func (s *Server) registrationRedirectAllowed(redirect string) bool {
	if len(redirect) == 0 || len(redirect) > 1024 || !utf8.ValidString(redirect) || strings.ContainsAny(redirect, "\\#") || strings.IndexFunc(redirect, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(redirect)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.EscapedPath() != u.Path || len(u.Path) > 512 || u.String() != redirect {
		return false
	}
	if redirect == "https://claude.ai/api/mcp/auth_callback" || validChatGPTRedirect(redirect) {
		return true
	}
	// An administrator may already have approved an exact legacy loopback URI.
	// Registration never broadens its host, port or path.
	for _, configured := range s.cfg.RedirectURIs {
		if redirect == configured {
			return true
		}
	}
	if u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.Port() == "" || !validPort(u) {
		return false
	}
	return u.Host == net.JoinHostPort(u.Hostname(), u.Port())
}

func metadataString(values map[string]json.RawMessage, key, fallback string) (string, bool) {
	raw, present := values[key]
	if !present {
		return fallback, true
	}
	raw = bytes.TrimSpace(raw)
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func metadataStrings(values map[string]json.RawMessage, key string, fallback []string) ([]string, bool) {
	raw, present := values[key]
	if !present {
		return fallback, true
	}
	raw = bytes.TrimSpace(raw)
	var value []string
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	return value, true
}

func (s *Server) validateRegistration(values map[string]json.RawMessage) (registrationMetadata, string) {
	redirects, redirectsType := metadataStrings(values, "redirect_uris", nil)
	redirectsOK := redirectsType
	seen := make(map[string]bool)
	for _, redirect := range redirects {
		if !s.registrationRedirectAllowed(redirect) || seen[redirect] {
			redirectsOK = false
		}
		seen[redirect] = true
	}
	auth, authType := metadataString(values, "token_endpoint_auth_method", "none")
	grants, grantsType := metadataStrings(values, "grant_types", []string{"authorization_code"})
	grantSeen := make(map[string]bool)
	grantsOK := grantsType && len(grants) >= 1 && len(grants) <= 2
	for _, grant := range grants {
		if (grant != "authorization_code" && grant != "refresh_token") || grantSeen[grant] {
			grantsOK = false
		}
		grantSeen[grant] = true
	}
	grantsOK = grantsOK && grantSeen["authorization_code"]
	responses, responseType := metadataStrings(values, "response_types", []string{"code"})
	responseOK := responseType && len(responses) == 1 && responses[0] == "code"
	wantedScope, scopeType := metadataString(values, "scope", scope)
	scopeOK := scopeType && (wantedScope == "" || wantedScope == scope)
	name, nameType := metadataString(values, "client_name", "")
	if !nameType || len(name) > 128 || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return registrationMetadata{}, "invalid_client_metadata"
	}
	// Unknown descriptive metadata is ignored, as RFC7591 permits. It is never
	// fetched, stored or rendered; it cannot select a client ID, grant or resource.
	if !policy.ValidOAuthRegistration(len(redirects), authType && auth == "none", grantsOK, responseOK, scopeOK, redirectsOK) {
		if !redirectsOK || len(redirects) < 1 || len(redirects) > 4 {
			return registrationMetadata{}, "invalid_redirect_uri"
		}
		return registrationMetadata{}, "invalid_client_metadata"
	}
	return registrationMetadata{RedirectURIs: redirects, GrantTypes: grants, Refresh: grantSeen["refresh_token"]}, ""
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.registrationLimiter.Allow() {
		w.Header().Set("Retry-After", "10")
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	decoder := json.NewDecoder(r.Body)
	var values map[string]json.RawMessage
	if err = decoder.Decode(&values); err != nil || values == nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			oauthError(w, 413, "invalid_client_metadata")
		} else {
			oauthError(w, 400, "invalid_client_metadata")
		}
		return
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			oauthError(w, 413, "invalid_client_metadata")
		} else {
			oauthError(w, 400, "invalid_client_metadata")
		}
		return
	}
	metadata, failure := s.validateRegistration(values)
	if failure != "" {
		oauthError(w, 400, failure)
		return
	}
	id, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	id = dynamicClientPrefix + id
	now := time.Now().Unix()
	raw, _ := json.Marshal(metadata.RedirectURIs)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM oauth_clients WHERE expires>0 AND expires<=? AND NOT EXISTS(
 SELECT 1 FROM oauth_grants WHERE client=oauth_clients.client AND kind IN ('code','access','refresh') AND used=0 AND expires>?)`, now, now)
	var count int
	if err == nil {
		err = tx.QueryRow("SELECT count(*) FROM oauth_clients").Scan(&count)
	}
	if err != nil || !policy.CanRegisterOAuthClient(count, registeredClientCapacity) {
		w.Header().Set("Retry-After", "60")
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	_, err = tx.Exec("INSERT INTO oauth_clients(client,redirects,refresh,expires) VALUES(?,?,?,?)", id, string(raw), metadata.Refresh, now+int64(pendingClientLifetime/time.Second))
	if err != nil || tx.Commit() != nil {
		w.Header().Set("Retry-After", "60")
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	jsonReply(w, 201, registrationResponse{ClientID: id, ClientIDIssuedAt: now,
		RedirectURIs: metadata.RedirectURIs, TokenEndpointAuthMethod: "none", GrantTypes: metadata.GrantTypes,
		ResponseTypes: []string{"code"}, Scope: scope})
}

func revokeDatabase(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM oauth_grants"); err != nil {
		return err
	}
	// The standalone command must also work against a pre-registration database.
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='oauth_clients'").Scan(&count); err != nil {
		return err
	}
	if count == 1 {
		if _, err = tx.Exec("DELETE FROM oauth_clients"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
