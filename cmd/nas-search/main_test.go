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
	defer a.Store.Close()
	if e = a.Store.Replace(store.File{Path: "doc.txt", Fingerprint: "test", Kind: "document"}, []store.Chunk{{Location: "text:1", Text: "kitchen renovation"}}, nil); e != nil {
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
		encoded, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err = json.Unmarshal(encoded, &schema); err != nil || schema["type"] != "object" {
			t.Fatalf("%s output schema must be object: %s (%v)", tool.Name, encoded, err)
		}
	}
	if len(names) != 6 || !names["search"] || !names["browse"] || !names["read_file"] || !names["index_status"] || !names["read_document"] || !names["fetch_file"] {
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
	status := call("index_status", map[string]any{})
	if status["index"] == nil {
		t.Fatal(status)
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
