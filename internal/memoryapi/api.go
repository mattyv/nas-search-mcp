// Package memoryapi is the contract between the shared-memory service, the MCP tools in
// the search process, and the editor. The memory service owns the database; other
// processes reach it through Client over a private Unix socket. Decision rules (scopes,
// status lifecycle, checkpoint versions, context packing, routing) live in the verified
// core and are reached through internal/policy; this package only carries data.
package memoryapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Sentinel errors survive the socket: the server encodes them as codes and the client
// decodes them back, so callers can use errors.Is on either side.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("checkpoint version conflict")
	ErrInvalid  = errors.New("invalid request")
	ErrScope    = errors.New("scope not permitted")
)

const (
	ActorUser      = "user"      // written or confirmed by the owner (editor, approvals)
	ActorAssistant = "assistant" // written by an AI client; never treated as the owner's word
)

// Source points at evidence. Available is false when the locator can no longer be fetched.
type Source struct {
	Kind      string `json:"kind"`              // conversation, file, commit, pr, url, document
	Locator   string `json:"locator"`           // URI, path, or commit SHA
	Excerpt   string `json:"excerpt,omitempty"` // bounded supporting text (at most 2000 characters)
	Available bool   `json:"available"`
}

// Event is one append-only journal entry: what happened, when, and who said so.
type Event struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	Type           string    `json:"type"`   // policy.ValidEventType
	Status         string    `json:"status"` // policy.ValidEventStatus
	Title          string    `json:"title"`  // 1..200 characters
	Body           string    `json:"body,omitempty"`
	Project        string    `json:"project,omitempty"`
	Scope          string    `json:"scope"` // policy.ValidScope
	Client         string    `json:"client"`
	Actor          string    `json:"actor"` // ActorUser or ActorAssistant
	SessionID      string    `json:"session_id,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
	RecordedAt     time.Time `json:"recorded_at"`
	Sources        []Source  `json:"sources,omitempty"`
	Related        []string  `json:"related,omitempty"`
	NextActions    []string  `json:"next_actions,omitempty"`
	Supersedes     string    `json:"supersedes,omitempty"` // event ID this entry corrects
	Classified     bool      `json:"classified"`           // consolidation has processed it
}

type EventQuery struct {
	Text    string    `json:"text,omitempty"` // full-text match; empty lists by time
	Project string    `json:"project,omitempty"`
	Types   []string  `json:"types,omitempty"`
	From    time.Time `json:"from,omitempty"` // inclusive, on OccurredAt
	To      time.Time `json:"to,omitempty"`   // exclusive, on OccurredAt
	Limit   int       `json:"limit,omitempty"`
}

// Checkpoint is a versioned, resumable snapshot of where a project stands.
type Checkpoint struct {
	Project   string    `json:"project"`
	Scope     string    `json:"scope"`
	Version   int       `json:"version"`
	Goal      string    `json:"goal,omitempty"`
	Summary   string    `json:"summary"` // plain prose; the main payload
	Branch    string    `json:"branch,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	Done      []string  `json:"done,omitempty"`
	Decisions []string  `json:"decisions,omitempty"`
	Blockers  []string  `json:"blockers,omitempty"`
	Next      []string  `json:"next,omitempty"`
	Client    string    `json:"client"`
	Actor     string    `json:"actor"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Memory is a curated, mutable statement derived from journal evidence.
type Memory struct {
	ID          string    `json:"id"`
	Category    string    `json:"category"` // policy.ValidMemoryCategory
	Statement   string    `json:"statement"`
	Scope       string    `json:"scope"`
	Project     string    `json:"project,omitempty"`
	Status      string    `json:"status"`             // candidate, accepted, disputed, rejected, superseded, retracted
	Evidence    []string  `json:"evidence,omitempty"` // event IDs
	Probability int       `json:"probability"`        // classifier per-mille score, -1 when unscored
	Model       string    `json:"model,omitempty"`    // classifier that scored it
	Reason      string    `json:"reason,omitempty"`   // last correction reason
	ReplacedBy  string    `json:"replaced_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type MemoryQuery struct {
	Text       string   `json:"text,omitempty"`
	Project    string   `json:"project,omitempty"`
	Statuses   []string `json:"statuses,omitempty"` // empty means accepted only
	Categories []string `json:"categories,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// Correction changes a memory's status. Statement, when set with Status "superseded",
// creates the replacement memory. Assistants may only request "disputed".
type Correction struct {
	MemoryID  string `json:"memory_id"`
	Status    string `json:"status"`
	Statement string `json:"statement,omitempty"`
	Reason    string `json:"reason"`
	Client    string `json:"client"`
	Actor     string `json:"actor"`
}

type ContextRequest struct {
	Project string `json:"project,omitempty"` // empty means the most recently active project
	Intent  string `json:"intent,omitempty"`  // resume, review, explore
	Budget  int    `json:"budget,omitempty"`  // approximate tokens; default 1500, at most 8000
}

// ContextPackage is the bounded handoff returned at the start of a conversation.
// Unconfirmed holds candidate and disputed memories, clearly separated from accepted ones.
type ContextPackage struct {
	Project     string      `json:"project"`
	Scope       string      `json:"scope"`
	Checkpoint  *Checkpoint `json:"checkpoint,omitempty"`
	Decisions   []Memory    `json:"decisions"`
	Memories    []Memory    `json:"memories"`
	Unconfirmed []Memory    `json:"unconfirmed"`
	Recent      []Event     `json:"recent"`
	NextActions []string    `json:"next_actions"`
	Omitted     int         `json:"omitted"` // items dropped to fit the budget
	Budget      int         `json:"budget"`
	Used        int         `json:"used"`
	Notes       []string    `json:"notes,omitempty"` // e.g. "no checkpoint saved yet"
}

type ProjectSummary struct {
	Project           string    `json:"project"`
	Scope             string    `json:"scope"`
	LastActivity      time.Time `json:"last_activity"`
	Events            int       `json:"events"`
	CheckpointVersion int       `json:"checkpoint_version"`
	OpenCandidates    int       `json:"open_candidates"`
}

// Store is implemented by the memory service (internal/memory) and by Client.
// Every method takes the caller's permitted scope roots; the store enforces
// policy.ScopeVisible on reads and requires written scopes to lie within roots.
type Store interface {
	// AppendEvent is idempotent on IdempotencyKey: a repeat returns the stored event and false.
	AppendEvent(ctx context.Context, roots []string, e Event) (Event, bool, error)
	GetEvent(ctx context.Context, roots []string, id string) (Event, error)
	SearchEvents(ctx context.Context, roots []string, q EventQuery) ([]Event, error)

	// SaveCheckpoint fails with ErrConflict unless expectedVersion is the stored version
	// (0 for a project's first checkpoint). It also appends a "checkpoint" event.
	SaveCheckpoint(ctx context.Context, roots []string, c Checkpoint, expectedVersion int) (Checkpoint, error)
	LatestCheckpoint(ctx context.Context, roots []string, project string) (*Checkpoint, error)
	CheckpointHistory(ctx context.Context, roots []string, project string, limit int) ([]Checkpoint, error)

	OpenContext(ctx context.Context, roots []string, r ContextRequest) (ContextPackage, error)

	// ProposeMemory sets Status from policy.InitialMemoryStatus.
	ProposeMemory(ctx context.Context, roots []string, m Memory, userAttested bool) (Memory, error)
	GetMemory(ctx context.Context, roots []string, id string) (Memory, error)
	SearchMemories(ctx context.Context, roots []string, q MemoryQuery) ([]Memory, error)
	CorrectMemory(ctx context.Context, roots []string, c Correction) (Memory, error)

	// Forget erases content and derived rows, keeping only a content-free audit record.
	ForgetEvent(ctx context.Context, roots []string, id string, actor string) error
	ForgetMemory(ctx context.Context, roots []string, id string, actor string) error

	Projects(ctx context.Context, roots []string) ([]ProjectSummary, error)

	// Consolidation support: events not yet classified, oldest first.
	UnclassifiedEvents(ctx context.Context, roots []string, limit int) ([]Event, error)
	MarkClassified(ctx context.Context, roots []string, eventID string) error
}

// Wire format for the private socket: POST CallPath with a CallRequest; the reply is a
// CallResponse. Params and Result hold the JSON of the method's arguments and results.
const CallPath = "/v1/call"

type CallRequest struct {
	Method string          `json:"method"` // Store method name, e.g. "AppendEvent"
	Roots  []string        `json:"roots"`
	Params json.RawMessage `json:"params"`
}

type CallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *WireError      `json:"error,omitempty"`
}

// WireError Code is one of: not_found, conflict, invalid, scope, internal.
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
