package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicBoundary(t *testing.T) {
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	h := hardenPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			w.Write(bytes.Repeat([]byte("x"), publicResponseLimit+1))
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer resource_metadata=\"https://example.test/.well-known/oauth-protected-resource/mcp\"")
		w.WriteHeader(http.StatusUnauthorized)
	}), logger)
	r := httptest.NewRequest("POST", "http://localhost/mcp?password=private-passcode", strings.NewReader("private document"))
	r.Header.Set("Authorization", "Bearer private-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("unauthenticated challenge lost: %d", w.Code)
	}
	for _, secret := range []string{"private-passcode", "private-token", "private document"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret appeared in request log")
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/large", nil))
	if w.Code != http.StatusBadGateway || w.Body.Len() > 1024 {
		t.Fatalf("response cap failed: %d %d", w.Code, w.Body.Len())
	}
}

func TestPublicRequestRateLimit(t *testing.T) {
	h := hardenPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), log.New(&bytes.Buffer{}, "", 0))
	limited := false
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)))
		if w.Code == 429 {
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("rate limit omitted Retry-After")
			}
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("public requests were not rate limited")
	}
}

func TestPublicProtocolTrafficDoesNotSpendCallLimit(t *testing.T) {
	h := admitPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	protocol := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
	}
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(protocol[i%len(protocol)])))
		if w.Code != http.StatusNoContent {
			t.Fatalf("protocol request %d got %d", i+1, w.Code)
		}
	}
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search"}}`)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("tools/call %d after protocol startup got %d", i+1, w.Code)
		}
	}
}

func TestPublicRequestBodyInspectionStaysBounded(t *testing.T) {
	var seen int
	h := admitPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		seen = len(b)
		if len(b) > 64*1024 || (len(b) == 64*1024 && err == nil) {
			t.Errorf("body was not bounded: bytes=%d err=%v", len(b), err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		body string
		want int
	}{{"{malformed", http.StatusNoContent}, {strings.Repeat("x", 2*1024*1024), http.StatusRequestEntityTooLarge}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(tc.body)))
		if w.Code != tc.want || seen > 64*1024 {
			t.Fatalf("bounded body handling failed: status=%d bytes=%d", w.Code, seen)
		}
	}
}

func TestPublicShortBurstFitsMobileReconnect(t *testing.T) {
	h := admitPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 40; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)))
		if w.Code != 204 {
			t.Fatalf("benign reconnect request %d got %d", i+1, w.Code)
		}
	}
}

func TestPublicConcurrentRequestsWaitForSlot(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var count atomic.Int32
	h := admitPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) <= 2 {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(204)
	}))
	first := make(chan int, 2)
	for _, name := range []string{"search", "index_status"} {
		go func(name string) {
			w := httptest.NewRecorder()
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `"}}`
			h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
			first <- w.Code
		}(name)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("first two requests did not enter")
		}
	}
	third := make(chan int, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search"}}`)))
		third <- w.Code
	}()
	<-started
	time.Sleep(30 * time.Millisecond)
	release <- struct{}{}
	release <- struct{}{}
	var got int
	select {
	case got = <-third:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("third request did not finish")
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case code := <-first:
			if code != 204 {
				t.Fatalf("first request got %d", code)
			}
		case <-time.After(time.Second):
			t.Fatal("first request did not finish")
		}
	}
	if got != 204 {
		t.Fatalf("briefly overlapping request got %d", got)
	}
}

func TestPublicStatusAndUpgradeCallsRemainAvailableDuringSlowSearch(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	h := admitPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Params.Name == "search" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search"}}`)))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow search did not occupy normal slot")
	}
	for _, name := range []string{"index_status", "upgrade_status", "upgrade_from_main"} {
		result := make(chan int, 1)
		go func() {
			w := httptest.NewRecorder()
			body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + name + `"}}`
			h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
			result <- w.Code
		}()
		select {
		case code := <-result:
			if code != http.StatusNoContent {
				t.Fatalf("%s returned %d while searches were busy", name, code)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s blocked behind slow searches", name)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("tools/list returned %d while search was busy", w.Code)
	}
}

func TestPublicQueueStaysBounded(t *testing.T) {
	release := make(chan struct{})
	closed := false
	defer func() {
		if !closed {
			close(release)
		}
	}()
	h := admitPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	done := make(chan int, 150)
	for i := 0; i < cap(done); i++ {
		go func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)))
			done <- w.Code
		}()
	}
	select {
	case code := <-done:
		if code != 429 {
			t.Fatalf("full queue returned %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full queue accepted unbounded waiters")
	}
	close(release)
	closed = true
	for i := 1; i < cap(done); i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("queued requests did not finish")
		}
	}
}
