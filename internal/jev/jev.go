// Package jev is a small client for TypeSafe's Jev classifier (POST /v1/systemone).
// It validates every response strictly and never puts the API key, the request state,
// or the answers into an error message.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBase   = "https://api.typesafe.ai"
	defaultModel  = "jev-latest"
	callTimeout   = 20 * time.Second
	maxResponse   = 1 << 20
	maxStateChars = 12000
	massTolerance = 0.05
	systemOnePath = "/v1/systemone"
	typeNoul      = "noul"
	typeChoice    = "choice"
)

// Question is one thing to ask about the state. Type is "noul" (yes/no probability) or
// "choice". Criteria is {option: description} for choice and optional for noul.
type Question struct {
	Type         string
	Instructions string
	Criteria     any
}

// Answer holds the validated reply to one question.
type Answer struct {
	Type          string
	Choice        string
	Probabilities map[string]float64
	Confidence    float64
	Noul          float64
}

// Client calls the Jev API. Zero values for BaseURL, Model and HTTP select defaults.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// Error is returned for every failure. Its text never contains the API key.
type Error struct {
	msg       string
	retryable bool
}

func (e *Error) Error() string { return e.msg }

// IsRetryable reports whether trying the same call again later may succeed:
// rate limits (429), server errors (5xx), and network failures.
func IsRetryable(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.retryable
}

func (c *Client) fail(retryable bool, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if c.APIKey != "" {
		msg = strings.ReplaceAll(msg, c.APIKey, "[redacted]")
	}
	return &Error{msg: "jev: " + msg, retryable: retryable}
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type wireRequest struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireAnswer struct {
	Type          string             `json:"type"`
	Choice        *string            `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"`
	Noul          *float64           `json:"noul"`
}

type wireResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
}

// Decide asks the questions about state and returns one validated answer per question.
func (c *Client) Decide(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, error) {
	answers, _, err := c.DecideWithModel(ctx, state, questions)
	return answers, err
}

// DecideWithModel is Decide that also returns the model name the service reports.
func (c *Client) DecideWithModel(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, string, error) {
	if c.APIKey == "" {
		return nil, "", c.fail(false, "no API key configured")
	}
	if len(questions) == 0 {
		return nil, "", c.fail(false, "no questions")
	}
	options := map[string]map[string]bool{}
	wireQs := make(map[string]wireQuestion, len(questions))
	for id, q := range questions {
		switch q.Type {
		case typeNoul:
		case typeChoice:
			opts, err := optionNames(q.Criteria)
			if err != nil || len(opts) == 0 {
				return nil, "", c.fail(false, "question %q needs choice options", id)
			}
			options[id] = opts
		default:
			return nil, "", c.fail(false, "question %q has unsupported type %q", id, q.Type)
		}
		wireQs[id] = wireQuestion{Type: q.Type, Instructions: q.Instructions, Criteria: q.Criteria}
	}

	model := c.Model
	if model == "" {
		model = defaultModel
	}
	body, err := json.Marshal(wireRequest{Model: model, State: limitState(state), Questions: wireQs})
	if err != nil {
		return nil, "", c.fail(false, "cannot encode request")
	}

	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = defaultBase
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+systemOnePath, bytes.NewReader(body))
	if err != nil {
		return nil, "", c.fail(false, "invalid base URL")
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: callTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", c.fail(true, "request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponse))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, "", c.fail(retry, "service returned status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, "", c.fail(true, "reading response failed: %v", err)
	}
	if len(raw) > maxResponse {
		return nil, "", c.fail(false, "response too large")
	}
	var wr wireResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return nil, "", c.fail(false, "response is not valid JSON")
	}
	out := make(map[string]Answer, len(questions))
	for id, q := range questions {
		wa, ok := wr.Answers[id]
		if !ok {
			return nil, "", c.fail(false, "no answer for question %q", id)
		}
		a, msg := validate(q, options[id], wa)
		if msg != "" {
			return nil, "", c.fail(false, "invalid answer for %q: %s", id, msg)
		}
		out[id] = a
	}
	return out, wr.Model, nil
}

func validate(q Question, opts map[string]bool, wa wireAnswer) (Answer, string) {
	if wa.Type != q.Type {
		return Answer{}, "type mismatch"
	}
	switch q.Type {
	case typeNoul:
		if wa.Noul == nil || !prob(*wa.Noul) {
			return Answer{}, "noul missing or out of range"
		}
		return Answer{Type: typeNoul, Noul: *wa.Noul}, ""
	default:
		if wa.Choice == nil || !opts[*wa.Choice] {
			return Answer{}, "unknown choice"
		}
		if wa.Confidence == nil || !prob(*wa.Confidence) {
			return Answer{}, "confidence missing or out of range"
		}
		total := 0.0
		for k, p := range wa.Probabilities {
			if !opts[k] {
				return Answer{}, "unknown option in probabilities"
			}
			if !prob(p) {
				return Answer{}, "probability out of range"
			}
			total += p
		}
		if _, ok := wa.Probabilities[*wa.Choice]; !ok {
			return Answer{}, "choice missing from probabilities"
		}
		if math.Abs(total-1) > massTolerance {
			return Answer{}, "probabilities do not sum to 1"
		}
		probs := make(map[string]float64, len(wa.Probabilities))
		for k, p := range wa.Probabilities {
			probs[k] = p
		}
		return Answer{Type: typeChoice, Choice: *wa.Choice, Probabilities: probs, Confidence: *wa.Confidence}, ""
	}
}

func prob(p float64) bool { return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1 }

func optionNames(criteria any) (map[string]bool, error) {
	if criteria == nil {
		return nil, errors.New("no criteria")
	}
	raw, err := json.Marshal(criteria)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out, nil
}

// limitState keeps the request inside the service's token limit. Strings are cut to
// maxStateChars characters; other values are sent as-is unless their JSON is longer,
// in which case the JSON text is cut.
func limitState(state any) any {
	if s, ok := state.(string); ok {
		return truncate(s)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	if len([]rune(string(raw))) <= maxStateChars {
		return state
	}
	return truncate(string(raw))
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxStateChars {
		return s
	}
	return string(r[:maxStateChars])
}
