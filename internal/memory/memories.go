package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

const memCols = `m.id, m.category, m.statement, m.scope, m.project, m.status, m.evidence, m.probability,
 m.model, m.reason, m.replaced_by, m.created_at, m.updated_at`

func scanMemories(rows *sql.Rows) ([]memoryapi.Memory, error) {
	defer rows.Close()
	out := []memoryapi.Memory{}
	for rows.Next() {
		var m memoryapi.Memory
		var evidence string
		var created, updated int64
		if err := rows.Scan(&m.ID, &m.Category, &m.Statement, &m.Scope, &m.Project, &m.Status, &evidence, &m.Probability,
			&m.Model, &m.Reason, &m.ReplacedBy, &created, &updated); err != nil {
			return nil, internal(err)
		}
		m.CreatedAt, m.UpdatedAt = fromMS(created), fromMS(updated)
		fromJSON(evidence, &m.Evidence)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	return out, nil
}

func getMemory(ctx context.Context, q queryer, roots []string, id string) (memoryapi.Memory, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+memCols+` FROM memories m WHERE m.id = ?`, id)
	if err != nil {
		return memoryapi.Memory{}, internal(err)
	}
	ms, err := scanMemories(rows)
	if err != nil {
		return memoryapi.Memory{}, err
	}
	if len(ms) == 0 || !policy.ScopeVisible(ms[0].Scope, roots) {
		return memoryapi.Memory{}, fmt.Errorf("%w: memory", memoryapi.ErrNotFound)
	}
	return ms[0], nil
}

func insertMemory(ctx context.Context, q queryer, m memoryapi.Memory) error {
	res, err := q.ExecContext(ctx, `INSERT INTO memories (id, category, statement, scope, project, status, evidence, probability,
 model, reason, replaced_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Category, m.Statement, m.Scope, m.Project, m.Status, toJSON(nonNilStr(m.Evidence)), m.Probability,
		m.Model, m.Reason, m.ReplacedBy, ms(m.CreatedAt), ms(m.UpdatedAt))
	if err != nil {
		return internal(err)
	}
	rowid, err := res.LastInsertId()
	if err != nil {
		return internal(err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO memories_fts(rowid, statement) VALUES (?,?)`, rowid, m.Statement); err != nil {
		return internal(err)
	}
	return nil
}

func (s *Store) validateMemory(roots []string, m memoryapi.Memory) (memoryapi.Memory, error) {
	m.Statement = strings.TrimSpace(redact(m.Statement))
	switch {
	case !policy.ValidMemoryCategory(m.Category):
		return m, invalid("unknown memory category")
	case !validLength(m.Statement, 1, 1000):
		return m, invalid("statement must be 1 to 1000 characters")
	case !validProject(m.Project, true):
		return m, invalid("project must be 1 to 100 characters of a-z 0-9 . _ -")
	case m.Probability < -1 || m.Probability > 1000:
		return m, invalid("probability must be -1 (unscored) or 0 to 1000")
	case len(m.Evidence) > 20:
		return m, invalid("at most 20 evidence ids")
	case !validLength(m.Model, 0, 100):
		return m, invalid("model name is too long")
	}
	for _, id := range m.Evidence {
		if !validLength(id, 1, 64) {
			return m, invalid("evidence id is not valid")
		}
	}
	if err := writeScope(m.Scope, roots); err != nil {
		return m, err
	}
	return m, nil
}

func (s *Store) ProposeMemory(ctx context.Context, roots []string, m memoryapi.Memory, userAttested bool) (memoryapi.Memory, error) {
	m, err := s.validateMemory(roots, m)
	if err != nil {
		return memoryapi.Memory{}, err
	}
	m.ID = newID("mem_")
	m.Status = policy.InitialMemoryStatus(userAttested, m.Probability, s.acceptAt, s.rejectAt)
	m.Reason, m.ReplacedBy = "", ""
	m.CreatedAt = s.now()
	m.UpdatedAt = m.CreatedAt
	if err := s.tx(ctx, func(q queryer) error { return insertMemory(ctx, q, m) }); err != nil {
		return memoryapi.Memory{}, err
	}
	return m, nil
}

func (s *Store) GetMemory(ctx context.Context, roots []string, id string) (memoryapi.Memory, error) {
	return getMemory(ctx, s.db, roots, id)
}

func (s *Store) SearchMemories(ctx context.Context, roots []string, query memoryapi.MemoryQuery) ([]memoryapi.Memory, error) {
	scopes, err := visibleScopes(ctx, s.db, "memories", roots)
	if err != nil || len(scopes) == 0 {
		return []memoryapi.Memory{}, err
	}
	var where []string
	var args []any
	from := "memories m"
	order := "m.updated_at DESC, m.rowid DESC"
	if query.Text != "" {
		match := ftsQuery(query.Text)
		if match == "" {
			return []memoryapi.Memory{}, nil
		}
		from = "memories_fts JOIN memories m ON m.rowid = memories_fts.rowid"
		where = append(where, "memories_fts MATCH ?")
		args = append(args, match)
		order = "memories_fts.rank, m.rowid DESC"
	}
	list, a := inList(scopes)
	where = append(where, "m.scope IN "+list)
	args = append(args, a...)
	statuses := query.Statuses
	if len(statuses) == 0 {
		statuses = []string{"accepted"}
	}
	list, a = inList(statuses)
	where = append(where, "m.status IN "+list)
	args = append(args, a...)
	if query.Project != "" {
		where = append(where, "m.project = ?")
		args = append(args, query.Project)
	}
	if len(query.Categories) > 0 {
		list, a = inList(query.Categories)
		where = append(where, "m.category IN "+list)
		args = append(args, a...)
	}
	args = append(args, clampLimit(query.Limit, 20, 100))
	rows, err := s.db.QueryContext(ctx, `SELECT `+memCols+` FROM `+from+` WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, internal(err)
	}
	return scanMemories(rows)
}

func (s *Store) CorrectMemory(ctx context.Context, roots []string, c memoryapi.Correction) (memoryapi.Memory, error) {
	if !validActor(c.Actor) {
		return memoryapi.Memory{}, invalid("actor must be user or assistant")
	}
	reason := strings.TrimSpace(redact(c.Reason))
	if !validLength(reason, 1, 500) {
		return memoryapi.Memory{}, invalid("a reason of 1 to 500 characters is required")
	}
	if c.Statement != "" && c.Status != "superseded" {
		return memoryapi.Memory{}, invalid("a replacement statement is only allowed when superseding")
	}
	var out memoryapi.Memory
	err := s.tx(ctx, func(q queryer) error {
		m, err := getMemory(ctx, q, roots, c.MemoryID)
		if err != nil {
			return err
		}
		allowed := policy.ValidMemoryTransition(m.Status, c.Status)
		if c.Actor == memoryapi.ActorAssistant {
			allowed = policy.AssistantMayTransition(m.Status, c.Status)
		}
		if !allowed {
			return invalid("cannot change a %s memory to %s", m.Status, c.Status)
		}
		now := s.now()
		if c.Statement != "" {
			rep, err := s.validateMemory(roots, memoryapi.Memory{
				Category: m.Category, Statement: c.Statement, Scope: m.Scope, Project: m.Project,
				Evidence: m.Evidence, Probability: -1,
			})
			if err != nil {
				return err
			}
			rep.ID = newID("mem_")
			rep.Status = policy.InitialMemoryStatus(c.Actor == memoryapi.ActorUser, -1, s.acceptAt, s.rejectAt)
			rep.CreatedAt, rep.UpdatedAt = now, now
			if err := insertMemory(ctx, q, rep); err != nil {
				return err
			}
			m.ReplacedBy = rep.ID
		}
		m.Status, m.Reason, m.UpdatedAt = c.Status, reason, now
		if _, err := q.ExecContext(ctx, `UPDATE memories SET status = ?, reason = ?, replaced_by = ?, updated_at = ? WHERE id = ?`,
			m.Status, m.Reason, m.ReplacedBy, ms(now), m.ID); err != nil {
			return internal(err)
		}
		out = m
		return nil
	})
	if err != nil {
		return memoryapi.Memory{}, err
	}
	return out, nil
}

func audit(ctx context.Context, q queryer, kind, id, actor string, at int64) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO forget_audit (kind, target_id, actor, at) VALUES (?,?,?,?)`, kind, id, actor, at); err != nil {
		return internal(err)
	}
	return nil
}

func deleteMemoryRow(ctx context.Context, q queryer, id string) error {
	var rowid int64
	var statement string
	if err := q.QueryRowContext(ctx, `SELECT rowid, statement FROM memories WHERE id = ?`, id).Scan(&rowid, &statement); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return internal(err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO memories_fts(memories_fts, rowid, statement) VALUES ('delete', ?, ?)`, rowid, statement); err != nil {
		return internal(err)
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id); err != nil {
		return internal(err)
	}
	return nil
}

func (s *Store) ForgetMemory(ctx context.Context, roots []string, id string, actor string) error {
	if !validActor(actor) {
		return invalid("actor must be user or assistant")
	}
	return s.tx(ctx, func(q queryer) error {
		m, err := getMemory(ctx, q, roots, id)
		if err != nil {
			return err
		}
		if err := deleteMemoryRow(ctx, q, m.ID); err != nil {
			return err
		}
		return audit(ctx, q, "memory", m.ID, actor, ms(s.now()))
	})
}

func (s *Store) ForgetEvent(ctx context.Context, roots []string, id string, actor string) error {
	if !validActor(actor) {
		return invalid("actor must be user or assistant")
	}
	return s.tx(ctx, func(q queryer) error {
		e, err := getEvent(ctx, q, roots, id)
		if err != nil {
			return err
		}
		var rowid int64
		var extra string
		if err := q.QueryRowContext(ctx, `SELECT rowid, extra FROM events WHERE id = ?`, e.ID).Scan(&rowid, &extra); err != nil {
			return internal(err)
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO events_fts(events_fts, rowid, title, body) VALUES ('delete', ?, ?, ?)`, rowid, e.Title, e.Body); err != nil {
			return internal(err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, e.ID); err != nil {
			return internal(err)
		}
		// A checkpoint's text lives in two places. Blank the snapshot but keep its row so
		// version numbers stay monotonic.
		var meta struct {
			Version int `json:"checkpoint_version"`
		}
		fromJSON(extra, &meta)
		if meta.Version > 0 && e.Project != "" {
			tomb := memoryapi.Checkpoint{Project: e.Project, Scope: e.Scope, Version: meta.Version, Summary: "[forgotten]", UpdatedAt: e.RecordedAt}
			if _, err := q.ExecContext(ctx, `UPDATE checkpoints SET payload = ? WHERE project = ? AND version = ?`, toJSON(tomb), e.Project, meta.Version); err != nil {
				return internal(err)
			}
		}
		// Derived memories: drop those that rested only on this event, unlink the rest.
		rows, err := q.QueryContext(ctx, `SELECT id, evidence FROM memories WHERE evidence LIKE ?`, `%"`+e.ID+`"%`)
		if err != nil {
			return internal(err)
		}
		type derived struct {
			id       string
			evidence []string
		}
		var found []derived
		for rows.Next() {
			var d derived
			var ev string
			if err := rows.Scan(&d.id, &ev); err != nil {
				rows.Close()
				return internal(err)
			}
			fromJSON(ev, &d.evidence)
			found = append(found, d)
		}
		rows.Close()
		for _, d := range found {
			rest, orphaned := withoutEvidence(d.evidence, e.ID), policy.OrphanedAfterForget(d.evidence, e.ID)
			switch {
			case len(rest) == len(d.evidence):
				continue
			case orphaned:
				if err := deleteMemoryRow(ctx, q, d.id); err != nil {
					return err
				}
				if err := audit(ctx, q, "memory", d.id, actor, ms(s.now())); err != nil {
					return err
				}
			default:
				if _, err := q.ExecContext(ctx, `UPDATE memories SET evidence = ? WHERE id = ?`, toJSON(nonNilStr(rest)), d.id); err != nil {
					return internal(err)
				}
			}
		}
		return audit(ctx, q, "event", e.ID, actor, ms(s.now()))
	})
}
