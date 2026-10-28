package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/consolidate"
	"github.com/mattyv/nas-search-mcp/internal/editor"
	"github.com/mattyv/nas-search-mcp/internal/jev"
	"github.com/mattyv/nas-search-mcp/internal/memory"
	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// memoryScopes parses a comma list of scope roots; every root must be a valid scope.
func memoryScopes(value string) ([]string, error) {
	roots := folderList(value)
	if len(roots) == 0 {
		return nil, errors.New("at least one memory scope is required")
	}
	for _, root := range roots {
		if !policy.ValidScope(root) {
			return nil, fmt.Errorf("invalid memory scope %q", root)
		}
	}
	return roots, nil
}

func threshold(key string, fallback int) (int, error) {
	value, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}

// memorySetup is the search process's view of the memory service.
type memorySetup struct {
	client       *memoryapi.Client
	roots        []string
	defaultScope string
}

func configuredMemory() (*memorySetup, error) {
	if !enabled("NAS_MEMORY_ENABLED", false) {
		return nil, nil
	}
	roots, err := memoryScopes(env("NAS_MEMORY_SCOPES", "personal"))
	if err != nil {
		return nil, err
	}
	return &memorySetup{client: &memoryapi.Client{Socket: env("NAS_MEMORY_SOCKET", "/data/memory-ipc/memory.sock")}, roots: roots, defaultScope: roots[0]}, nil
}

// runMemoryService runs inside the nas-memory container: the store, its private socket,
// the Tailscale-only editor, and consolidation.
func runMemoryService() error {
	roots, err := memoryScopes(env("NAS_MEMORY_SCOPES", "personal"))
	if err != nil {
		return err
	}
	acceptAt, err := threshold("NAS_MEMORY_ACCEPT_AT", 900)
	if err != nil {
		return err
	}
	rejectAt, err := threshold("NAS_MEMORY_REJECT_AT", 100)
	if err != nil {
		return err
	}
	if policy.RouteCandidate(1000, acceptAt, rejectAt) != "accept" {
		return errors.New("NAS_MEMORY_REJECT_AT and NAS_MEMORY_ACCEPT_AT must satisfy 0 <= reject < accept <= 1000")
	}
	external, err := memoryScopes(env("NAS_JEV_SCOPES", "personal"))
	if err != nil {
		return err
	}
	store, err := memory.Open(memory.Config{Path: env("NAS_MEMORY_DB", "/memory/memory.sqlite3"), AcceptAt: acceptAt, RejectAt: rejectAt})
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, 3)
	go func() {
		failures <- memoryapi.ServeSocket(ctx, env("NAS_MEMORY_LISTEN_SOCKET", "/ipc/memory.sock"), memoryapi.NewServer(store, roots))
	}()
	if owner := os.Getenv("NAS_EDITOR_OWNER"); owner != "" {
		handler, err := editor.New(store, editor.Config{Owner: owner, Origin: os.Getenv("NAS_EDITOR_ORIGIN"), Roots: roots, DefaultScope: roots[0]})
		if err != nil {
			return err
		}
		server := &http.Server{Addr: env("NAS_EDITOR_LISTEN", ":8083"), Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		go func() {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				failures <- err
			}
		}()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(shutdown)
		}()
	}
	var classifier consolidate.Classifier
	if key := os.Getenv("NAS_JEV_API_KEY"); key != "" {
		classifier = consolidate.NewJevClassifier(&jev.Client{BaseURL: env("NAS_JEV_URL", "https://api.typesafe.ai"), APIKey: key, Model: env("NAS_JEV_MODEL", "jev-latest"), HTTP: &http.Client{Timeout: 30 * time.Second}})
	}
	go func() {
		failures <- consolidate.Run(ctx, store, consolidate.Config{Roots: roots, ExternalScopes: external, Classifier: classifier})
	}()
	log.Printf("memory service ready (editor %t, classifier %t)", os.Getenv("NAS_EDITOR_OWNER") != "", classifier != nil)
	select {
	case <-ctx.Done():
		return nil
	case err := <-failures:
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func memoryHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return (&memoryapi.Client{Socket: env("NAS_MEMORY_LISTEN_SOCKET", "/ipc/memory.sock")}).Ping(ctx)
}

// withRoutes mounts extra exact-path handlers ahead of an existing handler.
func withRoutes(base http.Handler, routes map[string]http.Handler) http.Handler {
	if len(routes) == 0 {
		return base
	}
	mux := http.NewServeMux()
	for path, handler := range routes {
		mux.Handle(path, handler)
	}
	mux.Handle("/", base)
	return mux
}

// memoryTool reports whether a tool name belongs to shared memory. Those tools never use
// the search worker, so on the public connector they take the fast lane instead of
// queuing behind searches.
func memoryTool(name string) bool {
	switch name {
	case "context_open", "journal_append", "journal_search", "checkpoint_save", "memory_search", "memory_correct", "memory_forget":
		return true
	}
	return false
}

// passkeyMode reads NAS_OAUTH_PASSKEYS (default "enroll"). Passkeys need a host name, so
// when the setting is absent and the issuer is an IP address the default stays "off"
// rather than stopping the service after an upgrade.
func passkeyMode(issuer string) string {
	if mode := os.Getenv("NAS_OAUTH_PASSKEYS"); mode != "" {
		return mode
	}
	if u, err := url.Parse(issuer); err == nil && net.ParseIP(u.Hostname()) != nil {
		log.Printf("passkeys off: the OAuth issuer is an IP address")
		return "off"
	}
	return "enroll"
}
