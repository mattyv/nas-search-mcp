package photos

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	dir, err := os.MkdirTemp("/tmp", "photos-test-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close(); os.RemoveAll(dir) })
	return socket
}

func TestPeopleUsesFixedReadOnlyRouteAndPaging(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/people" || r.Host != "synology-photos" {
			t.Errorf("unexpected request %s %s %s", r.Method, r.URL.Path, r.Host)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected bearer credential")
		}
		var in PeopleInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in.Query != "Lucas" || in.Offset != 0 || in.Limit != 20 {
			t.Errorf("bad request: %#v", in)
		}
		w.Write([]byte(`{"people":[{"id":3753,"name":"Lucas","item_count":2037}],"offset":0,"next_offset":20,"truncated":true}`))
	})
	client, err := New(socket)
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.People(context.Background(), PeopleInput{Query: "Lucas"})
	if err != nil || len(out.People) != 1 || out.People[0].Name != "Lucas" || out.People[0].ItemCount != 2037 || out.NextOffset == nil || *out.NextOffset != 20 || !out.Truncated {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestPhotosUsesOnlyPersonID(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/photos" {
			t.Errorf("path %s", r.URL.Path)
		}
		var in PhotosInput
		json.NewDecoder(r.Body).Decode(&in)
		if in.PersonID != 3753 || in.Limit != 1 {
			t.Errorf("bad input %#v", in)
		}
		w.Write([]byte(`{"photos":[{"id":123,"filename":"IMG.jpg","taken_time":1700000000,"type":"photo"}],"offset":0,"next_offset":null,"truncated":false}`))
	})
	client, _ := New(socket)
	out, err := client.ForPerson(context.Background(), PhotosInput{PersonID: 3753, Limit: 1})
	if err != nil || len(out.Photos) != 1 || out.Photos[0].Filename != "IMG.jpg" || out.NextOffset != nil {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestCaptureTimesBefore1970RemainMetadata(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"photos":[{"id":123,"filename":"family.jpg","taken_time":-315619200,"type":"photo"}],"offset":0,"next_offset":null,"truncated":false}`))
	})
	client, _ := New(socket)
	out, err := client.ForPerson(context.Background(), PhotosInput{PersonID: 3753})
	if err != nil || len(out.Photos) != 1 || out.Photos[0].TakenTime != -315619200 {
		t.Fatalf("lost historical capture date: %#v %v", out, err)
	}
}

func TestPreviewReturnsBoundedJPEGFromFixedRoute(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x01, 0x02, 0xff, 0xd9}
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/preview" {
			t.Errorf("wrong preview route: %s %s", r.Method, r.URL.Path)
		}
		var in PreviewInput
		json.NewDecoder(r.Body).Decode(&in)
		if in.PersonID != 3753 || in.PhotoID != 89027 {
			t.Errorf("bad preview input %#v", in)
		}
		json.NewEncoder(w).Encode(map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(jpeg)})
	})
	client, _ := New(socket)
	out, err := client.Preview(context.Background(), PreviewInput{PersonID: 3753, PhotoID: 89027})
	if err != nil || out.MIMEType != "image/jpeg" || string(out.Data) != string(jpeg) {
		t.Fatalf("preview=%#v err=%v", out, err)
	}
}

func TestPreviewRejectsInvalidIDsBeforeConnecting(t *testing.T) {
	client, _ := New("/tmp/no-preview-broker.sock")
	for _, in := range []PreviewInput{{PersonID: 0, PhotoID: 1}, {PersonID: 1, PhotoID: 0}, {PersonID: -1, PhotoID: 1}, {PersonID: 1, PhotoID: -1}, {PersonID: 2147483648, PhotoID: 1}, {PersonID: 1, PhotoID: 2147483648}} {
		if _, err := client.Preview(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("accepted preview input %#v: %v", in, err)
		}
	}
}

func TestPreviewRejectsBadEncodingTypeSizeAndJPEGMarkers(t *testing.T) {
	oversized := make([]byte, 512*1024+1)
	copy(oversized, []byte{0xff, 0xd8})
	copy(oversized[len(oversized)-2:], []byte{0xff, 0xd9})
	for _, reply := range []map[string]any{
		{"mime_type": "image/png", "data": base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xd9})},
		{"mime_type": "image/jpeg", "data": "not-base64-private-token"},
		{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(oversized)},
		{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString([]byte{1, 2, 0xff, 0xd9})},
		{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 1, 2})},
		{"mime_type": "image/jpeg", "data": ""},
	} {
		socket := broker(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(reply) })
		client, _ := New(socket)
		if _, err := client.Preview(context.Background(), PreviewInput{PersonID: 3753, PhotoID: 89027}); err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatalf("bad preview accepted or exposed: %v", err)
		}
	}
}

func TestRejectsUnboundedInputsBeforeConnecting(t *testing.T) {
	client, _ := New("/tmp/nonexistent-photos.sock")
	for _, in := range []PeopleInput{{Query: strings.Repeat("x", 101)}, {Offset: -1}, {Offset: 100001}, {Limit: -1}, {Limit: 101}, {Query: "bad\x00name"}} {
		if _, err := client.People(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("people %#v: %v", in, err)
		}
	}
	for _, in := range []PhotosInput{{PersonID: 0}, {PersonID: -1}, {PersonID: 2147483648}, {PersonID: 1, Offset: -1}, {PersonID: 1, Offset: 100001}, {PersonID: 1, Limit: 101}} {
		if _, err := client.ForPerson(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Errorf("photos %#v: %v", in, err)
		}
	}
}

func TestSocketPathIsExplicitAndConfined(t *testing.T) {
	for _, path := range []string{"", "relative.sock", "/tmp/../other.sock", "/tmp/./other.sock", "/tmp/socket\x00bad", "/tmp/" + strings.Repeat("x", 108)} {
		if _, err := New(path); err == nil {
			t.Errorf("accepted socket %q", path)
		}
	}
}

func TestBrokerErrorsAndMalformedResponsesArePrivate(t *testing.T) {
	for _, body := range []string{`private-token`, `{"people":[],"offset":0,"next_offset":null,"truncated":false} garbage`, `{"people":[],"offset":0,"next_offset":null,"truncated":false,"token":"private-token"}`, `{"people":[{"id":-1,"name":"Lucas","item_count":2}],"offset":0,"next_offset":null,"truncated":false}`, `{"people":[],"offset":0,"next_offset":0,"truncated":true}`, strings.Repeat("x", 1024*1024+1)} {
		t.Run("invalid", func(t *testing.T) {
			socket := broker(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
			client, _ := New(socket)
			if _, err := client.People(context.Background(), PeopleInput{}); err == nil || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
	for _, status := range []int{401, 500, 503} {
		socket := broker(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte("private-token")) })
		client, _ := New(socket)
		if _, err := client.People(context.Background(), PeopleInput{}); err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatalf("unsafe error %v", err)
		}
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	calls := 0
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "http://unrelated.invalid/private", http.StatusTemporaryRedirect)
	})
	client, _ := New(socket)
	if _, err := client.People(context.Background(), PeopleInput{}); err == nil {
		t.Fatal("accepted redirect")
	}
	if calls != 1 {
		t.Fatalf("followed redirect: %d", calls)
	}
}

func TestCancellationAndUnavailableBrokerFailPromptly(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	client, _ := New(socket)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := client.People(ctx, PeopleInput{}); err == nil {
		t.Fatal("accepted canceled request")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation not respected")
	}
	missing, _ := New("/tmp/no-such-photos-broker.sock")
	if _, err := missing.People(context.Background(), PeopleInput{}); err == nil {
		t.Fatal("missing broker succeeded")
	}
}
