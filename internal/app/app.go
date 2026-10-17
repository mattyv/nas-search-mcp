package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
	"github.com/mattyv/nas-search-mcp/internal/store"
)

type App struct {
	Root, Data, Python, Worker string
	Semantic, Photos           bool
	Store                      *store.Store
	workerGate                 chan struct{}
	modelKeepalive             time.Duration
	sessionMu                  sync.Mutex
	session                    *modelSession
	closed                     bool
	accessMu                   sync.RWMutex
	accessConfigured           bool
	allowed, denied            []string
	scanMu                     sync.Mutex
	scanDelayMu                sync.RWMutex
	scanFileDelay              time.Duration
	stateMu                    sync.Mutex
	scanning                   bool
	lastScan                   string
	scanError                  string
	currentFile                string
	scanStage                  string
	fileStartedAt              string
	scanStartedAt              string
	now                        func() time.Time
	linkMu                     sync.Mutex
	linkBase                   string
	linkTTL                    time.Duration
	links                      map[[32]byte]downloadLink
}
type DocumentSection struct {
	Location string `json:"location"`
	Text     string `json:"text"`
}
type WorkerResult struct {
	Kind       string            `json:"kind"`
	Sections   []DocumentSection `json:"sections"`
	Text       string            `json:"text"`
	Offset     int64             `json:"offset"`
	NextOffset *int64            `json:"next_offset"`
	Complete   bool              `json:"complete"`
	Metadata   map[string]any    `json:"metadata"`
	Truncated  bool              `json:"truncated"`
	Vectors    [][]float32       `json:"vectors"`
	Error      string            `json:"error"`
}
type Hit struct {
	ID       int64  `json:"id"`
	Path     string `json:"path"`
	Location string `json:"location"`
	Text     string `json:"text"`
	Score    int64  `json:"score"`
}
type SearchResult struct {
	Mode    string `json:"mode"`
	Warning string `json:"warning,omitempty"`
	Hits    []Hit  `json:"hits"`
}

func New(root, data, python, worker, extension string, semantic, photos bool) (*App, error) {
	keepalive, err := modelKeepaliveFromEnv()
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(data, 0700); err != nil {
		return nil, err
	}
	data, err = filepath.EvalSymlinks(data)
	if err != nil {
		return nil, err
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, data)
	if err != nil || rel == "." || !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, errors.New("index data must be outside file root")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("file root must be a directory")
	}
	db, err := store.New(filepath.Join(data, "index.sqlite3"), extension)
	if err != nil {
		return nil, err
	}
	a := &App{Root: root, Data: data, Python: python, Worker: worker, Semantic: semantic, Photos: photos, Store: db, modelKeepalive: keepalive, workerGate: make(chan struct{}, 1)}
	// Store the extraction/model configuration. Never silently mix incompatible vectors.
	// Photo vectors live in their own table, so the photo setting is tracked per file instead.
	signature := fmt.Sprintf("v1;bge-small-en-v1.5;clip-ViT-B-32;semantic=%t;root=%s", semantic, root)
	file := filepath.Join(data, "pipeline.json")
	existing, err := os.ReadFile(file)
	legacy := err == nil && (string(existing) == fmt.Sprintf("v1;bge-small-en-v1.5;clip-ViT-B-32;semantic=%t;photos=false;root=%s", semantic, root) ||
		string(existing) == fmt.Sprintf("v1;bge-small-en-v1.5;clip-ViT-B-32;semantic=%t;photos=true;root=%s", semantic, root))
	if err == nil && string(existing) != signature && !legacy {
		db.Close()
		return nil, errors.New("pipeline changed: choose a new NAS_DATA directory to rebuild")
	}
	if err != nil && !os.IsNotExist(err) {
		db.Close()
		return nil, err
	}
	if os.IsNotExist(err) || legacy {
		if err = os.WriteFile(file, []byte(signature), 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	return a, nil
}
func (a *App) safeFile(relative string) (string, error) {
	f, err := a.openFile(relative)
	if err != nil {
		return "", err
	}
	f.Close()
	return filepath.Join(a.Root, filepath.FromSlash(relative)), nil
}

func (a *App) openFile(relative string) (*os.File, error) {
	if !policy.ValidRelative(relative) || !a.accessAllowed(relative, false) {
		return nil, errors.New("path outside allowed folders")
	}
	f, err := a.openNoSymlinks(relative, false)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("path is not a regular file")
	}
	return f, nil
}
func (a *App) ReadFile(relative string, limit int) (string, error) {
	if limit < 0 {
		return "", errors.New("max_bytes must be nonnegative")
	}
	f, err := a.openFile(relative)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Inspect a fixed prefix independent of the requested output size.
	b, err := io.ReadAll(io.LimitReader(f, 65536))
	if err != nil {
		return "", err
	}
	for _, c := range b {
		if c < 32 && c != '\t' && c != '\n' && c != '\r' {
			return "", errors.New("binary file: use read_document for extracted text or fetch_file for original bytes")
		}
	}
	end := policy.ClampLimit(limit, 65536)
	if end > len(b) {
		end = len(b)
	}
	return strings.ToValidUTF8(string(b[:end]), "�"), nil
}

type DocumentResult struct {
	Path       string            `json:"path"`
	Kind       string            `json:"kind"`
	Text       string            `json:"text"`
	Sections   []DocumentSection `json:"sections"`
	Offset     int64             `json:"offset"`
	NextOffset *int64            `json:"next_offset"`
	Complete   bool              `json:"complete"`
	Metadata   map[string]any    `json:"metadata"`
}
type FetchResult struct {
	Path       string `json:"path"`
	Data       string `json:"data"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	Offset     int64  `json:"offset"`
	NextOffset *int64 `json:"next_offset"`
	Complete   bool   `json:"complete"`
	Version    string `json:"version"`
}

const sourceLimit = 32 * 1024 * 1024

var fetchRequestID atomic.Uint64

// detectMIME prefers the file extension and falls back to sniffing the first 512 bytes.
func detectMIME(relative string, f *os.File) (string, error) {
	if byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(relative))); byExtension != "" {
		return byExtension, nil
	}
	sample := make([]byte, 512)
	n, err := f.ReadAt(sample, 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(sample[:n]), nil
}

func fileVersion(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

// FetchFile returns exact original bytes in bounded base64 batches, including binary files.
func (a *App) FetchFile(relative string, offset int64, maxBytes int, expectedVersion string) (out FetchResult, retErr error) {
	requestID := fetchRequestID.Add(1)
	started := time.Now()
	var openMS, statMS, mimeMS, readMS, restatMS int64
	out = FetchResult{Path: relative, Offset: offset}
	log.Printf("fetch_file request_id=%d stage=start", requestID)
	defer func() {
		outcome := "success"
		if retErr != nil {
			outcome = "error"
		}
		log.Printf("fetch_file request_id=%d stage=end outcome=%s total_ms=%d open_ms=%d stat_ms=%d mime_ms=%d read_ms=%d restat_ms=%d", requestID, outcome, time.Since(started).Milliseconds(), openMS, statMS, mimeMS, readMS, restatMS)
	}()
	if offset < 0 || maxBytes < 0 {
		return out, errors.New("offset and max_bytes must be nonnegative")
	}
	if offset > 0 && expectedVersion == "" {
		return out, errors.New("expected_version is required after the first batch")
	}
	if maxBytes == 0 {
		maxBytes = 65536
	}
	maxBytes = policy.ClampLimit(maxBytes, 262144)
	stageStarted := time.Now()
	f, err := a.openFile(relative)
	openMS = time.Since(stageStarted).Milliseconds()
	if err != nil {
		return out, err
	}
	defer f.Close()
	stageStarted = time.Now()
	info, err := f.Stat()
	statMS = time.Since(stageStarted).Milliseconds()
	if err != nil {
		return out, err
	}
	if info.Size() > sourceLimit {
		return out, errors.New("file exceeds 32 MiB source limit")
	}
	if offset > info.Size() {
		return out, errors.New("offset exceeds file size")
	}
	out.Size = info.Size()
	out.Version = fileVersion(info)
	if expectedVersion != "" && expectedVersion != out.Version {
		return out, errors.New("file changed; restart download at offset zero")
	}
	stageStarted = time.Now()
	out.MIME, err = detectMIME(relative, f)
	mimeMS = time.Since(stageStarted).Milliseconds()
	if err != nil {
		return out, err
	}
	remaining := info.Size() - offset
	if int64(maxBytes) > remaining {
		maxBytes = int(remaining)
	}
	stageStarted = time.Now()
	content := make([]byte, maxBytes)
	n, err := f.ReadAt(content, offset)
	readMS = time.Since(stageStarted).Milliseconds()
	if err != nil && err != io.EOF {
		return out, err
	}
	stageStarted = time.Now()
	after, err := f.Stat()
	restatMS = time.Since(stageStarted).Milliseconds()
	if err != nil {
		return out, err
	}
	if n != maxBytes || fileVersion(after) != out.Version {
		return out, errors.New("file changed while fetching; restart download")
	}
	out.Data = base64.StdEncoding.EncodeToString(content[:n])
	next := offset + int64(n)
	out.Complete = next == out.Size
	if !out.Complete {
		out.NextOffset = &next
	}
	return out, nil
}

// ReadDocument reads a window of the full extracted document, independently of the index's text cap.
// Python extraction consumes a private snapshot and is outside Dafny verification.
func (a *App) ReadDocument(ctx context.Context, relative string, offset int64, maxChars int) (DocumentResult, error) {
	return a.ReadDocumentWithOCR(ctx, relative, offset, maxChars, false)
}

// ReadDocumentWithOCR requests forced PDF OCR while retaining the worker's configured OCR limits.
func (a *App) ReadDocumentWithOCR(ctx context.Context, relative string, offset int64, maxChars int, forceOCR bool) (DocumentResult, error) {
	if forceOCR && !ocrEnabled() {
		return DocumentResult{}, errors.New("forced OCR is disabled by NAS_OCR; enable NAS_OCR=true to retry")
	}
	if forceOCR && !strings.EqualFold(filepath.Ext(relative), ".pdf") {
		return DocumentResult{}, errors.New("force_ocr is PDF-only; omit it for other file types")
	}

	out := DocumentResult{Path: relative, Offset: offset, Sections: []DocumentSection{}, Metadata: map[string]any{}}
	if offset < 0 || maxChars < 0 {
		return out, errors.New("offset and max_chars must be nonnegative")
	}
	if maxChars == 0 {
		maxChars = 12000
	}
	maxChars = policy.ClampLimit(maxChars, 20000)
	full, info, cleanup, err := a.snapshot(relative)
	if err != nil {
		return out, err
	}
	defer cleanup()
	result, err := a.worker(ctx, map[string]any{"operation": "read_document", "path": full, "offset": offset, "max_chars": maxChars, "max_bytes": sourceLimit, "force_ocr": forceOCR})
	if err != nil {
		return out, err
	}
	current, err := a.openFile(relative)
	if err != nil {
		return out, err
	}
	after, err := current.Stat()
	current.Close()
	if err != nil {
		return out, err
	}
	if fileVersion(info) != fileVersion(after) {
		return out, errors.New("file changed while reading; retry")
	}
	out.Kind = result.Kind
	out.Text = result.Text
	out.Sections = result.Sections
	out.Offset = result.Offset
	out.NextOffset = result.NextOffset
	out.Complete = result.Complete
	out.Metadata = result.Metadata
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	out.Metadata["source_version"] = fileVersion(info)
	return out, nil
}

func ocrEnabled() bool {
	ocr := os.Getenv("NAS_OCR")
	return ocr == "" || strings.EqualFold(ocr, "true")
}
func extractionRevision() string {
	return fmt.Sprintf("v3;ocr=%t", ocrEnabled())
}
func (a *App) markExtractionRevision(revision string) error {
	f, err := os.CreateTemp(a.Data, ".extraction-revision-")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.WriteString(revision); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(a.Data, "extraction-revision"))
}

// Protect authenticates every HTTP request. Token provisioning happens outside the app.
func Protect(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serialize workers; extraction releases the resident embedding worker before starting.
func (a *App) worker(ctx context.Context, request map[string]any) (WorkerResult, error) {
	var result WorkerResult
	select {
	case a.workerGate <- struct{}{}:
		defer func() { <-a.workerGate }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	input, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.closed {
		return result, errors.New("app is closed")
	}
	operation, _ := request["operation"].(string)
	if a.modelKeepalive > 0 && (operation == "embed" || operation == "image_query" || operation == "image_embed") {
		return a.sessionRequest(ctx, input)
	}
	a.stopSession()
	cmd := exec.CommandContext(ctx, a.Python, a.Worker)
	cmd.Env = append(os.Environ(), "NAS_DATA="+a.Data, "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "TOKENIZERS_PARALLELISM=false")
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return result, fmt.Errorf("worker failed: %w", err)
	}
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		return result, fmt.Errorf("invalid worker response: %w", err)
	}
	if result.Error != "" {
		return result, errors.New("document/model worker reported an error")
	}
	return result, nil
}
func (a *App) Search(ctx context.Context, query, prefix string, photos bool, limit int) (SearchResult, error) {
	result := SearchResult{Mode: "keyword", Hits: []Hit{}}
	if strings.TrimSpace(query) == "" || len(query) > 4000 {
		return result, errors.New("query must contain 1 to 4000 bytes")
	}
	if len(prefix) > 4096 {
		return result, errors.New("prefix too long")
	}
	if photos && !a.Photos {
		result.Warning = "Photo content indexing is disabled; matching photo metadata only"
	}
	var vector []float32
	if a.Semantic && (!photos || a.Photos) {
		op := "embed"
		if photos {
			op = "image_query"
		}
		reply, err := a.worker(ctx, map[string]any{"operation": op, "texts": []string{query}, "text": query})
		if err == nil && len(reply.Vectors) == 1 {
			vector = reply.Vectors[0]
			result.Mode = "hybrid"
		} else {
			result.Warning = "Semantic model unavailable; using keyword search"
			log.Printf("query embedding unavailable")
		}
	}
	candidates, err := a.Store.Candidates(query, prefix, photos, vector, policy.ClampLimit(limit, 50)*5)
	if err != nil && vector != nil {
		result.Mode = "keyword"
		result.Warning = "Vector search unavailable; using keyword search"
		candidates, err = a.Store.Candidates(query, prefix, photos, nil, policy.ClampLimit(limit, 50)*5)
	}
	if err != nil {
		return result, err
	}
	for _, c := range candidates {
		// Do not return indexed content for files which have been removed or escaped the root.
		if _, err = a.safeFile(c.Path); err != nil {
			continue
		}
		result.Hits = append(result.Hits, Hit{c.ID, c.Path, c.Location, c.Text, policy.RankScore(c.LexicalRank, c.SemanticRank)})
	}
	byID := make(map[int64]Hit, len(result.Hits))
	pairs := make([][2]int64, 0, len(result.Hits))
	for _, hit := range result.Hits {
		byID[hit.ID] = hit
		pairs = append(pairs, [2]int64{hit.ID, hit.Score})
	}
	result.Hits = []Hit{}
	for _, item := range policy.Top(pairs, policy.ClampLimit(limit, 50)) {
		result.Hits = append(result.Hits, byID[item[0]])
	}

	return result, nil
}

type FoundFile struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}
type FindResult struct {
	Files     []FoundFile `json:"files"`
	Truncated bool        `json:"truncated"`
	Warning   string      `json:"warning,omitempty"`
	Visited   int         `json:"visited"`
}

// FindFiles searches live relative paths without consulting the index or model worker.
func (a *App) FindFiles(ctx context.Context, query, prefix string, limit int) (FindResult, error) {
	return a.findFiles(ctx, query, prefix, limit, 100000, 5*time.Second)
}
func (a *App) findFiles(ctx context.Context, query, prefix string, limit, budget int, duration time.Duration) (FindResult, error) {
	out := FindResult{Files: []FoundFile{}}
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 256 {
		return out, errors.New("query must contain at most 256 Unicode characters")
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 || len(terms) > 16 {
		return out, errors.New("query must contain 1 to 16 literal terms")
	}
	if len(prefix) > 4096 {
		return out, errors.New("prefix too long")
	}
	if prefix != "" && !policy.ValidRelative(strings.TrimSuffix(prefix, "/")) {
		return out, errors.New("invalid relative prefix")
	}
	if limit < 0 {
		return out, errors.New("limit must be nonnegative")
	}
	if limit == 0 {
		limit = 20
	}
	limit = policy.ClampLimit(limit, 100)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	walkContext, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	stop := errors.New("bounded live search stopped")
	truncated := func(message string) error { out.Truncated = true; out.Warning = message; return stop }
	var walk func(string) error
	walk = func(directory string) error {
		if err := walkContext.Err(); err != nil {
			return err
		}
		if !a.accessAllowed(directory, true) {
			return nil
		}
		f, err := a.openNoSymlinks(directory, true)
		if err != nil {
			out.Truncated = true
			out.Warning = "Some directories could not be searched"
			return nil
		}
		defer f.Close()
		for {
			if err := walkContext.Err(); err != nil {
				return err
			}
			// Read incrementally instead of allocating every entry in a large NAS directory.
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				if err := walkContext.Err(); err != nil {
					return err
				}
				if out.Visited >= budget {
					return truncated("Entry limit reached; narrow the prefix or query")
				}
				if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				relative := entry.Name()
				if directory != "." {
					relative = directory + "/" + relative
				}
				if !a.accessAllowed(relative, entry.IsDir()) {
					continue
				}
				out.Visited++
				if entry.IsDir() {
					if excluded[entry.Name()] {
						continue
					}
					folder := relative + "/"
					if prefix != "" && !strings.HasPrefix(folder, prefix) && !strings.HasPrefix(prefix, folder) {
						continue
					}
					if err := walk(relative); err != nil {
						return err
					}
					continue
				}
				if !entry.Type().IsRegular() {
					continue
				}
				if !strings.HasPrefix(relative, prefix) || !policy.MatchesTerms(strings.ToLower(relative), terms) {
					continue
				}
				file, err := a.openFile(relative)
				if err != nil {
					out.Truncated = true
					out.Warning = "Some matching files could not be read; results are incomplete"
					continue
				}
				info, err := file.Stat()
				file.Close()
				if err != nil {
					out.Truncated = true
					out.Warning = "Some matching files could not be read; results are incomplete"
					continue
				}
				out.Files = append(out.Files, FoundFile{Path: relative, Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano)})
				if len(out.Files) >= limit {
					return truncated("Result limit reached; additional matching files may exist")
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				out.Truncated = true
				out.Warning = "Some directories could not be searched"
				return nil
			}
		}
	}
	err := walk(".")
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		out.Truncated = true
		out.Warning = "Time limit reached; narrow the prefix or query"
		return out, nil
	}
	if err == stop {
		return out, nil
	}
	return out, err
}

// Browse filters stale entries immediately, while the next scan reconciles the index.
// Candidate reads are bounded; heavy stale indexes can temporarily return fewer files.
func (a *App) Browse(prefix string, limit int) ([]store.File, error) {
	if len(prefix) > 4096 {
		return nil, errors.New("prefix too long")
	}
	if limit == 0 {
		limit = 50
	}
	limit = policy.ClampLimit(limit, 100)
	candidates, err := a.Store.List(prefix, limit+100)
	if err != nil {
		return nil, err
	}
	files := []store.File{}
	for _, candidate := range candidates {
		f, err := a.openFile(candidate.Path)
		if err != nil {
			continue
		}
		f.Close()
		files = append(files, candidate)
		if len(files) == limit {
			break
		}
	}
	return files, nil
}

func (a *App) Status() (map[string]any, error) {
	stats, err := a.Store.Status()
	if err != nil {
		return nil, err
	}
	var index any = stats
	a.accessMu.RLock()
	scoped := a.accessConfigured
	a.accessMu.RUnlock()
	if scoped {
		index = map[string]any{"scope": "configured folders", "details": "global index statistics hidden"}
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	scanError := a.scanError
	if scoped && scanError != "" {
		scanError = "scan failed"
	}
	currentFile := a.currentFile
	if currentFile != "" && !a.accessAllowed(currentFile, false) {
		currentFile = ""
	}
	return map[string]any{"index": index, "scanning": a.scanning, "last_scan": a.lastScan, "scan_error": scanError, "semantic": a.Semantic, "photos": a.Photos, "ocr": ocrEnabled(), "current_file": currentFile, "scan_stage": a.scanStage, "file_started_at": a.fileStartedAt, "scan_started_at": a.scanStartedAt}, nil
}

var excluded = map[string]bool{"node_modules": true, "vendor": true, "build": true, "dist": true, "__pycache__": true, "WDTimeMachine": true, "Time Machine": true, "@eaDir": true, "@Recycle": true}

func supported(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return imageFile(name) || strings.Contains("|txt|md|rst|csv|tsv|json|jsonl|xml|yaml|yml|toml|ini|cfg|log|py|go|rs|java|js|jsx|ts|tsx|c|h|cpp|hpp|cs|sh|bash|sql|dfy|css|scss|vue|svelte|rb|php|swift|kt|kts|r|tex|html|htm|pdf|docx|xlsx|xlsm|pptx|", "|"+strings.TrimPrefix(ext, ".")+"|")
}
func imageFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext != "" && strings.Contains("|jpg|jpeg|png|webp|gif|bmp|tif|tiff|heic|heif|", "|"+strings.TrimPrefix(ext, ".")+"|")
}
func fingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, 32*1024*1024+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (a *App) setScanProgress(path, stage string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if !a.scanning {
		return
	}
	if path != a.currentFile {
		a.currentFile = path
		if path == "" {
			a.fileStartedAt = ""
		} else {
			a.fileStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	a.scanStage = stage
}

// SetScanFileDelay sets the pause before each allowed, supported file is inspected.
// A zero or negative duration disables pacing.
func (a *App) SetScanFileDelay(delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	a.scanDelayMu.Lock()
	a.scanFileDelay = delay
	a.scanDelayMu.Unlock()
}

func (a *App) waitForScanFile(ctx context.Context) error {
	a.scanDelayMu.RLock()
	delay := a.scanFileDelay
	a.scanDelayMu.RUnlock()
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func (a *App) Scan(ctx context.Context) error {
	if !a.scanMu.TryLock() {
		return errors.New("scan already running")
	}
	defer a.scanMu.Unlock()
	a.stateMu.Lock()
	a.scanning = true
	a.scanStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	a.currentFile = ""
	a.fileStartedAt = ""
	a.scanStage = "walking"
	a.scanError = ""
	a.stateMu.Unlock()
	defer func() {
		a.stateMu.Lock()
		a.scanning = false
		a.currentFile = ""
		a.fileStartedAt = ""
		a.scanStage = ""
		a.lastScan = time.Now().UTC().Format(time.RFC3339)
		a.stateMu.Unlock()
	}()
	old, err := a.Store.Fingerprints()
	if err != nil {
		return err
	}
	revision := extractionRevision()
	existingRevision, revisionErr := os.ReadFile(filepath.Join(a.Data, "extraction-revision"))
	if revisionErr != nil && !os.IsNotExist(revisionErr) {
		return revisionErr
	}
	forceRefresh := string(existingRevision) != revision
	seen := map[string]bool{}
	err = filepath.WalkDir(a.Root, func(full string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			rel, e := filepath.Rel(a.Root, full)
			if e != nil {
				return e
			}
			if !a.accessAllowed(filepath.ToSlash(rel), true) {
				return filepath.SkipDir
			}
			if full != a.Root && (strings.HasPrefix(d.Name(), ".") || excluded[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || strings.HasPrefix(d.Name(), ".") || !supported(d.Name()) {
			return nil
		}
		rel, e := filepath.Rel(a.Root, full)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if !a.accessAllowed(rel, false) {
			return nil
		}
		a.setScanProgress(rel, "pacing")
		if e = a.waitForScanFile(ctx); e != nil {
			return e
		}
		a.setScanProgress(rel, "checking")
		defer a.setScanProgress("", "walking")
		if _, e = a.safeFile(rel); e != nil {
			return nil
		}
		seen[rel] = true
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Size() > 32*1024*1024 {
			if e = a.Store.Remove(rel); e != nil {
				return e
			}
			return a.Store.RecordError(rel, "File exceeds 32 MiB input limit")
		}
		snapshot, _, cleanup, e := a.snapshot(rel)
		if e != nil {
			return e
		}
		defer cleanup()
		hash, e := fingerprint(snapshot)
		if e != nil {
			return e
		}
		if !forceRefresh && !policy.ShouldReplace(old[rel], a.indexedFingerprint(rel, hash)) {
			return nil
		}
		if e = a.indexFile(ctx, snapshot, rel, hash); e != nil {
			if ctx.Err() != nil {
				// The source changed, so its old chunks no longer describe the file.
				if removeErr := a.Store.Remove(rel); removeErr != nil {
					return removeErr
				}
				return ctx.Err()
			}
			log.Printf("indexing failed")
			// Old chunks describe a different revision and must not survive a failed update.
			if removeErr := a.Store.Remove(rel); removeErr != nil {
				return removeErr
			}
			return a.Store.RecordError(rel, "document indexing failed")
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		for path := range old {
			if e := ctx.Err(); e != nil {
				err = e
				break
			}
			if !seen[path] {
				if e := a.Store.Remove(path); e != nil {
					return e
				}
			}
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		a.setScanProgress("", "saving_revision")
		err = a.markExtractionRevision(revision)
	}
	if err != nil {
		a.stateMu.Lock()
		a.scanError = err.Error()
		a.stateMu.Unlock()
	}
	return err
}
func (a *App) indexFile(ctx context.Context, full, relative, hash string) error {
	var extract WorkerResult
	var err error
	embedPhoto := a.Photos && a.Semantic && imageFile(relative)
	if embedPhoto {
		// One call reads metadata and embeds; a separate extract would unload the warm model.
		a.setScanProgress(relative, "embedding_photo")
		extract, err = a.worker(ctx, map[string]any{"operation": "image_embed", "path": full, "max_bytes": 32 * 1024 * 1024})
		if err == nil && (extract.Kind != "photo" || len(extract.Vectors) != 1) {
			err = errors.New("photo embedding returned no photo vector")
		}
	} else {
		a.setScanProgress(relative, "extracting")
		extract, err = a.worker(ctx, map[string]any{"operation": "extract", "path": full, "max_bytes": 32 * 1024 * 1024, "max_chars": 200000})
	}
	if err != nil {
		return err
	}
	metadata := extract.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["truncated"] = extract.Truncated
	var chunks []store.Chunk
	for _, section := range extract.Sections {
		for i, text := range policy.Chunk(section.Text, 900, 100) {
			chunks = append(chunks, store.Chunk{Location: fmt.Sprintf("%s:chunk:%d", section.Location, i+1), Text: text})
		}
	}
	var vectors [][]float32
	if extract.Kind == "photo" {
		encoded, _ := json.Marshal(metadata)
		chunks = []store.Chunk{{Location: "photo", Text: relative + " " + string(encoded)}}
		if embedPhoto {
			vectors = extract.Vectors
		}
	} else if a.Semantic && len(chunks) > 0 {
		a.setScanProgress(relative, "embedding")
		for start := 0; start < len(chunks); start += 16 {
			end := start + 16
			if end > len(chunks) {
				end = len(chunks)
			}
			texts := make([]string, 0, end-start)
			for _, chunk := range chunks[start:end] {
				texts = append(texts, chunk.Text)
			}
			r, e := a.worker(ctx, map[string]any{"operation": "embed", "texts": texts})
			if e != nil {
				return e
			}
			vectors = append(vectors, r.Vectors...)
		}
	}
	// Changed-during-read files are retried on the next scan, never committed as current.
	current, _, cleanup, err := a.snapshot(relative)
	if err != nil {
		return err
	}
	defer cleanup()
	after, err := fingerprint(current)
	if err != nil {
		return err
	}
	if after != hash {
		return errors.New("file changed while indexing; retry next scan")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	a.setScanProgress(relative, "storing")
	return a.Store.Replace(store.File{Path: relative, Fingerprint: a.indexedFingerprint(relative, hash), Kind: extract.Kind, Metadata: string(encoded)}, chunks, vectors)
}

// indexedFingerprint marks photos indexed with visual vectors, so changing the photo
// setting reindexes photos only and leaves documents and code untouched.
func (a *App) indexedFingerprint(relative, hash string) string {
	if a.Photos && a.Semantic && imageFile(relative) {
		return hash + ";clip"
	}
	return hash
}
