package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

const backupPrefix, backupSuffix = "memory-", ".sqlite3"

// Backup writes a consistent copy of the database into dir and keeps only the newest
// keep copies. SQLite's VACUUM INTO reads one transaction-consistent snapshot, so it is
// safe while the service is writing; the copy is renamed into place only when complete.
func (s *Store) Backup(dir string, keep int) (string, error) {
	if !policy.ValidBackupRetention(keep) {
		return "", fmt.Errorf("memory: keep between 1 and 90 backups (got %d)", keep)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("memory: create backup folder: %w", err)
	}
	name := backupPrefix + s.now().UTC().Format("20060102-150405") + backupSuffix
	final := filepath.Join(dir, name)
	partial := final + ".tmp"
	os.Remove(partial)
	if _, err := s.db.Exec("VACUUM INTO ?", partial); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("memory: write backup: %w", err)
	}
	if err := os.Chmod(partial, 0o600); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("memory: protect backup: %w", err)
	}
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("memory: finish backup: %w", err)
	}
	return final, pruneBackups(dir, keep)
}

// pruneBackups removes backups the verified retention rule does not keep. Only files
// named like backups are considered; anything else in the folder is left alone.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("memory: list backups: %w", err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix) {
			names = append(names, n)
		}
	}
	// Names embed a UTC timestamp, so reverse lexical order is newest first.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for rank, n := range names {
		if !policy.KeepBackup(rank, keep) {
			if err := os.Remove(filepath.Join(dir, n)); err != nil {
				return fmt.Errorf("memory: remove old backup: %w", err)
			}
		}
	}
	return nil
}
