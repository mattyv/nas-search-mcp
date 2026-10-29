package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// fakeMemoryStore is a minimal in-memory memoryapi.Store for tool tests. It records
// the roots and queries it sees so tests can check what the tools passed down.
type fakeMemoryStore struct {
	mu          sync.Mutex
	events      map[string]memoryapi.Event
	memories    map[string]memoryapi.Memory
	checkpoints map[string]memoryapi.Checkpoint
	byKey       map[string]string
	seq         int
	lastRoots   []string
	lastCtx     memoryapi.ContextRequest
	lastEventQ  memoryapi.EventQuery
	lastMemQ    memoryapi.MemoryQuery
	lastCorrect memoryapi.Correction
	forgotten   []string // "kind:id:actor"
}

func newFakeMemoryStore() *fakeMemoryStore {
	return &fakeMemoryStore{events: map[string]memoryapi.Event{}, memories: map[string]memoryapi.Memory{}, checkpoints: map[string]memoryapi.Checkpoint{}, byKey: map[string]string{}}
}

func (f *fakeMemoryStore) next(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%d", prefix, f.seq)
}

func (f *fakeMemoryStore) AppendEvent(_ context.Context, roots []string, e memoryapi.Event) (memoryapi.Event, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRoots = roots
	if !policy.ScopeVisible(e.Scope, roots) {
		return memoryapi.Event{}, false, memoryapi.ErrScope
	}
	if e.IdempotencyKey != "" {
		if id, ok := f.byKey[e.IdempotencyKey]; ok {
			return f.events[id], false, nil
		}
	}
	e.ID = f.next("ev")
	if e.Status == "" {
		e.Status = "unverified"
	}
	e.RecordedAt = time.Now().UTC()
	f.events[e.ID] = e
	if e.IdempotencyKey != "" {
		f.byKey[e.IdempotencyKey] = e.ID
	}
	return e, true, nil
}

func (f *fakeMemoryStore) GetEvent(_ context.Context, roots []string, id string) (memoryapi.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.events[id]
	if !ok || !policy.ScopeVisible(e.Scope, roots) {
		return memoryapi.Event{}, memoryapi.ErrNotFound
	}
	return e, nil
}

func (f *fakeMemoryStore) SearchEvents(_ context.Context, roots []string, q memoryapi.EventQuery) ([]memoryapi.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRoots, f.lastEventQ = roots, q
	var out []memoryapi.Event
	for _, e := range f.events {
		if policy.ScopeVisible(e.Scope, roots) && (q.Text == "" || strings.Contains(e.Title, q.Text)) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeMemoryStore) SaveCheckpoint(_ context.Context, roots []string, c memoryapi.Checkpoint, expected int) (memoryapi.Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRoots = roots
	if !policy.ScopeVisible(c.Scope, roots) {
		return memoryapi.Checkpoint{}, memoryapi.ErrScope
	}
	cur := f.checkpoints[c.Project]
	if cur.Version != expected {
		return memoryapi.Checkpoint{}, memoryapi.ErrConflict
	}
	c.Version = expected + 1
	c.UpdatedAt = time.Now().UTC()
	f.checkpoints[c.Project] = c
	return c, nil
}

func (f *fakeMemoryStore) LatestCheckpoint(_ context.Context, _ []string, project string) (*memoryapi.Checkpoint, error) {
	c, ok := f.checkpoints[project]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (f *fakeMemoryStore) CheckpointHistory(context.Context, []string, string, int) ([]memoryapi.Checkpoint, error) {
	return nil, nil
}

func (f *fakeMemoryStore) OpenContext(_ context.Context, roots []string, r memoryapi.ContextRequest) (memoryapi.ContextPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRoots, f.lastCtx = roots, r
	return memoryapi.ContextPackage{Project: r.Project, Scope: roots[0], Budget: r.Budget, Decisions: []memoryapi.Memory{}, Memories: []memoryapi.Memory{}, Unconfirmed: []memoryapi.Memory{}, Recent: []memoryapi.Event{}, NextActions: []string{"ship it"}}, nil
}

func (f *fakeMemoryStore) ProposeMemory(_ context.Context, _ []string, m memoryapi.Memory, _ bool) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m.ID = f.next("mem")
	m.Status = "candidate"
	f.memories[m.ID] = m
	return m, nil
}

func (f *fakeMemoryStore) GetMemory(_ context.Context, roots []string, id string) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.memories[id]
	if !ok || !policy.ScopeVisible(m.Scope, roots) {
		return memoryapi.Memory{}, memoryapi.ErrNotFound
	}
	return m, nil
}

func (f *fakeMemoryStore) SearchMemories(_ context.Context, roots []string, q memoryapi.MemoryQuery) ([]memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRoots, f.lastMemQ = roots, q
	var out []memoryapi.Memory
	for _, m := range f.memories {
		if policy.ScopeVisible(m.Scope, roots) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeMemoryStore) CorrectMemory(_ context.Context, _ []string, c memoryapi.Correction) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCorrect = c
	m, ok := f.memories[c.MemoryID]
	if !ok {
		return memoryapi.Memory{}, memoryapi.ErrNotFound
	}
	if c.Actor == memoryapi.ActorAssistant && !policy.AssistantMayTransition(m.Status, c.Status) {
		return memoryapi.Memory{}, fmt.Errorf("%w: assistants may only dispute", memoryapi.ErrInvalid)
	}
	m.Status, m.Reason = c.Status, c.Reason
	f.memories[m.ID] = m
	return m, nil
}

func (f *fakeMemoryStore) ForgetEvent(_ context.Context, roots []string, id, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.events[id]
	if !ok || !policy.ScopeVisible(e.Scope, roots) {
		return memoryapi.ErrNotFound
	}
	delete(f.events, id)
	f.forgotten = append(f.forgotten, "event:"+id+":"+actor)
	return nil
}

func (f *fakeMemoryStore) ForgetMemory(_ context.Context, roots []string, id, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.memories[id]
	if !ok || !policy.ScopeVisible(m.Scope, roots) {
		return memoryapi.ErrNotFound
	}
	delete(f.memories, id)
	f.forgotten = append(f.forgotten, "memory:"+id+":"+actor)
	return nil
}

func (f *fakeMemoryStore) Projects(context.Context, []string) ([]memoryapi.ProjectSummary, error) {
	return nil, nil
}
func (f *fakeMemoryStore) UnclassifiedEvents(context.Context, []string, int) ([]memoryapi.Event, error) {
	return nil, nil
}
func (f *fakeMemoryStore) MarkClassified(context.Context, []string, string) error { return nil }

func (f *fakeMemoryStore) forgottenList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.forgotten...)
}
