// Package vacuum is the protocol boundary to the robot-vacuum broker. The
// broker owns the vendor connection; this client validates every command in
// the verified core before it is forwarded.
package vacuum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

var ErrInput = errors.New("invalid vacuum command: use start, pause, stop, dock, locate, or clean_rooms; clean_rooms needs 1-32 distinct room IDs from vacuum_rooms, repeats 1-3, and suction quiet, standard, strong, or turbo")
var ErrUnavailable = errors.New("the robot vacuum is unavailable; check the vacuum service and that the vacuum is online")
var errReply = errors.New("the robot vacuum returned an invalid or unsupported response")

var suctionLevels = map[string]int{"": -1, "quiet": 0, "standard": 1, "strong": 2, "turbo": 3}

type Status struct {
	Name            string `json:"name"`
	Connected       bool   `json:"connected"`
	State           string `json:"state"`
	Battery         int    `json:"battery"`
	Docked          bool   `json:"docked"`
	Error           string `json:"error"`
	Suction         string `json:"suction"`
	CurrentRoom     string `json:"current_room"`
	CleanedAreaM2   int    `json:"cleaned_area_m2"`
	CleaningMinutes int    `json:"cleaning_minutes"`
}

type Room struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type RoomsResult struct {
	Rooms []Room `json:"rooms"`
}

type CommandInput struct {
	Action  string `json:"action" jsonschema:"one of start, pause, stop, dock, locate, clean_rooms"`
	Rooms   []int  `json:"rooms,omitempty" jsonschema:"room IDs from vacuum_rooms; only for clean_rooms"`
	Repeats int    `json:"repeats,omitempty" jsonschema:"passes per room, 1-3; only for clean_rooms; default 1"`
	Suction string `json:"suction,omitempty" jsonschema:"quiet, standard, strong, or turbo; only for clean_rooms; default keeps the current setting"`
}

type CommandResult struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message"`
}

type commandRequest struct {
	Action  string `json:"action"`
	Rooms   []int  `json:"rooms"`
	Repeats int    `json:"repeats"`
	Suction int    `json:"suction"`
}

type Client struct{ http *http.Client }

func New(socket string) (*Client, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 107 || strings.ContainsRune(socket, 0) || socket == "/" {
		return nil, errors.New("vacuum socket must be an absolute normalized Unix socket path of at most 107 bytes")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxConnsPerHost: 2, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	if err := c.post(ctx, "/status", struct{}{}, &out); err != nil {
		return Status{}, err
	}
	if out.Battery < 0 || out.Battery > 100 || out.CleanedAreaM2 < 0 || out.CleanedAreaM2 > 100000 || out.CleaningMinutes < 0 || out.CleaningMinutes > 100000 {
		return Status{}, errReply
	}
	for _, text := range []string{out.Name, out.State, out.Error, out.Suction, out.CurrentRoom} {
		if !boundedText(text, 256) {
			return Status{}, errReply
		}
	}
	return out, nil
}

func (c *Client) Rooms(ctx context.Context) (RoomsResult, error) {
	var out RoomsResult
	if err := c.post(ctx, "/rooms", struct{}{}, &out); err != nil {
		return RoomsResult{}, err
	}
	if out.Rooms == nil || len(out.Rooms) > 256 {
		return RoomsResult{}, errReply
	}
	for _, room := range out.Rooms {
		if room.ID < 1 || room.ID > 2147483647 || !boundedText(room.Name, 256) {
			return RoomsResult{}, errReply
		}
	}
	return out, nil
}

// Command normalizes defaults, checks the command in the verified core against
// the vacuum's current room list, and only then forwards it.
func (c *Client) Command(ctx context.Context, in CommandInput) (CommandResult, error) {
	suction, ok := suctionLevels[in.Suction]
	if !ok || !policy.VacuumAction(in.Action) {
		return CommandResult{}, ErrInput
	}
	request := commandRequest{Action: in.Action, Rooms: in.Rooms, Repeats: in.Repeats, Suction: suction}
	if request.Rooms == nil {
		request.Rooms = []int{}
	}
	var known []int
	if policy.VacuumNeedsRooms(in.Action) {
		if request.Repeats == 0 {
			request.Repeats = 1
		}
		rooms, err := c.Rooms(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		for _, room := range rooms.Rooms {
			known = append(known, room.ID)
		}
	}
	if !policy.ValidVacuumCommand(request.Action, request.Rooms, known, request.Repeats, request.Suction) {
		return CommandResult{}, ErrInput
	}
	var out CommandResult
	if err := c.post(ctx, "/command", request, &out); err != nil {
		return CommandResult{}, err
	}
	if !boundedText(out.Message, 512) {
		return CommandResult{}, errReply
	}
	return out, nil
}

func boundedText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	if c == nil || c.http == nil {
		return ErrUnavailable
	}
	body, err := json.Marshal(in)
	if err != nil {
		return ErrInput
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dreame-vacuum"+path, bytes.NewReader(body))
	if err != nil {
		return ErrInput
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(data) > 256*1024 {
		return errReply
	}
	if response.StatusCode != http.StatusOK {
		// The broker reports vacuum-side refusals (for example, "map not ready")
		// as a short message; pass it through only when it is plain bounded text.
		var failure struct {
			Error string `json:"error"`
		}
		if response.StatusCode == http.StatusConflict && json.Unmarshal(data, &failure) == nil && failure.Error != "" && boundedText(failure.Error, 256) {
			return errors.New("the robot vacuum refused: " + failure.Error)
		}
		return ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return errReply
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errReply
	}
	return nil
}
