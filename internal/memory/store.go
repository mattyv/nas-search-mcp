// Package memory is the shared-memory service's SQLite store. It is an unverified
// adapter: every decision (scope visibility, status transitions, checkpoint versions,
// context packing, initial status, confirmation, forget orphaning) is delegated to
// internal/policy. rules.go holds input-format checks and list helpers only.
package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

type Config struct {
	Path     string
	AcceptAt int // per-mille; 0 selects the defaults 900/100
	RejectAt int // per-mille; used as given unless AcceptAt is 0
	Now      func() time.Time
}

type Store struct {
	db       *sql.DB
	path     string
	acceptAt int
	rejectAt int
	now      func() time.Time
}

var _ memoryapi.Store = (*Store)(nil)

// The schema only ever gains tables. Every table carries `extra` so future fields never
// need a new column, which keeps the file usable by the previous release.
const schema = `
CREATE TABLE IF NOT EXISTS events (
  rowid INTEGER PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  idempotency_key TEXT,
  type TEXT NOT NULL, status TEXT NOT NULL,
  title TEXT NOT NULL, body TEXT NOT NULL DEFAULT '',
  project TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL,
  client TEXT NOT NULL DEFAULT '', actor TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
  occurred_at INTEGER NOT NULL, recorded_at INTEGER NOT NULL,
  sources TEXT NOT NULL DEFAULT '[]', related TEXT NOT NULL DEFAULT '[]', next_actions TEXT NOT NULL DEFAULT '[]',
  supersedes TEXT NOT NULL DEFAULT '', classified INTEGER NOT NULL DEFAULT 0,
  extra TEXT NOT NULL DEFAULT '{}',
  UNIQUE (scope, idempotency_key));
CREATE INDEX IF NOT EXISTS events_project_idx ON events(project, occurred_at);
CREATE INDEX IF NOT EXISTS events_scope_idx ON events(scope);
CREATE INDEX IF NOT EXISTS events_unclassified_idx ON events(classified, rowid);
CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(title, body, content='events', content_rowid='rowid');
CREATE TABLE IF NOT EXISTS checkpoints (
  project TEXT NOT NULL, scope TEXT NOT NULL, version INTEGER NOT NULL,
  payload TEXT NOT NULL, updated_at INTEGER NOT NULL, extra TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (scope, project, version));
CREATE TABLE IF NOT EXISTS memories (
  rowid INTEGER PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  category TEXT NOT NULL, statement TEXT NOT NULL,
  scope TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
  evidence TEXT NOT NULL DEFAULT '[]', probability INTEGER NOT NULL DEFAULT -1,
  model TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', replaced_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, extra TEXT NOT NULL DEFAULT '{}');
CREATE INDEX IF NOT EXISTS memories_project_idx ON memories(project, status);
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(statement, content='memories', content_rowid='rowid');
CREATE TABLE IF NOT EXISTS forget_audit (
  rowid INTEGER PRIMARY KEY,
  kind TEXT NOT NULL, target_id TEXT NOT NULL, actor TEXT NOT NULL, at INTEGER NOT NULL,
  extra TEXT NOT NULL DEFAULT '{}');`

func Open(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("memory: database path is required")
	}
	acceptAt, rejectAt := cfg.AcceptAt, cfg.RejectAt
	if acceptAt == 0 { // an accept threshold of 0 is invalid, so 0 means "use the defaults"
		acceptAt, rejectAt = 900, 100
	}
	if !policy.ValidThresholds(acceptAt, rejectAt) {
		return nil, fmt.Errorf("memory: thresholds must satisfy 0 <= reject < accept <= 1000 (got accept %d, reject %d)", acceptAt, rejectAt)
	}
	f, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("memory: create database: %w", err)
	}
	f.Close()
	if err := os.Chmod(cfg.Path, 0o600); err != nil {
		return nil, fmt.Errorf("memory: protect database: %w", err)
	}
	db, err := sql.Open("sqlite3", cfg.Path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`, `PRAGMA synchronous=NORMAL`} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: %s: %w", p, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: create tables: %w", err)
	}
	s := &Store{db: db, path: cfg.Path, acceptAt: acceptAt, rejectAt: rejectAt, now: cfg.Now}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// tx runs fn in one transaction. Inside fn only the transaction may be used: the
// database has a single connection, so touching s.db would deadlock.
func (s *Store) tx(ctx context.Context, fn func(q queryer) error) error {
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internal(err)
	}
	if err := fn(t); err != nil {
		t.Rollback()
		return err
	}
	if err := t.Commit(); err != nil {
		return internal(err)
	}
	return nil
}

func internal(err error) error { return fmt.Errorf("memory store: %w", err) }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", memoryapi.ErrInvalid, fmt.Sprintf(format, args...))
}

func newID(prefix string) string {
	var b [17]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("memory: no randomness: " + err.Error())
	}
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))[:26]
}

func ms(t time.Time) int64     { return t.UnixMilli() }
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
func toJSON(v any) string      { b, _ := json.Marshal(v); return string(b) }
func fromJSON(s string, v any) { _ = json.Unmarshal([]byte(s), v) }
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// visibleScopes returns the stored scopes of table that policy says roots may see.
func visibleScopes(ctx context.Context, q queryer, table string, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT scope FROM `+table)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sc string
		if err := rows.Scan(&sc); err != nil {
			return nil, internal(err)
		}
		if policy.ScopeVisible(sc, roots) {
			out = append(out, sc)
		}
	}
	return out, rows.Err()
}

func inList(vals []string) (string, []any) {
	marks := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		marks[i] = "?"
		args[i] = v
	}
	return "(" + strings.Join(marks, ",") + ")", args
}

// writeScope validates a scope for writing and enforces that it lies within roots.
func writeScope(scope string, roots []string) error {
	if !policy.ValidScope(scope) {
		return invalid("scope is not valid")
	}
	if !policy.ScopeVisible(scope, roots) {
		return fmt.Errorf("%w: scope is outside the permitted roots", memoryapi.ErrScope)
	}
	return nil
}

// ftsQuery turns free text into an AND of quoted terms, or "" when nothing searchable remains.
func ftsQuery(text string) string {
	var terms []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			terms = append(terms, `"`+string(cur)+`"`)
			cur = cur[:0]
		}
	}
	for _, r := range text {
		if r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 {
			cur = append(cur, r)
		} else {
			flush()
		}
		if len(terms) >= 12 {
			break
		}
	}
	flush()
	return strings.Join(terms, " ")
}
