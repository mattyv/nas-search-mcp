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
	"path/filepath"
	"strconv"
	"strings"
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

// memoryThresholds reads the accept and reject scores (per mille) and checks them with the
// verified rule.
func memoryThresholds() (acceptAt, rejectAt int, err error) {
	if acceptAt, err = threshold("NAS_MEMORY_ACCEPT_AT", 900); err != nil {
		return 0, 0, err
	}
	if rejectAt, err = threshold("NAS_MEMORY_REJECT_AT", 100); err != nil {
		return 0, 0, err
	}
	if !policy.ValidThresholds(acceptAt, rejectAt) {
		return 0, 0, errors.New("NAS_MEMORY_REJECT_AT and NAS_MEMORY_ACCEPT_AT must satisfy 0 <= reject < accept <= 1000")
	}
	return acceptAt, rejectAt, nil
}

// externalScopes reads NAS_JEV_SCOPES: "none" sends no entry outside the NAS; anything
// else is a list of valid scopes (default personal).
func externalScopes(value string) ([]string, error) {
	if value == "" {
		value = "personal"
	}
	if strings.TrimSpace(value) == "none" {
		return []string{}, nil
	}
	return memoryScopes(value)
}

// memorySetup is the search process's view of the memory service.
type memorySetup struct {
	client       *memoryapi.Client
	roots        []string
	defaultScope string
	editorOrigin string // where the owner forgets things; empty when not configured
}

func configuredMemory() (*memorySetup, error) {
	if !enabled("NAS_MEMORY_ENABLED", false) {
		return nil, nil
	}
	roots, err := memoryScopes(env("NAS_MEMORY_SCOPES", "personal"))
	if err != nil {
		return nil, err
	}
	return &memorySetup{client: &memoryapi.Client{Socket: env("NAS_MEMORY_SOCKET", "/data/memory-ipc/memory.sock")}, roots: roots, defaultScope: roots[0], editorOrigin: os.Getenv("NAS_EDITOR_ORIGIN")}, nil
}

// runMemoryService runs inside the nas-memory container: the store, its private socket,
// the Tailscale-only editor, and consolidation.
func runMemoryService() error {
	roots, err := memoryScopes(env("NAS_MEMORY_SCOPES", "personal"))
	if err != nil {
		return err
	}
	acceptAt, rejectAt, err := memoryThresholds()
	if err != nil {
		return err
	}
	external, err := externalScopes(os.Getenv("NAS_JEV_SCOPES"))
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

// passkeyMode reads NAS_OAUTH_PASSKEYS; when it is unset the verified default applies,
// which depends only on whether the issuer host is an IP address (passkeys need a name).
func passkeyMode(issuer string) string {
	if mode := os.Getenv("NAS_OAUTH_PASSKEYS"); mode != "" {
		return mode
	}
	isIP := false
	if u, err := url.Parse(issuer); err == nil {
		isIP = net.ParseIP(u.Hostname()) != nil
	}
	mode := policy.DefaultPasskeyMode(isIP)
	if isIP {
		log.Printf("passkey mode %q by default: the OAuth issuer is an IP address", mode)
	}
	return mode
}

// runMemoryBackup writes a consistent copy of the memory database into a "backups"
// folder next to it and keeps the newest NAS_MEMORY_BACKUP_KEEP copies (default 7).
// Run it inside the memory container, for example nightly before an offsite sync.
func runMemoryBackup() (string, error) {
	keep, err := strconv.Atoi(env("NAS_MEMORY_BACKUP_KEEP", "7"))
	if err != nil {
		return "", errors.New("NAS_MEMORY_BACKUP_KEEP must be a number from 1 to 90")
	}
	path := env("NAS_MEMORY_DB", "/memory/memory.sqlite3")
	store, err := memory.Open(memory.Config{Path: path})
	if err != nil {
		return "", err
	}
	defer store.Close()
	return store.Backup(filepath.Join(filepath.Dir(path), "backups"), keep)
}
