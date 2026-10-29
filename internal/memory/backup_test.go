package memory

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
)

func TestBackupWritesAConsistentCopyAndKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)
	s, err := Open(Config{Path: filepath.Join(dir, "memory.sqlite3"), Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.AppendEvent(context.Background(), []string{"personal"}, memoryapi.Event{Type: "decision", Title: "Beds are 40 cm deep", Scope: "personal", Client: "t", Actor: memoryapi.ActorUser}); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	keepMe := filepath.Join(backups, "notes.txt")
	os.WriteFile(keepMe, []byte("not a backup"), 0o600)

	var made []string
	for i := 0; i < 9; i++ {
		clock = clock.Add(24 * time.Hour)
		path, err := s.Backup(backups, 7)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, path)
	}
	entries, _ := filepath.Glob(filepath.Join(backups, "memory-*.sqlite3"))
	sort.Strings(entries)
	if len(entries) != 7 || entries[6] != made[8] || entries[0] != made[2] {
		t.Fatalf("expected the newest 7 backups, got %v", entries)
	}
	if _, err := os.Stat(keepMe); err != nil {
		t.Fatal("pruning touched a file that is not a backup")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(backups, "*.tmp")); len(leftovers) != 0 {
		t.Fatal("a temporary file was left behind", leftovers)
	}
	info, _ := os.Stat(made[8])
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup must be private, got %v", info.Mode().Perm())
	}
	copyDB, err := sql.Open("sqlite3", made[8]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var title string
	if err := copyDB.QueryRow("SELECT title FROM events").Scan(&title); err != nil || title != "Beds are 40 cm deep" {
		t.Fatal("backup does not hold the data", title, err)
	}
	if _, err := s.Backup(backups, 0); err == nil {
		t.Fatal("retention outside 1..90 must be refused")
	}
}
