package app

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

type FolderResult struct {
	Folders   []string `json:"folders"`
	Truncated bool     `json:"truncated"`
	Warning   string   `json:"warning,omitempty"`
}

// ListFolders shows direct children of a live, authorized directory.
func (a *App) ListFolders(ctx context.Context, path string, limit int) (FolderResult, error) {
	out := FolderResult{Folders: []string{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if len(path) > 4096 || limit < 0 {
		return out, errors.New("invalid folder listing request")
	}
	if strings.HasPrefix(path, "/") {
		return out, errors.New("invalid relative folder path")
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "."
	}
	if path != "." && !policy.ValidRelative(path) {
		return out, errors.New("invalid relative folder path")
	}
	if !a.accessAllowed(path, true) {
		return out, errors.New("folder is not authorized")
	}
	if limit == 0 {
		limit = 20
	}
	limit = policy.ClampLimit(limit, 100)
	f, err := a.openNoSymlinks(path, true)
	if err != nil {
		return out, err
	}
	defer f.Close()
	deadline := time.Now().Add(5 * time.Second)
	visited := 0
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if time.Now().After(deadline) {
			out.Truncated, out.Warning = true, "Time limit reached; try a narrower folder"
			break
		}
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if time.Now().After(deadline) {
				out.Truncated, out.Warning = true, "Time limit reached; try a narrower folder"
				break
			}
			visited++
			if visited > 100000 {
				out.Truncated, out.Warning = true, "Entry limit reached; try a narrower folder"
				break
			}
			if policy.SourceComponentExcluded(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if !entry.IsDir() {
				continue
			}
			child := entry.Name()
			if path != "." {
				child = path + "/" + child
			}
			if !a.accessAllowed(child, true) {
				continue
			}
			opened, openErr := a.openNoSymlinks(child, true)
			if openErr != nil {
				continue
			}
			opened.Close()
			out.Folders = append(out.Folders, child)
			if len(out.Folders) >= limit {
				out.Truncated, out.Warning = true, "Result limit reached; additional folders may exist"
				break
			}
		}
		if out.Truncated || readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Truncated, out.Warning = true, "Some folders could not be listed"
			break
		}
	}
	sort.Strings(out.Folders)
	return out, nil
}
