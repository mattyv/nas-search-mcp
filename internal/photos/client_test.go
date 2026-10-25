package photos

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

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
		if in.Query != "Alex" || in.Offset != 0 || in.Limit != 20 {
			t.Errorf("bad request: %#v", in)
		}
		w.Write([]byte(`{"people":[{"id":42,"name":"Alex","item_count":7}],"offset":0,"next_offset":20,"truncated":true}`))
	})
	client, err := New(socket)
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.People(context.Background(), PeopleInput{Query: "Alex"})
	if err != nil || len(out.People) != 1 || out.People[0].Name != "Alex" || out.People[0].ItemCount != 7 || out.NextOffset == nil || *out.NextOffset != 20 || !out.Truncated {
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
		if in.PersonID != 42 || in.Limit != 1 {
			t.Errorf("bad input %#v", in)
		}
		w.Write([]byte(`{"photos":[{"id":123,"filename":"IMG.jpg","taken_time":1700000000,"type":"photo"}],"offset":0,"next_offset":null,"truncated":false}`))
	})
	client, _ := New(socket)
	out, err := client.ForPerson(context.Background(), PhotosInput{PersonID: 42, Limit: 1})
	if err != nil || len(out.Photos) != 1 || out.Photos[0].Filename != "IMG.jpg" || out.NextOffset != nil {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestCaptureTimesBefore1970RemainMetadata(t *testing.T) {
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"photos":[{"id":123,"filename":"family.jpg","taken_time":-315619200,"type":"photo"}],"offset":0,"next_offset":null,"truncated":false}`))
	})
	client, _ := New(socket)
	out, err := client.ForPerson(context.Background(), PhotosInput{PersonID: 42})
	if err != nil || len(out.Photos) != 1 || out.Photos[0].TakenTime != -315619200 {
		t.Fatalf("lost historical capture date: %#v %v", out, err)
	}
}

func TestPreviewReturnsBoundedJPEGFromFixedRoute(t *testing.T) {
	input := tinyJPEG(t)
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/preview" {
			t.Errorf("wrong preview route: %s %s", r.Method, r.URL.Path)
		}
		var in PreviewInput
		json.NewDecoder(r.Body).Decode(&in)
		if in.PersonID != 42 || in.PhotoID != 314 {
			t.Errorf("bad preview input %#v", in)
		}
		json.NewEncoder(w).Encode(map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(input)})
	})
	client, _ := New(socket)
	out, err := client.Preview(context.Background(), PreviewInput{PersonID: 42, PhotoID: 314})
	if err != nil || out.MIMEType != "image/jpeg" {
		t.Fatalf("preview=%#v err=%v", out, err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(out.Data))
	if err != nil || config.Width != 2 || config.Height != 2 {
		t.Fatalf("invalid preview JPEG: %#v %v", config, err)
	}
}

func TestPreviewStripsAPP1MetadataBeforeReturningImage(t *testing.T) {
	input := tinyJPEG(t)
	private := []byte("Exif\x00\x00private-gps-coordinate-marker")
	length := len(private) + 2
	segment := append([]byte{0xff, 0xe1, byte(length >> 8), byte(length)}, private...)
	withMetadata := append(append(append([]byte{}, input[:2]...), segment...), input[2:]...)
	if _, err := jpeg.Decode(bytes.NewReader(withMetadata)); err != nil {
		t.Fatal(err)
	}
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(withMetadata)})
	})
	client, _ := New(socket)
	out, err := client.Preview(context.Background(), PreviewInput{PersonID: 42, PhotoID: 314})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Data, private) || bytes.Contains(out.Data, []byte("Exif")) {
		t.Fatal("preview leaked source APP1 metadata")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out.Data)); err != nil {
		t.Fatalf("re-encoded preview invalid: %v", err)
	}
}

func TestPreviewRejectsMarkerOnlyImageAndOversizedDimensions(t *testing.T) {
	wide := tinyJPEG(t)
	sof := bytes.Index(wide, []byte{0xff, 0xc0})
	if sof < 0 {
		t.Fatal("fixture has no baseline frame header")
	}
	wide[sof+7], wide[sof+8] = 0x08, 0x01 // Width2049, without allocating an image that wide.
	config, err := jpeg.DecodeConfig(bytes.NewReader(wide))
	if err != nil || config.Width != 2049 {
		t.Fatalf("fixture dimensions unavailable: %#v %v", config, err)
	}
	tall := tinyJPEG(t)
	tall[sof+5], tall[sof+6] = 0x08, 0x01
	config, err = jpeg.DecodeConfig(bytes.NewReader(tall))
	if err != nil || config.Height != 2049 {
		t.Fatalf("fixture height unavailable: %#v %v", config, err)
	}
	for name, input := range map[string][]byte{"marker-only": {0xff, 0xd8, 0xff, 0xd9}, "width": wide, "height": tall} {
		t.Run(name, func(t *testing.T) {
			socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(input)})
			})
			client, _ := New(socket)
			if _, err := client.Preview(context.Background(), PreviewInput{PersonID: 42, PhotoID: 314}); err == nil {
				t.Fatal("accepted malformed JPEG or dimensions above2048")
			}
		})
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
	oversized := tinyJPEG(t)
	// A valid JPEG with opaque APP1 padding isolates the input byte cap from
	// image validation; rejecting only malformed pixel data would miss this.
	padding := append([]byte{0xff, 0xe1, 0xea, 0x62}, make([]byte, 60000)...)
	for len(oversized) <= 512*1024 {
		oversized = append(append(append([]byte{}, oversized[:2]...), padding...), oversized[2:]...)
	}
	if _, err := jpeg.Decode(bytes.NewReader(oversized)); err != nil {
		t.Fatalf("oversize JPEG fixture invalid: %v", err)
	}
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
		if _, err := client.Preview(context.Background(), PreviewInput{PersonID: 42, PhotoID: 314}); err == nil || strings.Contains(err.Error(), "private-token") {
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
	for _, body := range []string{`private-token`, `{"people":[],"offset":0,"next_offset":null,"truncated":false} garbage`, `{"people":[],"offset":0,"next_offset":null,"truncated":false,"token":"private-token"}`, `{"people":[{"id":-1,"name":"Alex","item_count":2}],"offset":0,"next_offset":null,"truncated":false}`, `{"people":[],"offset":0,"next_offset":0,"truncated":true}`, strings.Repeat("x", 1024*1024+1)} {
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
