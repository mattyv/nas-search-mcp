package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/mattyv/nas-search-mcp/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(c)
}
func TestMCPHTTPTools(t *testing.T) {
	t.Setenv("NAS_OCR", "true")
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "doc.txt"), []byte("kitchen renovation"), 0600); e != nil {
		t.Fatal(e)
	}
	worker, e := filepath.Abs("../../worker/worker.py")
	if e != nil {
		t.Fatal(e)
	}
	python := os.Getenv("NAS_PYTHON")
	if python == "" {
		python = "python3"
	}
	a, e := app.New(root, t.TempDir(), python, worker, "", false, false)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = a.Store.Replace(store.File{Path: "doc.txt", Fingerprint: "test", Kind: "document"}, []store.Chunk{{Location: "text:1", Text: "kitchen renovation"}}, nil); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Replace(store.File{Path: "obsolete.pdf", Kind: "document"}, nil, nil); e != nil {
		t.Fatal(e)
	}
	token := "integration-test-token-at-least-24"
	ts := httptest.NewServer(buildHTTPHandler(a, token))
	defer ts.Close()
	for _, method := range []string{"GET", "POST", "DELETE"} {
		req, e := http.NewRequest(method, ts.URL+"/mcp", nil)
		if e != nil {
			t.Fatal(e)
		}
		resp, e := ts.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s unauthenticated got %d", method, resp.StatusCode)
		}
	}
	resp, e := ts.Client().Get(ts.URL + "/healthz")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token, base: ts.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.Name == "read_document" {
			encoded, e := json.Marshal(tool.InputSchema)
			if e != nil {
				t.Fatal(e)
			}
			var schema map[string]any
			if e = json.Unmarshal(encoded, &schema); e != nil {
				t.Fatal(e)
			}
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatal(schema)
			}
			flag, ok := properties["force_ocr"].(map[string]any)
			if !ok || flag["type"] != "boolean" {
				t.Fatal("missing boolean force_ocr schema", schema)
			}
			if required, ok := schema["required"].([]any); ok {
				for _, name := range required {
					if name == "force_ocr" {
						t.Fatal("force_ocr must be optional")
					}
				}
			}
		}

		encoded, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err = json.Unmarshal(encoded, &schema); err != nil || schema["type"] != "object" {
			t.Fatalf("%s output schema must be object: %s (%v)", tool.Name, encoded, err)
		}
	}
	if len(names) != 7 || !names["search"] || !names["browse"] || !names["read_file"] || !names["index_status"] || !names["read_document"] || !names["fetch_file"] || !names["find_files"] {
		t.Fatal(names)
	}
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || result.IsError {
			t.Fatalf("%s: %v %v", name, result, e)
		}
		b, e := json.Marshal(result.StructuredContent)
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		if e = json.Unmarshal(b, &out); e != nil {
			t.Fatalf("%s output must be object: %s", name, b)
		}
		return out
	}
	browse := call("browse", map[string]any{})
	files, ok := browse["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatal(browse)
	}

	if e = os.WriteFile(filepath.Join(root, "raw.backup"), []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	found := call("find_files", map[string]any{"query": "RAW .backup"})
	live, ok := found["files"].([]any)
	if !ok || len(live) != 1 || live[0].(map[string]any)["path"] != "raw.backup" {
		t.Fatal(found)
	}
	invalidFind, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "find_files", Arguments: map[string]any{"query": "doc", "prefix": "../"}})
	if e != nil || !invalidFind.IsError {
		t.Fatalf("invalid find prefix accepted: %v %v", invalidFind, e)
	}
	search := call("search", map[string]any{"query": "renovation"})
	hits, ok := search["hits"].([]any)
	if !ok || len(hits) != 1 {
		t.Fatal(search)
	}
	read := call("read_file", map[string]any{"path": "doc.txt", "max_bytes": 7})
	if read["text"] != "kitchen" {
		t.Fatal(read)
	}

	document := call("read_document", map[string]any{"path": "doc.txt", "offset": 8, "max_chars": 10})
	if document["text"] != "renovation" || document["complete"] != true {
		t.Fatal(document)
	}
	fetched := call("fetch_file", map[string]any{"path": "doc.txt", "max_bytes": 7})
	decoded, e := base64.StdEncoding.DecodeString(fetched["data"].(string))
	if e != nil || string(decoded) != "kitchen" || fetched["next_offset"] != float64(7) {
		t.Fatal(fetched, e)
	}
	capture := filepath.Join(t.TempDir(), "capture.py")
	script := `import json,sys
request=json.load(sys.stdin)
force=request['force_ocr']
json.dump({'kind':'document','text':'forced' if force else 'automatic','sections':[],'offset':request['offset'],'next_offset':None,'complete':True,'metadata':{'received_force_ocr':force}},sys.stdout)
`
	if e = os.WriteFile(capture, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	a.Worker = capture
	if e = os.WriteFile(filepath.Join(root, "doc.PDF"), []byte("source"), 0600); e != nil {
		t.Fatal(e)
	}
	forced := call("read_document", map[string]any{"path": "doc.PDF", "force_ocr": true})
	if forced["text"] != "forced" || forced["metadata"].(map[string]any)["received_force_ocr"] != true {
		t.Fatal(forced)
	}
	automatic := call("read_document", map[string]any{"path": "doc.txt"})
	if automatic["text"] != "automatic" {
		t.Fatal(automatic)
	}

	assertToolError := func(path, want string) {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_document", Arguments: map[string]any{"path": path, "force_ocr": true}})
		if e != nil || !result.IsError {
			t.Fatalf("expected tool error: %v %v", result, e)
		}
		encoded, e := json.Marshal(result)
		if e != nil || !strings.Contains(string(encoded), want) {
			t.Fatalf("expected %s in error: %s %v", want, encoded, e)
		}
	}
	assertToolError("doc.txt", "PDF-only")
	t.Setenv("NAS_OCR", "false")
	assertToolError("doc.PDF", "NAS_OCR")
	t.Setenv("NAS_OCR", "true")
	status := call("index_status", map[string]any{})
	if status["index"] == nil {
		t.Fatal(status)
	}
	negative, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "doc.txt", "max_bytes": -1}})
	if e != nil || !negative.IsError {
		t.Fatalf("negative max_bytes must be a tool error: %v %v", negative, e)
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "../secret"}})
	if e != nil || !result.IsError {
		t.Fatalf("traversal should be a tool error: %v %v", result, e)
	}
	result, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": ""}})
	if e != nil || !result.IsError {
		t.Fatalf("empty query should be tool error: %v %v", result, e)
	}
}
