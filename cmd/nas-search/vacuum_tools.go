package main

import (
	"context"

	"github.com/mattyv/nas-search-mcp/internal/vacuum"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type VacuumEmptyInput struct{}

// configuredVacuum returns a client only when the optional vacuum service is
// turned on; the socket defaults to the installer-created IPC directory.
func configuredVacuum() (*vacuum.Client, error) {
	if !enabled("NAS_VACUUM_ENABLED", false) {
		return nil, nil
	}
	return vacuum.New(env("NAS_VACUUM_SOCKET", "/data/vacuum-ipc/vacuum.sock"))
}

// addVacuumTools registers the robot-vacuum tools only when the vacuum broker
// is configured. Every control command is checked in the verified core first.
func addVacuumTools(server *mcp.Server) {
	client, err := configuredVacuum()
	if client == nil || err != nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_status", Description: "Show the robot vacuum's current state: activity, battery, whether it is docked, any error, suction level, the room it is in, and the current job's cleaned area and minutes. Status can lag the vacuum by up to a minute.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.Status, error) {
		out, err := client.Status(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_rooms", Description: "List the rooms on the robot vacuum's saved map with their IDs, each room's box [x0, y0, x1, y1], and the map bounds, all in map units (millimetres). Use room IDs with vacuum_control clean_rooms and with room_ settings in vacuum_settings; use boxes and bounds to aim clean_zones and clean_spot. Empty when the vacuum has no saved map yet.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.RoomsResult, error) {
		out, err := client.Rooms(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_control", Description: "Control the robot vacuum. action: start (clean everywhere), pause, stop, dock (return to the base), locate (play a sound), clean_rooms (rooms: IDs from vacuum_rooms), clean_zones (zones: up to 5 rectangles [x0, y0, x1, y1] inside the map bounds, at least 300 units per side), clean_spot (point: [x, y] inside the map bounds), go_to (point: drive to [x, y] and stop there, without cleaning; use a room's box centre to reach a room), empty_dustbin, wash_mops, dry_mops, or stop_drying. Cleaning actions take optional repeats (1-3 passes, default 1) and suction (quiet, standard, strong, turbo; default keeps the current setting). Only use when the user asks; this physically moves the vacuum or runs its base station.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.CommandInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.Command(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_details", Description: "Show robot vacuum upkeep and setup: remaining life of brushes, filter, mop pads and other parts (percent), base-station state (water tanks, dust bag, detergent, washing, drying), and current settings (cleaning mode, suction, mop wetness, volume, do-not-disturb, child lock, carpet handling). Fields the model does not have are omitted.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.Details, error) {
		out, err := client.Details(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_history", Description: "List the robot vacuum's most recent cleaning runs, newest first: start time (Unix seconds), minutes, area in square metres, whether it finished, and how it ended. limit 1-20, default 10.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.HistoryInput) (*mcp.CallToolResult, vacuum.HistoryResult, error) {
		out, err := client.History(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_map", Description: "Show a picture of the robot vacuum's saved map with rooms, the vacuum and its dock. Pair it with vacuum_rooms for coordinates when aiming zone or spot cleaning.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, any, error) {
		out, err := client.Map(ctx)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: out.Data, MIMEType: out.MIMEType}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_settings", Description: "Change one robot vacuum setting. setting and value: cleaning_mode (sweeping, mopping, sweeping_and_mopping, mopping_after_sweeping), suction (quiet, standard, strong, turbo), mop_wetness (dry, moist, wet), volume (0-100), dnd (on/off), dnd_start and dnd_end (HH:MM), child_lock (on/off), carpet_avoidance (on/off), carpet_recognition (on/off). Per-room settings need room (an ID from vacuum_rooms): room_mode, room_suction, room_wetness (same values as above) and room_passes (1-3). Only use when the user asks. Check vacuum_details first to see current values; a setting the model lacks is refused.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.SettingInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.Setting(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_drive", Description: "Move the robot vacuum one small remote-control step, like the joystick in the vendor app. turn: degrees, -360 to 360 (positive turns left); speed: -600 to 600 (negative reverses; about 200 is a gentle roll). Each call is one short nudge, so send several to travel further, and check vacuum_status between them. Its own bump and cliff sensors still apply. To reach a room or spot, prefer vacuum_control go_to. Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.MoveInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.Move(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_shortcuts", Description: "List the cleaning shortcuts saved in the vendor app (one-tap routines with their own rooms, order and settings): ID, name, whether one is running, and the rooms it covers.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.ShortcutsResult, error) {
		out, err := client.Shortcuts(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_shortcut", Description: "Run, rename or delete a cleaning shortcut from vacuum_shortcuts. action: start (runs it now with its saved rooms and settings), rename (name: 1-32 characters), or delete (removes it from the app; cannot be undone, so confirm with the user first). Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(true), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.ShortcutInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.Shortcut(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_options", Description: "List every setting this robot vacuum model has, as the vendor app and the original integration expose them (about 100 on/off switches, choices, numbers and times: carpet boost, AI detection, hot washing, mop pressure, water temperature, cleaning route, drying time, off-peak charging hours and more), with the current value and allowed choices or range. search filters by words in the setting name, for example mop or carpet. Use vacuum_set_option to change one.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.OptionsInput) (*mcp.CallToolResult, vacuum.OptionsResult, error) {
		out, err := client.Options(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_set_option", Description: "Change one setting listed by vacuum_options. value: on or off for switches, one of the listed labels for choices, a number on the listed step within the range for numbers, or HH:MM for times. Only settings the vacuum listed, with allowed values, are accepted. Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.SetOptionInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.SetOption(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_buttons", Description: "List the one-press actions this robot vacuum has, as the original integration exposes them: reset the wear counter of each part (kind reset, for example reset_filter after fitting a new filter), clear a warning, drain or empty the water tank, base station self-clean or self-repair, dust-bag drying, start mapping or fast mapping, re-clean missed areas, back up the saved map.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.ButtonsResult, error) {
		out, err := client.Buttons(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_press", Description: "Press one button from vacuum_buttons. Wear-counter resets should only be pressed after the user says they replaced or cleaned that part. Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.PressInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.Press(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_maps", Description: "List the robot vacuum's saved maps (for example one per floor): ID, name, which is selected, and its backups with dates. temporary_map is true when a freshly scanned map is waiting to be saved, discarded or used to replace a saved map.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.MapsResult, error) {
		out, err := client.Maps(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_manage_map", Description: "Manage the robot vacuum's saved maps. action: select (switch to map_id), rename (map_id, name), delete (map_id; permanent), backup (map_id), restore (map_id and backup index from vacuum_maps; replaces the current map), request (refresh the map), save_temporary, discard_temporary, or replace_temporary (map_id to overwrite with the new scan). Confirm delete, restore and replace with the user first. Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(true), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.MapActionInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.ManageMap(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_manage_rooms", Description: "Manage rooms on the selected map, using IDs from vacuum_rooms. action: rename (one room, name), set_type (one room, type label from vacuum_room_options), merge (two neighbouring rooms into one), split (one room along line [x0, y0, x1, y1] inside the map; use the room's box from vacuum_rooms), order (every room in the order to clean them; empty clears the custom order), or hide (rooms to hide; empty shows all). Merge and split change the map, so confirm with the user first. Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(true), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.RoomActionInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.ManageRooms(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_room_options", Description: "List one room's own cleaning settings (suction, water or mop wetness, cleaning mode, passes, mopping route, cleaning route, mop pressure and temperature, mop type, floor material and direction, visibility) with current values and allowed choices, plus the room types available for vacuum_manage_rooms set_type.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.RoomOptionsInput) (*mcp.CallToolResult, vacuum.RoomOptionsResult, error) {
		out, err := client.RoomOptions(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_set_room_option", Description: "Change one room setting listed by vacuum_room_options, for example mop_pressure strong for the kitchen. Only listed settings with allowed values are accepted. Only use when the user asks.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.SetRoomOptionInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.SetRoomOption(ctx, in)
		return nil, out, err
	})
	if !enabled("NAS_VACUUM_CAMERA", true) {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_obstacles", Description: "List things the robot vacuum recognised and avoided on its current map (for example socks, cables, shoes, pet mess), with map position, confidence (percent), and whether it took a photo. Use the index with vacuum_obstacle_photo. Photos are taken automatically while cleaning; the vacuum cannot take one on request.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.ObstaclesResult, error) {
		out, err := client.Obstacles(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_obstacle_photo", Description: "Show the photo the robot vacuum's camera took of one avoided obstacle, by index from vacuum_obstacles (only those with has_photo true). Describe what is visible; do not identify people. View only: nothing is saved.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.ObstaclePhotoInput) (*mcp.CallToolResult, any, error) {
		out, err := client.ObstaclePhoto(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: out.Data, MIMEType: out.MIMEType}}}, nil, nil
	})
}
