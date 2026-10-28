package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// The installer rolls back the program, never the database, so every schema this
// release leaves behind must keep working for the previous release's own SQL.

const previousSchema = `PRAGMA journal_mode=WAL;
CREATE TABLE files(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,kind TEXT NOT NULL,metadata TEXT NOT NULL);
CREATE TABLE chunks(id INTEGER PRIMARY KEY AUTOINCREMENT,path TEXT NOT NULL,location TEXT NOT NULL,text TEXT NOT NULL);
CREATE INDEX chunk_path ON chunks(path);
CREATE VIRTUAL TABLE words USING fts5(text);
CREATE TABLE errors(path TEXT PRIMARY KEY,message TEXT NOT NULL);`

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	return db
}

// previousReplace is the previous release's Replace for a single text chunk, verbatim in effect.
func previousReplace(t *testing.T, db *sql.DB, path, text string) {
	t.Helper()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM words WHERE rowid IN (SELECT id FROM chunks WHERE path=?)", "DELETE FROM chunks WHERE path=?"} {
		if _, e = tx.Exec(q, path); e != nil {
			t.Fatalf("previous release clear: %v", e)
		}
	}
	if _, e = tx.Exec("INSERT OR REPLACE INTO files VALUES(?,?,?,?)", path, "h-"+text, "document", "{}"); e != nil {
		t.Fatalf("previous release file write: %v", e)
	}
	r, e := tx.Exec("INSERT INTO chunks(path,location,text) VALUES(?,?,?)", path, "text:chunk:1", text)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := r.LastInsertId()
	if _, e = tx.Exec("INSERT INTO words(rowid,text) VALUES(?,?)", id, text); e != nil {
		t.Fatalf("previous release keyword write: %v", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

func keywordPaths(t *testing.T, s *Store, query string) []string {
	t.Helper()
	c, e := s.Candidates(query, "", false, nil, 10)
	if e != nil {
		t.Fatal(e)
	}
	var out []string
	for _, x := range c {
		out = append(out, x.Path)
	}
	return out
}

func checkBothReleases(t *testing.T, path string) {
	t.Helper()
	s, e := New(path, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Replace(File{Path: "new.txt", Fingerprint: "n", Stamp: "1:2", Kind: "document", Metadata: "{}"}, []Chunk{{"text:chunk:1", "walnut cabinet"}}, nil); e != nil {
		t.Fatal(e)
	}
	if got := keywordPaths(t, s, "walnut"); len(got) != 1 || got[0] != "new.txt" {
		t.Fatalf("new release keyword search: %v", got)
	}
	s.Close()

	// Roll back: the previous release writes into the same database.
	db := rawDB(t, path)
	previousReplace(t, db, "old.txt", "granite benchtop")
	previousReplace(t, db, "old.txt", "marble benchtop")
	previousReplace(t, db, "new.txt", "oak cabinet")
	db.Close()

	// Upgrade again: the new release must read what the previous one wrote, with no stale terms.
	s, e = New(path, "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for query, want := range map[string]string{"marble": "old.txt", "oak": "new.txt", "granite": "", "walnut": ""} {
		got := keywordPaths(t, s, query)
		if want == "" && len(got) != 0 || want != "" && (len(got) != 1 || got[0] != want) {
			t.Fatalf("%q: got %v want %q", query, got, want)
		}
	}
	m, e := s.Indexed()
	if e != nil || m["old.txt"].Fingerprint != "h-marble benchtop" {
		t.Fatal(m, e)
	}
	// A stamp written before the rollback must not let a rewritten file be skipped.
	if m["new.txt"].Stamp != "1:2" || m["new.txt"].Fingerprint != "h-oak cabinet" {
		t.Fatal(m["new.txt"])
	}
}

func TestFreshIndexStaysUsableByPreviousRelease(t *testing.T) {
	checkBothReleases(t, filepath.Join(t.TempDir(), "fresh.db"))
}

func TestPreviousReleaseIndexOpensWithoutRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "previous.db")
	db := rawDB(t, path)
	if _, e := db.Exec(previousSchema); e != nil {
		t.Fatal(e)
	}
	previousReplace(t, db, "a.txt", "copper kettle")
	db.Close()
	s, e := New(path, "")
	if e != nil {
		t.Fatal(e)
	}
	var def string
	if e = s.db.QueryRow("SELECT sql FROM sqlite_master WHERE name='words'").Scan(&def); e != nil || strings.Contains(def, "content=") {
		t.Fatalf("opening must not rebuild the keyword index on startup: %q %v", def, e)
	}
	var cols int
	if e = s.db.QueryRow("SELECT count(*) FROM pragma_table_info('files')").Scan(&cols); e != nil || cols != 4 {
		t.Fatalf("files table must keep the previous release's columns: %d %v", cols, e)
	}
	if got := keywordPaths(t, s, "copper"); len(got) != 1 {
		t.Fatalf("existing keyword index unreadable: %v", got)
	}
	s.Close()
	checkBothReleases(t, path)
}

// A failed upgrade can leave a half-migrated index: stamp column added, keyword table rebuilt.
func TestHalfMigratedIndexStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "half.db")
	db := rawDB(t, path)
	if _, e := db.Exec(strings.Replace(previousSchema, "fts5(text)", "fts5(text, content='chunks', content_rowid='id')", 1) + `ALTER TABLE files ADD COLUMN stamp TEXT NOT NULL DEFAULT '';`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := New(path, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Replace(File{Path: "a.txt", Fingerprint: "a", Stamp: "5:6", Kind: "document", Metadata: "{}"}, []Chunk{{"text:chunk:1", "slate roof"}}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateStamp("a.txt", "7:8"); e != nil {
		t.Fatal(e)
	}
	m, e := s.Indexed()
	if e != nil || m["a.txt"].Stamp != "7:8" {
		t.Fatal(m, e)
	}
	if got := keywordPaths(t, s, "slate"); len(got) != 1 {
		t.Fatalf("keyword search: %v", got)
	}
	if e = s.Remove("a.txt"); e != nil {
		t.Fatal(e)
	}
	if got := keywordPaths(t, s, "slate"); len(got) != 0 {
		t.Fatalf("removed file still matches: %v", got)
	}
	s.Close()
}
