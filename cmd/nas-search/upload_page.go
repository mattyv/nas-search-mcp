package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/mattyv/nas-search-mcp/internal/upload"
)

const uploadLimit = int64(32 << 20)

type uploadPage struct {
	issuer  string
	dir     string
	onSaved func(string) bool
	mu      sync.Mutex
	pending map[[32]byte]*pendingUpload
}

type pendingUpload struct {
	csrf    [32]byte
	expires time.Time
	browser string
}

func newUploadPage(issuer, dir string) *uploadPage {
	return &uploadPage{issuer: strings.TrimRight(issuer, "/"), dir: dir, pending: make(map[[32]byte]*pendingUpload)}
}

func (p *uploadPage) prepare(ctx context.Context) (map[string]any, error) {
	if oauth.AuthenticatedKey(ctx) == "" {
		return nil, errors.New("OAuth authentication required for uploads")
	}
	nonce, err := randomUpgradeToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, item := range p.pending {
		if !now.Before(item.expires) {
			delete(p.pending, key)
		}
	}
	if len(p.pending) >= 128 {
		return nil, errors.New("too many pending uploads; try again in five minutes")
	}
	expiry := now.Add(5 * time.Minute)
	p.pending[sha256.Sum256([]byte(nonce))] = &pendingUpload{expires: expiry}
	return map[string]any{"state": "upload_ready", "upload_url": p.issuer + "/upload/" + nonce, "expires_at": expiry.UTC().Format(time.RFC3339), "message": "Open upload_url yourself, sign in through OAuth, and choose one file of any type up to 32 MB. Never send your password or file in chat. Supported formats are indexed after the file is saved."}, nil
}

func uploadCookieName(nonce string) string {
	digest := sha256.Sum256([]byte(nonce))
	return "nas_upload_" + hex.EncodeToString(digest[:6])
}

func uploadNonce(r *http.Request) string {
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path || !strings.HasPrefix(r.URL.Path, "/upload/") {
		return ""
	}
	nonce := strings.TrimPrefix(r.URL.Path, "/upload/")
	if len(nonce) != 43 {
		return ""
	}
	for _, c := range nonce {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return nonce
}

func (p *uploadPage) known(r *http.Request) bool {
	nonce := uploadNonce(r)
	if nonce == "" {
		return false
	}
	key := sha256.Sum256([]byte(nonce))
	p.mu.Lock()
	defer p.mu.Unlock()
	item := p.pending[key]
	return item != nil && time.Now().Before(item.expires)
}

func (p *uploadPage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	// Send only the site origin on the form POST; never expose the upload link.
	w.Header().Set("Referrer-Policy", "strict-origin")
	browser := oauth.BrowserActionKey(r.Context())
	if browser == "" {
		http.Error(w, "OAuth sign-in required", http.StatusUnauthorized)
		return
	}
	nonce := uploadNonce(r)
	if nonce == "" {
		http.NotFound(w, r)
		return
	}
	key := sha256.Sum256([]byte(nonce))
	p.mu.Lock()
	item := p.pending[key]
	if item == nil || !time.Now().Before(item.expires) {
		delete(p.pending, key)
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		csrf, err := randomUpgradeToken()
		if err != nil {
			p.mu.Unlock()
			http.Error(w, "upload unavailable", http.StatusInternalServerError)
			return
		}
		item.csrf = sha256.Sum256([]byte(csrf))
		item.browser = browser
		p.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: uploadCookieName(nonce), Value: csrf, Path: "/upload/" + nonce, MaxAge: 300, HttpOnly: true, Secure: strings.HasPrefix(p.issuer, "https://"), SameSite: http.SameSiteStrictMode})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Upload to NAS search</title></head><body><h1>Upload one file</h1><p>Choose any file type up to 32 MB. Ask your assistant to find it by filename after upload; supported formats also appear in content search after indexing.</p><form method="post" enctype="multipart/form-data"><input type="hidden" name="csrf" value="%s"><label>File <input type="file" name="file" required></label><button type="submit">Upload</button></form></body></html>`, csrf)
		return
	}
	if r.Method != http.MethodPost {
		p.mu.Unlock()
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != p.issuer || r.ContentLength > uploadLimit+(64<<10) {
		p.mu.Unlock()
		http.Error(w, "invalid upload request", http.StatusForbidden)
		return
	}
	cookie, err := r.Cookie(uploadCookieName(nonce))
	if err != nil || len(cookie.Value) != 43 || item.browser != browser {
		p.mu.Unlock()
		http.Error(w, "invalid upload request", http.StatusForbidden)
		return
	}
	expected := item.csrf
	p.mu.Unlock()
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		http.Error(w, "invalid upload form", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit+(64<<10))
	defer r.Body.Close()
	parts := multipart.NewReader(r.Body, params["boundary"])
	first, err := parts.NextPart()
	if err != nil || first.FormName() != "csrf" || first.FileName() != "" {
		http.Error(w, "invalid upload form", http.StatusBadRequest)
		return
	}
	csrfBytes, err := io.ReadAll(io.LimitReader(first, 44))
	first.Close()
	if err != nil || len(csrfBytes) != 43 || subtle.ConstantTimeCompare([]byte(cookie.Value), csrfBytes) != 1 || subtle.ConstantTimeCompare(sha256Sum(cookie.Value), expected[:]) != 1 {
		http.Error(w, "invalid upload request", http.StatusForbidden)
		return
	}
	file, err := parts.NextPart()
	if err != nil || file.FormName() != "file" || file.FileName() == "" {
		http.Error(w, "invalid upload form", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	item = p.pending[key]
	if item == nil || !time.Now().Before(item.expires) || item.browser != browser || subtle.ConstantTimeCompare(item.csrf[:], expected[:]) != 1 {
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	delete(p.pending, key)
	p.mu.Unlock()
	_, disposition, err := mime.ParseMediaType(file.Header.Get("Content-Disposition"))
	if err != nil || disposition["filename"] == "" {
		http.Error(w, "invalid upload form", http.StatusBadRequest)
		return
	}
	result, err := upload.Save(p.dir, disposition["filename"], &singleUploadFile{file: file, parts: parts}, uploadLimit)
	if err != nil {
		if errors.Is(err, upload.ErrExists) {
			http.Error(w, "file already exists; nothing was replaced", http.StatusConflict)
			return
		}
		http.Error(w, "file could not be saved; check its type, name, and size", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if p.onSaved != nil {
		p.onSaved(result.Name)
	}
	fmt.Fprint(w, "File saved. Ask your assistant to find it by filename; supported formats appear in content search after indexing.")
}

// singleUploadFile forces the multipart parser to find any extra part before Save commits.
type singleUploadFile struct {
	file  *multipart.Part
	parts *multipart.Reader
	done  bool
}

func (f *singleUploadFile) Read(dst []byte) (int, error) {
	if f.done {
		return 0, io.EOF
	}
	n, err := f.file.Read(dst)
	if err != io.EOF {
		return n, err
	}
	extra, nextErr := f.parts.NextPart()
	if extra != nil {
		extra.Close()
		return n, errors.New("extra upload part")
	}
	if nextErr != io.EOF {
		return n, errors.New("invalid upload form")
	}
	f.done = true
	if n > 0 {
		return n, nil
	}
	return 0, io.EOF
}
