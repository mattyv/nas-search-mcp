package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
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
		case "/command", "/setting":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			body["path"] = r.URL.Path
			commands = append(commands, body)
			w.Write([]byte(`{"accepted":true,"message":"Cleaning Kitchen"}`))
		case "/details":
			w.Write([]byte(`{"consumables":{"filter":64},"station":{"dust_bag":"installed"},"settings":{"volume":"40"}}`))
		case "/history":
			w.Write([]byte(`{"entries":[{"time":1795000000,"minutes":30,"area_m2":40}]}`))
		case "/obstacles":
			w.Write([]byte(`{"obstacles":[{"index":2,"type":"sock","x":10,"y":20,"confidence":90,"has_photo":true}]}`))
		case "/obstacle_photo":
			var picture bytes.Buffer
			jpeg.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil)
			w.Write([]byte(`{"mime_type":"image/jpeg","data":"` + base64.StdEncoding.EncodeToString(picture.Bytes()) + `"}`))
		case "/move":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			body["path"] = r.URL.Path
			commands = append(commands, body)
			w.Write([]byte(`{"accepted":true,"message":"Moved"}`))
		case "/map":
			var picture bytes.Buffer
			png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 3, 2)))
			w.Write([]byte(`{"mime_type":"image/png","data":"` + base64.StdEncoding.EncodeToString(picture.Bytes()) + `"}`))
		}
	})}
	go broker.Serve(listener)
	t.Cleanup(func() { broker.Close() })
	t.Setenv("NAS_VACUUM_ENABLED", "true")
	t.Setenv("NAS_VACUUM_SOCKET", socket)

	session, ctx := vacuumSession(t)
	found := vacuumTools(t, session, ctx)
	readOnly := []string{"vacuum_status", "vacuum_rooms", "vacuum_details", "vacuum_history", "vacuum_map", "vacuum_obstacles"}
	acting := []string{"vacuum_control", "vacuum_settings", "vacuum_drive"}
	if len(found) != len(readOnly)+len(acting)+1 {
		t.Fatalf("tools=%v", found)
	}
	for _, name := range readOnly {
		if found[name] == nil || !found[name].Annotations.ReadOnlyHint {
			t.Fatalf("%s missing or not read-only", name)
		}
	}
	for _, name := range acting {
		if found[name] == nil || found[name].Annotations.ReadOnlyHint {
			t.Fatalf("%s missing or marked read-only", name)
		}
	}
	for _, name := range readOnly {
		args := map[string]any{}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError || len(result.Content) == 0 {
			t.Fatalf("%s: result=%#v err=%v", name, result, err)
		}
	}
	if found["vacuum_obstacle_photo"] == nil || !found["vacuum_obstacle_photo"].Annotations.ReadOnlyHint {
		t.Fatal("vacuum_obstacle_photo missing or not read-only")
	}
	shot, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_obstacle_photo", Arguments: map[string]any{"index": 2}})
	if err != nil || shot.IsError {
		t.Fatalf("shot=%#v err=%v", shot, err)
	}
	if _, ok := shot.Content[0].(*mcp.ImageContent); !ok {
		t.Fatalf("obstacle photo content=%#v", shot.Content)
	}
	picture, _ := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_map", Arguments: map[string]any{}})
	if _, ok := picture.Content[0].(*mcp.ImageContent); !ok {
		t.Fatalf("vacuum_map content=%#v", picture.Content)
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
	set, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_settings", Arguments: map[string]any{"setting": "room_suction", "value": "turbo", "room": 3}})
	if err != nil || set.IsError || len(commands) != 2 || commands[1]["path"] != "/setting" || commands[1]["value"] != float64(3) {
		t.Fatalf("set=%#v commands=%v err=%v", set, commands, err)
	}
	bad, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_settings", Arguments: map[string]any{"setting": "firmware", "value": "1"}})
	if err != nil || !bad.IsError || len(commands) != 2 {
		t.Fatalf("unknown setting reached vacuum: bad=%#v err=%v", bad, err)
	}
	moved, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vacuum_drive", Arguments: map[string]any{"turn": 90}})
	if err != nil || moved.IsError || len(commands) != 3 || commands[2]["path"] != "/move" {
		t.Fatalf("moved=%#v commands=%v err=%v", moved, commands, err)
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

func TestVacuumCameraSwitchHidesObstacleTools(t *testing.T) {
	t.Setenv("NAS_VACUUM_ENABLED", "true")
	t.Setenv("NAS_VACUUM_SOCKET", "/tmp/vacuum-test-unused/vacuum.sock")
	t.Setenv("NAS_VACUUM_CAMERA", "false")
	session, ctx := vacuumSession(t)
	found := vacuumTools(t, session, ctx)
	if found["vacuum_obstacles"] != nil || found["vacuum_obstacle_photo"] != nil || found["vacuum_status"] == nil {
		t.Fatalf("camera switch ignored: %v", found)
	}
}
