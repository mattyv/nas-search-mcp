package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOptionalPhotosMCPTools(t *testing.T) {
	t.Setenv("NAS_SYNOLOGY_PHOTOS_SOCKET", "")
	a, err := app.New(t.TempDir(), t.TempDir(), "python3", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := os.MkdirAll(filepath.Join(a.Root, "Photos", "Family"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureAccess([]string{"Photos"}, nil); err != nil {
		t.Fatal(err)
	}
	connect := func() (*mcp.ClientSession, context.Context) {
		t.Helper()
		server := httptest.NewServer(buildHTTPHandler(a, "photos-test-token-with-enough-length"))
		t.Cleanup(server.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		client := mcp.NewClient(&mcp.Implementation{Name: "photos-test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "photos-test-token-with-enough-length", base: server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session, ctx
	}
	session, ctx := connect()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "photos_people" || tool.Name == "photos_for_person" || tool.Name == "photos_preview" {
			t.Fatal("Photos tools enabled without explicit socket")
		}
	}
	dir, err := os.MkdirTemp("/tmp", "mcp-photos-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/people" {
			w.Write([]byte(`{"people":[{"id":3753,"name":"Lucas","item_count":2037}],"offset":0,"next_offset":null,"truncated":false}`))
			return
		}
		if r.URL.Path == "/preview" {
			var args map[string]any
			json.NewDecoder(r.Body).Decode(&args)
			if args["person_id"] != float64(3753) || args["photo_id"] != float64(89027) {
				t.Errorf("wrong preview IDs %#v", args)
			}
			json.NewEncoder(w).Encode(map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0, 0x01, 0x02, 0xff, 0xd9})})
			return
		}
		if r.URL.Path != "/photos" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var args map[string]any
		json.NewDecoder(r.Body).Decode(&args)
		if args["person_id"] != float64(3753) {
			t.Errorf("wrong person: %#v", args)
		}
		w.Write([]byte(`{"photos":[{"id":123,"filename":"IMG.jpg","taken_time":1700000000,"type":"photo"}],"offset":0,"next_offset":null,"truncated":false}`))
	})}
	go host.Serve(listener)
	defer host.Close()
	t.Setenv("NAS_SYNOLOGY_PHOTOS_SOCKET", socket)
	session, ctx = connect()
	list, err = session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range list.Tools {
		if tool.Name == "photos_people" || tool.Name == "photos_for_person" || tool.Name == "photos_preview" {
			found[tool.Name] = true
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s not read-only", tool.Name)
			}
		}
	}
	if !found["photos_people"] || !found["photos_for_person"] || !found["photos_preview"] {
		t.Fatalf("missing tools: %#v", found)
	}
	people, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "photos_people", Arguments: map[string]any{"query": "Lucas"}})
	if err != nil || people.IsError {
		t.Fatalf("people: %#v %v", people, err)
	}
	peopleJSON, err := json.Marshal(people.StructuredContent)
	if err != nil || !strings.Contains(string(peopleJSON), `"name":"Lucas"`) || !strings.Contains(string(peopleJSON), `"item_count":2037`) {
		t.Fatalf("missing structured people metadata: %s %v", peopleJSON, err)
	}
	photos, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "photos_for_person", Arguments: map[string]any{"person_id": 3753}})
	if err != nil || photos.IsError {
		t.Fatalf("photos: %#v %v", photos, err)
	}
	photosJSON, err := json.Marshal(photos.StructuredContent)
	if err != nil || !strings.Contains(string(photosJSON), `"filename":"IMG.jpg"`) {
		t.Fatalf("missing structured photo metadata: %s %v", photosJSON, err)
	}
	bad, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "photos_for_person", Arguments: map[string]any{"person_id": -1}})
	if err != nil || !bad.IsError {
		t.Fatalf("invalid ID accepted: %#v %v", bad, err)
	}
	preview, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "photos_preview", Arguments: map[string]any{"person_id": 3753, "photo_id": 89027}})
	if err != nil || preview.IsError {
		t.Fatalf("preview: %#v %v", preview, err)
	}
	foundImage := false
	for _, content := range preview.Content {
		if image, ok := content.(*mcp.ImageContent); ok {
			foundImage = true
			if image.MIMEType != "image/jpeg" || string(image.Data) != string([]byte{0xff, 0xd8, 0xff, 0xe0, 0x01, 0x02, 0xff, 0xd9}) {
				t.Fatalf("wrong image content: %#v", image)
			}
		}
	}
	if !foundImage {
		t.Fatal("preview did not return MCP image content")
	}
	badPreview, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "photos_preview", Arguments: map[string]any{"person_id": 3753, "photo_id": -1}})
	if err != nil || !badPreview.IsError {
		t.Fatalf("invalid preview accepted: %#v %v", badPreview, err)
	}
	if requests.Load() != 3 {
		t.Fatalf("invalid query reached root broker, count=%d", requests.Load())
	}
}

func TestPhotosConfigurationFailsClosedWithoutRequiringBrokerAtBoot(t *testing.T) {
	t.Setenv("NAS_SYNOLOGY_PHOTOS_SOCKET", "")
	if client, err := configuredPhotos(); client != nil || err != nil {
		t.Fatalf("unset config: %v %v", client, err)
	}
	t.Setenv("NAS_SYNOLOGY_PHOTOS_SOCKET", "relative.sock")
	if client, err := configuredPhotos(); client != nil || err == nil {
		t.Fatal("accepted relative socket configuration")
	}
	t.Setenv("NAS_SYNOLOGY_PHOTOS_SOCKET", "/synology-photos/photos.sock")
	if client, err := configuredPhotos(); client == nil || err != nil {
		t.Fatalf("broker need not be started yet: %v", err)
	}
}

func TestPhotosMCPAccessRestrictionsNeverReachBroker(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "photos-deny-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	broker := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte(`{"error":"must-not-be-called"}`))
	})}
	go broker.Serve(listener)
	defer broker.Close()
	t.Setenv("NAS_SYNOLOGY_PHOTOS_SOCKET", socket)
	for _, scenario := range []struct {
		name        string
		configured  bool
		allow, deny []string
	}{
		{name: "unconfigured"},
		{name: "deny-all", configured: true},
		{name: "Photos-excluded", configured: true, allow: []string{"Matthew"}},
		{name: "Photos-subfolder-only", configured: true, allow: []string{"Photos/Family"}},
		{name: "Photos-denied", configured: true, allow: []string{"Photos"}, deny: []string{"Photos"}},
		{name: "Photos-subfolder-denied", configured: true, allow: []string{"Photos"}, deny: []string{"Photos/Family"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"Photos/Family", "Matthew"} {
				if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			a, err := app.New(root, t.TempDir(), "python3", "", "", false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if scenario.configured {
				if err := a.ConfigureAccess(scenario.allow, scenario.deny); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(buildHTTPHandler(a, "photos-test-token-with-enough-length"))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "photos-denial-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "photos-test-token-with-enough-length", base: server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			for _, call := range []mcp.CallToolParams{
				{Name: "photos_people", Arguments: map[string]any{"query": "Lucas"}},
				{Name: "photos_for_person", Arguments: map[string]any{"person_id": 3753}},
				{Name: "photos_preview", Arguments: map[string]any{"person_id": 3753, "photo_id": 89027}},
			} {
				out, err := session.CallTool(ctx, &call)
				if err != nil || !out.IsError {
					t.Fatalf("%s did not reject restricted access: %#v %v", call.Name, out, err)
				}
				content, _ := json.Marshal(out.Content)
				if !strings.Contains(string(content), "current folder restrictions block Synology Photos") {
					t.Errorf("%s did not explain folder restriction: %s", call.Name, content)
				}
			}
			if requests.Load() != 0 {
				t.Fatalf("restricted Photos requests reached broker: %d", requests.Load())
			}
		})
	}
}
