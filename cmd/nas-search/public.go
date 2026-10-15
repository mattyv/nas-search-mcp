package main

import (
	"bytes"
	"log"
	"net/http"
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

// Admit only bounded work. OAuth and authenticated MCP use separate instances.
func admitPublic(next http.Handler) http.Handler {
	limiter := rate.NewLimiter(5, 60)
	gate := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "request limit reached", 429)
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
			next.ServeHTTP(w, r)
		case <-wait.C:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server busy", 429)
		case <-r.Context().Done():
			return
		}
	})
}

func hardenPublic(next http.Handler, logger *log.Logger) http.Handler {
	return publicResponseBoundary(admitPublic(next), logger)
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

func buildPublicHandler(server *mcp.Server, auth *oauth.Server, a *app.App) http.Handler {
	mux := http.NewServeMux()
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle("/mcp", publicResponseBoundary(auth.Protect(admitPublic(h)), log.Default()))
	mux.Handle("/", hardenPublic(auth.Handler(), log.Default()))
	routes := http.NewServeMux()
	routes.Handle("/download/", hardenDownloads(http.HandlerFunc(a.ServeLink), a.LinkKnown, log.Default()))
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
