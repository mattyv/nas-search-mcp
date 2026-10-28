package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

var memoryStatuses = []string{"candidate", "accepted", "disputed", "rejected", "superseded", "retracted"}

func statusesWhere(pred func(string) bool) []string {
	var out []string
	for _, st := range memoryStatuses {
		if pred(st) {
			out = append(out, st)
		}
	}
	return out
}

const (
	recentWindow   = 14 * 24 * time.Hour
	maxRecent      = 20
	maxContextMems = 50
	maxNextActions = 15
)

type ctxItem struct {
	kind int // 0 checkpoint, 1 decision, 2 memory, 3 unconfirmed, 4 event
	idx  int
	cost int
}

func (s *Store) memoryList(ctx context.Context, scopes []string, clause string, args []any) ([]memoryapi.Memory, error) {
	if len(scopes) == 0 {
		return []memoryapi.Memory{}, nil
	}
	list, a := inList(scopes)
	all := append(append([]any{}, a...), args...)
	all = append(all, maxContextMems)
	rows, err := s.db.QueryContext(ctx, `SELECT `+memCols+` FROM memories m WHERE m.scope IN `+list+` AND `+clause+` ORDER BY m.updated_at DESC, m.rowid DESC LIMIT ?`, all...)
	if err != nil {
		return nil, internal(err)
	}
	return scanMemories(rows)
}

func (s *Store) OpenContext(ctx context.Context, roots []string, r memoryapi.ContextRequest) (memoryapi.ContextPackage, error) {
	if r.Project != "" && !validProject(r.Project, false) {
		return memoryapi.ContextPackage{}, invalid("project must be 1 to 100 characters of a-z 0-9 . _ -")
	}
	pkg := memoryapi.ContextPackage{
		Decisions: []memoryapi.Memory{}, Memories: []memoryapi.Memory{}, Unconfirmed: []memoryapi.Memory{},
		Recent: []memoryapi.Event{}, NextActions: []string{}, Budget: contextBudget(r.Budget),
	}
	evScopes, err := visibleScopes(ctx, s.db, "events", roots)
	if err != nil {
		return pkg, err
	}
	memScopes, err := visibleScopes(ctx, s.db, "memories", roots)
	if err != nil {
		return pkg, err
	}

	project := r.Project
	if len(evScopes) > 0 {
		list, args := inList(evScopes)
		query := `SELECT project, scope FROM events WHERE scope IN ` + list
		if project != "" {
			query += ` AND project = ?`
			args = append(args, project)
		} else {
			query += ` AND project <> ''`
		}
		var p, sc string
		switch err := s.db.QueryRowContext(ctx, query+` ORDER BY recorded_at DESC, rowid DESC LIMIT 1`, args...).Scan(&p, &sc); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return pkg, internal(err)
		default:
			project, pkg.Scope = p, sc
		}
	}
	pkg.Project = project

	var cp *memoryapi.Checkpoint
	var decisions, others, unconfirmed []memoryapi.Memory
	var recent []memoryapi.Event
	if project != "" {
		if cp, err = s.LatestCheckpoint(ctx, roots, project); err != nil {
			return pkg, err
		}
		if cp != nil && pkg.Scope == "" {
			pkg.Scope = cp.Scope
		}
		cur, un := statusesWhere(policy.IsCurrentMemory), statusesWhere(isUnconfirmedMemory)
		curList, curArgs := inList(cur)
		unList, unArgs := inList(un)
		if decisions, err = s.memoryList(ctx, memScopes, `m.category = 'decision' AND m.project = ? AND m.status IN `+curList, append([]any{project}, curArgs...)); err != nil {
			return pkg, err
		}
		if unconfirmed, err = s.memoryList(ctx, memScopes, `m.project = ? AND m.status IN `+unList, append([]any{project}, unArgs...)); err != nil {
			return pkg, err
		}
		if others, err = s.memoryList(ctx, memScopes,
			`m.status IN `+curList+` AND ((m.project = ? AND m.category <> 'decision') OR (m.project = '' AND m.category IN ('preference','constraint')))`,
			append(append([]any{}, curArgs...), project)); err != nil {
			return pkg, err
		}
		if recent, err = s.SearchEvents(ctx, roots, memoryapi.EventQuery{Project: project, From: s.now().Add(-recentWindow), Limit: maxRecent + 5}); err != nil {
			return pkg, err
		}
		kept := recent[:0]
		for _, e := range recent {
			if e.Type != "checkpoint" && len(kept) < maxRecent {
				kept = append(kept, e)
			}
		}
		recent = kept
	} else if others, err = s.memoryList(ctx, memScopes,
		`m.status IN `+mustList(statusesWhere(policy.IsCurrentMemory))+` AND m.project = '' AND m.category IN ('preference','constraint')`,
		argsOf(statusesWhere(policy.IsCurrentMemory))); err != nil {
		return pkg, err
	}

	var items []ctxItem
	add := func(kind, idx int, v any) {
		items = append(items, ctxItem{kind: kind, idx: idx, cost: itemCost(len(toJSON(v)))})
	}
	if cp != nil {
		add(0, 0, cp)
	}
	for i := range decisions {
		add(1, i, decisions[i])
	}
	for i := range others {
		add(2, i, others[i])
	}
	for i := range unconfirmed {
		add(3, i, unconfirmed[i])
	}
	for i := range recent {
		add(4, i, recent[i])
	}
	costs := make([]int, len(items))
	for i, it := range items {
		costs[i] = it.cost
	}
	kept := policy.PackBudget(costs, pkg.Budget)
	var cpNext []string
	var eventActions [][]string
	for _, k := range kept {
		it := items[k]
		pkg.Used += it.cost
		switch it.kind {
		case 0:
			pkg.Checkpoint = cp
			cpNext = cp.Next
		case 1:
			pkg.Decisions = append(pkg.Decisions, decisions[it.idx])
		case 2:
			pkg.Memories = append(pkg.Memories, others[it.idx])
		case 3:
			pkg.Unconfirmed = append(pkg.Unconfirmed, unconfirmed[it.idx])
		case 4:
			pkg.Recent = append(pkg.Recent, recent[it.idx])
			eventActions = append(eventActions, recent[it.idx].NextActions)
		}
	}
	pkg.Omitted = len(items) - len(kept)
	pkg.NextActions = mergeNextActions(maxNextActions, append([][]string{cpNext}, eventActions...)...)
	if cp == nil {
		pkg.Notes = append(pkg.Notes, "no checkpoint saved yet")
	}
	if n := len(unconfirmed); n == 1 {
		pkg.Notes = append(pkg.Notes, "1 memory awaits review in the editor")
	} else if n > 1 {
		pkg.Notes = append(pkg.Notes, fmt.Sprintf("%d memories await review in the editor", n))
	}
	return pkg, nil
}

func mustList(vals []string) string { l, _ := inList(vals); return l }
func argsOf(vals []string) []any    { _, a := inList(vals); return a }

func (s *Store) Projects(ctx context.Context, roots []string) ([]memoryapi.ProjectSummary, error) {
	scopes, err := visibleScopes(ctx, s.db, "events", roots)
	if err != nil || len(scopes) == 0 {
		return []memoryapi.ProjectSummary{}, err
	}
	scopeList, args := inList(scopes)
	cand := statusesWhere(isOpenCandidate)
	candList, candArgs := inList(cand)
	args = append(append([]any{}, candArgs...), args...)
	rows, err := s.db.QueryContext(ctx, `SELECT e.project, e.scope, MAX(e.recorded_at), COUNT(*),
 COALESCE((SELECT MAX(c.version) FROM checkpoints c WHERE c.project = e.project AND c.scope = e.scope), 0),
 (SELECT COUNT(*) FROM memories m WHERE m.project = e.project AND m.scope = e.scope AND m.status IN `+candList+`)
 FROM events e WHERE e.project <> '' AND e.scope IN `+scopeList+`
 GROUP BY e.project, e.scope ORDER BY 3 DESC, e.project`, args...)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	out := []memoryapi.ProjectSummary{}
	for rows.Next() {
		var p memoryapi.ProjectSummary
		var last int64
		if err := rows.Scan(&p.Project, &p.Scope, &last, &p.Events, &p.CheckpointVersion, &p.OpenCandidates); err != nil {
			return nil, internal(err)
		}
		p.LastActivity = fromMS(last)
		out = append(out, p)
	}
	return out, rows.Err()
}
