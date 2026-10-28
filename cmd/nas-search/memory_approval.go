package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/oauth"
)

// memoryApprovalPath is where the integrator mounts the approval page, wrapped in
// auth.BrowserActionHandler(approval, approval.known) like /upgrade/approve.
const memoryApprovalPath = "/memory/approve"

// A public memory_forget call only prepares a request. The owner must open the link,
// sign in again through OAuth, see exactly what will be erased, and press Approve.
// Pending state is deliberately lost on restart.
type memoryApproval struct {
	issuer  string
	store   memoryapi.Store
	roots   []string
	mu      sync.Mutex
	pending map[[32]byte]*pendingForget
}

type pendingForget struct {
	kind, id string
	csrf     [32]byte
	expires  time.Time
	browser  string
}

func newMemoryApproval(issuer string, store memoryapi.Store, roots []string) *memoryApproval {
	return &memoryApproval{issuer: strings.TrimRight(issuer, "/"), store: store, roots: roots, pending: make(map[[32]byte]*pendingForget)}
}

func memoryCookieName(nonce string) string {
	digest := sha256.Sum256([]byte(nonce))
	return "nas_memory_" + hex.EncodeToString(digest[:6])
}

// forgetTarget is what the approval page shows.
type forgetTarget struct {
	Kind, ID, Heading, Text, Project, Scope, Extra string
}

func (a *memoryApproval) describe(ctx context.Context, kind, id string) (forgetTarget, error) {
	switch kind {
	case "event":
		e, err := a.store.GetEvent(ctx, a.roots, id)
		if err != nil {
			return forgetTarget{}, err
		}
		return forgetTarget{Kind: "journal entry", ID: id, Heading: e.Title, Text: e.Body, Project: e.Project, Scope: e.Scope, Extra: "type " + e.Type}, nil
	case "memory":
		m, err := a.store.GetMemory(ctx, a.roots, id)
		if err != nil {
			return forgetTarget{}, err
		}
		return forgetTarget{Kind: "memory", ID: id, Heading: m.Statement, Project: m.Project, Scope: m.Scope, Extra: "category " + m.Category}, nil
	}
	return forgetTarget{}, fmt.Errorf("%w: kind must be \"event\" or \"memory\"", memoryapi.ErrInvalid)
}

// prepare validates the target is visible, then issues a single-use, five-minute link.
func (a *memoryApproval) prepare(ctx context.Context, kind, id string) (map[string]any, error) {
	if _, err := a.describe(ctx, kind, id); err != nil {
		return nil, err
	}
	nonce, err := randomUpgradeToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, p := range a.pending {
		if !now.Before(p.expires) {
			delete(a.pending, key)
		}
	}
	if len(a.pending) >= 128 {
		return nil, errors.New("too many pending approvals; try again in five minutes")
	}
	expires := now.Add(5 * time.Minute)
	a.pending[sha256.Sum256([]byte(nonce))] = &pendingForget{kind: kind, id: id, expires: expires}
	return map[string]any{
		"state":        "approval_required",
		"approval_url": a.issuer + memoryApprovalPath + "?request=" + nonce,
		"expires_at":   expires.UTC().Format(time.RFC3339),
		"message":      "Give approval_url to the user. They must open it themselves, sign in through OAuth, review what will be erased, and press Approve. Never ask for a password in chat. Nothing is forgotten until they approve.",
	}, nil
}

func (a *memoryApproval) known(r *http.Request) bool {
	nonce := r.URL.Query().Get("request")
	if len(nonce) != 43 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending[sha256.Sum256([]byte(nonce))]
	return p != nil && time.Now().Before(p.expires)
}

var memoryApprovalPage = template.Must(template.New("approve").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Approve forgetting</title></head><body><h1>Forget this {{.Kind}} permanently?</h1>
<dl><dt>Kind</dt><dd>{{.Kind}} ({{.Extra}})</dd><dt>Content</dt><dd>{{.Heading}}</dd>{{if .Text}}<dt>Details</dt><dd>{{.Text}}</dd>{{end}}<dt>Project</dt><dd>{{if .Project}}{{.Project}}{{else}}none{{end}}</dd><dt>Scope</dt><dd>{{.Scope}}</dd></dl>
<p>The content is erased and cannot be recovered. Only a record that something was forgotten remains.</p>
<form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" name="consent" value="approve">Approve</button></form></body></html>`))

func (a *memoryApproval) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "strict-origin")
	browser := oauth.BrowserActionKey(r.Context())
	if browser == "" {
		http.Error(w, "OAuth sign-in required", http.StatusUnauthorized)
		return
	}
	nonce := r.URL.Query().Get("request")
	if len(nonce) != 43 {
		http.NotFound(w, r)
		return
	}
	key := sha256.Sum256([]byte(nonce))
	a.mu.Lock()
	pending := a.pending[key]
	if pending == nil || !time.Now().Before(pending.expires) {
		delete(a.pending, key)
		a.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		target, err := a.describe(r.Context(), pending.kind, pending.id)
		if err != nil {
			delete(a.pending, key)
			a.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		csrf, err := randomUpgradeToken()
		if err != nil {
			a.mu.Unlock()
			http.Error(w, "approval unavailable", http.StatusInternalServerError)
			return
		}
		pending.csrf = sha256.Sum256([]byte(csrf))
		pending.browser = browser
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: memoryCookieName(nonce), Value: csrf, Path: memoryApprovalPath, MaxAge: 300, HttpOnly: true, Secure: strings.HasPrefix(a.issuer, "https://"), SameSite: http.SameSiteStrictMode})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = memoryApprovalPage.Execute(w, struct {
			forgetTarget
			CSRF string
		}{target, csrf})
		return
	case http.MethodPost:
	default:
		a.mu.Unlock()
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != a.issuer || r.ContentLength > 2048 {
		a.mu.Unlock()
		http.Error(w, "invalid approval request", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		a.mu.Unlock()
		http.Error(w, "invalid approval request", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(memoryCookieName(nonce))
	if err != nil || pending.browser != browser || r.PostForm.Get("consent") != "approve" || len(r.PostForm.Get("csrf")) != 43 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.PostForm.Get("csrf"))) != 1 || subtle.ConstantTimeCompare(sha256Sum(cookie.Value), pending.csrf[:]) != 1 {
		a.mu.Unlock()
		http.Error(w, "invalid approval request", http.StatusForbidden)
		return
	}
	delete(a.pending, key)
	a.mu.Unlock()
	var forgetErr error
	if pending.kind == "event" {
		forgetErr = a.store.ForgetEvent(r.Context(), a.roots, pending.id, memoryapi.ActorUser)
	} else {
		forgetErr = a.store.ForgetMemory(r.Context(), a.roots, pending.id, memoryapi.ActorUser)
	}
	if forgetErr != nil {
		http.Error(w, "could not forget it; it may already be gone", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "Forgotten. You can close this page and tell the assistant.")
}
