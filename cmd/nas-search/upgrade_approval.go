package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/mattyv/nas-search-mcp/internal/upgrade"
)

// The public upgrade tool prepares a request; fresh browser OAuth and an
// explicit confirmation queue it. Approval state is deliberately lost on a service restart.
type upgradeApproval struct {
	issuer  string
	queue   upgrade.Queue
	mu      sync.Mutex
	pending map[[32]byte]*pendingUpgrade
}

type pendingUpgrade struct {
	csrf    [32]byte
	expires time.Time
	browser string
}

func newUpgradeApproval(issuer, directory string) *upgradeApproval {
	return &upgradeApproval{issuer: strings.TrimRight(issuer, "/"), queue: upgrade.Queue{Dir: directory}, pending: make(map[[32]byte]*pendingUpgrade)}
}

func randomUpgradeToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (a *upgradeApproval) prepare(ctx context.Context) (map[string]any, error) {
	if oauth.AuthenticatedKey(ctx) == "" {
		return nil, errors.New("OAuth authentication required for public upgrades")
	}
	nonce, err := randomUpgradeToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, pending := range a.pending {
		if !now.Before(pending.expires) {
			delete(a.pending, key)
		}
	}
	if len(a.pending) >= 128 {
		return nil, errors.New("too many pending approvals; try again in five minutes")
	}
	a.pending[sha256.Sum256([]byte(nonce))] = &pendingUpgrade{expires: now.Add(5 * time.Minute)}
	return map[string]any{"state": "approval_required", "approval_url": a.issuer + "/upgrade/approve?request=" + nonce, "expires_at": now.Add(5 * time.Minute).UTC().Format(time.RFC3339), "message": "Open approval_url yourself, sign in through OAuth, and approve the upgrade. Never send the password in chat. The upgrade is not queued until you approve it."}, nil
}

func upgradeCookieName(nonce string) string {
	digest := sha256.Sum256([]byte(nonce))
	return "nas_upgrade_" + hex.EncodeToString(digest[:6])
}

func (a *upgradeApproval) known(r *http.Request) bool {
	nonce := r.URL.Query().Get("request")
	if len(nonce) != 43 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := a.pending[sha256.Sum256([]byte(nonce))]
	return pending != nil && time.Now().Before(pending.expires)
}

func (a *upgradeApproval) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if r.Method == http.MethodGet {
		csrf, err := randomUpgradeToken()
		if err != nil {
			a.mu.Unlock()
			http.Error(w, "approval unavailable", 500)
			return
		}
		pending.csrf = sha256.Sum256([]byte(csrf))
		pending.browser = browser
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: upgradeCookieName(nonce), Value: csrf, Path: "/upgrade/approve", MaxAge: 300, HttpOnly: true, Secure: strings.HasPrefix(a.issuer, "https://"), SameSite: http.SameSiteStrictMode})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Approve NAS search upgrade</title></head><body><h1>Approve NAS search upgrade?</h1><p>This will replace the running search service with the latest verified main build. Search may pause briefly. Original documents stay read-only.</p><form method="post"><input type="hidden" name="csrf" value="%s"><button type="submit" name="consent" value="approve">Approve upgrade</button></form></body></html>`, csrf)
		return
	}
	if r.Method != http.MethodPost {
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
	cookie, err := r.Cookie(upgradeCookieName(nonce))
	if err != nil || pending.browser != browser || r.PostForm.Get("consent") != "approve" || len(r.PostForm.Get("csrf")) != 43 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.PostForm.Get("csrf"))) != 1 || subtle.ConstantTimeCompare(sha256Sum(cookie.Value), pending.csrf[:]) != 1 {
		a.mu.Unlock()
		http.Error(w, "invalid approval request", http.StatusForbidden)
		return
	}
	delete(a.pending, key)
	a.mu.Unlock()
	if _, err := a.queue.Request(); err != nil {
		http.Error(w, "upgrade could not be queued; check upgrade_status", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "Upgrade queued. Return to Claude and ask for upgrade_status.")
}

func sha256Sum(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
