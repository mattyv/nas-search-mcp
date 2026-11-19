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
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_rooms", Description: "List the rooms on the robot vacuum's saved map with their IDs. Use these IDs with vacuum_control action clean_rooms. Empty when the vacuum has no saved map yet.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in VacuumEmptyInput) (*mcp.CallToolResult, vacuum.RoomsResult, error) {
		out, err := client.Rooms(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vacuum_control", Description: "Control the robot vacuum. action: start (clean everywhere), pause, stop, dock (return to the base), locate (play a sound), or clean_rooms. clean_rooms needs room IDs from vacuum_rooms; optional repeats (1-3 passes, default 1) and suction (quiet, standard, strong, turbo; default keeps the current setting). Only use when the user asks; this physically moves the vacuum.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: false, OpenWorldHint: boolPointer(true)}}, func(ctx context.Context, req *mcp.CallToolRequest, in vacuum.CommandInput) (*mcp.CallToolResult, vacuum.CommandResult, error) {
		out, err := client.Command(ctx, in)
		return nil, out, err
	})
}
