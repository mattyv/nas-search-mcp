package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vecExt(t *testing.T) string {
	ext := os.Getenv("NAS_VEC_EXTENSION")
	if ext == "" {
		t.Skip("set NAS_VEC_EXTENSION")
	}
	return ext
}

func TestListPrefixRange(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "index.db"), "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, p := range []string{"a/x", "a/y", "a0/x", "b/x"} {
		if e = s.Replace(File{Path: p, Kind: "text"}, nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	all, e := s.List("", 10)
	if e != nil || len(all) != 4 {
		t.Fatal(all, e)
	}
	got, e := s.List("a/", 10)
	if e != nil || len(got) != 2 || got[0].Path != "a/x" || got[1].Path != "a/y" {
		t.Fatal(got, e)
	}
	if got, _ = s.List("a/", 1); len(got) != 1 {
		t.Fatal("limit", got)
	}
}

func TestLegacyWordsTableMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE chunks(id INTEGER PRIMARY KEY AUTOINCREMENT,path TEXT NOT NULL,location TEXT NOT NULL,text TEXT NOT NULL);
 CREATE VIRTUAL TABLE words USING fts5(text);
 INSERT INTO chunks(id,path,location,text) VALUES(1,'old/a.txt','l1','legacy zebra text');
 INSERT INTO words(rowid,text) VALUES(1,'legacy zebra text');`)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(path, "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c, e := s.Candidates("zebra", "", false, nil, 10)
	if e != nil || len(c) != 1 || c[0].Path != "old/a.txt" {
		t.Fatal(c, e)
	}
	var def string
	if e = s.db.QueryRow("SELECT sql FROM sqlite_master WHERE name='words'").Scan(&def); e != nil || !strings.Contains(def, "content=") {
		t.Fatal(def, e)
	}
	if e = s.Remove("old/a.txt"); e != nil {
		t.Fatal(e)
	}
	if c, e = s.Candidates("zebra", "", false, nil, 10); e != nil || len(c) != 0 {
		t.Fatal(c, e)
	}
}

func TestSemanticKNNOrderingAndPrefix(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "index.db"), vecExt(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	mk := func(x, y float32) []float32 { v := make([]float32, 384); v[0], v[1] = x, y; return v }
	add := func(p string, v []float32) {
		if e := s.Replace(File{Path: p, Kind: "text"}, []Chunk{{Text: "t"}}, [][]float32{v}); e != nil {
			t.Fatal(e)
		}
	}
	add("a/near", mk(1, 0.1))
	add("b/mid", mk(1, 1))
	add("a/far", mk(0, 1))
	got, e := s.Candidates("", "", false, mk(1, 0), 3)
	if e != nil || len(got) != 3 || got[0].Path != "a/near" || got[1].Path != "b/mid" || got[2].Path != "a/far" {
		t.Fatal(got, e)
	}
	for i, c := range got {
		if c.SemanticRank != i+1 || c.Distance <= 0 {
			t.Fatal("rank/distance", got)
		}
		if i > 0 && c.Distance < got[i-1].Distance {
			t.Fatal("not ordered", got)
		}
	}
	got, e = s.Candidates("", "a/", false, mk(1, 0), 2)
	if e != nil || len(got) != 2 || got[0].Path != "a/near" || got[1].Path != "a/far" || got[1].SemanticRank != 2 {
		t.Fatal(got, e)
	}
}
