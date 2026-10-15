package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// maxLinks bounds the in-memory link table; expired links are released first.
const maxLinks = 200

// 32 random bytes in unpadded base64url.
const linkTokenLength = 43

const linkGoneMessage = "This link has expired or is not valid. Ask Claude for a new link."

type LinkResult struct {
	URL            string `json:"url"`
	Path           string `json:"path"`
	Name           string `json:"name"`
	MIME           string `json:"mime"`
	Size           int64  `json:"size"`
	OpensInBrowser bool   `json:"opens_in_browser"`
	ExpiresAt      string `json:"expires_at"`
}

type downloadLink struct {
	path, version, mime, name string
	inline                    bool
	expires                   int64
}

func (a *App) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// ConfigureLinks sets the absolute HTTP(S) origin that serves /download/ and the link lifetime.
func (a *App) ConfigureLinks(base string, ttl time.Duration) error {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("download link base must be an absolute http(s) URL without credentials, query, or fragment")
	}
	if ttl < time.Minute || ttl > time.Hour {
		return errors.New("download link lifetime must be between 1 and 60 minutes")
	}
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	a.linkBase = strings.TrimRight(base, "/")
	a.linkTTL = ttl
	return nil
}

func (a *App) LinksEnabled() bool {
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	return a.linkBase != ""
}

// CreateLink issues an unguessable, expiring URL for one authorized original file.
func (a *App) CreateLink(relative string) (LinkResult, error) {
	out := LinkResult{Path: relative}
	if !a.LinksEnabled() {
		return out, errors.New("download links are not configured on this server")
	}
	f, err := a.openFile(relative)
	if err != nil {
		return out, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out, err
	}
	if info.Size() > sourceLimit {
		return out, errors.New("file exceeds 32 MiB source limit")
	}
	out.MIME, err = detectMIME(relative, f)
	if err != nil {
		return out, err
	}
	out.Name = path.Base(relative)
	out.Size = info.Size()
	out.OpensInBrowser = policy.ShowInline(mediaType(out.MIME))
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return out, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	now := a.clock()
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	for key, link := range a.links {
		if !policy.LinkActive(now.UnixNano(), link.expires) {
			delete(a.links, key)
		}
	}
	if len(a.links) >= maxLinks {
		return out, errors.New("too many active download links; try again in a few minutes")
	}
	if a.links == nil {
		a.links = make(map[[32]byte]downloadLink)
	}
	expires := now.Add(a.linkTTL)
	// Only a hash of the token is retained.
	a.links[sha256.Sum256([]byte(token))] = downloadLink{path: relative, version: fileVersion(info), mime: out.MIME, name: out.Name, inline: out.OpensInBrowser, expires: expires.UnixNano()}
	out.URL = a.linkBase + "/download/" + token + "/" + url.PathEscape(out.Name)
	out.ExpiresAt = expires.UTC().Format(time.RFC3339)
	return out, nil
}

// ServeLink streams the original for /download/{token}/{name}. The token is the only credential.
func (a *App) ServeLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	link, ok := a.activeLink(r)
	if !ok {
		http.Error(w, linkGoneMessage, http.StatusNotFound)
		return
	}
	// Reopen through the current access policy, so revoked folders stop serving.
	f, err := a.openFile(link.path)
	if err != nil {
		http.Error(w, linkGoneMessage, http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > sourceLimit {
		http.Error(w, linkGoneMessage, http.StatusNotFound)
		return
	}
	if fileVersion(info) != link.version {
		http.Error(w, "This file changed after the link was made. Ask Claude for a new link.", http.StatusGone)
		return
	}
	disposition := "attachment"
	if link.inline {
		disposition = "inline"
		h.Set("Content-Type", link.mime)
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	if formatted := mime.FormatMediaType(disposition, map[string]string{"filename": link.name}); formatted != "" {
		disposition = formatted
	}
	h.Set("Content-Disposition", disposition)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func mediaType(value string) string {
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed)
}

// LinkKnown cheaply reports whether a request names an active link, before any file access.
func (a *App) LinkKnown(r *http.Request) bool {
	_, ok := a.activeLink(r)
	return ok
}

func (a *App) activeLink(r *http.Request) (downloadLink, bool) {
	token, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/download/"), "/")
	if len(token) != linkTokenLength {
		return downloadLink{}, false
	}
	key := sha256.Sum256([]byte(token))
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	link, ok := a.links[key]
	if ok && !policy.LinkActive(a.clock().UnixNano(), link.expires) {
		delete(a.links, key)
		return downloadLink{}, false
	}
	return link, ok
}
