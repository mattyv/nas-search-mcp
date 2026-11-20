package vacuum

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type notice struct{ title, body string }

// scripted serves a sequence of vacuum readings, one per Check.
func scripted(t *testing.T, statuses, details []string) (*Client, *int) {
	t.Helper()
	step := 0
	var mu sync.Mutex
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(statuses[step]))
		case "/details":
			w.Write([]byte(details[step]))
		}
	})
	return newClient(t, socket), &step
}

func reading(state, errorText string, area, minutes int) string {
	return `{"name":"Roberto","connected":true,"state":"` + state + `","battery":80,"docked":false,"error":"` + errorText +
		`","suction":"","current_room":"Hall","cleaned_area_m2":` + strconv.Itoa(area) + `,"cleaning_minutes":` + strconv.Itoa(minutes) + `}`
}

func wear(filter int) string {
	return `{"consumables":{"filter":` + strconv.Itoa(filter) + `},"station":{},"settings":{}}`
}

func TestNotifierSendsOnlyOnChanges(t *testing.T) {
	statuses := []string{
		reading("sweeping", "", 5, 4),
		reading("returning_to_wash", "", 8, 6),
		reading("washing", "", 8, 7),
		reading("mopping", "", 10, 9),             // mid-job wash: no notice
		reading("mopping", "wheel_stuck", 11, 10), // new error
		reading("mopping", "wheel_stuck", 11, 10), // same error: no repeat
		reading("returning", "", 14, 11),
		reading("charging", "", 14, 11), // finished
		reading("charging", "", 14, 11), // unchanged: no repeat
	}
	details := []string{wear(12), wear(12), wear(11), wear(11), wear(10), wear(9), wear(9), wear(9), wear(8)}
	client, step := scripted(t, statuses, details)
	var sent []notice
	n := NewNotifier(client, func(_ context.Context, title, body string) error {
		sent = append(sent, notice{title, body})
		return nil
	})
	for i := range statuses {
		*step = i
		if err := n.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 3 {
		t.Fatalf("sent %d notices: %#v", len(sent), sent)
	}
	if !strings.Contains(sent[0].title, "needs help") || !strings.Contains(strings.ToLower(sent[0].body), "wheel stuck") || !strings.Contains(sent[0].body, "Hall") {
		t.Errorf("error notice = %#v", sent[0])
	}
	if !strings.Contains(sent[1].title, "filter") || !strings.Contains(sent[1].body, "9%") {
		t.Errorf("wear notice = %#v", sent[1])
	}
	if !strings.Contains(sent[2].title, "finished") || !strings.Contains(sent[2].body, "14 m²") || !strings.Contains(sent[2].body, "11 min") {
		t.Errorf("finished notice = %#v", sent[2])
	}
}

func TestNotifierToleratesUnavailableVacuum(t *testing.T) {
	client := newClient(t, "/tmp/vacuum-test-missing/vacuum.sock")
	n := NewNotifier(client, func(context.Context, string, string) error { t.Fatal("sent while unavailable"); return nil })
	if err := n.Check(context.Background()); err == nil {
		t.Fatal("expected an unavailable error")
	}
}

func TestNtfySenderPostsTitleAndBody(t *testing.T) {
	var gotPath, gotTitle, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotTitle = r.URL.Path, r.Header.Get("Title")
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
	}))
	defer server.Close()
	send, err := NewNtfySender(server.URL, "roberto-a1b2c3d4e5f6g7h8")
	if err != nil {
		t.Fatal(err)
	}
	if err := send(context.Background(), "Roberto finished", "14 m² in 11 min"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/roberto-a1b2c3d4e5f6g7h8" || gotTitle != "Roberto finished" || gotBody != "14 m² in 11 min" {
		t.Fatalf("path=%q title=%q body=%q", gotPath, gotTitle, gotBody)
	}
	for _, topic := range []string{"short", "has space in it here", "../../etc/passwd/x"} {
		if _, err := NewNtfySender(server.URL, topic); err == nil {
			t.Errorf("topic %q accepted", topic)
		}
	}
}
