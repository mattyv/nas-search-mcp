package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func linkTestApp(t *testing.T, content []byte) *app.App {
	t.Helper()
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "Matthew"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "Matthew", "scan.pdf"), content, 0600); e != nil {
		t.Fatal(e)
	}
	a, e := app.New(root, t.TempDir(), "python3", "", "", false, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Store.Close() })
	if e = a.ConfigureAccess([]string{"Matthew"}, nil); e != nil {
		t.Fatal(e)
	}
	return a
}

func TestDownloadLinkToolOpensFileOverPrivateListener(t *testing.T) {
	content := []byte("%PDF original bytes")
	a := linkTestApp(t, content)
	var handler http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer ts.Close()
	if e := a.ConfigureLinks(ts.URL, 10*time.Minute); e != nil {
		t.Fatal(e)
	}
	token := "integration-test-token-at-least-24"
	handler = buildHTTPHandler(a, token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "link-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token, base: ts.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	listed := false
	for _, tool := range tools.Tools {
		if tool.Name == "download_link" {
			listed = strings.Contains(tool.Description, "clickable")
		}
	}
	if !listed {
		t.Fatal("download_link tool missing or undocumented")
	}
	for _, tool := range tools.Tools {
		if tool.Name == "fetch_file" && !strings.Contains(tool.Description, "download_link") {
			t.Fatal("fetch_file should point to download_link when links are enabled")
		}
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "download_link", Arguments: map[string]any{"path": "Matthew/scan.pdf"}})
	if e != nil || result.IsError {
		t.Fatal(result, e)
	}
	encoded, e := json.Marshal(result.StructuredContent)
	if e != nil {
		t.Fatal(e)
	}
	var link app.LinkResult
	if e = json.Unmarshal(encoded, &link); e != nil || !strings.HasPrefix(link.URL, ts.URL+"/download/") {
		t.Fatal(string(encoded), e)
	}
	text := ""
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "]("+link.URL+")") {
		t.Fatal("tool text lacks a clickable markdown link:", text)
	}
	// The link itself is the credential: no bearer token is sent.
	resp, e := http.Get(link.URL)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, content) {
		t.Fatal(resp.StatusCode, string(body))
	}
	denied, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "download_link", Arguments: map[string]any{"path": "../secret"}})
	if e != nil || !denied.IsError {
		t.Fatal("traversal produced a link", denied, e)
	}
}

func TestPublicDownloadsStreamBeyondMCPResponseCap(t *testing.T) {
	content := bytes.Repeat([]byte("p"), publicResponseLimit+4096)
	a := linkTestApp(t, content)
	var handler http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer ts.Close()
	hash, e := passwordHash(strings.NewReader("test-only-password-123"))
	if e != nil {
		t.Fatal(e)
	}
	auth, e := oauth.New(oauth.Config{Issuer: ts.URL, DatabasePath: filepath.Join(t.TempDir(), "oauth.sqlite3"), PasswordHash: string(hash), ClientID: "nas-claude"})
	if e != nil {
		t.Fatal(e)
	}
	defer auth.Close()
	if e = a.ConfigureLinks(ts.URL, 10*time.Minute); e != nil {
		t.Fatal(e)
	}
	handler = buildPublicHandler(buildMCPServer(a), auth, a)
	link, e := a.CreateLink("Matthew/scan.pdf")
	if e != nil {
		t.Fatal(e)
	}
	resp, e := http.Get(link.URL)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, content) {
		t.Fatal(resp.StatusCode, len(body))
	}
	probe, e := http.Get(ts.URL + "/mcp")
	if e != nil {
		t.Fatal(e)
	}
	probe.Body.Close()
	if probe.StatusCode != 401 {
		t.Fatal("MCP lost OAuth protection", probe.StatusCode)
	}
}

func TestDownloadRequestsAreRateLimitedAndUnlogged(t *testing.T) {
	var logs bytes.Buffer
	h := hardenDownloads(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), func(*http.Request) bool { return false }, log.New(&logs, "", 0))
	limited := false
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/download/secret-link-token/passport.pdf", nil))
		if w.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("downloads were not rate limited")
	}
	for _, secret := range []string{"secret-link-token", "passport"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("link details appeared in logs")
		}
	}
}

func TestInvalidDownloadRequestsCannotStarveValidLinks(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(206) })
	known := func(r *http.Request) bool { return strings.Contains(r.URL.Path, "valid-link") }
	h := hardenDownloads(ok, known, log.New(io.Discard, "", 0))
	for i := 0; i < 100; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://localhost/download/junk/x", nil))
	}
	// A PDF viewer issues many range requests for one document.
	for i := 0; i < 60; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/download/valid-link/scan.pdf", nil))
		if w.Code != 206 {
			t.Fatalf("valid range request %d refused: %d", i, w.Code)
		}
	}
}

func TestPanickingRequestsReleaseConcurrencySlots(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("panic") != "" {
			panic("handler failure")
		}
		w.WriteHeader(204)
	})
	quiet := log.New(io.Discard, "", 0)
	for name, h := range map[string]http.Handler{
		"download": hardenDownloads(panicking, func(*http.Request) bool { return true }, quiet),
		"public":   hardenPublic(panicking, quiet),
	} {
		for i := 0; i < 10; i++ {
			func() {
				defer func() { recover() }()
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://localhost/download/x/y?panic=1", nil))
			}()
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/download/x/y", nil))
		if w.Code != 204 {
			t.Fatalf("%s: slots leaked after panics: %d", name, w.Code)
		}
	}
}
