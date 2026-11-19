package vacuum

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func broker(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "vacuum-test-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "vacuum.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close(); os.RemoveAll(dir) })
	return socket
}

func newClient(t *testing.T, socket string) *Client {
	t.Helper()
	client, err := New(socket)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

const statusJSON = `{"name":"Robo","connected":true,"state":"charging","battery":87,"docked":true,"error":"","suction":"standard","current_room":"Kitchen","cleaned_area_m2":12,"cleaning_minutes":20}`

func TestStatusUsesFixedRoute(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/status" || r.Host != "dreame-vacuum" {
			t.Errorf("unexpected request %s %s %s", r.Method, r.URL.Path, r.Host)
		}
		w.Write([]byte(statusJSON))
	})
	out, err := newClient(t, socket).Status(context.Background())
	if err != nil || out.Name != "Robo" || out.Battery != 87 || !out.Docked || out.State != "charging" || out.CurrentRoom != "Kitchen" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestStatusRejectsOutOfRangeBattery(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"Robo","connected":true,"state":"idle","battery":140,"docked":false,"error":"","suction":"","current_room":"","cleaned_area_m2":0,"cleaning_minutes":0}`))
	})
	if _, err := newClient(t, socket).Status(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("err=%v", err)
	}
}

func TestStatusRejectsUnknownFields(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"Robo","token":"secret"}`))
	})
	if _, err := newClient(t, socket).Status(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("err=%v", err)
	}
}

func TestRoomsValidatesReply(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rooms" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"rooms":[{"id":3,"name":"Kitchen"},{"id":5,"name":"Hall"}]}`))
	})
	out, err := newClient(t, socket).Rooms(context.Background())
	if err != nil || len(out.Rooms) != 2 || out.Rooms[1].Name != "Hall" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	bad := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"rooms":[{"id":-1,"name":"Kitchen"}]}`))
	})
	if _, err := newClient(t, bad).Rooms(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("negative room id accepted: %v", err)
	}
}

func TestSimpleCommandForwardsWithoutRoomLookup(t *testing.T) {
	var paths []string
	var got commandRequest
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"accepted":true,"message":"Returning to dock"}`))
	})
	out, err := newClient(t, socket).Command(context.Background(), CommandInput{Action: "dock"})
	if err != nil || !out.Accepted || out.Message != "Returning to dock" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	if len(paths) != 1 || paths[0] != "/command" || got.Action != "dock" || len(got.Rooms) != 0 || len(got.Zones) != 0 || got.Point != nil || got.Repeats != 0 || got.Suction != -1 {
		t.Fatalf("paths=%v got=%#v", paths, got)
	}
}

func TestCleanRoomsChecksKnownRoomsThenForwards(t *testing.T) {
	var got commandRequest
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Write([]byte(`{"rooms":[{"id":3,"name":"Kitchen"},{"id":5,"name":"Hall"}]}`))
		case "/command":
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Cleaning 2 rooms"}`))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	})
	_, err := newClient(t, socket).Command(context.Background(), CommandInput{Action: "clean_rooms", Rooms: []int{5, 3}, Suction: "turbo"})
	if err != nil || got.Action != "clean_rooms" || len(got.Rooms) != 2 || got.Repeats != 1 || got.Suction != 3 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestCommandRejectsBeforeReachingVacuum(t *testing.T) {
	commands := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/command" {
			commands++
		}
		w.Write([]byte(`{"rooms":[{"id":3,"name":"Kitchen"}]}`))
	})
	client := newClient(t, socket)
	for _, in := range []CommandInput{
		{Action: "factory_reset"},
		{Action: "start", Rooms: []int{3}},
		{Action: "clean_rooms", Rooms: []int{9}},
		{Action: "clean_rooms", Rooms: []int{3, 3}},
		{Action: "clean_rooms"},
		{Action: "clean_rooms", Rooms: []int{3}, Repeats: 4},
		{Action: "clean_rooms", Rooms: []int{3}, Suction: "max"},
		{Action: "pause", Suction: "quiet"},
	} {
		if _, err := client.Command(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if commands != 0 {
		t.Fatalf("%d invalid commands reached the vacuum", commands)
	}
}

func TestUnavailableBroker(t *testing.T) {
	client := newClient(t, "/tmp/vacuum-test-missing/vacuum.sock")
	if _, err := client.Status(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewRejectsRelativeSocket(t *testing.T) {
	if _, err := New("vacuum.sock"); err == nil {
		t.Fatal("relative socket accepted")
	}
}

const mapRooms = `{"rooms":[{"id":3,"name":"Kitchen","box":[0,0,3000,3000]}],"bounds":[-5000,-5000,5000,5000]}`

func TestRoomsCarryBoxesAndBounds(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(mapRooms)) })
	out, err := newClient(t, socket).Rooms(context.Background())
	if err != nil || out.Bounds == nil || out.Bounds[2] != 5000 || out.Rooms[0].Box == nil || out.Rooms[0].Box[2] != 3000 {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	bad := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"rooms":[],"bounds":[10,0,5,5]}`))
	})
	if _, err := newClient(t, bad).Rooms(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("inverted bounds accepted: %v", err)
	}
}

func TestZoneAndSpotCleaningCheckedAgainstMap(t *testing.T) {
	var got commandRequest
	commands := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Write([]byte(mapRooms))
		case "/command":
			commands++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"ok"}`))
		}
	})
	client := newClient(t, socket)
	if _, err := client.Command(context.Background(), CommandInput{Action: "clean_zones", Zones: [][4]int{{0, 0, 1000, 1000}}, Suction: "quiet"}); err != nil {
		t.Fatal(err)
	}
	if got.Action != "clean_zones" || len(got.Zones) != 1 || got.Repeats != 1 || got.Suction != 0 || len(got.Rooms) != 0 {
		t.Fatalf("got=%#v", got)
	}
	if _, err := client.Command(context.Background(), CommandInput{Action: "clean_spot", Point: &[2]int{100, 200}, Repeats: 2}); err != nil {
		t.Fatal(err)
	}
	if got.Point == nil || got.Point[1] != 200 || got.Repeats != 2 {
		t.Fatalf("got=%#v", got)
	}
	for _, in := range []CommandInput{
		{Action: "clean_zones"},
		{Action: "clean_zones", Zones: [][4]int{{0, 0, 9000, 1000}}},
		{Action: "clean_zones", Zones: [][4]int{{0, 0, 1000, 1000}}, Rooms: []int{3}},
		{Action: "clean_spot"},
		{Action: "clean_spot", Point: &[2]int{0, 9000}},
		{Action: "dock", Zones: [][4]int{{0, 0, 1000, 1000}}},
		{Action: "start", Point: &[2]int{0, 0}},
	} {
		if _, err := client.Command(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if commands != 2 {
		t.Fatalf("%d commands reached the vacuum", commands)
	}
}

func TestZoneCleaningNeedsSavedMap(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"rooms":[]}`)) })
	_, err := newClient(t, socket).Command(context.Background(), CommandInput{Action: "clean_spot", Point: &[2]int{0, 0}})
	if !errors.Is(err, ErrNoMap) {
		t.Fatalf("err=%v", err)
	}
}

func TestBaseStationActionsAreSimple(t *testing.T) {
	var got commandRequest
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"accepted":true,"message":"Emptying"}`))
	})
	for _, action := range []string{"empty_dustbin", "wash_mops", "dry_mops", "stop_drying"} {
		if _, err := newClient(t, socket).Command(context.Background(), CommandInput{Action: action}); err != nil || got.Action != action {
			t.Fatalf("%s: got=%#v err=%v", action, got, err)
		}
	}
}

func TestSettingsParsedAndChecked(t *testing.T) {
	var got settingRequest
	settings := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Write([]byte(mapRooms))
		case "/setting":
			settings++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Done"}`))
		}
	})
	client := newClient(t, socket)
	cases := []struct {
		in   SettingInput
		want settingRequest
	}{
		{SettingInput{Setting: "cleaning_mode", Value: "mopping_after_sweeping"}, settingRequest{"cleaning_mode", 3, 0}},
		{SettingInput{Setting: "suction", Value: "Turbo"}, settingRequest{"suction", 3, 0}},
		{SettingInput{Setting: "mop_wetness", Value: "wet"}, settingRequest{"mop_wetness", 2, 0}},
		{SettingInput{Setting: "volume", Value: "40"}, settingRequest{"volume", 40, 0}},
		{SettingInput{Setting: "dnd", Value: "on"}, settingRequest{"dnd", 1, 0}},
		{SettingInput{Setting: "dnd_start", Value: "22:30"}, settingRequest{"dnd_start", 1350, 0}},
		{SettingInput{Setting: "child_lock", Value: "off"}, settingRequest{"child_lock", 0, 0}},
		{SettingInput{Setting: "room_passes", Value: "2", Room: 3}, settingRequest{"room_passes", 2, 3}},
		{SettingInput{Setting: "room_wetness", Value: "dry", Room: 3}, settingRequest{"room_wetness", 0, 3}},
	}
	for _, c := range cases {
		if _, err := client.Setting(context.Background(), c.in); err != nil || got != c.want {
			t.Fatalf("%#v: got=%#v err=%v", c.in, got, err)
		}
	}
	before := settings
	for _, in := range []SettingInput{
		{Setting: "firmware", Value: "1"},
		{Setting: "volume", Value: "101"},
		{Setting: "volume", Value: "loud"},
		{Setting: "dnd_start", Value: "24:00"},
		{Setting: "dnd_start", Value: "7pm"},
		{Setting: "suction", Value: "max"},
		{Setting: "suction", Value: "turbo", Room: 3},
		{Setting: "room_passes", Value: "2"},
		{Setting: "room_passes", Value: "2", Room: 9},
		{Setting: "room_passes", Value: "4", Room: 3},
	} {
		if _, err := client.Setting(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if settings != before {
		t.Fatalf("%d invalid settings reached the vacuum", settings-before)
	}
}

func TestDetailsValidated(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/details" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"consumables":{"main_brush":80,"filter":12},"station":{"clean_water_tank":"installed"},"settings":{"cleaning_mode":"sweeping_and_mopping"}}`))
	})
	out, err := newClient(t, socket).Details(context.Background())
	if err != nil || out.Consumables["filter"] != 12 || out.Station["clean_water_tank"] != "installed" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	bad := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"consumables":{"main_brush":180},"station":{},"settings":{}}`))
	})
	if _, err := newClient(t, bad).Details(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("out-of-range wear accepted: %v", err)
	}
}

func TestHistoryLimitAndReply(t *testing.T) {
	var asked historyRequest
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&asked)
		w.Write([]byte(`{"entries":[{"time":1795000000,"minutes":42,"area_m2":55,"completed":true,"status":"Cleaning"}]}`))
	})
	client := newClient(t, socket)
	out, err := client.History(context.Background(), HistoryInput{})
	if err != nil || asked.Limit != 10 || len(out.Entries) != 1 || out.Entries[0].Minutes != 42 {
		t.Fatalf("out=%#v asked=%#v err=%v", out, asked, err)
	}
	if _, err := client.History(context.Background(), HistoryInput{Limit: 21}); !errors.Is(err, ErrInput) {
		t.Fatalf("limit 21 accepted: %v", err)
	}
}

func tinyPNG(t *testing.T, w, h int) string {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func TestMapReturnsCleanPNG(t *testing.T) {
	data := tinyPNG(t, 4, 3)
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"mime_type":"image/png","data":"` + data + `"}`))
	})
	out, err := newClient(t, socket).Map(context.Background())
	if err != nil || out.MIMEType != "image/png" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(out.Data))
	if err != nil || config.Width != 4 || config.Height != 3 {
		t.Fatalf("config=%#v err=%v", config, err)
	}
	bad := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"mime_type":"image/png","data":"bm90IGEgcG5n"}`))
	})
	if _, err := newClient(t, bad).Map(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("non-PNG accepted: %v", err)
	}
	huge := tinyPNG(t, 3000, 10)
	big := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"mime_type":"image/png","data":"` + huge + `"}`))
	})
	if _, err := newClient(t, big).Map(context.Background()); !errors.Is(err, errReply) {
		t.Fatalf("oversized map accepted: %v", err)
	}
}

func TestUnavailableExplainsWhy(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"login failed: check the account, password, and country"}`))
	})
	_, err := newClient(t, socket).Status(context.Background())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "login failed: check the account") {
		t.Fatalf("err=%v", err)
	}
	silent := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("\x00\x01 not json"))
	})
	if _, err := newClient(t, silent).Status(context.Background()); err != ErrUnavailable {
		t.Fatalf("unreadable reason not dropped: %v", err)
	}
}

func TestSlowBrokerReportsBusyQuickly(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	client := newClient(t, socket)
	client.quick = 100 * time.Millisecond
	started := time.Now()
	_, err := client.Status(context.Background())
	if !errors.Is(err, ErrBusy) || time.Since(started) > 2*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(started))
	}
}

func TestGoToAimedInsideMap(t *testing.T) {
	var got commandRequest
	commands := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Write([]byte(mapRooms))
		case "/command":
			commands++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Driving there"}`))
		}
	})
	client := newClient(t, socket)
	if _, err := client.Command(context.Background(), CommandInput{Action: "go_to", Point: &[2]int{1500, 1200}}); err != nil {
		t.Fatal(err)
	}
	if got.Action != "go_to" || got.Point == nil || got.Repeats != 0 || got.Suction != -1 {
		t.Fatalf("got=%#v", got)
	}
	for _, in := range []CommandInput{
		{Action: "go_to"},
		{Action: "go_to", Point: &[2]int{9000, 0}},
		{Action: "go_to", Point: &[2]int{0, 0}, Repeats: 2},
		{Action: "go_to", Point: &[2]int{0, 0}, Suction: "turbo"},
	} {
		if _, err := client.Command(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if commands != 1 {
		t.Fatalf("%d commands reached the vacuum", commands)
	}
}

func TestMoveStepChecked(t *testing.T) {
	var got moveRequest
	moves := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/move" {
			t.Errorf("path %s", r.URL.Path)
		}
		moves++
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"accepted":true,"message":"Moved"}`))
	})
	client := newClient(t, socket)
	if _, err := client.Move(context.Background(), MoveInput{Turn: -90, Speed: 0}); err != nil || got.Rotation != -90 || got.Velocity != 0 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	for _, in := range []MoveInput{{}, {Turn: 400}, {Speed: 700}} {
		if _, err := client.Move(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if moves != 1 {
		t.Fatalf("%d moves reached the vacuum", moves)
	}
}

func tinyJPEG(t *testing.T, w, h int) string {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func TestObstaclesAndPhotos(t *testing.T) {
	var asked obstacleRequest
	photos := 0
	data := tinyJPEG(t, 5, 4)
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/obstacles":
			w.Write([]byte(`{"obstacles":[{"index":2,"type":"sock","x":120,"y":-40,"confidence":87,"has_photo":true},{"index":4,"type":"wire","x":0,"y":0,"confidence":60,"has_photo":false}]}`))
		case "/obstacle_photo":
			photos++
			json.NewDecoder(r.Body).Decode(&asked)
			w.Write([]byte(`{"mime_type":"image/jpeg","data":"` + data + `"}`))
		}
	})
	client := newClient(t, socket)
	list, err := client.Obstacles(context.Background())
	if err != nil || len(list.Obstacles) != 2 || list.Obstacles[0].Type != "sock" || !list.Obstacles[0].HasPhoto {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	photo, err := client.ObstaclePhoto(context.Background(), ObstaclePhotoInput{Index: 2})
	if err != nil || photo.MIMEType != "image/jpeg" || asked.Index != 2 {
		t.Fatalf("photo=%#v asked=%#v err=%v", photo, asked, err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(photo.Data))
	if err != nil || config.Width != 5 {
		t.Fatalf("config=%#v err=%v", config, err)
	}
	for _, index := range []int{4, 9} {
		if _, err := client.ObstaclePhoto(context.Background(), ObstaclePhotoInput{Index: index}); !errors.Is(err, ErrInput) {
			t.Errorf("index %d: err=%v", index, err)
		}
	}
	if photos != 1 {
		t.Fatalf("%d photo requests reached the vacuum", photos)
	}
}

func TestShortcutsListedAndChecked(t *testing.T) {
	var got shortcutRequest
	sent := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shortcuts":
			w.Write([]byte(`{"shortcuts":[{"id":32,"name":"After dinner","running":false,"rooms":["Kitchen","Living Room"]}]}`))
		case "/shortcut":
			sent++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Started After dinner"}`))
		}
	})
	client := newClient(t, socket)
	list, err := client.Shortcuts(context.Background())
	if err != nil || len(list.Shortcuts) != 1 || list.Shortcuts[0].Name != "After dinner" || len(list.Shortcuts[0].Rooms) != 2 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	if _, err := client.Shortcut(context.Background(), ShortcutInput{Action: "start", ID: 32}); err != nil || got.Action != "start" || got.ID != 32 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := client.Shortcut(context.Background(), ShortcutInput{Action: "rename", ID: 32, Name: "Evening tidy"}); err != nil || got.Name != "Evening tidy" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	for _, in := range []ShortcutInput{
		{Action: "start", ID: 99},
		{Action: "rename", ID: 32},
		{Action: "rename", ID: 32, Name: strings.Repeat("x", 33)},
		{Action: "start", ID: 32, Name: "x"},
		{Action: "edit", ID: 32},
		{Action: "rename", ID: 32, Name: "bad\nname"},
	} {
		if _, err := client.Shortcut(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if sent != 2 {
		t.Fatalf("%d shortcut commands reached the vacuum", sent)
	}
}

func TestStatusTrimsName(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"Roberto ","connected":true,"state":"idle","battery":50,"docked":true,"error":"","suction":"","current_room":"","cleaned_area_m2":0,"cleaning_minutes":0}`))
	})
	out, err := newClient(t, socket).Status(context.Background())
	if err != nil || out.Name != "Roberto" {
		t.Fatalf("name=%q err=%v", out.Name, err)
	}
}

const optionsJSON = `{"options":[
 {"name":"carpet_boost","kind":"switch","current":"off","choices":[{"label":"off","value":0},{"label":"on","value":1}]},
 {"name":"mop_pressure","kind":"select","current":"medium","choices":[{"label":"light","value":1},{"label":"medium","value":2},{"label":"strong","value":3}]},
 {"name":"mop_cleaning_remainder","kind":"number","current":"30","min":0,"max":180,"step":15},
 {"name":"off_peak_charging_start","kind":"time","current":"22:00"}
]}`

func TestOptionsListedAndChangesChecked(t *testing.T) {
	var got optionRequest
	sent := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/options":
			w.Write([]byte(optionsJSON))
		case "/set_option":
			sent++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Done"}`))
		}
	})
	client := newClient(t, socket)
	list, err := client.Options(context.Background(), OptionsInput{})
	if err != nil || len(list.Options) != 4 || list.Options[1].Choices[2].Label != "strong" {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	filtered, _ := client.Options(context.Background(), OptionsInput{Search: "MOP"})
	if len(filtered.Options) != 2 {
		t.Fatalf("filter returned %d", len(filtered.Options))
	}
	cases := []struct {
		in   SetOptionInput
		want optionRequest
	}{
		{SetOptionInput{Name: "carpet_boost", Value: "on"}, optionRequest{"carpet_boost", 1}},
		{SetOptionInput{Name: "mop_pressure", Value: "Strong"}, optionRequest{"mop_pressure", 3}},
		{SetOptionInput{Name: "mop_cleaning_remainder", Value: "45"}, optionRequest{"mop_cleaning_remainder", 45}},
		{SetOptionInput{Name: "off_peak_charging_start", Value: "23:30"}, optionRequest{"off_peak_charging_start", 1410}},
	}
	for _, c := range cases {
		if _, err := client.SetOption(context.Background(), c.in); err != nil || got != c.want {
			t.Fatalf("%#v: got=%#v err=%v", c.in, got, err)
		}
	}
	for _, in := range []SetOptionInput{
		{Name: "firmware_update", Value: "on"},
		{Name: "carpet_boost", Value: "maybe"},
		{Name: "mop_pressure", Value: "extreme"},
		{Name: "mop_cleaning_remainder", Value: "40"},
		{Name: "mop_cleaning_remainder", Value: "200"},
		{Name: "off_peak_charging_start", Value: "25:00"},
	} {
		if _, err := client.SetOption(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if sent != len(cases) {
		t.Fatalf("%d invalid changes reached the vacuum", sent-len(cases))
	}
}

func TestButtonsListedAndPressChecked(t *testing.T) {
	var got pressRequest
	sent := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/buttons":
			w.Write([]byte(`{"buttons":[{"name":"reset_filter","kind":"reset"},{"name":"start_mapping","kind":"action"}]}`))
		case "/press":
			sent++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Pressed reset filter"}`))
		}
	})
	client := newClient(t, socket)
	list, err := client.Buttons(context.Background())
	if err != nil || len(list.Buttons) != 2 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	if _, err := client.Press(context.Background(), PressInput{Name: "reset_filter"}); err != nil || got.Name != "reset_filter" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := client.Press(context.Background(), PressInput{Name: "factory_reset"}); !errors.Is(err, ErrInput) || sent != 1 {
		t.Fatalf("unknown button reached vacuum: err=%v sent=%d", err, sent)
	}
}

const mapsJSON = `{"maps":[{"id":1,"name":"Ground","selected":true,"backups":[{"index":1,"date":"2026-11-01 09:00"},{"index":2,"date":"2026-11-10 09:00"}]},{"id":2,"name":"Upstairs","selected":false,"backups":[]}],"temporary_map":false}`

func TestMapsListedAndActionsChecked(t *testing.T) {
	var got mapActionRequest
	sent := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/maps":
			w.Write([]byte(mapsJSON))
		case "/manage_map":
			sent++
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"accepted":true,"message":"Done"}`))
		}
	})
	client := newClient(t, socket)
	list, err := client.Maps(context.Background())
	if err != nil || len(list.Maps) != 2 || len(list.Maps[0].Backups) != 2 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	for _, in := range []MapActionInput{
		{Action: "select", MapID: 2}, {Action: "rename", MapID: 1, Name: "Downstairs"},
		{Action: "restore", MapID: 1, Backup: 2}, {Action: "request"},
	} {
		if _, err := client.ManageMap(context.Background(), in); err != nil || got.Action != in.Action || got.MapID != in.MapID {
			t.Fatalf("%#v: got=%#v err=%v", in, got, err)
		}
	}
	for _, in := range []MapActionInput{
		{Action: "select", MapID: 7}, {Action: "restore", MapID: 2, Backup: 1}, {Action: "rename", MapID: 1},
		{Action: "request", MapID: 1}, {Action: "wipe", MapID: 1},
	} {
		if _, err := client.ManageMap(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if sent != 4 {
		t.Fatalf("%d map actions reached the vacuum", sent)
	}
}

func TestRoomActionsAndOptionsChecked(t *testing.T) {
	var gotAction roomActionRequest
	var gotOption roomOptionRequest
	sent := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Write([]byte(mapRooms[:len(mapRooms)-len(`],"bounds":[-5000,-5000,5000,5000]}`)] + `,{"id":5,"name":"Hall"}],"bounds":[-5000,-5000,5000,5000]}`))
		case "/room_options":
			w.Write([]byte(`{"options":[{"name":"mop_pressure","kind":"select","current":"medium","choices":[{"label":"light","value":1},{"label":"medium","value":2}]}],"types":[{"label":"Room","value":0},{"label":"Kitchen","value":4}]}`))
		case "/manage_rooms":
			sent++
			json.NewDecoder(r.Body).Decode(&gotAction)
			w.Write([]byte(`{"accepted":true,"message":"Done"}`))
		case "/set_room_option":
			sent++
			json.NewDecoder(r.Body).Decode(&gotOption)
			w.Write([]byte(`{"accepted":true,"message":"Done"}`))
		}
	})
	client := newClient(t, socket)
	ok := []RoomActionInput{
		{Action: "rename", Rooms: []int{3}, Name: "Kitchen"},
		{Action: "set_type", Rooms: []int{3}, Type: "kitchen"},
		{Action: "merge", Rooms: []int{3, 5}},
		{Action: "split", Rooms: []int{3}, Line: &[4]int{0, 0, 1000, 0}},
		{Action: "order", Rooms: []int{5, 3}},
		{Action: "hide", Rooms: []int{5}},
	}
	for _, in := range ok {
		if _, err := client.ManageRooms(context.Background(), in); err != nil || gotAction.Action != in.Action {
			t.Fatalf("%#v: got=%#v err=%v", in, gotAction, err)
		}
	}
	if gotAction.Action != "hide" || len(gotAction.Rooms) != 1 {
		t.Fatalf("got=%#v", gotAction)
	}
	bad := []RoomActionInput{
		{Action: "rename", Rooms: []int{9}, Name: "X"},
		{Action: "set_type", Rooms: []int{3}, Type: "garage"},
		{Action: "split", Rooms: []int{3}},
		{Action: "split", Rooms: []int{3}, Line: &[4]int{0, 0, 9000, 0}},
		{Action: "merge", Rooms: []int{3}, Line: &[4]int{0, 0, 10, 0}},
		{Action: "order", Rooms: []int{3}},
	}
	for _, in := range bad {
		if _, err := client.ManageRooms(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	options, err := client.RoomOptions(context.Background(), RoomOptionsInput{Room: 3})
	if err != nil || len(options.Options) != 1 || len(options.Types) != 2 {
		t.Fatalf("options=%#v err=%v", options, err)
	}
	if _, err := client.SetRoomOption(context.Background(), SetRoomOptionInput{Room: 3, Name: "mop_pressure", Value: "light"}); err != nil || gotOption != (roomOptionRequest{3, "mop_pressure", 1}) {
		t.Fatalf("got=%#v err=%v", gotOption, err)
	}
	if _, err := client.SetRoomOption(context.Background(), SetRoomOptionInput{Room: 9, Name: "mop_pressure", Value: "light"}); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown room accepted: %v", err)
	}
	if sent != len(ok)+1 {
		t.Fatalf("%d room changes reached the vacuum", sent)
	}
}

func TestMapEditingChecked(t *testing.T) {
	var gotEdit mapEditRequest
	var gotPath pathRequest
	var gotIgnore ignoreRequest
	sent := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Write([]byte(mapRooms))
		case "/map_elements":
			w.Write([]byte(`{"no_go_zones":[[0,0,500,500]],"virtual_walls":[],"predefined_points":[[10,20]],"router_position":[[5,5]]}`))
		case "/obstacles":
			w.Write([]byte(`{"obstacles":[{"index":2,"type":"shoes","x":10,"y":20,"confidence":90,"has_photo":false}]}`))
		case "/edit_map":
			sent++
			json.NewDecoder(r.Body).Decode(&gotEdit)
			w.Write([]byte(`{"accepted":true,"message":"Done"}`))
		case "/patrol":
			sent++
			json.NewDecoder(r.Body).Decode(&gotPath)
			w.Write([]byte(`{"accepted":true,"message":"Patrolling"}`))
		case "/ignore_obstacle":
			sent++
			json.NewDecoder(r.Body).Decode(&gotIgnore)
			w.Write([]byte(`{"accepted":true,"message":"Ignoring"}`))
		}
	})
	client := newClient(t, socket)
	elements, err := client.MapElements(context.Background())
	if err != nil || len(elements["no_go_zones"]) != 1 || elements["predefined_points"][0][1] != 20 {
		t.Fatalf("elements=%#v err=%v", elements, err)
	}
	if _, err := client.EditMap(context.Background(), MapEditInput{Element: "no_go_zones", Items: [][]int{{0, 0, 1000, 1000}, {2000, 2000, 3000, 3000}}}); err != nil || len(gotEdit.Items) != 2 {
		t.Fatalf("got=%#v err=%v", gotEdit, err)
	}
	if _, err := client.EditMap(context.Background(), MapEditInput{Element: "predefined_points", Items: [][]int{{10, 20}}}); err != nil || len(gotEdit.Items[0]) != 2 {
		t.Fatalf("points sent as %#v err=%v", gotEdit.Items, err)
	}
	if _, err := client.Patrol(context.Background(), PatrolInput{Points: [][2]int{{0, 0}, {100, 100}}}); err != nil || len(gotPath.Points) != 2 {
		t.Fatalf("got=%#v err=%v", gotPath, err)
	}
	if _, err := client.IgnoreObstacle(context.Background(), IgnoreObstacleInput{Index: 2, Ignore: true}); err != nil || gotIgnore.X != 10 || !gotIgnore.Ignore {
		t.Fatalf("got=%#v err=%v", gotIgnore, err)
	}
	for _, in := range []MapEditInput{
		{Element: "furniture", Items: [][]int{{0, 0, 10, 10}}},
		{Element: "no_go_zones", Items: [][]int{{0, 0, 9000, 10}}},
		{Element: "no_go_zones", Items: [][]int{{0, 0, 10}}},
		{Element: "router_position", Items: [][]int{}},
	} {
		if _, err := client.EditMap(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("%#v: err=%v", in, err)
		}
	}
	if _, err := client.Patrol(context.Background(), PatrolInput{Points: [][2]int{{0, 9000}}}); !errors.Is(err, ErrInput) {
		t.Errorf("off-map patrol accepted: %v", err)
	}
	if _, err := client.IgnoreObstacle(context.Background(), IgnoreObstacleInput{Index: 7, Ignore: true}); !errors.Is(err, ErrInput) {
		t.Errorf("unknown obstacle accepted: %v", err)
	}
	if sent != 4 {
		t.Fatalf("%d edits reached the vacuum", sent)
	}
}
