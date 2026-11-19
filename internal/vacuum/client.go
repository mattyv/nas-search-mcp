// Package vacuum is the protocol boundary to the robot-vacuum broker. The
// broker owns the vendor connection; this client validates every command in
// the verified core before it is forwarded.
package vacuum

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

var ErrInput = errors.New("invalid vacuum request: check the action or setting name, use room IDs from vacuum_rooms, zones and points inside the map bounds from vacuum_rooms (zones at least 300 units per side, up to 5), repeats 1-3, and suction quiet, standard, strong, or turbo")
var ErrBusy = errors.New("the robot vacuum service is busy (for example, still signing in or loading the map); try again in a minute")
var ErrNoMap = errors.New("the robot vacuum has no saved map yet, so it cannot aim at zones or spots; let it finish a full mapping run first")
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
	// Box is the room's extent (x0, y0, x1, y1) in map units, when known.
	Box *[4]int `json:"box,omitempty"`
}

type RoomsResult struct {
	Rooms []Room `json:"rooms"`
	// Bounds is the saved map's extent in the same units as zones and points.
	Bounds *[4]int `json:"bounds,omitempty"`
}

type Details struct {
	Consumables map[string]int    `json:"consumables"`
	Station     map[string]string `json:"station"`
	Settings    map[string]string `json:"settings"`
}

type HistoryInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"number of recent cleaning runs, 1-20; default 10"`
}

type historyRequest struct {
	Limit int `json:"limit"`
}

type HistoryEntry struct {
	Time      int64  `json:"time"`
	Minutes   int    `json:"minutes"`
	AreaM2    int    `json:"area_m2"`
	Completed *bool  `json:"completed,omitempty"`
	Status    string `json:"status,omitempty"`
}

type HistoryResult struct {
	Entries []HistoryEntry `json:"entries"`
}

type MapImage struct {
	MIMEType string
	Data     []byte
}

type SettingInput struct {
	Setting string `json:"setting" jsonschema:"cleaning_mode, suction, mop_wetness, volume, dnd, dnd_start, dnd_end, child_lock, carpet_avoidance, carpet_recognition, room_mode, room_suction, room_wetness, or room_passes"`
	Value   string `json:"value" jsonschema:"modes: sweeping, mopping, sweeping_and_mopping, mopping_after_sweeping; suction: quiet, standard, strong, turbo; wetness: dry, moist, wet; volume 0-100; passes 1-3; on/off; times HH:MM"`
	Room    int    `json:"room,omitempty" jsonschema:"room ID from vacuum_rooms; required for room_ settings and not allowed otherwise"`
}

type settingRequest struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Room  int    `json:"room"`
}

type CommandInput struct {
	Action  string   `json:"action" jsonschema:"one of start, pause, stop, dock, locate, clean_rooms, clean_zones, clean_spot, go_to, empty_dustbin, wash_mops, dry_mops, stop_drying"`
	Rooms   []int    `json:"rooms,omitempty" jsonschema:"room IDs from vacuum_rooms; only for clean_rooms"`
	Zones   [][4]int `json:"zones,omitempty" jsonschema:"up to 5 rectangles [x0, y0, x1, y1] inside the map bounds from vacuum_rooms; only for clean_zones"`
	Point   *[2]int  `json:"point,omitempty" jsonschema:"[x, y] inside the map bounds from vacuum_rooms; only for clean_spot and go_to"`
	Repeats int      `json:"repeats,omitempty" jsonschema:"passes, 1-3; only for clean_rooms, clean_zones, clean_spot; default 1"`
	Suction string   `json:"suction,omitempty" jsonschema:"quiet, standard, strong, or turbo; only for clean_rooms, clean_zones, clean_spot; default keeps the current setting"`
}

type CommandResult struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message"`
}

type MoveInput struct {
	Turn  int `json:"turn,omitempty" jsonschema:"degrees to turn in this step, -360 to 360; positive turns left"`
	Speed int `json:"speed,omitempty" jsonschema:"forward speed for this step, -600 to 600; negative reverses; 200 is a gentle roll"`
}

type moveRequest struct {
	Rotation int `json:"rotation"`
	Velocity int `json:"velocity"`
}

type Obstacle struct {
	Index      int    `json:"index"`
	Type       string `json:"type"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Confidence int    `json:"confidence"`
	HasPhoto   bool   `json:"has_photo"`
}

type ObstaclesResult struct {
	Obstacles []Obstacle `json:"obstacles"`
}

type ObstaclePhotoInput struct {
	Index int `json:"index" jsonschema:"obstacle index from vacuum_obstacles with has_photo true"`
}

type obstacleRequest struct {
	Index int `json:"index"`
}

type Shortcut struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Running bool     `json:"running"`
	Rooms   []string `json:"rooms"`
}

type ShortcutsResult struct {
	Shortcuts []Shortcut `json:"shortcuts"`
}

type ShortcutInput struct {
	Action string `json:"action" jsonschema:"start, rename, or delete"`
	ID     int    `json:"id" jsonschema:"shortcut ID from vacuum_shortcuts"`
	Name   string `json:"name,omitempty" jsonschema:"new name, 1-32 characters; only for rename"`
}

type shortcutRequest struct {
	Action string `json:"action"`
	ID     int    `json:"id"`
	Name   string `json:"name"`
}

type Choice struct {
	Label string `json:"label"`
	Value int    `json:"value"`
}

// Option is one setting the vacuum has on this model, as the original
// integration exposes it: an on/off switch, a choice, a number, or a time.
type Option struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Current string   `json:"current"`
	Choices []Choice `json:"choices,omitempty"`
	Min     int      `json:"min,omitempty"`
	Max     int      `json:"max,omitempty"`
	Step    int      `json:"step,omitempty"`
}

type OptionsResult struct {
	Options []Option `json:"options"`
}

type OptionsInput struct {
	Search string `json:"search,omitempty" jsonschema:"optional words to filter setting names, for example mop or carpet"`
}

type SetOptionInput struct {
	Name  string `json:"name" jsonschema:"setting name from vacuum_options"`
	Value string `json:"value" jsonschema:"for switches on or off; for choices one of the listed labels; for numbers a number on the step; for times HH:MM"`
}

type optionRequest struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type Button struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type ButtonsResult struct {
	Buttons []Button `json:"buttons"`
}

type PressInput struct {
	Name string `json:"name" jsonschema:"button name from vacuum_buttons"`
}

type pressRequest struct {
	Name string `json:"name"`
}

type MapBackup struct {
	Index int    `json:"index"`
	Date  string `json:"date"`
}

type MapInfo struct {
	ID       int         `json:"id"`
	Name     string      `json:"name"`
	Selected bool        `json:"selected"`
	Backups  []MapBackup `json:"backups"`
}

type MapsResult struct {
	Maps         []MapInfo `json:"maps"`
	TemporaryMap bool      `json:"temporary_map"`
}

type MapActionInput struct {
	Action string `json:"action" jsonschema:"select, rename, delete, backup, restore, request, save_temporary, discard_temporary, or replace_temporary"`
	MapID  int    `json:"map_id,omitempty" jsonschema:"map ID from vacuum_maps; not used for request, save_temporary or discard_temporary"`
	Name   string `json:"name,omitempty" jsonschema:"new map name, 1-32 characters; only for rename"`
	Backup int    `json:"backup,omitempty" jsonschema:"backup index from vacuum_maps; only for restore"`
}

type mapActionRequest struct {
	Action string `json:"action"`
	MapID  int    `json:"map_id"`
	Name   string `json:"name"`
	Backup int    `json:"backup"`
}

type RoomActionInput struct {
	Action string  `json:"action" jsonschema:"rename, set_type, merge, split, order, or hide"`
	Rooms  []int   `json:"rooms" jsonschema:"room IDs from vacuum_rooms: one for rename, set_type and split; two for merge; every room in cleaning order for order (empty clears it); rooms to hide for hide (empty shows all)"`
	Name   string  `json:"name,omitempty" jsonschema:"new room name, 1-32 characters; only for rename"`
	Type   string  `json:"type,omitempty" jsonschema:"room type label from vacuum_room_options types; only for set_type"`
	Line   *[4]int `json:"line,omitempty" jsonschema:"[x0, y0, x1, y1] dividing line inside the map; only for split"`
}

type roomActionRequest struct {
	Action string  `json:"action"`
	Rooms  []int   `json:"rooms"`
	Name   string  `json:"name"`
	Type   int     `json:"type"`
	Line   *[4]int `json:"line"`
}

type RoomOptionsInput struct {
	Room int `json:"room" jsonschema:"room ID from vacuum_rooms"`
}

type RoomOptionsResult struct {
	Options []Option `json:"options"`
	Types   []Choice `json:"types"`
}

type roomOptionsRequest struct {
	Room int `json:"room"`
}

type SetRoomOptionInput struct {
	Room  int    `json:"room" jsonschema:"room ID from vacuum_rooms"`
	Name  string `json:"name" jsonschema:"setting name from vacuum_room_options"`
	Value string `json:"value" jsonschema:"one of the listed labels, or a number on the step within the range"`
}

type roomOptionRequest struct {
	Room  int    `json:"room"`
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// MapElements maps each editable kind (no_go_zones, virtual_walls, ...) to its
// current items: [x0, y0, x1, y1] rectangles or lines, or [x, y] points.
type MapElements map[string][][]int

type MapEditInput struct {
	Element string  `json:"element" jsonschema:"no_go_zones, no_mop_zones, ramps, low_lying_areas, carpets (rectangles [x0, y0, x1, y1]); virtual_walls, virtual_thresholds, passable_thresholds, impassable_thresholds, curtains (lines [x0, y0, x1, y1]); predefined_points (points [x, y]); router_position (one point [x, y])"`
	Items   [][]int `json:"items" jsonschema:"the complete new list for that kind, replacing the current one; read vacuum_map_elements first and include the items to keep; empty removes them all"`
}

type mapEditRequest struct {
	Element string  `json:"element"`
	Items   [][]int `json:"items"`
}

type PatrolInput struct {
	Points [][2]int `json:"points" jsonschema:"1-20 points [x, y] inside the map, visited in order"`
}

type pathRequest struct {
	Points [][2]int `json:"points"`
}

type IgnoreObstacleInput struct {
	Index  int  `json:"index" jsonschema:"obstacle index from vacuum_obstacles"`
	Ignore bool `json:"ignore" jsonschema:"true to stop avoiding it; false to avoid it again"`
}

type ignoreRequest struct {
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Ignore bool `json:"ignore"`
}

type commandRequest struct {
	Action  string   `json:"action"`
	Rooms   []int    `json:"rooms"`
	Zones   [][4]int `json:"zones"`
	Point   *[2]int  `json:"point"`
	Repeats int      `json:"repeats"`
	Suction int      `json:"suction"`
}

type Client struct {
	http *http.Client
	// Deadlines stay under the assistant's own tool timeout, so a slow vacuum
	// returns ErrBusy instead of hanging the conversation.
	quick, slow time.Duration
}

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
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		quick: 20 * time.Second, slow: 40 * time.Second}, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	if err := c.post(ctx, "/status", struct{}{}, &out); err != nil {
		return Status{}, err
	}
	if out.Battery < 0 || out.Battery > 100 || out.CleanedAreaM2 < 0 || out.CleanedAreaM2 > 100000 || out.CleaningMinutes < 0 || out.CleaningMinutes > 100000 {
		return Status{}, errReply
	}
	out.Name = strings.TrimSpace(out.Name)
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
	if out.Rooms == nil || len(out.Rooms) > 256 || (out.Bounds != nil && !validBox(*out.Bounds)) {
		return RoomsResult{}, errReply
	}
	for _, room := range out.Rooms {
		if room.ID < 1 || room.ID > 2147483647 || !boundedText(room.Name, 256) || (room.Box != nil && !validBox(*room.Box)) {
			return RoomsResult{}, errReply
		}
	}
	return out, nil
}

// Command normalizes defaults, checks the command in the verified core against
// the vacuum's current rooms or map bounds, and only then forwards it.
func (c *Client) Command(ctx context.Context, in CommandInput) (CommandResult, error) {
	suction, ok := suctionLevels[in.Suction]
	if !ok || !policy.VacuumAction(in.Action) {
		return CommandResult{}, ErrInput
	}
	request := commandRequest{Action: in.Action, Rooms: in.Rooms, Zones: in.Zones, Point: in.Point, Repeats: in.Repeats, Suction: suction}
	if request.Rooms == nil {
		request.Rooms = []int{}
	}
	if request.Zones == nil {
		request.Zones = [][4]int{}
	}
	aimed := policy.VacuumNeedsRooms(in.Action) || (policy.VacuumNeedsMap(in.Action) && in.Action != "go_to")
	if aimed && request.Repeats == 0 {
		request.Repeats = 1
	}
	if !policy.VacuumTargetsMatch(in.Action, len(request.Rooms), len(request.Zones), request.Point != nil) {
		return CommandResult{}, ErrInput
	}
	switch {
	case policy.VacuumNeedsMap(in.Action):
		rooms, err := c.Rooms(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		if rooms.Bounds == nil {
			return CommandResult{}, ErrNoMap
		}
		valid := false
		switch in.Action {
		case "clean_zones":
			valid = policy.ValidVacuumZones(request.Zones, *rooms.Bounds, request.Repeats, request.Suction)
		case "go_to":
			valid = policy.ValidVacuumGoTo(request.Point[0], request.Point[1], *rooms.Bounds, request.Repeats, request.Suction)
		default:
			valid = policy.ValidVacuumSpot(request.Point[0], request.Point[1], *rooms.Bounds, request.Repeats, request.Suction)
		}
		if !valid {
			return CommandResult{}, ErrInput
		}
	default:
		var known []int
		if policy.VacuumNeedsRooms(in.Action) {
			rooms, err := c.Rooms(ctx)
			if err != nil {
				return CommandResult{}, err
			}
			known = roomIDs(rooms)
		}
		if !policy.ValidVacuumCommand(request.Action, request.Rooms, known, request.Repeats, request.Suction) {
			return CommandResult{}, ErrInput
		}
	}
	return c.accepted(ctx, "/command", request)
}

func roomIDs(rooms RoomsResult) []int {
	known := make([]int, 0, len(rooms.Rooms))
	for _, room := range rooms.Rooms {
		known = append(known, room.ID)
	}
	return known
}

func (c *Client) accepted(ctx context.Context, path string, request any) (CommandResult, error) {
	var out CommandResult
	if err := c.post(ctx, path, request, &out); err != nil {
		return CommandResult{}, err
	}
	if !boundedText(out.Message, 512) {
		return CommandResult{}, errReply
	}
	return out, nil
}

var settingWords = map[string]map[string]int{
	"mode":    {"sweeping": 0, "mopping": 1, "sweeping_and_mopping": 2, "mopping_after_sweeping": 3},
	"suction": {"quiet": 0, "standard": 1, "strong": 2, "turbo": 3},
	"wetness": {"dry": 0, "moist": 1, "wet": 2},
	"switch":  {"off": 0, "on": 1, "false": 0, "true": 1},
}

var settingKinds = map[string]string{
	"cleaning_mode": "mode", "room_mode": "mode", "suction": "suction", "room_suction": "suction",
	"mop_wetness": "wetness", "room_wetness": "wetness", "dnd": "switch", "child_lock": "switch",
	"carpet_avoidance": "switch", "carpet_recognition": "switch", "volume": "number", "room_passes": "number",
	"dnd_start": "time", "dnd_end": "time",
}

var clockTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// parseSetting turns the assistant's words into the integer the core checks.
func parseSetting(name, value string) (int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch kind := settingKinds[name]; kind {
	case "number":
		number, err := strconv.Atoi(value)
		return number, err == nil
	case "time":
		parts := clockTime.FindStringSubmatch(value)
		if parts == nil {
			return 0, false
		}
		hours, _ := strconv.Atoi(parts[1])
		minutes, _ := strconv.Atoi(parts[2])
		return hours*60 + minutes, true
	case "":
		return 0, false
	default:
		number, ok := settingWords[kind][value]
		return number, ok
	}
}

// Setting parses one setting change, checks it in the verified core (including
// that a per-room setting names a reported room), and only then forwards it.
func (c *Client) Setting(ctx context.Context, in SettingInput) (CommandResult, error) {
	value, ok := parseSetting(in.Setting, in.Value)
	if !ok {
		return CommandResult{}, ErrInput
	}
	var known []int
	if policy.VacuumSettingNeedsRoom(in.Setting) {
		rooms, err := c.Rooms(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		known = roomIDs(rooms)
	}
	if !policy.ValidVacuumSetting(in.Setting, value, in.Room, known) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/setting", settingRequest{Name: in.Setting, Value: value, Room: in.Room})
}

var detailKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func (c *Client) Details(ctx context.Context) (Details, error) {
	var out Details
	if err := c.post(ctx, "/details", struct{}{}, &out); err != nil {
		return Details{}, err
	}
	if out.Consumables == nil || out.Station == nil || out.Settings == nil || len(out.Consumables) > 40 || len(out.Station) > 40 || len(out.Settings) > 40 {
		return Details{}, errReply
	}
	for key, percent := range out.Consumables {
		if !detailKey.MatchString(key) || percent < 0 || percent > 100 {
			return Details{}, errReply
		}
	}
	for _, values := range []map[string]string{out.Station, out.Settings} {
		for key, value := range values {
			if !detailKey.MatchString(key) || !boundedText(value, 128) {
				return Details{}, errReply
			}
		}
	}
	return out, nil
}

func (c *Client) History(ctx context.Context, in HistoryInput) (HistoryResult, error) {
	if in.Limit == 0 {
		in.Limit = 10
	}
	if !policy.ValidVacuumHistoryLimit(in.Limit) {
		return HistoryResult{}, ErrInput
	}
	var out HistoryResult
	if err := c.post(ctx, "/history", historyRequest{Limit: in.Limit}, &out); err != nil {
		return HistoryResult{}, err
	}
	if out.Entries == nil || len(out.Entries) > in.Limit {
		return HistoryResult{}, errReply
	}
	for _, entry := range out.Entries {
		if entry.Time < 0 || entry.Minutes < 0 || entry.Minutes > 100000 || entry.AreaM2 < 0 || entry.AreaM2 > 100000 || !boundedText(entry.Status, 128) {
			return HistoryResult{}, errReply
		}
	}
	return out, nil
}

const maxMapBytes = 2 * 1024 * 1024

// Map returns the vacuum's map picture as a freshly encoded PNG, so only pixel
// data (no embedded text chunks or metadata) reaches the assistant.
func (c *Client) Map(ctx context.Context) (MapImage, error) {
	var reply struct {
		MIMEType string `json:"mime_type"`
		Data     string `json:"data"`
	}
	if err := c.post(ctx, "/map", struct{}{}, &reply); err != nil {
		return MapImage{}, err
	}
	if reply.MIMEType != "image/png" || len(reply.Data) > base64.StdEncoding.EncodedLen(maxMapBytes) {
		return MapImage{}, errReply
	}
	data, err := base64.StdEncoding.Strict().DecodeString(reply.Data)
	if err != nil {
		return MapImage{}, errReply
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return MapImage{}, errReply
	}
	pixels, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return MapImage{}, errReply
	}
	var clean bytes.Buffer
	if err := png.Encode(&clean, pixels); err != nil || clean.Len() > maxMapBytes {
		return MapImage{}, errReply
	}
	return MapImage{MIMEType: "image/png", Data: clean.Bytes()}, nil
}

// Move sends one remote-control step after checking it in the verified core.
func (c *Client) Move(ctx context.Context, in MoveInput) (CommandResult, error) {
	if !policy.ValidVacuumMove(in.Turn, in.Speed) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/move", moveRequest{Rotation: in.Turn, Velocity: in.Speed})
}

var obstacleType = regexp.MustCompile(`^[a-z][a-z0-9_ ]{0,39}$`)

func (c *Client) Obstacles(ctx context.Context) (ObstaclesResult, error) {
	var out ObstaclesResult
	if err := c.post(ctx, "/obstacles", struct{}{}, &out); err != nil {
		return ObstaclesResult{}, err
	}
	if out.Obstacles == nil || len(out.Obstacles) > 200 {
		return ObstaclesResult{}, errReply
	}
	for _, o := range out.Obstacles {
		if o.Index < 1 || o.Index > 100000 || !obstacleType.MatchString(o.Type) || o.Confidence < 0 || o.Confidence > 100 ||
			o.X < -1000000 || o.X > 1000000 || o.Y < -1000000 || o.Y > 1000000 {
			return ObstaclesResult{}, errReply
		}
	}
	return out, nil
}

const maxPhotoBytes = 2 * 1024 * 1024

// ObstaclePhoto fetches a photo only for an obstacle the vacuum reported with
// one, then re-encodes it so only pixels (no metadata) reach the assistant.
func (c *Client) ObstaclePhoto(ctx context.Context, in ObstaclePhotoInput) (MapImage, error) {
	list, err := c.Obstacles(ctx)
	if err != nil {
		return MapImage{}, err
	}
	var known []int
	for _, o := range list.Obstacles {
		if o.HasPhoto {
			known = append(known, o.Index)
		}
	}
	if !policy.ValidVacuumObstacle(in.Index, known) {
		return MapImage{}, ErrInput
	}
	var reply struct {
		MIMEType string `json:"mime_type"`
		Data     string `json:"data"`
	}
	if err := c.post(ctx, "/obstacle_photo", obstacleRequest{Index: in.Index}, &reply); err != nil {
		return MapImage{}, err
	}
	if reply.MIMEType != "image/jpeg" || len(reply.Data) > base64.StdEncoding.EncodedLen(maxPhotoBytes) {
		return MapImage{}, errReply
	}
	data, err := base64.StdEncoding.Strict().DecodeString(reply.Data)
	if err != nil {
		return MapImage{}, errReply
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return MapImage{}, errReply
	}
	var pixels image.Image
	if pixels, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		return MapImage{}, errReply
	}
	var clean bytes.Buffer
	if err := jpeg.Encode(&clean, pixels, &jpeg.Options{Quality: 85}); err != nil || clean.Len() > maxPhotoBytes {
		return MapImage{}, errReply
	}
	return MapImage{MIMEType: "image/jpeg", Data: clean.Bytes()}, nil
}

func (c *Client) Shortcuts(ctx context.Context) (ShortcutsResult, error) {
	var out ShortcutsResult
	if err := c.post(ctx, "/shortcuts", struct{}{}, &out); err != nil {
		return ShortcutsResult{}, err
	}
	if out.Shortcuts == nil || len(out.Shortcuts) > 104 {
		return ShortcutsResult{}, errReply
	}
	for _, s := range out.Shortcuts {
		if s.ID < 1 || s.ID > 1000 || !boundedText(s.Name, 64) || len(s.Rooms) > 64 {
			return ShortcutsResult{}, errReply
		}
		for _, room := range s.Rooms {
			if !boundedText(room, 64) {
				return ShortcutsResult{}, errReply
			}
		}
	}
	return out, nil
}

// Shortcut starts, renames or deletes an app shortcut, checked in the verified
// core against the shortcuts the vacuum currently lists.
func (c *Client) Shortcut(ctx context.Context, in ShortcutInput) (CommandResult, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !boundedText(in.Name, 64) {
		return CommandResult{}, ErrInput
	}
	list, err := c.Shortcuts(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	known := make([]int, 0, len(list.Shortcuts))
	for _, s := range list.Shortcuts {
		known = append(known, s.ID)
	}
	if !policy.ValidVacuumShortcut(in.Action, in.ID, known, utf8.RuneCountInString(in.Name)) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/shortcut", shortcutRequest{Action: in.Action, ID: in.ID, Name: in.Name})
}

var optionName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (c *Client) allOptions(ctx context.Context) (OptionsResult, error) {
	var out OptionsResult
	if err := c.post(ctx, "/options", struct{}{}, &out); err != nil {
		return OptionsResult{}, err
	}
	if out.Options == nil || len(out.Options) > 400 {
		return OptionsResult{}, errReply
	}
	if _, err := validOptions(out.Options); err != nil {
		return OptionsResult{}, err
	}
	return out, nil
}

func validOptions(options []Option) ([]Option, error) {
	for _, o := range options {
		if !optionName.MatchString(o.Name) || !boundedText(o.Current, 64) || len(o.Choices) > 64 {
			return nil, errReply
		}
		switch o.Kind {
		case "switch", "select":
			if len(o.Choices) == 0 {
				return nil, errReply
			}
		case "number":
			if o.Step < 1 || o.Min > o.Max {
				return nil, errReply
			}
		case "time":
		default:
			return nil, errReply
		}
		for _, choice := range o.Choices {
			if !boundedText(choice.Label, 64) || choice.Label == "" {
				return nil, errReply
			}
		}
	}
	return options, nil
}

// Options lists every setting the vacuum has, optionally filtered by words.
func (c *Client) Options(ctx context.Context, in OptionsInput) (OptionsResult, error) {
	if !boundedText(in.Search, 64) {
		return OptionsResult{}, ErrInput
	}
	all, err := c.allOptions(ctx)
	if err != nil {
		return OptionsResult{}, err
	}
	words := strings.Fields(strings.ToLower(in.Search))
	out := OptionsResult{Options: []Option{}}
	for _, o := range all.Options {
		keep := true
		for _, word := range words {
			if !strings.Contains(o.Name, word) {
				keep = false
			}
		}
		if keep {
			out.Options = append(out.Options, o)
		}
	}
	return out, nil
}

var switchWords = map[string]string{"true": "on", "enable": "on", "enabled": "on", "false": "off", "disable": "off", "disabled": "off"}

// parseOptionValue turns the assistant's words into the integer the core checks.
func parseOptionValue(o Option, value string) (int, bool) {
	value = strings.TrimSpace(value)
	switch o.Kind {
	case "switch", "select":
		lowered := strings.ToLower(value)
		if synonym, ok := switchWords[lowered]; ok && o.Kind == "switch" {
			lowered = synonym
		}
		for _, choice := range o.Choices {
			if strings.ToLower(choice.Label) == lowered {
				return choice.Value, true
			}
		}
		return 0, false
	case "number":
		number, err := strconv.Atoi(value)
		return number, err == nil
	case "time":
		parts := clockTime.FindStringSubmatch(value)
		if parts == nil {
			return 0, false
		}
		hours, _ := strconv.Atoi(parts[1])
		minutes, _ := strconv.Atoi(parts[2])
		return hours*60 + minutes, true
	}
	return 0, false
}

// reportedOptions converts reported settings into the core's form and parses
// the requested value for the named one.
func reportedOptions(options []Option, name, raw string) ([]policy.VacuumOption, int, bool) {
	reported := make([]policy.VacuumOption, 0, len(options))
	value, parsed := 0, false
	for _, o := range options {
		entry := policy.VacuumOption{Name: o.Name, Lowest: o.Min, Highest: o.Max, Step: o.Step}
		switch o.Kind {
		case "switch", "select":
			for _, choice := range o.Choices {
				entry.Allowed = append(entry.Allowed, choice.Value)
			}
		case "time":
			entry.Lowest, entry.Highest, entry.Step = 0, 1439, 1
		}
		reported = append(reported, entry)
		if o.Name == name {
			value, parsed = parseOptionValue(o, raw)
		}
	}
	return reported, value, parsed
}

// SetOption changes one reported setting after the verified core accepts the
// value against the setting's allowed values or range.
func (c *Client) SetOption(ctx context.Context, in SetOptionInput) (CommandResult, error) {
	all, err := c.allOptions(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	reported, value, parsed := reportedOptions(all.Options, in.Name, in.Value)
	if !parsed || !policy.ValidVacuumOption(in.Name, value, reported) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/set_option", optionRequest{Name: in.Name, Value: value})
}

func (c *Client) Buttons(ctx context.Context) (ButtonsResult, error) {
	var out ButtonsResult
	if err := c.post(ctx, "/buttons", struct{}{}, &out); err != nil {
		return ButtonsResult{}, err
	}
	if out.Buttons == nil || len(out.Buttons) > 64 {
		return ButtonsResult{}, errReply
	}
	for _, b := range out.Buttons {
		if !optionName.MatchString(b.Name) || (b.Kind != "reset" && b.Kind != "action") {
			return ButtonsResult{}, errReply
		}
	}
	return out, nil
}

// Press presses one button the vacuum listed, after the verified core check.
func (c *Client) Press(ctx context.Context, in PressInput) (CommandResult, error) {
	list, err := c.Buttons(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	known := make([]string, 0, len(list.Buttons))
	for _, b := range list.Buttons {
		known = append(known, b.Name)
	}
	if !policy.ValidVacuumButton(in.Name, known) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/press", pressRequest{Name: in.Name})
}

func (c *Client) Maps(ctx context.Context) (MapsResult, error) {
	var out MapsResult
	if err := c.post(ctx, "/maps", struct{}{}, &out); err != nil {
		return MapsResult{}, err
	}
	if out.Maps == nil || len(out.Maps) > 16 {
		return MapsResult{}, errReply
	}
	for _, m := range out.Maps {
		if m.ID < 1 || m.ID > 1000000 || !boundedText(m.Name, 64) || len(m.Backups) > 32 {
			return MapsResult{}, errReply
		}
		for _, b := range m.Backups {
			if b.Index < 1 || b.Index > 32 || !boundedText(b.Date, 64) {
				return MapsResult{}, errReply
			}
		}
	}
	return out, nil
}

// ManageMap selects, renames, deletes, backs up or restores a listed map,
// after the verified core checks the map and backup against vacuum_maps.
func (c *Client) ManageMap(ctx context.Context, in MapActionInput) (CommandResult, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !boundedText(in.Name, 64) {
		return CommandResult{}, ErrInput
	}
	maps, err := c.Maps(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	known, backups := []int{}, 0
	for _, m := range maps.Maps {
		known = append(known, m.ID)
		if m.ID == in.MapID {
			backups = len(m.Backups)
		}
	}
	if !policy.ValidVacuumMapAction(in.Action, in.MapID, known, utf8.RuneCountInString(in.Name), in.Backup, backups) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/manage_map", mapActionRequest{Action: in.Action, MapID: in.MapID, Name: in.Name, Backup: in.Backup})
}

func (c *Client) roomOptions(ctx context.Context, room int) (RoomOptionsResult, error) {
	var out RoomOptionsResult
	if err := c.post(ctx, "/room_options", roomOptionsRequest{Room: room}, &out); err != nil {
		return RoomOptionsResult{}, err
	}
	if out.Options == nil || len(out.Options) > 64 || len(out.Types) > 64 {
		return RoomOptionsResult{}, errReply
	}
	if _, err := validOptions(out.Options); err != nil {
		return RoomOptionsResult{}, err
	}
	for _, t := range out.Types {
		if !boundedText(t.Label, 64) || t.Label == "" {
			return RoomOptionsResult{}, errReply
		}
	}
	return out, nil
}

// reportedRoom asks the core whether room is one the vacuum reported.
func reportedRoom(room int, known []int) bool {
	return policy.ValidVacuumCommand("clean_rooms", []int{room}, known, 1, -1)
}

// RoomOptions lists the per-room settings and room types for one reported room.
func (c *Client) RoomOptions(ctx context.Context, in RoomOptionsInput) (RoomOptionsResult, error) {
	rooms, err := c.Rooms(ctx)
	if err != nil {
		return RoomOptionsResult{}, err
	}
	if !reportedRoom(in.Room, roomIDs(rooms)) {
		return RoomOptionsResult{}, ErrInput
	}
	return c.roomOptions(ctx, in.Room)
}

// ManageRooms renames, retypes, merges, splits, orders or hides rooms after
// the verified core checks every room, type and line against the vacuum.
func (c *Client) ManageRooms(ctx context.Context, in RoomActionInput) (CommandResult, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !boundedText(in.Name, 64) || !boundedText(in.Type, 64) || len(in.Rooms) > 64 {
		return CommandResult{}, ErrInput
	}
	rooms, err := c.Rooms(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	request := roomActionRequest{Action: in.Action, Rooms: in.Rooms, Name: in.Name, Line: in.Line}
	if request.Rooms == nil {
		request.Rooms = []int{}
	}
	var types []int
	if in.Action == "set_type" && len(in.Rooms) == 1 {
		options, err := c.roomOptions(ctx, in.Rooms[0])
		if err != nil {
			return CommandResult{}, err
		}
		request.Type = -1
		for _, t := range options.Types {
			types = append(types, t.Value)
			if strings.EqualFold(t.Label, in.Type) {
				request.Type = t.Value
			}
		}
	}
	if in.Line != nil && (rooms.Bounds == nil || !policy.ValidVacuumLine(*in.Line, *rooms.Bounds)) {
		return CommandResult{}, ErrInput
	}
	if !policy.ValidVacuumRoomAction(in.Action, request.Rooms, roomIDs(rooms), utf8.RuneCountInString(in.Name), request.Type, types, in.Line != nil) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/manage_rooms", request)
}

// SetRoomOption changes one per-room setting for a reported room.
func (c *Client) SetRoomOption(ctx context.Context, in SetRoomOptionInput) (CommandResult, error) {
	rooms, err := c.Rooms(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	known := roomIDs(rooms)
	if !reportedRoom(in.Room, known) {
		return CommandResult{}, ErrInput
	}
	options, err := c.roomOptions(ctx, in.Room)
	if err != nil {
		return CommandResult{}, err
	}
	reported, value, parsed := reportedOptions(options.Options, in.Name, in.Value)
	if !parsed || !policy.ValidVacuumRoomOption(in.Room, known, in.Name, value, reported) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/set_room_option", roomOptionRequest{Room: in.Room, Name: in.Name, Value: value})
}

var elementName = regexp.MustCompile(`^[a-z_]{1,40}$`)

func (c *Client) MapElements(ctx context.Context) (MapElements, error) {
	out := MapElements{}
	if err := c.post(ctx, "/map_elements", struct{}{}, &out); err != nil {
		return nil, err
	}
	if len(out) > 20 {
		return nil, errReply
	}
	for name, items := range out {
		if !elementName.MatchString(name) || len(items) > 200 {
			return nil, errReply
		}
		for _, item := range items {
			if len(item) != 2 && len(item) != 4 {
				return nil, errReply
			}
		}
	}
	return out, nil
}

// EditMap replaces one kind of map element after the verified core checks
// every item's shape and that it lies inside the saved map.
func (c *Client) EditMap(ctx context.Context, in MapEditInput) (CommandResult, error) {
	if len(in.Items) > 30 {
		return CommandResult{}, ErrInput
	}
	items := make([][4]int, 0, len(in.Items))
	for _, item := range in.Items {
		switch len(item) {
		case 2:
			items = append(items, [4]int{item[0], item[1], item[0], item[1]})
		case 4:
			items = append(items, [4]int{item[0], item[1], item[2], item[3]})
		default:
			return CommandResult{}, ErrInput
		}
	}
	rooms, err := c.Rooms(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if rooms.Bounds == nil {
		return CommandResult{}, ErrNoMap
	}
	if !policy.ValidVacuumMapEdit(in.Element, items, *rooms.Bounds) {
		return CommandResult{}, ErrInput
	}
	request := mapEditRequest{Element: in.Element, Items: in.Items}
	if request.Items == nil {
		request.Items = [][]int{}
	}
	return c.accepted(ctx, "/edit_map", request)
}

// Patrol sends the vacuum along points inside the map (camera models).
func (c *Client) Patrol(ctx context.Context, in PatrolInput) (CommandResult, error) {
	rooms, err := c.Rooms(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if rooms.Bounds == nil {
		return CommandResult{}, ErrNoMap
	}
	if !policy.ValidVacuumPath(in.Points, *rooms.Bounds) {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/patrol", pathRequest{Points: in.Points})
}

// IgnoreObstacle stops (or resumes) avoiding one reported obstacle.
func (c *Client) IgnoreObstacle(ctx context.Context, in IgnoreObstacleInput) (CommandResult, error) {
	list, err := c.Obstacles(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	known := make([]int, 0, len(list.Obstacles))
	var target *Obstacle
	for i, o := range list.Obstacles {
		known = append(known, o.Index)
		if o.Index == in.Index {
			target = &list.Obstacles[i]
		}
	}
	if !policy.ValidVacuumObstacle(in.Index, known) || target == nil {
		return CommandResult{}, ErrInput
	}
	return c.accepted(ctx, "/ignore_obstacle", ignoreRequest{X: target.X, Y: target.Y, Ignore: in.Ignore})
}

func validBox(box [4]int) bool {
	const limit = 1000000
	return box[0] < box[2] && box[1] < box[3] && box[0] > -limit && box[1] > -limit && box[2] < limit && box[3] < limit
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
	limit := c.quick
	if path == "/map" || path == "/command" || path == "/setting" || path == "/move" || path == "/obstacle_photo" || path == "/shortcut" || path == "/set_option" || path == "/press" || path == "/manage_map" || path == "/manage_rooms" || path == "/set_room_option" || path == "/edit_map" || path == "/patrol" || path == "/ignore_obstacle" {
		limit = c.slow
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dreame-vacuum"+path, bytes.NewReader(body))
	if err != nil {
		return ErrInput
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return ErrBusy
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 3*1024*1024+1))
	if err != nil || len(data) > 3*1024*1024 {
		return errReply
	}
	if response.StatusCode != http.StatusOK {
		// The broker reports vacuum-side refusals (for example, "map not ready")
		// as a short message; pass it through only when it is plain bounded text.
		var failure struct {
			Error string `json:"error"`
		}
		readable := json.Unmarshal(data, &failure) == nil && failure.Error != "" && boundedText(failure.Error, 256)
		if response.StatusCode == http.StatusConflict && readable {
			return errors.New("the robot vacuum refused: " + failure.Error)
		}
		if response.StatusCode == http.StatusServiceUnavailable && readable {
			// The broker says why it is not connected, such as a failed sign-in.
			return fmt.Errorf("%w (%s)", ErrUnavailable, failure.Error)
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
