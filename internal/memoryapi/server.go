package memoryapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

const (
	maxRequestBytes = 1 << 20
	maxRoots        = 50
	healthPath      = "/v1/health"
)

// Wire parameter and result shapes, shared by the server and Client.
type (
	appendParams struct {
		Event Event `json:"event"`
	}
	appendResult struct {
		Event   Event `json:"event"`
		Created bool  `json:"created"`
	}
	idParams struct {
		ID    string `json:"id"`
		Actor string `json:"actor,omitempty"`
	}
	searchEventsParams struct {
		Query EventQuery `json:"query"`
	}
	saveCheckpointParams struct {
		Checkpoint      Checkpoint `json:"checkpoint"`
		ExpectedVersion int        `json:"expected_version"`
	}
	projectParams struct {
		Project string `json:"project"`
		Limit   int    `json:"limit,omitempty"`
	}
	contextParams struct {
		Request ContextRequest `json:"request"`
	}
	proposeParams struct {
		Memory       Memory `json:"memory"`
		UserAttested bool   `json:"user_attested"`
	}
	searchMemoriesParams struct {
		Query MemoryQuery `json:"query"`
	}
	correctParams struct {
		Correction Correction `json:"correction"`
	}
	limitParams struct {
		Limit int `json:"limit"`
	}
	noParams struct{}
)

type handlerFunc func(ctx context.Context, roots []string, params json.RawMessage) (any, error)

func method[P any, R any](fn func(ctx context.Context, roots []string, p P) (R, error)) handlerFunc {
	return func(ctx context.Context, roots []string, raw json.RawMessage) (any, error) {
		var p P
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("%w: parameters are not valid", ErrInvalid)
			}
		}
		return fn(ctx, roots, p)
	}
}

// methods maps wire names to store calls. Socket callers are AI clients, never the owner:
// every write is attributed to the assistant and no write is attested by the owner. The
// owner acts through the editor, which uses the store directly. Forget methods are
// registered so Client keeps compiling, but ServeHTTP rejects them (policy.SocketMethodAllowed).
func methods(s Store) map[string]handlerFunc {
	return map[string]handlerFunc{
		"AppendEvent": method(func(ctx context.Context, r []string, p appendParams) (appendResult, error) {
			p.Event.Actor = ActorAssistant
			if !policy.AssistantMayAppendType(p.Event.Type) {
				return appendResult{}, fmt.Errorf("%w: assistants cannot write this entry type", ErrInvalid)
			}
			e, created, err := s.AppendEvent(ctx, r, p.Event)
			return appendResult{e, created}, err
		}),
		"GetEvent": method(func(ctx context.Context, r []string, p idParams) (Event, error) { return s.GetEvent(ctx, r, p.ID) }),
		"SearchEvents": method(func(ctx context.Context, r []string, p searchEventsParams) ([]Event, error) {
			return s.SearchEvents(ctx, r, p.Query)
		}),
		"SaveCheckpoint": method(func(ctx context.Context, r []string, p saveCheckpointParams) (Checkpoint, error) {
			p.Checkpoint.Actor = ActorAssistant
			return s.SaveCheckpoint(ctx, r, p.Checkpoint, p.ExpectedVersion)
		}),
		"LatestCheckpoint": method(func(ctx context.Context, r []string, p projectParams) (*Checkpoint, error) {
			return s.LatestCheckpoint(ctx, r, p.Project)
		}),
		"CheckpointHistory": method(func(ctx context.Context, r []string, p projectParams) ([]Checkpoint, error) {
			return s.CheckpointHistory(ctx, r, p.Project, p.Limit)
		}),
		"OpenContext": method(func(ctx context.Context, r []string, p contextParams) (ContextPackage, error) {
			return s.OpenContext(ctx, r, p.Request)
		}),
		"ProposeMemory": method(func(ctx context.Context, r []string, p proposeParams) (Memory, error) {
			return s.ProposeMemory(ctx, r, p.Memory, false)
		}),
		"GetMemory": method(func(ctx context.Context, r []string, p idParams) (Memory, error) { return s.GetMemory(ctx, r, p.ID) }),
		"SearchMemories": method(func(ctx context.Context, r []string, p searchMemoriesParams) ([]Memory, error) {
			return s.SearchMemories(ctx, r, p.Query)
		}),
		"CorrectMemory": method(func(ctx context.Context, r []string, p correctParams) (Memory, error) {
			p.Correction.Actor = ActorAssistant
			return s.CorrectMemory(ctx, r, p.Correction)
		}),
		// No forget handlers: policy.SocketMethodAllowed refuses them, and leaving them out
		// means no route on the socket can reach the store's forget methods.
		"Projects": method(func(ctx context.Context, r []string, _ noParams) ([]ProjectSummary, error) { return s.Projects(ctx, r) }),
		"UnclassifiedEvents": method(func(ctx context.Context, r []string, p limitParams) ([]Event, error) {
			return s.UnclassifiedEvents(ctx, r, p.Limit)
		}),
		"MarkClassified": method(func(ctx context.Context, r []string, p idParams) (struct{}, error) {
			return struct{}{}, s.MarkClassified(ctx, r, p.ID)
		}),
	}
}

type server struct {
	methods map[string]handlerFunc
	allowed []string
}

// NewServer serves store over the wire format in api.go. allowedRoots bounds every
// request regardless of what the store would permit (defence in depth).
func NewServer(store Store, allowedRoots []string) http.Handler {
	return &server{methods: methods(store), allowed: append([]string(nil), allowedRoots...)}
}

var codes = []struct {
	err  error
	code string
	http int
}{
	{ErrNotFound, "not_found", http.StatusNotFound},
	{ErrConflict, "conflict", http.StatusConflict},
	{ErrInvalid, "invalid", http.StatusBadRequest},
	{ErrScope, "scope", http.StatusForbidden},
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	for _, c := range codes {
		if errors.Is(err, c.err) {
			msg := strings.TrimPrefix(err.Error(), c.err.Error()+": ")
			if len(msg) > 500 {
				msg = msg[:500]
			}
			writeJSON(w, c.http, CallResponse{Error: &WireError{Code: c.code, Message: msg}})
			return
		}
	}
	writeJSON(w, http.StatusInternalServerError, CallResponse{Error: &WireError{Code: "internal", Message: "the memory service could not complete the request"}})
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recover() != nil {
			writeError(w, errors.New("panic"))
		}
	}()
	switch {
	case r.URL.Path == healthPath && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case r.URL.Path != CallPath:
		writeJSON(w, http.StatusNotFound, CallResponse{Error: &WireError{Code: "not_found", Message: "no such endpoint"}})
		return
	case r.Method != http.MethodPost:
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, CallResponse{Error: &WireError{Code: "invalid", Message: "use POST"}})
		return
	}
	var req CallRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, CallResponse{Error: &WireError{Code: "invalid", Message: "request is larger than 1 MiB"}})
			return
		}
		writeError(w, fmt.Errorf("%w: request is not valid JSON", ErrInvalid))
		return
	}
	if !policy.SocketMethodAllowed(req.Method) {
		writeError(w, fmt.Errorf("%w: forget is only available in the editor", ErrInvalid))
		return
	}
	fn, ok := s.methods[req.Method]
	if !ok {
		writeError(w, fmt.Errorf("%w: unknown method", ErrInvalid))
		return
	}
	if len(req.Roots) > maxRoots {
		writeError(w, fmt.Errorf("%w: too many roots", ErrScope))
		return
	}
	for _, root := range req.Roots {
		if !policy.ValidScope(root) || !policy.ScopeVisible(root, s.allowed) {
			writeError(w, fmt.Errorf("%w: a requested root is not permitted", ErrScope))
			return
		}
	}
	roots := req.Roots
	if roots == nil {
		roots = []string{}
	}
	result, err := fn(r.Context(), roots, req.Params)
	if err != nil {
		writeError(w, err)
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, CallResponse{Result: raw})
}

// ServeSocket listens on a private Unix socket until ctx ends, then drains requests.
func ServeSocket(ctx context.Context, path string, h http.Handler) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("memory socket: %s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("memory socket: remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("memory socket: protect socket: %w", err)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdown)
	if errors.Is(err, context.DeadlineExceeded) {
		_ = srv.Close()
	}
	if serveErr := <-errc; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
