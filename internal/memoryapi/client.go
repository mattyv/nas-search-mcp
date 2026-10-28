package memoryapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const maxResponseBytes = 8 << 20

// Client implements Store over the memory service's private Unix socket.
type Client struct {
	Socket string
	HTTP   *http.Client // optional; the default dials Socket with a 30 second timeout
}

var _ Store = (*Client)(nil)

var defaultHTTP sync.Map // socket path -> *http.Client

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	if v, ok := defaultHTTP.Load(c.Socket); ok {
		return v.(*http.Client)
	}
	socket := c.Socket
	hc := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
			},
			MaxIdleConns:    2,
			IdleConnTimeout: time.Minute,
		},
	}
	v, _ := defaultHTTP.LoadOrStore(c.Socket, hc)
	return v.(*http.Client)
}

// Ping checks that the service answers.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://memory"+healthPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("memory service unhealthy: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) call(ctx context.Context, roots []string, name string, params, out any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if roots == nil {
		roots = []string{}
	}
	body, err := json.Marshal(CallRequest{Method: name, Roots: roots, Params: rawParams})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://memory"+CallPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("memory service unavailable: %w", err)
	}
	defer resp.Body.Close()
	var cr CallResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&cr); err != nil {
		return fmt.Errorf("memory service sent an unreadable reply (HTTP %d)", resp.StatusCode)
	}
	if cr.Error != nil {
		return decodeError(cr.Error)
	}
	if out == nil || len(cr.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(cr.Result, out); err != nil {
		return errors.New("memory service sent an unreadable result")
	}
	return nil
}

func decodeError(w *WireError) error {
	for _, c := range codes {
		if w.Code == c.code {
			return fmt.Errorf("%w: %s", c.err, w.Message)
		}
	}
	return errors.New("memory service error: " + w.Message)
}

func (c *Client) AppendEvent(ctx context.Context, roots []string, e Event) (Event, bool, error) {
	var out appendResult
	err := c.call(ctx, roots, "AppendEvent", appendParams{e}, &out)
	return out.Event, out.Created, err
}

func (c *Client) GetEvent(ctx context.Context, roots []string, id string) (Event, error) {
	var out Event
	err := c.call(ctx, roots, "GetEvent", idParams{ID: id}, &out)
	return out, err
}

func (c *Client) SearchEvents(ctx context.Context, roots []string, q EventQuery) ([]Event, error) {
	var out []Event
	err := c.call(ctx, roots, "SearchEvents", searchEventsParams{q}, &out)
	return out, err
}

func (c *Client) SaveCheckpoint(ctx context.Context, roots []string, cp Checkpoint, expectedVersion int) (Checkpoint, error) {
	var out Checkpoint
	err := c.call(ctx, roots, "SaveCheckpoint", saveCheckpointParams{cp, expectedVersion}, &out)
	return out, err
}

func (c *Client) LatestCheckpoint(ctx context.Context, roots []string, project string) (*Checkpoint, error) {
	var out *Checkpoint
	err := c.call(ctx, roots, "LatestCheckpoint", projectParams{Project: project}, &out)
	return out, err
}

func (c *Client) CheckpointHistory(ctx context.Context, roots []string, project string, limit int) ([]Checkpoint, error) {
	var out []Checkpoint
	err := c.call(ctx, roots, "CheckpointHistory", projectParams{Project: project, Limit: limit}, &out)
	return out, err
}

func (c *Client) OpenContext(ctx context.Context, roots []string, r ContextRequest) (ContextPackage, error) {
	var out ContextPackage
	err := c.call(ctx, roots, "OpenContext", contextParams{r}, &out)
	return out, err
}

func (c *Client) ProposeMemory(ctx context.Context, roots []string, m Memory, userAttested bool) (Memory, error) {
	var out Memory
	err := c.call(ctx, roots, "ProposeMemory", proposeParams{m, userAttested}, &out)
	return out, err
}

func (c *Client) GetMemory(ctx context.Context, roots []string, id string) (Memory, error) {
	var out Memory
	err := c.call(ctx, roots, "GetMemory", idParams{ID: id}, &out)
	return out, err
}

func (c *Client) SearchMemories(ctx context.Context, roots []string, q MemoryQuery) ([]Memory, error) {
	var out []Memory
	err := c.call(ctx, roots, "SearchMemories", searchMemoriesParams{q}, &out)
	return out, err
}

func (c *Client) CorrectMemory(ctx context.Context, roots []string, cor Correction) (Memory, error) {
	var out Memory
	err := c.call(ctx, roots, "CorrectMemory", correctParams{cor}, &out)
	return out, err
}

func (c *Client) ForgetEvent(ctx context.Context, roots []string, id string, actor string) error {
	return c.call(ctx, roots, "ForgetEvent", idParams{ID: id, Actor: actor}, nil)
}

func (c *Client) ForgetMemory(ctx context.Context, roots []string, id string, actor string) error {
	return c.call(ctx, roots, "ForgetMemory", idParams{ID: id, Actor: actor}, nil)
}

func (c *Client) Projects(ctx context.Context, roots []string) ([]ProjectSummary, error) {
	var out []ProjectSummary
	err := c.call(ctx, roots, "Projects", noParams{}, &out)
	return out, err
}

func (c *Client) UnclassifiedEvents(ctx context.Context, roots []string, limit int) ([]Event, error) {
	var out []Event
	err := c.call(ctx, roots, "UnclassifiedEvents", limitParams{limit}, &out)
	return out, err
}

func (c *Client) MarkClassified(ctx context.Context, roots []string, eventID string) error {
	return c.call(ctx, roots, "MarkClassified", idParams{ID: eventID}, nil)
}
