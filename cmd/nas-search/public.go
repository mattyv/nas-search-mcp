package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

const publicResponseLimit = 2 * 1024 * 1024

type cappedResponse struct {
	header   http.Header
	code     int
	body     bytes.Buffer
	overflow bool
}

func (w *cappedResponse) Header() http.Header { return w.header }
func (w *cappedResponse) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}
func (w *cappedResponse) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	if w.overflow || len(p) > publicResponseLimit-w.body.Len() {
		w.overflow = true
		return len(p), nil
	}
	return w.body.Write(p)
}

type publicCallLimits struct {
	mu       sync.Mutex
	byKey    map[string]*rate.Limiter
	fallback *rate.Limiter
}

func newPublicCallLimits() *publicCallLimits {
	return &publicCallLimits{byKey: make(map[string]*rate.Limiter), fallback: rate.NewLimiter(5, 60)}
}

func (l *publicCallLimits) allow(key, method string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if key == "" {
		allowed := l.fallback.Allow()
		if !allowed {
			log.Printf("public rejection reason=call_rate key=none method=%s tokens=%.2f", method, l.fallback.Tokens())
		}
		return allowed
	}
	limiter := l.byKey[key]
	if limiter == nil {
		if len(l.byKey) >= 128 {
			clear(l.byKey)
		}
		limiter = rate.NewLimiter(5, 60)
		l.byKey[key] = limiter
	}
	allowed := limiter.Allow()
	if !allowed {
		id := key
		if len(id) > 12 {
			id = id[:12]
		}
		log.Printf("public rejection reason=call_rate key=%s method=%s tokens=%.2f", id, method, limiter.Tokens())
	}
	return allowed
}

// Admit bounded MCP work, charging only authenticated tools/call requests.
func admitPublic(next http.Handler) http.Handler {
	callLimits := newPublicCallLimits()
	gate := make(chan struct{}, 2)
	queue := make(chan struct{}, 100)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case queue <- struct{}{}:
			defer func() { <-queue }()
		default:
			key := oauth.AuthenticatedKey(r.Context())
			if len(key) > 12 {
				key = key[:12]
			}
			log.Printf("public rejection reason=queue_full key=%s method=unread active=%d queued=%d", key, len(gate), len(queue)-len(gate))
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server busy", 429)
			return
		}
		wait := time.NewTimer(15 * time.Second)
		defer wait.Stop()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
			if r.Context().Err() != nil {
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				} else if r.Context().Err() == nil {
					http.Error(w, "invalid request body", http.StatusBadRequest)
				}
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			var request struct {
				Method string `json:"method"`
			}
			protocol := json.Unmarshal(body, &request) == nil && (request.Method == "initialize" || request.Method == "ping" || request.Method == "tools/list" || strings.HasPrefix(request.Method, "notifications/"))
			method := "other"
			if request.Method == "tools/call" {
				method = "tools/call"
			}
			if r.URL.Path == "/mcp" && len(body) > 0 && !protocol && !callLimits.allow(oauth.AuthenticatedKey(r.Context()), method) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "request limit reached", 429)
				return
			}
			next.ServeHTTP(w, r)
		case <-wait.C:
			key := oauth.AuthenticatedKey(r.Context())
			if len(key) > 12 {
				key = key[:12]
			}
			log.Printf("public rejection reason=work_busy key=%s method=unread active=%d", key, len(gate))
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server busy", 429)
		case <-r.Context().Done():
			return
		}
	})
}

func hardenPublic(next http.Handler, logger *log.Logger) http.Handler {
	bounded := admitPublic(next)
	limiter := rate.NewLimiter(5, 60)
	return publicResponseBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			log.Printf("public rejection reason=oauth_rate key=anonymous method=http:%s tokens=%.2f", r.Method, limiter.Tokens())
			w.Header().Set("Retry-After", "1")
			http.Error(w, "request limit reached", 429)
			return
		}
		bounded.ServeHTTP(w, r)
	}), logger)
}

// Stateless JSON responses finish before publication, so an oversized response leaks no prefix.
// This outer boundary also covers authentication failures; it does not allocate worker slots.
func publicResponseBoundary(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		out := &cappedResponse{header: make(http.Header)}
		next.ServeHTTP(out, r)
		if out.overflow {
			out = &cappedResponse{header: make(http.Header)}
			http.Error(out, "response exceeds limit", http.StatusBadGateway)
		}
		out.Header().Set("Cache-Control", "no-store")
		out.Header().Set("X-Content-Type-Options", "nosniff")
		if out.Header().Get("Referrer-Policy") == "" {
			out.Header().Set("Referrer-Policy", "no-referrer")
		}
		for k, values := range out.header {
			w.Header()[k] = values
		}
		if out.code == 0 {
			out.code = 200
		}
		w.WriteHeader(out.code)
		w.Write(out.body.Bytes())
		// No URL, query, headers, client address, request body, or tool result enters logs.
		logger.Printf("public request status=%d duration_ms=%d bytes=%d", out.code, time.Since(start).Milliseconds(), out.body.Len())
	})
}

func buildPublicHandler(server *mcp.Server, auth *oauth.Server, a *app.App, approval *upgradeApproval) http.Handler {
	mux := http.NewServeMux()
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle("/mcp", publicResponseBoundary(auth.Protect(admitPublic(h)), log.Default()))
	mux.Handle("/", hardenPublic(auth.Handler(), log.Default()))
	routes := http.NewServeMux()
	routes.Handle("/download/", hardenDownloads(auth.DownloadHandler(http.HandlerFunc(a.ServeLink), a.LinkKnown), a.LinkKnown, log.Default()))
	if approval != nil {
		routes.Handle("/upgrade/approve", hardenPublic(auth.BrowserActionHandler(approval, approval.known), log.Default()))
	}
	routes.Handle("/", mux)
	return routes
}

type statusRecorder struct {
	http.ResponseWriter
	code  int
	bytes int64
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Downloads stream up to the 32 MiB source limit, so they bypass the buffered MCP response cap.
// Requests for unknown links share a strict budget, so guessing cannot starve real links.
// Active links get a larger budget: browser PDF viewers issue many range requests per file.
func hardenDownloads(next http.Handler, known func(*http.Request) bool, logger *log.Logger) http.Handler {
	misses := rate.NewLimiter(rate.Every(time.Second), 20)
	hits := rate.NewLimiter(20, 100)
	gate := make(chan struct{}, 6)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		out := &statusRecorder{ResponseWriter: w}
		// The URL carries the link token and filename, so it never enters logs.
		defer func() {
			logger.Printf("download request status=%d duration_ms=%d bytes=%d", out.code, time.Since(start).Milliseconds(), out.bytes)
		}()
		limiter := misses
		if known(r) {
			limiter = hits
		}
		if !limiter.Allow() {
			out.Header().Set("Retry-After", "1")
			http.Error(out, "request limit reached", 429)
			return
		}
		// Queue briefly rather than failing a viewer's parallel range requests.
		wait := time.NewTimer(10 * time.Second)
		defer wait.Stop()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
			next.ServeHTTP(out, r)
		case <-wait.C:
			out.Header().Set("Retry-After", "1")
			http.Error(out, "server busy", 429)
		case <-r.Context().Done():
		}
	})
}
