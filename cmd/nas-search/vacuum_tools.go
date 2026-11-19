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
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_control", Description: "Control the robot vacuum. action: start (clean everywhere), pause, stop, dock (return to the base), locate (play a sound), clean_rooms (rooms: IDs from vacuum_rooms), clean_zones (zones: up to 5 rectangles [x0, y0, x1, y1] inside the map bounds, at least 300 units per side), clean_spot (point: [x, y] inside the map bounds), empty_dustbin, wash_mops, dry_mops, or stop_drying. Cleaning actions take optional repeats (1-3 passes, default 1) and suction (quiet, standard, strong, turbo; default keeps the current setting). Only use when the user asks; this physically moves the vacuum or runs its base station.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.CommandInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
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
}
