package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"golang.org/x/crypto/bcrypt"
)

func uploadRequest(t *testing.T, target, origin, csrf, name, contents string, extra bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("csrf", csrf); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if extra {
		if err := form.WriteField("extra", "unexpected"); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, target, &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", origin)
	return r
}

func TestUploadPageRequiresOAuth(t *testing.T) {
	page := newUploadPage("https://nas.test", filepath.Join(t.TempDir(), "uploads"))
	if _, err := page.prepare(context.Background()); err == nil {
		t.Fatal("unauthenticated caller prepared upload")
	}
	if len(page.pending) != 0 {
		t.Fatal("unauthenticated caller created pending upload")
	}
}

func TestUploadPageBoundary(t *testing.T) {
	const issuer = "http://127.0.0.1:8080"
	for _, tc := range []struct {
		name     string
		origin   string
		csrf     bool
		extra    bool
		filename string
		want     int
	}{
		{"valid", issuer, true, false, "notes.md", 200},
		{"photo", issuer, true, false, "photo.jpg", 200},
		{"duplicate", issuer, true, false, "notes.md", 409},
		{"missing origin", "", true, false, "notes.md", 403},
		{"null origin", "null", true, false, "notes.md", 403},
		{"wrong origin", "https://evil.test", true, false, "notes.md", 403},
		{"missing csrf", issuer, false, false, "notes.md", 403},
		{"extra part", issuer, true, true, "notes.md", 400},
		{"unsafe filename", issuer, true, false, "../bad.md", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "uploads")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			hash, err := bcrypt.GenerateFromPassword([]byte("owner-password"), 10)
			if err != nil {
				t.Fatal(err)
			}
			auth, err := oauth.New(oauth.Config{Issuer: issuer, DatabasePath: filepath.Join(t.TempDir(), "oauth.db"), PasswordHash: string(hash), ClientID: "claude"})
			if err != nil {
				t.Fatal(err)
			}
			defer auth.Close()
			page := newUploadPage(issuer, dir)
			var queued string
			page.onSaved = func(name string) bool { queued = name; return true }
			nonce := strings.Repeat("n", 43)
			page.pending[sha256.Sum256([]byte(nonce))] = &pendingUpload{expires: time.Now().Add(time.Minute)}
			target := issuer + "/upload/" + nonce
			mux := http.NewServeMux()
			mux.Handle("/upload/", hardenUploads(auth.BrowserActionHandler(page, page.known), log.New(io.Discard, "", 0)))
			mux.Handle("/", auth.Handler())
			jar, _ := cookiejar.New(nil)
			request := func(r *http.Request) *httptest.ResponseRecorder {
				t.Helper()
				for _, c := range jar.Cookies(r.URL) {
					r.AddCookie(c)
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				jar.SetCookies(r.URL, w.Result().Cookies())
				return w
			}
			if page.known(httptest.NewRequest("GET", target+"?x=1", nil)) {
				t.Fatal("query accepted")
			}
			start := request(httptest.NewRequest("GET", target, nil))
			if start.Code != 303 {
				t.Fatalf("browser sign-in did not begin: %d", start.Code)
			}
			login := request(httptest.NewRequest("GET", start.Header().Get("Location"), nil))
			if login.Code != 200 {
				t.Fatalf("login: %d", login.Code)
			}
			var session string
			for _, c := range login.Result().Cookies() {
				if strings.HasPrefix(c.Name, "nas_oauth_") {
					session = c.Value
				}
			}
			values := url.Values{"session": {session}, "password": {"owner-password"}, "consent": {"allow"}}
			consent := httptest.NewRequest("POST", issuer+"/authorize", strings.NewReader(values.Encode()))
			consent.Header.Set("Origin", issuer)
			consent.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			callback := request(consent)
			if callback.Code != 303 {
				t.Fatalf("consent: %d", callback.Code)
			}
			done := request(httptest.NewRequest("GET", callback.Header().Get("Location"), nil))
			if done.Code != 303 {
				t.Fatalf("callback: %d", done.Code)
			}
			form := request(httptest.NewRequest("GET", target, nil))
			if form.Code != 200 || !strings.Contains(form.Body.String(), "multipart/form-data") {
				t.Fatalf("form: %d", form.Code)
			}
			if !strings.Contains(form.Body.String(), "any file type") {
				t.Fatalf("upload page does not explain supported files: %s", form.Body.String())
			}
			if policy := form.Header().Get("Referrer-Policy"); policy != "strict-origin" {
				t.Fatalf("upload form must send only its origin on same-origin POST, got %q", policy)
			}
			var csrf string
			for _, c := range form.Result().Cookies() {
				if strings.HasPrefix(c.Name, "nas_upload_") {
					csrf = c.Value
				}
			}
			if csrf == "" {
				t.Fatal("missing CSRF cookie")
			}
			if !tc.csrf {
				csrf = ""
			}
			contents := "hello\n"
			if tc.name == "valid" {
				contents = strings.Repeat("a", 70<<10)
			}
			if tc.name == "duplicate" {
				if err := os.WriteFile(filepath.Join(dir, tc.filename), []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			submit := request(uploadRequest(t, target, tc.origin, csrf, tc.filename, contents, tc.extra))
			if submit.Code != tc.want {
				t.Fatalf("submit: got %d, want %d: %s", submit.Code, tc.want, submit.Body.String())
			}
			content, err := os.ReadFile(filepath.Join(dir, tc.filename))
			if tc.name == "duplicate" {
				if string(content) != "original" || !strings.Contains(submit.Body.String(), "already exists") || queued != "" {
					t.Fatalf("duplicate replaced file or gave unclear result: %q, %q, queued=%q", content, submit.Body.String(), queued)
				}
				return
			}
			if tc.want == 200 {
				if err != nil || string(content) != contents {
					t.Fatalf("upload missing: %q, %v", content, err)
				}
				if queued != tc.filename {
					t.Fatalf("targeted indexing not queued: %q", queued)
				}
				replay := request(uploadRequest(t, target, issuer, csrf, "again.md", "hello", false))
				if replay.Code != 404 {
					t.Fatalf("replay: %d", replay.Code)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("rejected upload saved file: %v", err)
			} else if queued != "" {
				t.Fatalf("rejected upload queued for indexing: %q", queued)
			}
		})
	}
}
