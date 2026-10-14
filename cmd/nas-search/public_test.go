package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/mcp", nil))
		if w.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("public requests were not rate limited")
	}
}
