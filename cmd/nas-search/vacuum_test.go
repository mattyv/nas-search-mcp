package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/app"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func vacuumSession(t *testing.T) (*mcp.ClientSession, context.Context) {
	t.Helper()
	a, err := app.New(t.TempDir(), t.TempDir(), "python3", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	server := httptest.NewServer(buildHTTPHandler(a, "vacuum-test-token-with-enough-length"))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "vacuum-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "vacuum-test-token-with-enough-length", base: server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, ctx
}

func vacuumTools(t *testing.T, session *mcp.ClientSession, ctx context.Context) map[string]*mcp.Tool {
	t.Helper()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "vacuum_") {
			found[tool.Name] = tool
		}
	}
	return found
}

func TestVacuumToolsHiddenUnlessConfigured(t *testing.T) {
	t.Setenv("NAS_VACUUM_ENABLED", "")
	session, ctx := vacuumSession(t)
	if found := vacuumTools(t, session, ctx); len(found) != 0 {
		t.Fatalf("vacuum tools listed without configuration: %v", found)
	}
}

func TestVacuumToolsReachBroker(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "vacuum-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "vacuum.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var commands []map[string]any
	broker := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"name":"Robo","connected":true,"state":"charging","battery":87,"docked":true,"error":"","suction":"standard","current_room":"","cleaned_area_m2":0,"cleaning_minutes":0}`))
		case "/rooms":
			w.Write([]byte(`{"rooms":[{"id":3,"name":"Kitchen"}]}`))
		case "/command":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			commands = append(commands, body)
			w.Write([]byte(`{"accepted":true,"message":"Cleaning Kitchen"}`))
		}
	})}
	go broker.Serve(listener)
	t.Cleanup(func() { broker.Close() })
	t.Setenv("NAS_VACUUM_ENABLED", "true")
	t.Setenv("NAS_VACUUM_SOCKET", socket)

	session, ctx := vacuumSession(t)
	found := vacuumTools(t, session, ctx)
	if len(found) != 3 || found["vacuum_status"] == nil || found["vacuum_rooms"] == nil || found["vacuum_control"] == nil {
		t.Fatalf("tools=%v", found)
	}
	if !found["vacuum_status"].Annotations.ReadOnlyHint || found["vacuum_control"].Annotations.ReadOnlyHint {
		t.Fatal("read-only hints wrong")
	}

	status, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_status", Arguments: map[string]any{}})
	if err != nil || status.IsError || !strings.Contains(status.Content[0].(*mcp.TextContent).Text, `"battery":87`) {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	cleaned, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_control", Arguments: map[string]any{"action": "clean_rooms", "rooms": []int{3}}})
	if err != nil || cleaned.IsError || len(commands) != 1 || commands[0]["action"] != "clean_rooms" {
		t.Fatalf("cleaned=%#v commands=%v err=%v", cleaned, commands, err)
	}
	refused, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_control", Arguments: map[string]any{"action": "clean_rooms", "rooms": []int{99}}})
	if err != nil || !refused.IsError || len(commands) != 1 {
		t.Fatalf("unknown room reached vacuum: refused=%#v commands=%v err=%v", refused, commands, err)
	}
}

func TestVacuumConfigurationNeedsSwitchAndAbsoluteSocket(t *testing.T) {
	t.Setenv("NAS_VACUUM_ENABLED", "true")
	t.Setenv("NAS_VACUUM_SOCKET", "relative.sock")
	if _, err := configuredVacuum(); err == nil {
		t.Fatal("relative socket accepted")
	}
	t.Setenv("NAS_VACUUM_SOCKET", "")
	if client, err := configuredVacuum(); client == nil || err != nil {
		t.Fatal("enabled vacuum should use the default socket")
	}
	t.Setenv("NAS_VACUUM_ENABLED", "false")
	t.Setenv("NAS_VACUUM_SOCKET", "/data/vacuum-ipc/vacuum.sock")
	if client, err := configuredVacuum(); client != nil || err != nil {
		t.Fatal("disabled vacuum must not register tools")
	}
}
