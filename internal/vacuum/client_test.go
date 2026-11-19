package vacuum

import (
	"context"
	"encoding/json"
	"errors"
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
	if len(paths) != 1 || paths[0] != "/command" || got.Action != "dock" || len(got.Rooms) != 0 || got.Repeats != 0 || got.Suction != -1 {
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
