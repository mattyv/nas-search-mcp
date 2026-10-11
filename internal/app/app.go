package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
}
type WorkerResult struct {
	Kind     string `json:"kind"`
	Sections []struct {
		Location string `json:"location"`
		Text     string `json:"text"`
	} `json:"sections"`
	Metadata  map[string]any `json:"metadata"`
	Truncated bool           `json:"truncated"`
	Vectors   [][]float32    `json:"vectors"`
	Error     string         `json:"error"`
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
	f, err := root.Open(filepath.FromSlash(relative))
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
	f, err := a.openFile(relative)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(policy.ClampLimit(limit, 65536))))
	if bytes.Contains(b, []byte{0}) {
		return "", errors.New("binary file: use search results and metadata")
	}
	return strings.ToValidUTF8(string(b), "�"), err
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
func (a *App) Status() (map[string]any, error) {
	stats, err := a.Store.Status()
	if err != nil {
		return nil, err
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return map[string]any{"index": stats, "scanning": a.scanning, "last_scan": a.lastScan, "scan_error": a.scanError, "semantic": a.Semantic, "photos": a.Photos}, nil
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
func (a *App) Scan(ctx context.Context) error {
	if !a.scanMu.TryLock() {
		return errors.New("scan already running")
	}
	defer a.scanMu.Unlock()
	a.stateMu.Lock()
	a.scanning = true
	a.scanError = ""
	a.stateMu.Unlock()
	defer func() {
		a.stateMu.Lock()
		a.scanning = false
		a.lastScan = time.Now().UTC().Format(time.RFC3339)
		a.stateMu.Unlock()
	}()
	old, err := a.Store.Fingerprints()
	if err != nil {
		return err
	}
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
		if !policy.ShouldReplace(old[rel], hash) {
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
	if err != nil {
		a.stateMu.Lock()
		a.scanError = err.Error()
		a.stateMu.Unlock()
	}
	return err
}
func (a *App) indexFile(ctx context.Context, full, relative, hash string) error {
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
			r, e := a.worker(ctx, map[string]any{"operation": "image_embed", "path": full, "max_bytes": 32 * 1024 * 1024})
			if e != nil {
				return e
			}
			vectors = r.Vectors
		}
	} else if a.Semantic && len(chunks) > 0 {
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
	return a.Store.Replace(store.File{Path: relative, Fingerprint: hash, Kind: extract.Kind, Metadata: string(encoded)}, chunks, vectors)
}
