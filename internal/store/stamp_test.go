package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStampStoredAndUpdated(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "i.db"), "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Replace(File{Path: "a.txt", Fingerprint: "h", Stamp: "1:2", Kind: "document", Metadata: "m"}, nil, nil); e != nil {
		t.Fatal(e)
	}
	m, e := s.Indexed()
	if e != nil || m["a.txt"].Stamp != "1:2" || m["a.txt"].Fingerprint != "h" || m["a.txt"].Kind != "document" {
		t.Fatal(m, e)
	}
	if e = s.UpdateStamp("a.txt", "3:4"); e != nil {
		t.Fatal(e)
	}
	l, e := s.List("a", 1)
	if e != nil || len(l) != 1 || l[0].Stamp != "3:4" || l[0].Metadata != "m" {
		t.Fatal(l, e)
	}
	fp, e := s.Fingerprints()
	if e != nil || fp["a.txt"] != "h" {
		t.Fatal(fp, e)
	}
}

func TestOldDatabaseGainsStampColumn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.db")
	db, e := sql.Open("sqlite3", p)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE files(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,kind TEXT NOT NULL,metadata TEXT NOT NULL); INSERT INTO files VALUES('a','h','document','')`)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	for range 2 { // reopening must be idempotent
		s, e := New(p, "")
		if e != nil {
			t.Fatal(e)
		}
		m, e := s.Indexed()
		if e != nil || m["a"].Fingerprint != "h" || m["a"].Stamp != "" {
			t.Fatal(m, e)
		}
		s.Close()
	}
}
