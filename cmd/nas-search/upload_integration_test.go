package main

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPublicUploadRouteRequiresFreshBrowserOAuth(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	a, err := app.New(root, data, "python3", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	issuer := "https://nas.example"
	hash, err := passwordHash(strings.NewReader("test-only-password-123"))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := oauth.New(oauth.Config{Issuer: issuer, DatabasePath: filepath.Join(data, "oauth.sqlite3"), PasswordHash: string(hash), ClientID: "nas-claude"})
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Close()
	page := newUploadPage(issuer, filepath.Join(t.TempDir(), "uploads"))
	nonce := strings.Repeat("n", 43)
	page.pending[sha256.Sum256([]byte(nonce))] = &pendingUpload{expires: time.Now().Add(time.Minute)}
	h := buildPublicHandler(buildMCPServer(a, nil, page), auth, a, nil, page)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", issuer+"/upload/"+nonce, nil))
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), issuer+"/authorize?") {
		t.Fatalf("upload did not start browser OAuth: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", issuer+"/upload/"+nonce, strings.NewReader("document")))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload reached write handler: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", issuer+"/upload/"+strings.Repeat("x", 43), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown upload link: %d", w.Code)
	}
}

func TestUploadToolOnlyWhenConfigured(t *testing.T) {
	a, err := app.New(t.TempDir(), t.TempDir(), "python3", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, test := range []struct {
		name    string
		page    *uploadPage
		want    bool
	}{
		{name: "disabled"},
		{name: "enabled", page: newUploadPage("https://nas.example", filepath.Join(t.TempDir(), "uploads")), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := buildMCPServer(a, nil, test.page)
			client := mcp.NewClient(&mcp.Implementation{Name: "upload-test", Version: "1"}, nil)
			serverTransport, clientTransport := mcp.NewInMemoryTransports()
			serverSession, err := server.Connect(context.Background(), serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			clientSession, err := client.Connect(context.Background(), clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer clientSession.Close()
			tools, err := clientSession.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, tool := range tools.Tools {
				found = found || tool.Name == "upload_document"
			}
			if found != test.want {
				t.Fatalf("upload tool available=%t, want %t", found, test.want)
			}
		})
	}
}
