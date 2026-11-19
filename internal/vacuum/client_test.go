package vacuum

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
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
