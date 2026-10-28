// Package store is an unverified SQLite boundary; formal properties apply to the core only.
package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	sqlite3 "github.com/mattn/go-sqlite3"
	"math"
	"strings"
	"sync/atomic"
	"unicode"
)

type File struct{ Path, Fingerprint, Stamp, Kind, Metadata string }
type Chunk struct{ Location, Text string }
type Candidate struct {
	ID                        int64
	Path, Location, Text      string
	LexicalRank, SemanticRank int
	Distance                  float64
}
type IndexError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}
type Stats struct {
	ErrorCount    int
	Errors        []IndexError
	Files, Chunks int
	Vectors       bool
}
type Store struct {
	db      *sql.DB
	vectors bool
	// externalWords is false for keyword indexes created by earlier releases; they are
	// left in place because rebuilding them on startup outlasts the installer's health wait.
	externalWords bool
	l2            map[string]bool // vector tables created without distance_metric=cosine (legacy)
}

var driverID atomic.Uint64

func New(path, extension string) (*Store, error) {
	name := fmt.Sprintf("nas-sqlite-%d", driverID.Add(1))
	sql.Register(name, &sqlite3.SQLiteDriver{ConnectHook: func(c *sqlite3.SQLiteConn) error {
		if extension != "" {
			return c.LoadExtension(extension, "sqlite3_vec_init")
		}
		return nil
	}})
	db, e := sql.Open(name, path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, vectors: extension != ""}
	schema := `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA cache_size=-8192;
 CREATE TABLE IF NOT EXISTS files(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,kind TEXT NOT NULL,metadata TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS chunks(id INTEGER PRIMARY KEY AUTOINCREMENT,path TEXT NOT NULL,location TEXT NOT NULL,text TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS chunk_path ON chunks(path);
 CREATE TABLE IF NOT EXISTS errors(path TEXT PRIMARY KEY,message TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS stamps(path TEXT PRIMARY KEY,stamp TEXT NOT NULL);`
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	if s.externalWords, e = ensureWords(db); e != nil {
		db.Close()
		return nil, e
	}
	if s.vectors {
		if _, e = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS text_vectors USING vec0(embedding float[384] distance_metric=cosine); CREATE VIRTUAL TABLE IF NOT EXISTS photo_vectors USING vec0(embedding float[512] distance_metric=cosine);`); e != nil {
			db.Close()
			return nil, e
		}
		// Databases created before distance_metric=cosine rank by L2. Embeddings are L2-normalised,
		// so the order is identical and cosine distance = L2^2/2; Candidates converts for them.
		s.l2 = map[string]bool{}
		for _, t := range []string{"text_vectors", "photo_vectors"} {
			var def string
			if db.QueryRow("SELECT sql FROM sqlite_master WHERE name=?", t).Scan(&def) == nil && !strings.Contains(def, "distance_metric=cosine") {
				s.l2[t] = true
			}
		}
	}
	return s, nil
}

// Rollback restores the previous program but never the database, so schema changes here
// must stay readable and writable by the previous release: add tables, never columns, and
// never rewrite an existing table on startup.

// ensureWords creates the keyword index for a new database as external content, which
// stores chunk text once. An existing index keeps its format; both behave identically.
func ensureWords(db *sql.DB) (bool, error) {
	var def string
	err := db.QueryRow("SELECT sql FROM sqlite_master WHERE name='words'").Scan(&def)
	if err == nil {
		return strings.Contains(def, "content="), nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	_, err = db.Exec("CREATE VIRTUAL TABLE words USING fts5(text, content='chunks', content_rowid='id')")
	return err == nil, err
}

// prefixRange returns bounds lo <= path < hi that match exactly the paths starting with prefix
// and let SQLite use the path indexes. An empty prefix matches everything.
func prefixRange(prefix string) (string, string) { return prefix, prefix + "\U0010FFFF" }
func (s *Store) Close() error                    { return s.db.Close() }
func vectorBlob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}
func validate(v []float32, n int) error {
	if len(v) != n {
		return fmt.Errorf("expected %d vector dimensions, got %d", n, len(v))
	}
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return errors.New("nonfinite vector")
		}
	}
	return nil
}
func (s *Store) clear(tx *sql.Tx, path string) error {
	// External-content FTS needs the old text to remove its index entries.
	deleteWords := "INSERT INTO words(words,rowid,text) SELECT 'delete',id,text FROM chunks WHERE path=?"
	if !s.externalWords {
		deleteWords = "DELETE FROM words WHERE rowid IN (SELECT id FROM chunks WHERE path=?)"
	}
	if _, e := tx.Exec(deleteWords, path); e != nil {
		return e
	}
	for _, table := range []string{"text_vectors", "photo_vectors"} {
		if !s.vectors {
			continue
		}
		if _, e := tx.Exec("DELETE FROM "+table+" WHERE rowid IN (SELECT id FROM chunks WHERE path=?)", path); e != nil {
			return e
		}
	}
	_, e := tx.Exec("DELETE FROM chunks WHERE path=?", path)
	return e
}
func (s *Store) Replace(f File, chunks []Chunk, vectors [][]float32) error {
	dim, table := 384, "text_vectors"
	if f.Kind == "photo" {
		dim, table = 512, "photo_vectors"
	}
	if vectors != nil {
		if len(vectors) != len(chunks) {
			return errors.New("chunk/vector count mismatch")
		}
		for _, v := range vectors {
			if e := validate(v, dim); e != nil {
				return e
			}
		}
		if !s.vectors {
			return errors.New("vector extension unavailable")
		}
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = s.clear(tx, f.Path); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT OR REPLACE INTO files(path,fingerprint,kind,metadata) VALUES(?,?,?,?)", f.Path, f.Fingerprint, f.Kind, f.Metadata); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM stamps WHERE path=?", f.Path); e != nil {
		return e
	}
	if f.Stamp != "" {
		if _, e = tx.Exec("INSERT INTO stamps VALUES(?,?)", f.Path, f.Stamp); e != nil {
			return e
		}
	}
	for i, c := range chunks {
		r, e := tx.Exec("INSERT INTO chunks(path,location,text) VALUES(?,?,?)", f.Path, c.Location, c.Text)
		if e != nil {
			return e
		}
		id, e := r.LastInsertId()
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO words(rowid,text) VALUES(?,?)", id, c.Text); e != nil {
			return e
		}
		if vectors != nil {
			if _, e = tx.Exec("INSERT INTO "+table+"(rowid,embedding) VALUES(?,?)", id, vectorBlob(vectors[i])); e != nil {
				return e
			}
		}
	}
	if _, e = tx.Exec("DELETE FROM errors WHERE path=?", f.Path); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Remove(path string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = s.clear(tx, path); e != nil {
		return e
	}
	for _, q := range []string{"DELETE FROM files WHERE path=?", "DELETE FROM stamps WHERE path=?"} {
		if _, e = tx.Exec(q, path); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("DELETE FROM errors WHERE path=?", path); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Fingerprints() (map[string]string, error) {
	m, e := s.Indexed()
	if e != nil {
		return nil, e
	}
	out := make(map[string]string, len(m))
	for p, f := range m {
		out[p] = f.Fingerprint
	}
	return out, nil
}

// Indexed returns every indexed file without metadata, to keep scans light.
func (s *Store) Indexed() (map[string]File, error) {
	rows, e := s.db.Query("SELECT f.path,f.fingerprint,coalesce(t.stamp,''),f.kind FROM files f LEFT JOIN stamps t ON t.path=f.path")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[string]File{}
	for rows.Next() {
		var f File
		if e = rows.Scan(&f.Path, &f.Fingerprint, &f.Stamp, &f.Kind); e != nil {
			return nil, e
		}
		m[f.Path] = f
	}
	return m, rows.Err()
}

// UpdateStamp records a new cheap stamp for an already indexed, unchanged file.
func (s *Store) UpdateStamp(path, stamp string) error {
	_, e := s.db.Exec("INSERT OR REPLACE INTO stamps SELECT path,? FROM files WHERE path=?", stamp, path)
	return e
}
func (s *Store) List(prefix string, limit int) ([]File, error) {
	lo, hi := prefixRange(prefix)
	rows, e := s.db.Query("SELECT f.path,f.fingerprint,coalesce(t.stamp,''),f.kind,f.metadata FROM files f LEFT JOIN stamps t ON t.path=f.path WHERE f.path>=? AND f.path<? ORDER BY f.path LIMIT ?", lo, hi, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		if e = rows.Scan(&f.Path, &f.Fingerprint, &f.Stamp, &f.Kind, &f.Metadata); e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) Status() (Stats, error) {
	st := Stats{Vectors: s.vectors, Errors: []IndexError{}}
	if e := s.db.QueryRow("SELECT count(*) FROM files").Scan(&st.Files); e != nil {
		return st, e
	}
	if e := s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&st.Chunks); e != nil {
		return st, e
	}
	if e := s.db.QueryRow("SELECT count(*) FROM errors").Scan(&st.ErrorCount); e != nil {
		return st, e
	}
	rows, e := s.db.Query("SELECT path,message FROM errors ORDER BY path LIMIT 100")
	if e != nil {
		return st, e
	}
	defer rows.Close()
	for rows.Next() {
		var item IndexError
		if e = rows.Scan(&item.Path, &item.Message); e != nil {
			return st, e
		}
		st.Errors = append(st.Errors, item)
	}
	return st, rows.Err()
}
func (s *Store) RecordError(path, message string) error {
	_, e := s.db.Exec("INSERT OR REPLACE INTO errors VALUES(?,?)", path, message)
	return e
}

// commonWords match nearly everything, so on their own they would rank unrelated files.
var commonWords = map[string]bool{"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true, "from": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "that": true, "the": true, "this": true, "to": true, "was": true, "with": true}

// keywordTerms drops common words unless the query has nothing else.
func keywordTerms(query string) []string {
	all := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var meaningful []string
	for _, term := range all {
		if !commonWords[strings.ToLower(term)] {
			meaningful = append(meaningful, term)
		}
	}
	if len(meaningful) == 0 {
		return all
	}
	return meaningful
}

func (s *Store) Candidates(query, prefix string, photos bool, vector []float32, limit int) ([]Candidate, error) {
	if limit <= 0 {
		return []Candidate{}, nil
	}
	out := []Candidate{}
	byID := map[int64]int{}
	lo, hi := prefixRange(prefix)
	terms := keywordTerms(query)
	for i, t := range terms {
		terms[i] = "\"" + t + "\""
	}
	if len(terms) > 0 {
		rows, e := s.db.Query(`SELECT c.id,c.path,c.location,c.text FROM words JOIN chunks c ON c.id=words.rowid WHERE words MATCH ? AND c.path>=? AND c.path<? AND (?=0 OR (SELECT kind FROM files WHERE path=c.path)='photo') ORDER BY bm25(words),c.id LIMIT ?`, strings.Join(terms, " OR "), lo, hi, photos, limit)
		if e != nil {
			return nil, e
		}
		rank := 0
		for rows.Next() {
			var c Candidate
			if e = rows.Scan(&c.ID, &c.Path, &c.Location, &c.Text); e != nil {
				rows.Close()
				return nil, e
			}
			rank++
			c.LexicalRank = rank
			byID[c.ID] = len(out)
			out = append(out, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if vector != nil {
		if !s.vectors {
			return nil, errors.New("vector extension unavailable")
		}
		dim, table := 384, "text_vectors"
		if photos {
			dim, table = 512, "photo_vectors"
		}
		if e := validate(vector, dim); e != nil {
			return nil, e
		}
		// Stage 1: KNN on the vector table alone. The prefix filter runs after ranking, so with a
		// prefix we ask for 4x neighbours (vec0 caps k at 4096); a narrow prefix inside a huge
		// index can therefore return fewer than limit semantic hits. photo_vectors holds only
		// photos, so no kind filter is needed here.
		k := limit
		if prefix != "" {
			k *= 4
		}
		if k > 4096 {
			k = 4096
		}
		nn, e := s.db.Query("SELECT rowid,distance FROM "+table+" WHERE embedding MATCH ? AND k=?", vectorBlob(vector), k)
		if e != nil {
			return nil, e
		}
		var ids []any
		dist := map[int64]float64{}
		var order []int64
		for nn.Next() {
			var id int64
			var d float64
			if e = nn.Scan(&id, &d); e != nil {
				nn.Close()
				return nil, e
			}
			if s.l2[table] {
				d = d * d / 2
			}
			ids, order, dist[id] = append(ids, id), append(order, id), d
		}
		e = nn.Err()
		nn.Close()
		if e != nil {
			return nil, e
		}
		// Stage 2: fetch chunks for those rowids only, filter by prefix, keep KNN order.
		found := map[int64]Candidate{}
		if len(ids) > 0 {
			rows, e := s.db.Query("SELECT id,path,location,text FROM chunks WHERE id IN (?"+strings.Repeat(",?", len(ids)-1)+") AND path>=? AND path<?", append(ids, lo, hi)...)
			if e != nil {
				return nil, e
			}
			for rows.Next() {
				var c Candidate
				if e = rows.Scan(&c.ID, &c.Path, &c.Location, &c.Text); e != nil {
					rows.Close()
					return nil, e
				}
				found[c.ID] = c
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
		}
		rank := 0
		for _, id := range order {
			c, ok := found[id]
			if !ok {
				continue
			}
			if rank++; rank > limit {
				break
			}
			c.Distance = dist[id]
			c.SemanticRank = rank
			if i, ok := byID[c.ID]; ok {
				out[i].SemanticRank = rank
				out[i].Distance = c.Distance
			} else {
				byID[c.ID] = len(out)
				out = append(out, c)
			}
		}
	}
	return out, nil
}
