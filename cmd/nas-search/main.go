package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/mattyv/nas-search-mcp/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func enabled(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true")
}

type SearchInput struct {
	Query  string `json:"query"`
	Prefix string `json:"prefix,omitempty"`
	Photos bool   `json:"photos,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type FindInput struct {
	Query  string `json:"query"`
	Prefix string `json:"prefix,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type BrowseInput struct {
	Prefix string `json:"prefix,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type ReadInput struct {
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}
type DocumentInput struct {
	ForceOCR bool   `json:"force_ocr,omitempty"`
	Path     string `json:"path"`
	Offset   int64  `json:"offset,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}
type FetchInput struct {
	Path            string `json:"path"`
	Offset          int64  `json:"offset,omitempty"`
	MaxBytes        int    `json:"max_bytes,omitempty"`
	ExpectedVersion string `json:"expected_version,omitempty"`
}
type BrowseOutput struct {
	Files []store.File `json:"files"`
}
type ReadOutput struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		c := http.Client{Timeout: 3 * time.Second}
		r, err := c.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	token := os.Getenv("NAS_TOKEN")
	if len(token) < 24 {
		log.Fatal("NAS_TOKEN must contain at least 24 characters")
	}
	a, err := app.New(env("NAS_ROOT", "/files"), env("NAS_DATA", "/data"), env("NAS_PYTHON", "python3"), env("NAS_WORKER", "/app/worker/worker.py"), os.Getenv("NAS_VEC_EXTENSION"), enabled("NAS_SEMANTIC", true), enabled("NAS_PHOTOS", false))
	if err != nil {
		log.Fatal(err)
	}
	defer a.Store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	interval, err := strconv.Atoi(env("NAS_SCAN_INTERVAL", "300"))
	if err != nil || interval < 30 {
		log.Fatal("NAS_SCAN_INTERVAL must be an integer >=30")
	}
	go func() {
		for {
			if err := a.Scan(ctx); err != nil && ctx.Err() == nil {
				log.Printf("scan failed: %v", err)
			}
			select {
			case <-time.After(time.Duration(interval) * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}()
	mux := buildHTTPHandler(a, token)
	httpServer := &http.Server{Addr: env("NAS_LISTEN", ":8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()
	log.Printf("NAS search listening on %s (bearer authentication required)", httpServer.Addr)
	if err = httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func buildHTTPHandler(a *app.App, token string) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "nas-search", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "search", Description: "Search indexed NAS files using keywords and meaning. Set photos=true for visual photo search. Results include source paths and page/section locations. If the user names a file or this search misses it, use find_files to match live filename and folder terms. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, app.SearchResult, error) {
		if in.Limit == 0 {
			in.Limit = 10
		}
		out, err := a.Search(ctx, in.Query, in.Prefix, in.Photos, in.Limit)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{Name: "find_files", Description: "Find files by live filename and relative folder path, independently of indexing, OCR and semantic models. All whitespace-separated query terms must occur literally in the path, ignoring case; no regex. Includes unsupported file formats and failed document extractions. prefix is a relative, case-sensitive path prefix. limit defaults to 20, capped at 100. Searches at most 100000 entries or 5 seconds; truncated/warning explains incomplete searches. Known excluded directories, hidden entries and symlinks are skipped. Use this when the user names a file or content search misses it. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in FindInput) (*mcp.CallToolResult, app.FindResult, error) {
		out, err := a.FindFiles(ctx, in.Query, in.Prefix, in.Limit)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "browse", Description: "List indexed files and metadata. Prefix is a relative source path, such as Matthew/Berklee/. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowseInput) (*mcp.CallToolResult, BrowseOutput, error) {
		if in.Limit == 0 {
			in.Limit = 50
		}
		files, err := a.Browse(in.Prefix, in.Limit)
		return nil, BrowseOutput{Files: files}, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read_file", Description: "Read up to 65536 bytes from a text/code file inside configured NAS shares. Use read_document for full extracted PDF/Office content or fetch_file for original bytes. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, ReadOutput, error) {
		if in.MaxBytes == 0 {
			in.MaxBytes = 16384
		}
		text, err := a.ReadFile(in.Path, in.MaxBytes)
		return nil, ReadOutput{in.Path, text}, err
	})

	mcp.AddTool(server, &mcp.Tool{Name: "read_document", Description: "Read the full extracted text of a PDF, Office document, or text/code file in character windows, including OCR of scanned PDF pages when enabled. offset is a Unicode character offset; max_chars defaults to 12000, capped at 20000. Follow next_offset until complete. Set force_ocr=true to retry PDF pages with suspect extracted text; use the same flag on every window. Configured OCR enablement and resource limits still apply. Source files are limited to 32 MiB. metadata reports OCR status and source_version. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in DocumentInput) (*mcp.CallToolResult, app.DocumentResult, error) {
		out, err := a.ReadDocumentWithOCR(ctx, in.Path, in.Offset, in.MaxChars, in.ForceOCR)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "fetch_file", Description: "Fetch exact original file bytes, including binary PDF/Office/image files, as base64 batches. max_bytes defaults to 65536, capped at 262144; source limit 32 MiB. Reassemble decoded data locally, following next_offset until complete. Send the first response's version as expected_version on every subsequent batch to reject changed files. This does not create a chat attachment automatically. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in FetchInput) (*mcp.CallToolResult, app.FetchResult, error) {
		out, err := a.FetchFile(in.Path, in.Offset, in.MaxBytes, in.ExpectedVersion)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "index_status", Description: "Show the active scan file, stage and start times, indexed file/chunk counts, errors, and whether OCR/semantic/photo indexing is enabled."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := a.Status()
		return nil, out, err
	})
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", app.Protect(token, handler))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); fmt.Fprint(w, "ok") })
	return mux
}
