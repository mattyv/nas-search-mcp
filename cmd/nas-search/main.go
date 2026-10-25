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
	"github.com/mattyv/nas-search-mcp/internal/oauth"
	"github.com/mattyv/nas-search-mcp/internal/photos"
	"github.com/mattyv/nas-search-mcp/internal/store"
	"github.com/mattyv/nas-search-mcp/internal/upgrade"
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
type LinkInput struct {
	Path string `json:"path"`
}
type PhotosPeopleInput struct {
	Query  string `json:"query,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type PhotosPersonInput struct {
	PersonID int64 `json:"person_id"`
	Offset   int   `json:"offset,omitempty"`
	Limit    int   `json:"limit,omitempty"`
}
type BrowseOutput struct {
	Files []store.File `json:"files"`
}
type ReadOutput struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		hash, err := passwordHash(os.Stdin)
		if err != nil {
			log.Fatal("password must contain 12 to 72 bytes")
		}
		// Setup captures this output directly into the private environment file.
		os.Stdout.Write(hash)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "revoke-oauth" {
		if err := oauth.RevokeAll(env("NAS_OAUTH_DB", env("NAS_DATA", "/data")+"/oauth.sqlite3")); err != nil {
			log.Fatal("OAuth revocation failed")
		}
		return
	}
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
	stdio := len(os.Args) > 1 && os.Args[1] == "--stdio"
	token := os.Getenv("NAS_TOKEN")
	if !stdio && len(token) < 24 {
		log.Fatal("NAS_TOKEN must contain at least 24 characters")
	}
	if _, err := configuredPhotos(); err != nil {
		log.Fatal("Synology Photos configuration invalid; set NAS_SYNOLOGY_PHOTOS_SOCKET to an absolute Unix socket path")
	}
	a, err := app.New(env("NAS_ROOT", "/files"), env("NAS_DATA", "/data"), env("NAS_PYTHON", "python3"), env("NAS_WORKER", "/app/worker/worker.py"), os.Getenv("NAS_VEC_EXTENSION"), enabled("NAS_SEMANTIC", true), enabled("NAS_PHOTOS", false))
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	if err = a.ConfigureAccess(folderList(os.Getenv("NAS_ALLOWED_FOLDERS")), folderList(os.Getenv("NAS_DENIED_FOLDERS"))); err != nil {
		log.Fatal("invalid folder access configuration")
	}
	if !stdio {
		if err = configureLinks(a); err != nil {
			log.Fatal("invalid download link configuration: set NAS_DOWNLOAD_URL to an http(s) origin and NAS_DOWNLOAD_LINK_MINUTES to 1-60")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := buildMCPServer(a)
	var publicServer *http.Server
	if issuer := os.Getenv("NAS_OAUTH_ISSUER"); issuer != "" && !stdio {
		auth, authErr := oauth.New(oauth.Config{ServiceUpgrades: os.Getenv("NAS_UPGRADE_DIR") != "", Issuer: issuer, DatabasePath: env("NAS_OAUTH_DB", a.Data+"/oauth.sqlite3"), PasswordHash: os.Getenv("NAS_OAUTH_PASSWORD_HASH"), ClientID: os.Getenv("NAS_OAUTH_CLIENT_ID"), ClientSecretHash: os.Getenv("NAS_OAUTH_CLIENT_SECRET_HASH"), RedirectURIs: folderList(os.Getenv("NAS_OAUTH_LOCAL_REDIRECTS"))})
		if authErr != nil {
			log.Fatal("OAuth configuration invalid; refusing to start")
		}
		defer auth.Close()
		publicServer = &http.Server{Addr: env("NAS_OAUTH_LISTEN", "127.0.0.1:8082"), Handler: buildPublicHandler(server, auth, a), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		go func() {
			if e := publicServer.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
				log.Fatal("OAuth listener failed")
			}
		}()
	}
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
	if stdio {
		if err = server.Run(ctx, &mcp.StdioTransport{}); err != nil {
			log.Fatal("stdio transport failed")
		}
		return
	}
	mux := buildPrivateHandler(server, token, a)
	httpServer := &http.Server{Addr: env("NAS_LISTEN", "127.0.0.1:8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
		if publicServer != nil {
			publicServer.Shutdown(shutdown)
		}
	}()
	log.Printf("NAS search listening on %s (bearer authentication required)", httpServer.Addr)
	if err = httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func buildHTTPHandler(a *app.App, token string) http.Handler {
	return buildPrivateHandler(buildMCPServer(a), token, a)
}

// configureLinks enables download links on NAS_DOWNLOAD_URL, else on the public OAuth origin.
func configureLinks(a *app.App) error {
	base := env("NAS_DOWNLOAD_URL", os.Getenv("NAS_OAUTH_ISSUER"))
	if base == "" {
		return nil
	}
	minutes, err := strconv.Atoi(env("NAS_DOWNLOAD_LINK_MINUTES", "10"))
	if err != nil {
		return err
	}
	return a.ConfigureLinks(base, time.Duration(minutes)*time.Minute)
}

func folderList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func configuredPhotos() (*photos.Client, error) {
	if socket := os.Getenv("NAS_SYNOLOGY_PHOTOS_SOCKET"); socket != "" {
		return photos.New(socket)
	}
	return nil, nil
}

func buildMCPServer(a *app.App) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "nas-search", Version: "0.1.0"}, nil)
	if client, configErr := configuredPhotos(); client != nil || configErr != nil {
		mcp.AddTool(server, &mcp.Tool{Name: "photos_people", Description: "Find named People albums in Synology Photos. query filters the existing labels; omit it to browse. This uses Synology's saved face groups and names, not new face recognition. Results contain person IDs for photos_for_person. offset defaults to 0 (maximum 100000); limit defaults to 20 (maximum 100). Follow next_offset for another page; truncated means more results may exist. Names and recognition can be incomplete or incorrect. Read-only metadata lookup; it does not rename, merge, share, or delete anything.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest, in PhotosPeopleInput) (*mcp.CallToolResult, photos.PeopleResult, error) {
			if !a.SynologyPhotosAllowed() {
				return nil, photos.PeopleResult{}, errors.New("current folder restrictions block Synology Photos: require the complete Photos root with no denied Photos subfolders")
			}
			if configErr != nil {
				return nil, photos.PeopleResult{}, errors.New("Synology Photos configuration invalid; check the host broker installation")
			}
			out, err := client.People(ctx, photos.PeopleInput{Query: in.Query, Offset: in.Offset, Limit: in.Limit})
			return nil, out, err
		})
		mcp.AddTool(server, &mcp.Tool{Name: "photos_for_person", Description: "List photo/video metadata in an existing Synology Photos People album. First use photos_people to resolve the name, then pass its person_id. offset defaults to 0 (maximum 100000); limit defaults to 20 (maximum 100). Follow next_offset for another page; truncated means more results may exist. Results are Synology's assigned matches, not independently verified identities. This lookup returns IDs, filenames and capture times. Use photos_preview with the person_id and a returned photo ID to show a small image preview. Original download links and filesystem paths are not returned. Read-only; it does not change photos or albums.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest, in PhotosPersonInput) (*mcp.CallToolResult, photos.PhotosResult, error) {
			if !a.SynologyPhotosAllowed() {
				return nil, photos.PhotosResult{}, errors.New("current folder restrictions block Synology Photos: require the complete Photos root with no denied Photos subfolders")
			}
			if configErr != nil {
				return nil, photos.PhotosResult{}, errors.New("Synology Photos configuration invalid; check the host broker installation")
			}
			out, err := client.ForPerson(ctx, photos.PhotosInput{PersonID: in.PersonID, Offset: in.Offset, Limit: in.Limit})
			return nil, out, err
		})
		mcp.AddTool(server, &mcp.Tool{Name: "photos_preview", Description: "Return a small JPEG preview for one photo/video item in a Synology Photos People album. First use photos_people and photos_for_person to obtain person_id and a matching photo_id; a People album cover ID is not a photo ID. The host checks that the item belongs to that person's album. Returns MCP image content that the client may display in chat. This uses Synology's stored album assignment; do not infer or confirm identity from the image. Read-only; no original download, public link, file path, or photo modification.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest, in photos.PreviewInput) (*mcp.CallToolResult, any, error) {
			if !a.SynologyPhotosAllowed() {
				return nil, nil, errors.New("current folder restrictions block Synology Photos: require the complete Photos root with no denied Photos subfolders")
			}
			if configErr != nil {
				return nil, nil, errors.New("Synology Photos configuration invalid; check the host broker installation")
			}
			out, err := client.Preview(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: out.Data, MIMEType: out.MIMEType}}}, nil, nil
		})
	}
	searchDescription := "Search indexed NAS files using keywords and meaning. Set photos=true for visual photo search. Results include source paths and page/section locations. If the user names a file or this search misses it, use find_files to match live filename and folder terms. Read-only."
	if os.Getenv("NAS_SYNOLOGY_PHOTOS_SOCKET") != "" {
		searchDescription += " For photos of a named person, use photos_people and photos_for_person; photos=true is the separate experimental visual-search index."
	}
	mcp.AddTool(server, &mcp.Tool{Name: "search", Description: searchDescription}, func(ctx context.Context, req *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, app.SearchResult, error) {
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
	fetchDescription := "Fetch exact original file bytes, including binary PDF/Office/image files, as base64 batches. max_bytes defaults to 65536, capped at 262144; source limit 32 MiB. Reassemble decoded data locally, following next_offset until complete. Send the first response's version as expected_version on every subsequent batch to reject changed files. This does not create a chat attachment automatically. Read-only."
	if a.LinksEnabled() {
		fetchDescription = strings.Replace(fetchDescription, "automatically.", "automatically; to let the user see or save the file, use download_link instead.", 1)
	}
	mcp.AddTool(server, &mcp.Tool{Name: "fetch_file", Description: fetchDescription}, func(ctx context.Context, req *mcp.CallToolRequest, in FetchInput) (*mcp.CallToolResult, app.FetchResult, error) {
		out, err := a.FetchFile(in.Path, in.Offset, in.MaxBytes, in.ExpectedVersion)
		return nil, out, err
	})
	if a.LinksEnabled() {
		mcp.AddTool(server, &mcp.Tool{Name: "download_link", Description: "Create a short-lived link that opens an original NAS file (PDF, photo, Office document, any format up to 32 MiB) in the user's own browser. Use this whenever the user wants to see, open, view, or save a file. Reply with the returned url as a clickable markdown link; you cannot open it yourself, and fetch_file is not needed first. PDFs, common images, and plain text open in the browser; other formats download. Anyone holding the link can open that one file until expires_at. Read-only."}, func(ctx context.Context, req *mcp.CallToolRequest, in LinkInput) (*mcp.CallToolResult, app.LinkResult, error) {
			out, err := a.CreateLink(in.Path)
			if err != nil {
				return nil, out, err
			}
			label := strings.NewReplacer("[", "\\[", "]", "\\]").Replace(out.Name)
			text := fmt.Sprintf("[%s](%s)\n\nShow the user this link. It expires at %s; ask for a new link after that.", label, out.URL, out.ExpiresAt)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
		})
	}
	mcp.AddTool(server, &mcp.Tool{Name: "index_status", Description: "Show the active scan file, stage and start times, indexed file/chunk counts, errors, and whether OCR/semantic/photo indexing is enabled."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := a.Status()
		return nil, out, err
	})
	if dir := os.Getenv("NAS_UPGRADE_DIR"); dir != "" {
		q := upgrade.Queue{Dir: dir}
		mcp.AddTool(server, &mcp.Tool{Name: "upgrade_from_main", Description: "Upgrade the NAS search service to the latest main commit after its CI checks and image publication pass. Call only when the user explicitly requests an upgrade. Changes the running service and may briefly interrupt search. No branch, image, command, or path arguments are accepted. Follow with upgrade_status after reconnecting.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPointer(true), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]any, error) {
			out, err := q.Request()
			return nil, out, err
		})
		mcp.AddTool(server, &mcp.Tool{Name: "upgrade_status", Description: "Read the last requested NAS service upgrade state and installed commit. Read-only.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]any, error) {
			out, err := q.Status()
			return nil, out, err
		})
	}
	return server
}

func buildPrivateHandler(server *mcp.Server, token string, a *app.App) http.Handler {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", app.Protect(token, handler))
	mux.Handle("/download/", hardenDownloads(http.HandlerFunc(a.ServeLink), a.LinkKnown, log.Default()))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); fmt.Fprint(w, "ok") })
	return mux
}

func boolPointer(value bool) *bool { return &value }
