package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const secretKey = "sk-test-SECRET-123"

func qs() map[string]Question {
	return map[string]Question{
		"decided": {Type: "noul", Instructions: "Did the owner decide?"},
		"category": {Type: "choice", Instructions: "Which category?", Criteria: map[string]string{
			"fact": "a fact", "decision": "a decision",
		}},
	}
}

func okBody() string {
	return `{"model":"jev-1.13.0","answers":{
	 "decided":{"type":"noul","noul":0.9},
	 "category":{"type":"choice","choice":"decision","probabilities":{"fact":0.2,"decision":0.8},"confidence":0.8}},
	 "usage":{"input_tokens":5,"output_tokens":2}}`
}

func serve(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, APIKey: secretKey}
}

func TestRequestShape(t *testing.T) {
	var gotReq *http.Request
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, okBody())
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/", APIKey: secretKey}
	if _, err := c.Decide(context.Background(), "some state", qs()); err != nil {
		t.Fatal(err)
	}
	if gotReq.Method != "POST" || gotReq.URL.Path != "/v1/systemone" {
		t.Fatalf("got %s %s", gotReq.Method, gotReq.URL.Path)
	}
	if gotReq.Header.Get("Authorization") != "Bearer "+secretKey {
		t.Fatal("bad auth header")
	}
	if gotReq.Header.Get("Content-Type") != "application/json" {
		t.Fatal("bad content type")
	}
	if gotBody["model"] != "jev-latest" || gotBody["state"] != "some state" {
		t.Fatalf("body: %v", gotBody)
	}
	q := gotBody["questions"].(map[string]any)["category"].(map[string]any)
	if q["type"] != "choice" || q["instructions"] != "Which category?" {
		t.Fatalf("question: %v", q)
	}
	if q["criteria"].(map[string]any)["fact"] != "a fact" {
		t.Fatalf("criteria: %v", q)
	}
	if _, has := gotBody["questions"].(map[string]any)["decided"].(map[string]any)["criteria"]; has {
		t.Fatal("nil criteria must be omitted")
	}
}

func TestCustomModel(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, okBody())
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: secretKey, Model: "jev-x"}
	if _, err := c.Decide(context.Background(), "s", qs()); err != nil {
		t.Fatal(err)
	}
	if gotBody["model"] != "jev-x" {
		t.Fatal(gotBody["model"])
	}
}

func TestStateTruncated(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, okBody())
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: secretKey}
	long := strings.Repeat("é", 20000)
	if _, err := c.Decide(context.Background(), long, qs()); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(gotBody["state"].(string))); n != 12000 {
		t.Fatalf("state length %d", n)
	}
	obj := map[string]string{"body": strings.Repeat("x", 20000)}
	if _, err := c.Decide(context.Background(), obj, qs()); err != nil {
		t.Fatal(err)
	}
	if s, ok := gotBody["state"].(string); !ok || len([]rune(s)) != 12000 {
		t.Fatalf("object state not truncated: %T", gotBody["state"])
	}
	if _, err := c.Decide(context.Background(), map[string]string{"a": "b"}, qs()); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["state"].(map[string]any); !ok {
		t.Fatalf("small object changed: %T", gotBody["state"])
	}
}

func TestHappyPath(t *testing.T) {
	c := serve(t, 200, okBody())
	ans, err := c.Decide(context.Background(), "s", qs())
	if err != nil {
		t.Fatal(err)
	}
	if ans["decided"].Type != "noul" || ans["decided"].Noul != 0.9 {
		t.Fatalf("%+v", ans["decided"])
	}
	a := ans["category"]
	if a.Type != "choice" || a.Choice != "decision" || a.Confidence != 0.8 || a.Probabilities["fact"] != 0.2 {
		t.Fatalf("%+v", a)
	}
}

func TestModelReported(t *testing.T) {
	c := serve(t, 200, okBody())
	_, model, err := c.DecideWithModel(context.Background(), "s", qs())
	if err != nil || model != "jev-1.13.0" {
		t.Fatalf("%q %v", model, err)
	}
}

func TestValidationFailures(t *testing.T) {
	goodCat := `"category":{"type":"choice","choice":"fact","probabilities":{"fact":1,"decision":0},"confidence":1}`
	goodNoul := `"decided":{"type":"noul","noul":0.5}`
	wrap := func(inner string) string { return `{"model":"m","answers":{` + inner + `}}` }
	cases := map[string]string{
		"missing question":    wrap(goodNoul),
		"missing noul q":      wrap(goodCat),
		"no answers":          `{"model":"m"}`,
		"type mismatch":       wrap(`"decided":{"type":"choice","choice":"fact","probabilities":{"fact":1},"confidence":1},` + goodCat),
		"noul missing":        wrap(`"decided":{"type":"noul"},` + goodCat),
		"noul high":           wrap(`"decided":{"type":"noul","noul":1.5},` + goodCat),
		"noul negative":       wrap(`"decided":{"type":"noul","noul":-0.1},` + goodCat),
		"unknown choice":      wrap(goodNoul + `,"category":{"type":"choice","choice":"opinion","probabilities":{"opinion":1},"confidence":1}`),
		"choice not in probs": wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"decision":1},"confidence":1}`),
		"prob out of range":   wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"fact":1.2,"decision":-0.2},"confidence":1}`),
		"mass low":            wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"fact":0.5,"decision":0.3},"confidence":0.5}`),
		"mass high":           wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"fact":0.9,"decision":0.9},"confidence":0.9}`),
		"unknown prob key":    wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"fact":0.9,"other":0.1},"confidence":0.9}`),
		"bad confidence":      wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"fact":0.9,"decision":0.1},"confidence":2}`),
		"missing confidence":  wrap(goodNoul + `,"category":{"type":"choice","choice":"fact","probabilities":{"fact":0.9,"decision":0.1}}`),
		"not json":            `nope`,
		"empty":               ``,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := serve(t, 200, body)
			_, err := c.Decide(context.Background(), "s", qs())
			if err == nil {
				t.Fatal("expected error")
			}
			if IsRetryable(err) {
				t.Fatalf("validation errors must not be retryable: %v", err)
			}
		})
	}
}

func TestMassTolerance(t *testing.T) {
	body := `{"model":"m","answers":{"decided":{"type":"noul","noul":0.5},"category":{"type":"choice","choice":"fact","probabilities":{"fact":0.6,"decision":0.37},"confidence":0.6}}}`
	c := serve(t, 200, body)
	if _, err := c.Decide(context.Background(), "s", qs()); err != nil {
		t.Fatalf("0.97 mass is within 0.05: %v", err)
	}
}

func TestRejectsBadRequests(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", APIKey: secretKey}
	if _, err := c.Decide(context.Background(), "s", nil); err == nil {
		t.Fatal("no questions")
	}
	if _, err := c.Decide(context.Background(), "s", map[string]Question{"x": {Type: "weird"}}); err == nil {
		t.Fatal("unsupported type")
	}
	if _, err := c.Decide(context.Background(), "s", map[string]Question{"x": {Type: "choice", Instructions: "i"}}); err == nil {
		t.Fatal("choice needs options")
	}
	if _, err := (&Client{BaseURL: "http://x"}).Decide(context.Background(), "s", qs()); err == nil {
		t.Fatal("missing key")
	}
}

func TestRetryableClassification(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		c := serve(t, status, `{"error":"echo `+secretKey+`"}`)
		_, err := c.Decide(context.Background(), "s", qs())
		if err == nil || !IsRetryable(err) {
			t.Fatalf("%d should be retryable: %v", status, err)
		}
	}
	for _, status := range []int{400, 401, 403, 404, 422} {
		c := serve(t, status, `{"error":"bad"}`)
		_, err := c.Decide(context.Background(), "s", qs())
		if err == nil || IsRetryable(err) {
			t.Fatalf("%d should not be retryable: %v", status, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := &Client{BaseURL: url, APIKey: secretKey}
	_, err := c.Decide(context.Background(), "s", qs())
	if err == nil || !IsRetryable(err) {
		t.Fatalf("network error should be retryable: %v", err)
	}
	if IsRetryable(nil) || IsRetryable(errors.New("plain")) {
		t.Fatal("nil and plain errors are not retryable")
	}
}

func TestResponseSizeCap(t *testing.T) {
	big := `{"model":"m","pad":"` + strings.Repeat("a", 1<<20+10) + `"}`
	c := serve(t, 200, big)
	_, err := c.Decide(context.Background(), "s", qs())
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected size error, got %v", err)
	}
	if IsRetryable(err) {
		t.Fatal("oversize is not retryable")
	}
}

func TestContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: secretKey}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Decide(ctx, "s", qs())
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("expected quick timeout: %v after %v", err, time.Since(start))
	}
	if !IsRetryable(err) {
		t.Fatalf("timeout should be retryable: %v", err)
	}
}

func TestKeyNeverInErrors(t *testing.T) {
	for _, status := range []int{400, 401, 500} {
		c := serve(t, status, `{"error":"`+secretKey+`"}`)
		_, err := c.Decide(context.Background(), "s", qs())
		if err == nil || strings.Contains(err.Error(), secretKey) {
			t.Fatalf("status %d leaked key: %v", status, err)
		}
	}
	c := &Client{BaseURL: "http://127.0.0.1:1/" + secretKey, APIKey: secretKey}
	_, err := c.Decide(context.Background(), "s", qs())
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("network error leaked key: %v", err)
	}
	c2 := serve(t, 200, `{"model":"`+secretKey+`","answers":{}}`)
	_, err = c2.Decide(context.Background(), "s", qs())
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("validation error leaked key: %v", err)
	}
}
