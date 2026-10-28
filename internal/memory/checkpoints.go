package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

func (s *Store) prepareCheckpoint(c memoryapi.Checkpoint) (memoryapi.Checkpoint, error) {
	if !validProject(c.Project, false) {
		return c, invalid("project must be 1 to 100 characters of a-z 0-9 . _ -")
	}
	if !validActor(c.Actor) {
		return c, invalid("actor must be user or assistant")
	}
	c.Summary = redact(c.Summary)
	c.Goal = redact(c.Goal)
	c.Done, c.Decisions = redactAll(c.Done), redactAll(c.Decisions)
	c.Blockers, c.Next = redactAll(c.Blockers), redactAll(c.Next)
	switch {
	case !validLength(strings.TrimSpace(c.Summary), 1, 8000):
		return c, invalid("summary must be 1 to 8000 characters")
	case !validLength(c.Goal, 0, 500), !validLength(c.Branch, 0, 200), !validLength(c.Commit, 0, 100), !validLength(c.Client, 0, 100):
		return c, invalid("goal, branch, commit or client is too long")
	}
	for name, list := range map[string][]string{"done": c.Done, "decisions": c.Decisions, "blockers": c.Blockers, "next": c.Next} {
		if len(list) > 30 {
			return c, invalid("%s has more than 30 items", name)
		}
		for _, item := range list {
			if !validLength(item, 0, 300) {
				return c, invalid("an item in %s is longer than 300 characters", name)
			}
		}
	}
	return c, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func (s *Store) SaveCheckpoint(ctx context.Context, roots []string, c memoryapi.Checkpoint, expectedVersion int) (memoryapi.Checkpoint, error) {
	c, err := s.prepareCheckpoint(c)
	if err != nil {
		return memoryapi.Checkpoint{}, err
	}
	if err := writeScope(c.Scope, roots); err != nil {
		return memoryapi.Checkpoint{}, err
	}
	err = s.tx(ctx, func(q queryer) error {
		current := 0
		var lastScope string
		switch err := q.QueryRowContext(ctx, `SELECT version, scope FROM checkpoints WHERE project = ? ORDER BY version DESC LIMIT 1`, c.Project).Scan(&current, &lastScope); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return internal(err)
		case !policy.ScopeVisible(lastScope, roots):
			return fmt.Errorf("%w: project belongs to another scope", memoryapi.ErrScope)
		}
		ok, next := policy.CheckpointWrite(current, expectedVersion)
		if !ok {
			return fmt.Errorf("%w: current version is %d", memoryapi.ErrConflict, current)
		}
		c.Version = next
		c.UpdatedAt = s.now()
		if _, err := q.ExecContext(ctx, `INSERT INTO checkpoints (project, scope, version, payload, updated_at) VALUES (?,?,?,?,?)`,
			c.Project, c.Scope, c.Version, toJSON(c), ms(c.UpdatedAt)); err != nil {
			return internal(err)
		}
		heading := c.Goal
		if strings.TrimSpace(heading) == "" {
			heading = firstLine(c.Summary)
		}
		actions := c.Next
		if len(actions) > 20 {
			actions = actions[:20]
		}
		e, err := s.prepareEvent(roots, memoryapi.Event{
			Type: "checkpoint", Title: truncateRunes(fmt.Sprintf("Checkpoint v%d: %s", c.Version, heading), 200),
			Body: c.Summary, Project: c.Project, Scope: c.Scope, Client: c.Client, Actor: c.Actor,
			NextActions: actions, OccurredAt: c.UpdatedAt,
		})
		if err != nil {
			return err
		}
		_, err = insertEvent(ctx, q, e, fmt.Sprintf(`{"checkpoint_version":%d}`, c.Version))
		return err
	})
	if err != nil {
		return memoryapi.Checkpoint{}, err
	}
	return c, nil
}

func scanCheckpoints(rows *sql.Rows) ([]memoryapi.Checkpoint, error) {
	defer rows.Close()
	out := []memoryapi.Checkpoint{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, internal(err)
		}
		var c memoryapi.Checkpoint
		fromJSON(payload, &c)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	return out, nil
}

func (s *Store) checkpoints(ctx context.Context, roots []string, project string, limit int) ([]memoryapi.Checkpoint, error) {
	scopes, err := visibleScopes(ctx, s.db, "checkpoints", roots)
	if err != nil || len(scopes) == 0 {
		return []memoryapi.Checkpoint{}, err
	}
	list, args := inList(scopes)
	where := "scope IN " + list
	order := "version DESC"
	if project != "" {
		where += " AND project = ?"
		args = append(args, project)
	} else {
		order = "updated_at DESC, version DESC"
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM checkpoints WHERE `+where+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, internal(err)
	}
	return scanCheckpoints(rows)
}

func (s *Store) LatestCheckpoint(ctx context.Context, roots []string, project string) (*memoryapi.Checkpoint, error) {
	list, err := s.checkpoints(ctx, roots, project, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (s *Store) CheckpointHistory(ctx context.Context, roots []string, project string, limit int) ([]memoryapi.Checkpoint, error) {
	return s.checkpoints(ctx, roots, project, clampLimit(limit, 20, 100))
}
