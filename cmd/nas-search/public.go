package main

import (
	"bytes"
	"log"
	"net/http"
	"time"

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

// Stateless JSON responses finish before publication, so an oversized response leaks no prefix.
func hardenPublic(next http.Handler, logger *log.Logger) http.Handler {
	limiter := rate.NewLimiter(rate.Every(time.Second), 30)
	gate := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		out := &cappedResponse{header: make(http.Header)}
		if !limiter.Allow() {
			out.Header().Set("Retry-After", "1")
			http.Error(out, "request limit reached", 429)
		} else {
			select {
			case gate <- struct{}{}:
				r.Body = http.MaxBytesReader(out, r.Body, 64*1024)
				next.ServeHTTP(out, r)
				<-gate
			default:
				out.Header().Set("Retry-After", "1")
				http.Error(out, "server busy", 429)
			}
		}
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

func buildPublicHandler(server *mcp.Server, auth *oauth.Server) http.Handler {
	mux := http.NewServeMux()
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle("/mcp", auth.Protect(h))
	mux.Handle("/", auth.Handler())
	return hardenPublic(mux, log.Default())
}
