package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceSearchRemove(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "index.db"), "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	f := File{Path: "/Docs/a.txt", Fingerprint: "one", Kind: "text"}
	if e = s.Replace(f, []Chunk{{Location: "line 1", Text: "hello renovation"}}, nil); e != nil {
		t.Fatal(e)
	}
	c, e := s.Candidates("renovation", "/Docs/", false, nil, 10)
	if e != nil || len(c) != 1 {
		t.Fatalf("%v %v", c, e)
	}
	c, e = s.Candidates("renovation", "/docs/", false, nil, 10)
	if e != nil || len(c) != 0 {
		t.Fatalf("case sensitive: %v %v", c, e)
	}
	if e = s.Replace(f, []Chunk{{Text: "replacement"}}, [][]float32{{1}}); e == nil {
		t.Fatal("invalid dimension accepted")
	}
	c, e = s.Candidates("renovation", "", false, nil, 10)
	if e != nil || len(c) != 1 {
		t.Fatal("failed replacement damaged original", c, e)
	}
	if e = s.Remove(f.Path); e != nil {
		t.Fatal(e)
	}
	c, e = s.Candidates("renovation", "", false, nil, 10)
	if e != nil || len(c) != 0 {
		t.Fatal(c, e)
	}
}

func TestVectorsReplacement(t *testing.T) {
	ext := os.Getenv("NAS_VEC_EXTENSION")
	if ext == "" {
		b, err := exec.Command("../../.venv/bin/python", "-c", "import sqlite_vec; print(sqlite_vec.loadable_path())").Output()
		if err != nil {
			t.Skip("sqlite-vec not installed; set NAS_VEC_EXTENSION to run vector integration tests")
		}
		ext = strings.TrimSpace(string(b))
	}
	s, e := New(filepath.Join(t.TempDir(), "index.db"), ext)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v := make([]float32, 384)
	v[0] = 1
	f := File{Path: "/docs/a", Fingerprint: "a", Kind: "text"}
	if e = s.Replace(f, []Chunk{{Text: "old"}}, [][]float32{v}); e != nil {
		t.Fatal(e)
	}
	c, e := s.Candidates("", "/docs/", false, v, 10)
	if e != nil || len(c) != 1 {
		t.Fatal(c, e)
	}
	if e = s.Replace(f, []Chunk{{Text: "new"}}, [][]float32{v}); e != nil {
		t.Fatal(e)
	}
	c, e = s.Candidates("", "/docs/", false, v, 10)
	if e != nil || len(c) != 1 || c[0].Text != "new" {
		t.Fatal(c, e)
	}
	c, e = s.Candidates("", "/DOCS/", false, v, 10)
	if e != nil || len(c) != 0 {
		t.Fatal(c, e)
	}
	if e = s.Remove(f.Path); e != nil {
		t.Fatal(e)
	}
	c, e = s.Candidates("", "", false, v, 10)
	if e != nil || len(c) != 0 {
		t.Fatal(c, e)
	}
}

func TestKeywordEscapingAndMetadata(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "index.db"), "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	f := File{Path: "/a", Fingerprint: "fp", Kind: "text", Metadata: `{"source":"test"}`}
	if e = s.Replace(f, []Chunk{{Text: "café naïve code"}}, nil); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"café", `" OR café`, "naïve:*"} {
		c, e := s.Candidates(q, "", false, nil, 10)
		if e != nil || len(c) != 1 {
			t.Fatal(q, c, e)
		}
	}
	fps, e := s.Fingerprints()
	if e != nil || fps["/a"] != "fp" {
		t.Fatal(fps, e)
	}
	files, e := s.List("/", 10)
	if e != nil || len(files) != 1 || files[0] != f {
		t.Fatal(files, e)
	}
	stats, e := s.Status()
	if e != nil || stats.Files != 1 || stats.Chunks != 1 || stats.Vectors {
		t.Fatal(stats, e)
	}
}

func TestStatusReportsAndClearsErrors(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "index.db"), "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.RecordError("a.txt", "bad document"); e != nil {
		t.Fatal(e)
	}
	st, e := s.Status()
	if e != nil || st.ErrorCount != 1 || len(st.Errors) != 1 || st.Errors[0].Path != "a.txt" {
		t.Fatal(st, e)
	}
	if e = s.Replace(File{Path: "a.txt"}, nil, nil); e != nil {
		t.Fatal(e)
	}
	st, e = s.Status()
	if e != nil || st.ErrorCount != 0 || len(st.Errors) != 0 {
		t.Fatal(st, e)
	}
}

func TestPhotoQueryDoesNotReturnDocuments(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "index.db"), "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, kind := range []string{"photo", "document"} {
		if e = s.Replace(File{Path: kind, Kind: kind}, []Chunk{{Text: "holiday beach"}}, nil); e != nil {
			t.Fatal(e)
		}
	}
	got, e := s.Candidates("holiday", "", true, nil, 10)
	if e != nil || len(got) != 1 || got[0].Path != "photo" {
		t.Fatal(got, e)
	}
}
