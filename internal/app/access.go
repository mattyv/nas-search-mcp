package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattyv/nas-search-mcp/internal/policy"
	"golang.org/x/sys/unix"
)

// ConfigureAccess uses relative directory names; an empty allowlist grants no access.
// Call before serving or scanning. A rejected update preserves the previous policy.
func (a *App) ConfigureAccess(allow, deny []string) error {
	for _, paths := range [][]string{allow, deny} {
		for _, path := range paths {
			if !policy.ValidRelative(path) || filepath.ToSlash(filepath.Clean(path)) != path || hiddenOrExcluded(path) {
				return errors.New("access folders must be normalized relative paths")
			}
			f, err := a.openNoSymlinks(path, true)
			if err != nil {
				return fmt.Errorf("access folder is not an existing nonsymlink directory: %s", path)
			}
			f.Close()
		}
	}
	a.accessMu.Lock()
	defer a.accessMu.Unlock()
	a.accessConfigured = true
	a.allowed = append([]string{}, allow...)
	a.denied = append([]string{}, deny...)
	return nil
}

func within(path, folder string) bool { return path == folder || strings.HasPrefix(path, folder+"/") }
func hiddenOrExcluded(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ".") || excluded[part] {
			return true
		}
	}
	return false
}
func (a *App) accessAllowed(path string, directory bool) bool {
	a.accessMu.RLock()
	defer a.accessMu.RUnlock()
	if !a.accessConfigured {
		return true
	}
	if path != "." && hiddenOrExcluded(path) {
		return false
	}
	for _, deny := range a.denied {
		if within(path, deny) {
			return false
		}
	}
	for _, allow := range a.allowed {
		if within(path, allow) || directory && (path == "." || within(allow, path)) {
			return true
		}
	}
	return false
}

// Each component is opened relative to its pinned parent descriptor, never following symlinks.
func (a *App) openNoSymlinks(relative string, directory bool) (*os.File, error) {
	if relative != "." && !policy.ValidRelative(relative) {
		return nil, errors.New("invalid relative path")
	}
	fd, err := unix.Open(a.Root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("file root unavailable")
	}
	if relative == "." {
		return os.NewFile(uintptr(fd), a.Root), nil
	}
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, errors.New("path unavailable or symlink forbidden")
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Join(a.Root, filepath.FromSlash(relative))), nil
}

// Python receives a private bounded snapshot, so it cannot reopen a raced source path.
func (a *App) snapshot(relative string) (string, os.FileInfo, func(), error) {
	source, err := a.openFile(relative)
	if err != nil {
		return "", nil, nil, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", nil, nil, err
	}
	if info.Size() > sourceLimit {
		return "", nil, nil, errors.New("file exceeds 32 MiB source limit")
	}
	f, err := os.CreateTemp(a.Data, ".source-*"+filepath.Ext(relative))
	if err != nil {
		return "", nil, nil, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	n, err := io.Copy(f, io.LimitReader(source, sourceLimit+1))
	closeErr := f.Close()
	after, statErr := source.Stat()
	if err != nil || closeErr != nil || statErr != nil || n > sourceLimit || fileVersion(info) != fileVersion(after) {
		cleanup()
		return "", nil, nil, errors.New("file changed or unavailable while reading")
	}
	return f.Name(), info, cleanup, nil
}
