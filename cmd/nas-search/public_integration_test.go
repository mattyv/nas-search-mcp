package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/mattyv/nas-search-mcp/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPublicOAuthToolFlow(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "Documents"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "Documents", "doc.txt"), []byte("harmless search fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := app.New(root, data, "python3", "", "", false, false)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Store.Close()
	if e = a.ConfigureAccess([]string{"Documents"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Replace(store.File{Path: "Documents/doc.txt", Kind: "document"}, []store.Chunk{{Location: "text", Text: "harmless search fixture"}}, nil); e != nil {
		t.Fatal(e)
	}
	var handler http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer ts.Close()
	hash, e := passwordHash(strings.NewReader("test-only-password-123"))
	if e != nil {
		t.Fatal(e)
	}
	auth, e := oauth.New(oauth.Config{Issuer: ts.URL, DatabasePath: filepath.Join(data, "oauth.sqlite3"), PasswordHash: string(hash), ClientID: "nas-claude"})
	if e != nil {
		t.Fatal(e)
	}
	defer auth.Close()
	handler = buildPublicHandler(buildMCPServer(a), auth, a)
	probe, e := ts.Client().Get(ts.URL + "/mcp")
	if e != nil {
		t.Fatal(e)
	}
	probe.Body.Close()
	if probe.StatusCode != 401 || !strings.Contains(probe.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatal("public MCP did not challenge")
	}
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {"nas-claude"}, "response_type": {"code"}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}, "resource": {ts.URL + "/mcp"}, "state": {"test-state"}}
	login, e := ts.Client().Get(ts.URL + "/authorize?" + q.Encode())
	if e != nil {
		t.Fatal(e)
	}
	login.Body.Close()
	if login.StatusCode != 200 || len(login.Cookies()) == 0 {
		t.Fatal("login page unavailable")
	}
	if got := login.Header.Get("Referrer-Policy"); got != "strict-origin" {
		t.Fatalf("public login referrer policy: %q", got)
	}
	session := login.Cookies()[0].Value
	form := url.Values{"session": {session}, "password": {"test-only-password-123"}, "consent": {"allow"}}
	req, _ := http.NewRequest("POST", ts.URL+"/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	req.AddCookie(login.Cookies()[0])
	client := *ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	consent, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	consent.Body.Close()
	callback, e := url.Parse(consent.Header.Get("Location"))
	if e != nil || consent.StatusCode != 303 || callback.Query().Get("state") != "test-state" {
		t.Fatal("consent callback failed")
	}
	tokens := url.Values{"client_id": {"nas-claude"}, "grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "code_verifier": {verifier}, "resource": {ts.URL + "/mcp"}}
	exchange, e := ts.Client().PostForm(ts.URL+"/token", tokens)
	if e != nil {
		t.Fatal(e)
	}
	defer exchange.Body.Close()
	if exchange.StatusCode != 200 {
		t.Fatal("OAuth code exchange failed", exchange.StatusCode)
	}
	var grant struct {
		AccessToken string `json:"access_token"`
	}
	if e = json.NewDecoder(exchange.Body).Decode(&grant); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientMCP := mcp.NewClient(&mcp.Implementation{Name: "public-test", Version: "1"}, nil)
	sessionMCP, e := clientMCP.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: grant.AccessToken, base: ts.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer sessionMCP.Close()
	tools, e := sessionMCP.ListTools(ctx, nil)
	expectedTools := 7
	if os.Getenv("NAS_UPGRADE_DIR") != "" {
		expectedTools = 9
	}
	if e != nil || len(tools.Tools) != expectedTools {
		t.Fatal("public tool list failed", e)
	}
	result, e := sessionMCP.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "harmless"}})
	if e != nil || result.IsError {
		t.Fatal("public search failed", e)
	}
	if os.Getenv("NAS_UPGRADE_DIR") != "" {
		upgraded, err := sessionMCP.CallTool(ctx, &mcp.CallToolParams{Name: "upgrade_from_main", Arguments: map[string]any{}})
		if err != nil || upgraded.IsError {
			t.Fatal("OAuth upgrade failed", err, upgraded)
		}
		status, err := sessionMCP.CallTool(ctx, &mcp.CallToolParams{Name: "upgrade_status", Arguments: map[string]any{}})
		if err != nil || status.IsError {
			t.Fatal("OAuth upgrade status failed", err, status)
		}
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(encoded), "Documents/doc.txt") {
		t.Fatal("authorized search missed allowed fixture")
	}
}

func TestPublicOAuthUpgradeFlow(t *testing.T) {
	t.Setenv("NAS_UPGRADE_DIR", t.TempDir())
	TestPublicOAuthToolFlow(t)
}
