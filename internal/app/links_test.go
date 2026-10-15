package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func linkApp(t *testing.T) *App {
	t.Helper()
	a := &App{Root: t.TempDir()}
	if err := a.ConfigureLinks("https://nas.example", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	return a
}

func open(t *testing.T, a *App, link, method string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://local"+u.EscapedPath(), nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	a.ServeLink(w, r)
	return w
}

func TestLinkOpensOriginalInBrowser(t *testing.T) {
	a := linkApp(t)
	content := []byte("%PDF-1.4\x00\xffbinary original")
	if err := os.MkdirAll(filepath.Join(a.Root, "Week 10"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Root, "Week 10/Melody #1.pdf"), content, 0600); err != nil {
		t.Fatal(err)
	}
	link, err := a.CreateLink("Week 10/Melody #1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link.URL, "https://nas.example/download/") || !strings.HasSuffix(link.URL, "/Melody%20%231.pdf") {
		t.Fatal(link.URL)
	}
	if link.MIME != "application/pdf" || !link.OpensInBrowser || link.Size != int64(len(content)) || link.ExpiresAt == "" || link.Name != "Melody #1.pdf" {
		t.Fatal(link)
	}
	w := open(t, a, link.URL, "GET", nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) {
		t.Fatal(w.Code, w.Body.String())
	}
	h := w.Header()
	if h.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") || !strings.Contains(h.Get("Cache-Control"), "no-store") || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(h)
	}
	partial := open(t, a, link.URL, "GET", map[string]string{"Range": "bytes=0-3"})
	if partial.Code != http.StatusPartialContent || partial.Body.String() != "%PDF" {
		t.Fatal(partial.Code, partial.Body.String())
	}
	if w := open(t, a, link.URL, "POST", nil); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}

func TestLinkDownloadsActiveContentInsteadOfRendering(t *testing.T) {
	a := linkApp(t)
	for _, name := range []string{"page.html", "drawing.svg", "notes.docx"} {
		if err := os.WriteFile(filepath.Join(a.Root, name), []byte("<script>alert(1)</script>"), 0600); err != nil {
			t.Fatal(err)
		}
		link, err := a.CreateLink(name)
		if err != nil {
			t.Fatal(err)
		}
		if link.OpensInBrowser {
			t.Fatal(name, "must not render in the browser")
		}
		w := open(t, a, link.URL, "GET", nil)
		h := w.Header()
		if w.Code != 200 || h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
			t.Fatal(name, w.Code, h)
		}
	}
}

func TestLinkExpiresAndRejectsUnknownOrChangedFiles(t *testing.T) {
	a := linkApp(t)
	now := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return now }
	p := filepath.Join(a.Root, "photo.jpg")
	if err := os.WriteFile(p, []byte("jpeg"), 0600); err != nil {
		t.Fatal(err)
	}
	link, err := a.CreateLink("photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if w := open(t, a, link.URL, "GET", nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal(w.Code, w.Header())
	}
	token := strings.Split(strings.TrimPrefix(link.URL, "https://nas.example/download/"), "/")[0]
	forged := strings.Replace(link.URL, token, strings.Repeat("A", len(token)), 1)
	for _, bad := range []string{forged, "https://nas.example/download/", "https://nas.example/download/short/photo.jpg"} {
		if w := open(t, a, bad, "GET", nil); w.Code != 404 || strings.Contains(w.Body.String(), "photo") {
			t.Fatal(bad, w.Code, w.Body.String())
		}
	}
	if err := os.WriteFile(p, []byte("changed jpeg"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := open(t, a, link.URL, "GET", nil); w.Code != http.StatusGone {
		t.Fatal("changed file served", w.Code)
	}
	link, err = a.CreateLink("photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if w := open(t, a, link.URL, "GET", nil); w.Code != 404 {
		t.Fatal("expired link served", w.Code)
	}
}

func TestLinkHonoursAccessAndLimits(t *testing.T) {
	a := accessApp(t)
	if _, err := a.CreateLink("Documents/open.txt"); err == nil {
		t.Fatal("created a link before links were configured")
	}
	for _, bad := range []string{"ftp://nas.example", "https://nas.example/?x=1", "https://user:pw@nas.example", "nas.example", ""} {
		if err := a.ConfigureLinks(bad, 10*time.Minute); err == nil {
			t.Fatal("accepted base", bad)
		}
	}
	for _, ttl := range []time.Duration{0, -time.Minute, 61 * time.Minute} {
		if err := a.ConfigureLinks("https://nas.example", ttl); err == nil {
			t.Fatal("accepted ttl", ttl)
		}
	}
	if err := a.ConfigureLinks("https://nas.example/", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.Root, "Documents/open.txt"), "visible")
	write(t, filepath.Join(a.Root, "Documents/private/passport.txt"), "secret")
	if err := a.ConfigureAccess([]string{"Documents"}, []string{"Documents/private"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Documents/private/passport.txt", "../Documents/open.txt", "Documents/missing.txt", "Documents"} {
		if _, err := a.CreateLink(p); err == nil {
			t.Fatal("created link for", p)
		}
	}
	link, err := a.CreateLink("Documents/open.txt")
	if err != nil || !strings.HasPrefix(link.URL, "https://nas.example/download/") {
		t.Fatal(link, err)
	}
	if err := a.ConfigureAccess([]string{"Documents"}, []string{"Documents"}); err != nil {
		t.Fatal(err)
	}
	if w := open(t, a, link.URL, "GET", nil); w.Code != 404 || strings.Contains(w.Body.String(), "visible") {
		t.Fatal("revoked folder still downloadable", w.Code)
	}
	if err := a.ConfigureAccess([]string{"Documents"}, nil); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(a.Root, "Documents/large.bin")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(sourceLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := a.CreateLink("Documents/large.bin"); err == nil {
		t.Fatal("created link above source limit")
	}
}

func TestLinkCapacityRecoversAfterExpiry(t *testing.T) {
	a := linkApp(t)
	now := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return now }
	write(t, filepath.Join(a.Root, "a.txt"), "a")
	for i := 0; i < maxLinks; i++ {
		if _, err := a.CreateLink("a.txt"); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := a.CreateLink("a.txt"); err == nil {
		t.Fatal("exceeded active link limit")
	}
	now = now.Add(11 * time.Minute)
	if _, err := a.CreateLink("a.txt"); err != nil {
		t.Fatal("expired links were not released", err)
	}
}
