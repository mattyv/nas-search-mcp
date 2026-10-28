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

const eventCols = `e.id, e.idempotency_key, e.type, e.status, e.title, e.body, e.project, e.scope, e.client, e.actor,
 e.session_id, e.occurred_at, e.recorded_at, e.sources, e.related, e.next_actions, e.supersedes, e.classified`

func scanEvents(rows *sql.Rows) ([]memoryapi.Event, error) {
	defer rows.Close()
	out := []memoryapi.Event{}
	for rows.Next() {
		var e memoryapi.Event
		var key sql.NullString
		var occ, rec int64
		var sources, related, actions string
		var classified int
		if err := rows.Scan(&e.ID, &key, &e.Type, &e.Status, &e.Title, &e.Body, &e.Project, &e.Scope, &e.Client, &e.Actor,
			&e.SessionID, &occ, &rec, &sources, &related, &actions, &e.Supersedes, &classified); err != nil {
			return nil, internal(err)
		}
		e.IdempotencyKey = key.String
		e.OccurredAt, e.RecordedAt, e.Classified = fromMS(occ), fromMS(rec), classified != 0
		fromJSON(sources, &e.Sources)
		fromJSON(related, &e.Related)
		fromJSON(actions, &e.NextActions)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	return out, nil
}

// prepareEvent validates e, applies defaults and redacts secrets. It does not touch the database.
func (s *Store) prepareEvent(roots []string, e memoryapi.Event) (memoryapi.Event, error) {
	if !policy.ValidEventType(e.Type) {
		return e, invalid("unknown event type")
	}
	if !validActor(e.Actor) {
		return e, invalid("actor must be user or assistant")
	}
	if e.Status == "" {
		e.Status = policy.DefaultEventStatus(e.Actor == memoryapi.ActorUser, e.Type)
	}
	if !policy.ValidEventStatus(e.Status) {
		return e, invalid("unknown event status")
	}
	if !policy.EventStatusAllowed(e.Actor == memoryapi.ActorUser, e.Status) {
		return e, invalid("an assistant cannot write a confirmed event")
	}
	e.Title = strings.TrimSpace(redact(e.Title))
	e.Body = redact(e.Body)
	switch {
	case !validLength(e.Title, 1, 200):
		return e, invalid("title must be 1 to 200 characters")
	case !validLength(e.Body, 0, 20000):
		return e, invalid("body must be at most 20000 characters")
	case !validProject(e.Project, true):
		return e, invalid("project must be 1 to 100 characters of a-z 0-9 . _ -")
	case len(e.Sources) > 10:
		return e, invalid("at most 10 sources")
	case len(e.NextActions) > 20:
		return e, invalid("at most 20 next actions")
	case len(e.Related) > 20:
		return e, invalid("at most 20 related ids")
	case !validLength(e.IdempotencyKey, 0, 128):
		return e, invalid("idempotency key must be at most 128 characters")
	case !validLength(e.Client, 0, 100), !validLength(e.SessionID, 0, 200), !validLength(e.Supersedes, 0, 64):
		return e, invalid("client, session or supersedes value is too long")
	}
	for i, src := range e.Sources {
		if !validLength(src.Kind, 0, 50) || !validLength(src.Locator, 0, 1000) || !validLength(src.Excerpt, 0, 2000) {
			return e, invalid("source %d is too long", i)
		}
		e.Sources[i].Excerpt = redact(src.Excerpt)
	}
	e.NextActions = redactAll(e.NextActions)
	for i, a := range e.NextActions {
		if !validLength(a, 0, 300) {
			return e, invalid("next action %d is longer than 300 characters", i)
		}
	}
	for i, r := range e.Related {
		if !validLength(r, 0, 64) {
			return e, invalid("related id %d is too long", i)
		}
	}
	if err := writeScope(e.Scope, roots); err != nil {
		return e, err
	}
	now := s.now()
	e.RecordedAt = now
	if e.OccurredAt.IsZero() {
		e.OccurredAt = now
	}
	e.Classified = !policy.ProposesMemory(e.Type)
	return e, nil
}

func insertEvent(ctx context.Context, q queryer, e memoryapi.Event, extra string) (memoryapi.Event, error) {
	e.ID = newID("evt_")
	var key any
	if e.IdempotencyKey != "" {
		key = e.IdempotencyKey
	}
	res, err := q.ExecContext(ctx, `INSERT INTO events (id, idempotency_key, type, status, title, body, project, scope, client, actor,
 session_id, occurred_at, recorded_at, sources, related, next_actions, supersedes, classified, extra)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, key, e.Type, e.Status, e.Title, e.Body, e.Project, e.Scope, e.Client, e.Actor,
		e.SessionID, ms(e.OccurredAt), ms(e.RecordedAt), toJSON(nonNil(e.Sources)), toJSON(nonNilStr(e.Related)), toJSON(nonNilStr(e.NextActions)),
		e.Supersedes, boolInt(e.Classified), extra)
	if err != nil {
		return e, internal(err)
	}
	rowid, err := res.LastInsertId()
	if err != nil {
		return e, internal(err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO events_fts(rowid, title, body) VALUES (?,?,?)`, rowid, e.Title, e.Body); err != nil {
		return e, internal(err)
	}
	return e, nil
}

func nonNil(s []memoryapi.Source) []memoryapi.Source {
	if s == nil {
		return []memoryapi.Source{}
	}
	return s
}

func nonNilStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func getEvent(ctx context.Context, q queryer, roots []string, id string) (memoryapi.Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+eventCols+` FROM events e WHERE e.id = ?`, id)
	if err != nil {
		return memoryapi.Event{}, internal(err)
	}
	evs, err := scanEvents(rows)
	if err != nil {
		return memoryapi.Event{}, err
	}
	if len(evs) == 0 || !policy.ScopeVisible(evs[0].Scope, roots) {
		return memoryapi.Event{}, fmt.Errorf("%w: event", memoryapi.ErrNotFound)
	}
	return evs[0], nil
}

func (s *Store) AppendEvent(ctx context.Context, roots []string, e memoryapi.Event) (memoryapi.Event, bool, error) {
	e, err := s.prepareEvent(roots, e)
	if err != nil {
		return memoryapi.Event{}, false, err
	}
	var out memoryapi.Event
	created := false
	err = s.tx(ctx, func(q queryer) error {
		if e.IdempotencyKey != "" {
			rows, err := q.QueryContext(ctx, `SELECT `+eventCols+` FROM events e WHERE e.scope = ? AND e.idempotency_key = ?`, e.Scope, e.IdempotencyKey)
			if err != nil {
				return internal(err)
			}
			prev, err := scanEvents(rows)
			if err != nil {
				return err
			}
			if len(prev) > 0 {
				out = prev[0]
				return nil
			}
		}
		if e.Supersedes != "" {
			if old, err := getEvent(ctx, q, roots, e.Supersedes); err == nil {
				// The link is kept either way; only the status change is gated.
				if policy.MaySupersede(e.Actor == memoryapi.ActorUser, old.Status) {
					if _, err := q.ExecContext(ctx, `UPDATE events SET status = 'superseded' WHERE id = ?`, old.ID); err != nil {
						return internal(err)
					}
				}
			} else if !errors.Is(err, memoryapi.ErrNotFound) {
				return err
			}
		}
		stored, err := insertEvent(ctx, q, e, `{}`)
		if err != nil {
			return err
		}
		out, created = stored, true
		return nil
	})
	if err != nil {
		return memoryapi.Event{}, false, err
	}
	return out, created, nil
}

func (s *Store) GetEvent(ctx context.Context, roots []string, id string) (memoryapi.Event, error) {
	return getEvent(ctx, s.db, roots, id)
}

func (s *Store) SearchEvents(ctx context.Context, roots []string, query memoryapi.EventQuery) ([]memoryapi.Event, error) {
	scopes, err := visibleScopes(ctx, s.db, "events", roots)
	if err != nil || len(scopes) == 0 {
		return []memoryapi.Event{}, err
	}
	var where []string
	var args []any
	from := "events e"
	order := "e.occurred_at DESC, e.rowid DESC"
	if query.Text != "" {
		match := ftsQuery(query.Text)
		if match == "" {
			return []memoryapi.Event{}, nil
		}
		from = "events_fts JOIN events e ON e.rowid = events_fts.rowid"
		where = append(where, "events_fts MATCH ?")
		args = append(args, match)
		order = "events_fts.rank, e.rowid DESC"
	}
	list, a := inList(scopes)
	where = append(where, "e.scope IN "+list)
	args = append(args, a...)
	if query.Project != "" {
		where = append(where, "e.project = ?")
		args = append(args, query.Project)
	}
	if len(query.Types) > 0 {
		list, a := inList(query.Types)
		where = append(where, "e.type IN "+list)
		args = append(args, a...)
	}
	if !query.From.IsZero() {
		where = append(where, "e.occurred_at >= ?")
		args = append(args, ms(query.From))
	}
	if !query.To.IsZero() {
		where = append(where, "e.occurred_at < ?")
		args = append(args, ms(query.To))
	}
	args = append(args, clampLimit(query.Limit, 20, 100))
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventCols+` FROM `+from+` WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, internal(err)
	}
	return scanEvents(rows)
}

func (s *Store) UnclassifiedEvents(ctx context.Context, roots []string, limit int) ([]memoryapi.Event, error) {
	scopes, err := visibleScopes(ctx, s.db, "events", roots)
	if err != nil || len(scopes) == 0 {
		return []memoryapi.Event{}, err
	}
	list, args := inList(scopes)
	args = append(args, clampLimit(limit, 50, 500))
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventCols+` FROM events e WHERE e.classified = 0 AND e.scope IN `+list+` ORDER BY e.rowid ASC LIMIT ?`, args...)
	if err != nil {
		return nil, internal(err)
	}
	return scanEvents(rows)
}

func (s *Store) MarkClassified(ctx context.Context, roots []string, eventID string) error {
	return s.tx(ctx, func(q queryer) error {
		e, err := getEvent(ctx, q, roots, eventID)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE events SET classified = 1 WHERE id = ?`, e.ID); err != nil {
			return internal(err)
		}
		return nil
	})
}
