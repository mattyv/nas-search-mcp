package editor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// fakeStore is an in-memory memoryapi.Store that records every call.
type fakeStore struct {
	mu          sync.Mutex
	calls       []string
	events      []memoryapi.Event
	byKey       map[string]memoryapi.Event
	checkpoints map[string][]memoryapi.Checkpoint
	memories    map[string]memoryapi.Memory
	projects    []memoryapi.ProjectSummary
	nextID      int
	lastCtx     memoryapi.ContextRequest
	lastQuery   memoryapi.EventQuery
	lastMemQ    memoryapi.MemoryQuery
	lastRoots   []string
	proposed    []memoryapi.Memory
	proposedAtt []bool
	corrections []memoryapi.Correction
	forgotten   []string
	forgetActor []string
}

func newFake() *fakeStore {
	return &fakeStore{
		byKey:       map[string]memoryapi.Event{},
		checkpoints: map[string][]memoryapi.Checkpoint{},
		memories:    map[string]memoryapi.Memory{},
	}
}

func (f *fakeStore) rec(roots []string, format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	f.lastRoots = roots
}

func (f *fakeStore) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeStore) AppendEvent(_ context.Context, roots []string, e memoryapi.Event) (memoryapi.Event, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "AppendEvent")
	if e.IdempotencyKey != "" {
		if old, ok := f.byKey[e.IdempotencyKey]; ok {
			return old, false, nil
		}
	}
	f.nextID++
	e.ID = fmt.Sprintf("new%d", f.nextID)
	e.RecordedAt = time.Now().UTC()
	f.events = append(f.events, e)
	if e.IdempotencyKey != "" {
		f.byKey[e.IdempotencyKey] = e
	}
	return e, true, nil
}

func (f *fakeStore) GetEvent(_ context.Context, roots []string, id string) (memoryapi.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "GetEvent %s", id)
	for _, e := range f.events {
		if e.ID == id {
			return e, nil
		}
	}
	return memoryapi.Event{}, memoryapi.ErrNotFound
}

func (f *fakeStore) SearchEvents(_ context.Context, roots []string, q memoryapi.EventQuery) ([]memoryapi.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "SearchEvents")
	f.lastQuery = q
	var out []memoryapi.Event
	for _, e := range f.events {
		if q.Project != "" && e.Project != q.Project {
			continue
		}
		if !q.From.IsZero() && e.OccurredAt.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && !e.OccurredAt.Before(q.To) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OccurredAt.After(out[j].OccurredAt) })
	return out, nil
}

func (f *fakeStore) latest(project string) *memoryapi.Checkpoint {
	h := f.checkpoints[project]
	if len(h) == 0 {
		return nil
	}
	c := h[len(h)-1]
	return &c
}

func (f *fakeStore) SaveCheckpoint(_ context.Context, roots []string, c memoryapi.Checkpoint, expected int) (memoryapi.Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "SaveCheckpoint expected=%d actor=%s client=%s", expected, c.Actor, c.Client)
	cur := 0
	if l := f.latest(c.Project); l != nil {
		cur = l.Version
	}
	ok, next := policy.CheckpointWrite(cur, expected)
	if !ok {
		return memoryapi.Checkpoint{}, memoryapi.ErrConflict
	}
	c.Version = next
	c.UpdatedAt = time.Now().UTC()
	f.checkpoints[c.Project] = append(f.checkpoints[c.Project], c)
	return c, nil
}

func (f *fakeStore) LatestCheckpoint(_ context.Context, roots []string, project string) (*memoryapi.Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "LatestCheckpoint")
	return f.latest(project), nil
}

func (f *fakeStore) CheckpointHistory(_ context.Context, roots []string, project string, limit int) ([]memoryapi.Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "CheckpointHistory")
	return f.checkpoints[project], nil
}

func (f *fakeStore) OpenContext(_ context.Context, roots []string, r memoryapi.ContextRequest) (memoryapi.ContextPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "OpenContext")
	f.lastCtx = r
	known := f.checkpoints[r.Project] != nil
	for _, p := range f.projects {
		if p.Project == r.Project {
			known = true
		}
	}
	if !known {
		return memoryapi.ContextPackage{}, memoryapi.ErrNotFound
	}
	pkg := memoryapi.ContextPackage{Project: r.Project, Scope: "personal", Budget: r.Budget, Checkpoint: f.latest(r.Project)}
	ids := make([]string, 0, len(f.memories))
	for id := range f.memories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := f.memories[id]
		if m.Project != r.Project {
			continue
		}
		switch m.Status {
		case "accepted":
			if m.Category == "decision" {
				pkg.Decisions = append(pkg.Decisions, m)
			} else {
				pkg.Memories = append(pkg.Memories, m)
			}
		case "candidate", "disputed":
			pkg.Unconfirmed = append(pkg.Unconfirmed, m)
		}
	}
	for _, e := range f.events {
		if e.Project == r.Project {
			pkg.Recent = append(pkg.Recent, e)
		}
	}
	return pkg, nil
}

func (f *fakeStore) ProposeMemory(_ context.Context, roots []string, m memoryapi.Memory, att bool) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "ProposeMemory")
	f.nextID++
	m.ID = fmt.Sprintf("mem%d", f.nextID)
	m.Status = "candidate"
	if att {
		m.Status = "accepted"
	}
	f.memories[m.ID] = m
	f.proposed = append(f.proposed, m)
	f.proposedAtt = append(f.proposedAtt, att)
	return m, nil
}

func (f *fakeStore) GetMemory(_ context.Context, roots []string, id string) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "GetMemory %s", id)
	m, ok := f.memories[id]
	if !ok {
		return memoryapi.Memory{}, memoryapi.ErrNotFound
	}
	return m, nil
}

func (f *fakeStore) SearchMemories(_ context.Context, roots []string, q memoryapi.MemoryQuery) ([]memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "SearchMemories")
	f.lastMemQ = q
	statuses := q.Statuses
	if len(statuses) == 0 {
		statuses = []string{"accepted"}
	}
	var out []memoryapi.Memory
	for _, m := range f.memories {
		if q.Project != "" && m.Project != q.Project {
			continue
		}
		for _, s := range statuses {
			if m.Status == s {
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) CorrectMemory(_ context.Context, roots []string, c memoryapi.Correction) (memoryapi.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "CorrectMemory %s %s", c.MemoryID, c.Status)
	m, ok := f.memories[c.MemoryID]
	if !ok {
		return memoryapi.Memory{}, memoryapi.ErrNotFound
	}
	if !policy.ValidMemoryTransition(m.Status, c.Status) {
		return memoryapi.Memory{}, memoryapi.ErrInvalid
	}
	m.Status, m.Reason = c.Status, c.Reason
	f.memories[m.ID] = m
	f.corrections = append(f.corrections, c)
	return m, nil
}

func (f *fakeStore) ForgetEvent(_ context.Context, roots []string, id, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "ForgetEvent %s", id)
	f.forgotten = append(f.forgotten, "event:"+id)
	f.forgetActor = append(f.forgetActor, actor)
	return nil
}

func (f *fakeStore) ForgetMemory(_ context.Context, roots []string, id, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "ForgetMemory %s", id)
	f.forgotten = append(f.forgotten, "memory:"+id)
	f.forgetActor = append(f.forgetActor, actor)
	return nil
}

func (f *fakeStore) Projects(_ context.Context, roots []string) ([]memoryapi.ProjectSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec(roots, "Projects")
	return f.projects, nil
}

func (f *fakeStore) UnclassifiedEvents(context.Context, []string, int) ([]memoryapi.Event, error) {
	return nil, nil
}
func (f *fakeStore) MarkClassified(context.Context, []string, string) error { return nil }

var _ memoryapi.Store = (*fakeStore)(nil)
