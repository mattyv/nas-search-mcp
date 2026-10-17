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
	var approval *upgradeApproval
	if directory := os.Getenv("NAS_UPGRADE_DIR"); directory != "" {
		approval = newUpgradeApproval(ts.URL, directory)
	}
	handler = buildPublicHandler(buildMCPServer(a, approval), auth, a, approval)
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
	// Anonymous traffic, including plausible but forged bearers, cannot spend
	// authenticated MCP capacity. A header alone must never select that budget.
	forgedLimited := false
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		// The base case exhausts anonymous OAuth capacity; the upgrade case
		// still needs a fresh browser sign-in after its authenticated MCP call.
		if os.Getenv("NAS_UPGRADE_DIR") == "" {
			handler.ServeHTTP(w, httptest.NewRequest("GET", ts.URL+"/unknown", nil))
		}
		r := httptest.NewRequest("POST", ts.URL+"/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 43))
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code == 429 {
			forgedLimited = true
		} else if w.Code != 401 {
			t.Fatalf("forged token accepted: %d", w.Code)
		}
	}
	if !forgedLimited {
		t.Fatal("forged bearer requests were not bounded")
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
	expectedTools := 8
	if os.Getenv("NAS_UPGRADE_DIR") != "" {
		expectedTools = 10
	}
	if e != nil || len(tools.Tools) != expectedTools {
		t.Fatal("public tool list failed", e)
	}
	result, e := sessionMCP.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "harmless"}})
	if e != nil || result.IsError {
		t.Fatal("public search failed", e)
	}
	for _, call := range []mcp.CallToolParams{
		{Name: "index_status", Arguments: map[string]any{}},
		{Name: "list_folders", Arguments: map[string]any{}},
		{Name: "browse", Arguments: map[string]any{"prefix": "Documents"}},
		{Name: "find_files", Arguments: map[string]any{"query": "doc.txt"}},
		{Name: "read_file", Arguments: map[string]any{"path": "Documents/doc.txt"}},
	} {
		out, err := sessionMCP.CallTool(ctx, &call)
		if err != nil || out.IsError {
			t.Fatalf("public %s failed after startup: %v", call.Name, err)
		}
	}
	if os.Getenv("NAS_UPGRADE_DIR") != "" {
		upgraded, err := sessionMCP.CallTool(ctx, &mcp.CallToolParams{Name: "upgrade_from_main", Arguments: map[string]any{}})
		if err != nil || upgraded.IsError {
			t.Fatal("OAuth upgrade failed", err, upgraded)
		}
		upgradeContent, ok := upgraded.StructuredContent.(map[string]any)
		if !ok || upgradeContent["state"] != "approval_required" {
			t.Fatalf("public upgrade must wait for owner approval: %v", upgraded.StructuredContent)
		}
		if _, err := os.Stat(filepath.Join(os.Getenv("NAS_UPGRADE_DIR"), "request.json")); !os.IsNotExist(err) {
			t.Fatalf("upgrade queued before owner approval: %v", err)
		}
		approvalURL, ok := upgradeContent["approval_url"].(string)
		if !ok || !strings.HasPrefix(approvalURL, ts.URL+"/upgrade/approve?request=") {
			t.Fatalf("invalid approval destination: %v", upgradeContent)
		}
		browser := browserOAuthClient(t, ts.Client(), ts.URL, approvalURL, "test-only-password-123")
		page, err := browser.Get(approvalURL)
		if err != nil {
			t.Fatal(err)
		}
		page.Body.Close()
		if page.StatusCode != 200 || len(page.Cookies()) != 1 {
			t.Fatalf("approval page unavailable: %d", page.StatusCode)
		}
		postApproval := func(csrf bool) int {
			t.Helper()
			form := url.Values{"consent": {"approve"}}
			if csrf {
				form.Set("csrf", page.Cookies()[0].Value)
			}
			req, _ := http.NewRequest(http.MethodPost, approvalURL, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", ts.URL)
			response, err := browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			return response.StatusCode
		}
		if got := postApproval(false); got != http.StatusForbidden {
			t.Fatalf("approval without CSRF proof: %d", got)
		}

		if _, err := os.Stat(filepath.Join(os.Getenv("NAS_UPGRADE_DIR"), "request.json")); !os.IsNotExist(err) {
			t.Fatalf("upgrade queued after rejected approval: %v", err)
		}
		if got := postApproval(true); got != http.StatusOK {
			t.Fatalf("owner approval failed: %d", got)
		}
		if _, err := os.Stat(filepath.Join(os.Getenv("NAS_UPGRADE_DIR"), "request.json")); err != nil {
			t.Fatalf("approved upgrade was not queued: %v", err)
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
