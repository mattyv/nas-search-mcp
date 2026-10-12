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
	"syscall"
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
	scanMu                     sync.Mutex
	stateMu                    sync.Mutex
	scanning                   bool
	lastScan                   string
	scanError                  string
	currentFile                string
	scanStage                  string
	fileStartedAt              string
	scanStartedAt              string
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
	root, err := filepath.EvalSymlinks(root)
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
	a := &App{Root: root, Data: data, Python: python, Worker: worker, Semantic: semantic, Photos: photos, Store: db, workerGate: make(chan struct{}, 1)}
	// Store the extraction/model configuration. Never silently mix incompatible vectors.
	signature := fmt.Sprintf("v1;bge-small-en-v1.5;clip-ViT-B-32;semantic=%t;photos=%t;root=%s", semantic, photos, root)
	file := filepath.Join(data, "pipeline.json")
	existing, err := os.ReadFile(file)
	if err == nil && string(existing) != signature {
		db.Close()
		return nil, errors.New("pipeline changed: choose a new NAS_DATA directory to rebuild")
	}
	if err != nil && !os.IsNotExist(err) {
		db.Close()
		return nil, err
	}
	if os.IsNotExist(err) {
		if err = os.WriteFile(file, []byte(signature), 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	return a, nil
}
func (a *App) safeFile(relative string) (string, error) {
	if !policy.ValidRelative(relative) {
		return "", errors.New("invalid relative path")
	}
	full, err := filepath.EvalSymlinks(filepath.Join(a.Root, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(a.Root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes configured root")
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	return full, nil
}

// openFile uses the OS-enforced root boundary, including during concurrent symlink changes.
func (a *App) openFile(relative string) (*os.File, error) {
	if !policy.ValidRelative(relative) {
		return nil, errors.New("invalid relative path")
	}
	root, err := os.OpenRoot(a.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// Nonblocking open prevents a raced-in FIFO from waiting for a writer before Stat.
	f, err := root.OpenFile(filepath.FromSlash(relative), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
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

func fileVersion(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

// FetchFile returns exact original bytes in bounded base64 batches, including binary files.
func (a *App) FetchFile(relative string, offset int64, maxBytes int, expectedVersion string) (FetchResult, error) {
	out := FetchResult{Path: relative, Offset: offset}
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
	f, err := a.openFile(relative)
	if err != nil {
		return out, err
	}
	defer f.Close()
	info, err := f.Stat()
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
	sample := make([]byte, 512)
	n, err := f.ReadAt(sample, 0)
	if err != nil && err != io.EOF {
		return out, err
	}
	out.MIME = mime.TypeByExtension(strings.ToLower(filepath.Ext(relative)))
	if out.MIME == "" {
		out.MIME = http.DetectContentType(sample[:n])
	}
	remaining := info.Size() - offset
	if int64(maxBytes) > remaining {
		maxBytes = int(remaining)
	}
	content := make([]byte, maxBytes)
	n, err = f.ReadAt(content, offset)
	if err != nil && err != io.EOF {
		return out, err
	}
	after, err := f.Stat()
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
// Python extraction is a trusted boundary; its path open is not part of Dafny verification.
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
	full, err := a.safeFile(relative)
	if err != nil {
		return out, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return out, err
	}
	if info.Size() > sourceLimit {
		return out, errors.New("file exceeds 32 MiB source limit")
	}
	result, err := a.worker(ctx, map[string]any{"operation": "read_document", "path": full, "offset": offset, "max_chars": maxChars, "max_bytes": sourceLimit, "force_ocr": forceOCR})
	if err != nil {
		return out, err
	}
	after, err := os.Stat(full)
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

// A single subprocess at a time prevents document, text and image models coexisting.
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
	cmd := exec.CommandContext(ctx, a.Python, a.Worker)
	cmd.Env = append(os.Environ(), "NAS_DATA="+a.Data, "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "TOKENIZERS_PARALLELISM=false")
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return result, fmt.Errorf("worker failed: %w", err)
	}
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		return result, fmt.Errorf("invalid worker response: %w", err)
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
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
			log.Printf("query embedding unavailable: %v", err)
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
	root, err := os.OpenRoot(a.Root)
	if err != nil {
		return out, err
	}
	defer root.Close()
	stop := errors.New("bounded live search stopped")
	truncated := func(message string) error { out.Truncated = true; out.Warning = message; return stop }
	var walk func(string) error
	walk = func(directory string) error {
		if err := walkContext.Err(); err != nil {
			return err
		}
		f, err := root.OpenFile(directory, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
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
				out.Visited++
				if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				relative := entry.Name()
				if directory != "." {
					relative = directory + "/" + relative
				}
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
	err = walk(".")
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
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return map[string]any{"index": stats, "scanning": a.scanning, "last_scan": a.lastScan, "scan_error": a.scanError, "semantic": a.Semantic, "photos": a.Photos, "ocr": ocrEnabled(), "current_file": a.currentFile, "scan_stage": a.scanStage, "file_started_at": a.fileStartedAt, "scan_started_at": a.scanStartedAt}, nil
}

var excluded = map[string]bool{"node_modules": true, "vendor": true, "build": true, "dist": true, "__pycache__": true, "WDTimeMachine": true, "Time Machine": true, "@eaDir": true, "@Recycle": true}

func supported(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return strings.Contains("|txt|md|rst|csv|tsv|json|jsonl|xml|yaml|yml|toml|ini|cfg|log|py|go|rs|java|js|jsx|ts|tsx|c|h|cpp|hpp|cs|sh|bash|sql|dfy|css|scss|vue|svelte|rb|php|swift|kt|kts|r|tex|html|htm|pdf|docx|xlsx|xlsm|pptx|jpg|jpeg|png|webp|gif|bmp|tif|tiff|", "|"+strings.TrimPrefix(ext, ".")+"|")
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
		hash, e := fingerprint(full)
		if e != nil {
			return e
		}
		if !forceRefresh && !policy.ShouldReplace(old[rel], hash) {
			return nil
		}
		if e = a.indexFile(ctx, full, rel, hash); e != nil {
			log.Printf("index %s: %v", rel, e)
			// Old chunks describe a different revision and must not survive a failed update.
			if removeErr := a.Store.Remove(rel); removeErr != nil {
				return removeErr
			}
			return a.Store.RecordError(rel, e.Error())
		}
		return nil
	})
	if err == nil {
		for path := range old {
			if !seen[path] {
				if e := a.Store.Remove(path); e != nil {
					return e
				}
			}
		}
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
	a.setScanProgress(relative, "extracting")
	extract, err := a.worker(ctx, map[string]any{"operation": "extract", "path": full, "max_bytes": 32 * 1024 * 1024, "max_chars": 200000})
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
		if a.Photos && a.Semantic {
			a.setScanProgress(relative, "embedding_photo")
			r, e := a.worker(ctx, map[string]any{"operation": "image_embed", "path": full, "max_bytes": 32 * 1024 * 1024})
			if e != nil {
				return e
			}
			vectors = r.Vectors
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
	after, err := fingerprint(full)
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
	return a.Store.Replace(store.File{Path: relative, Fingerprint: hash, Kind: extract.Kind, Metadata: string(encoded)}, chunks, vectors)
}
