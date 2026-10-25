package app

import "github.com/mattyv/nas-search-mcp/internal/policy"

// SynologyPhotosAllowed requires explicit access to the whole Photos root. The
// native Photos API cannot enforce our path-level restrictions, so any denied
// photo subtree blocks this separate backend rather than widening that scope.
func (a *App) SynologyPhotosAllowed() bool {
	if a == nil {
		return false
	}
	a.accessMu.RLock()
	defer a.accessMu.RUnlock()
	return policy.SynologyPhotosAllowed(a.accessConfigured, a.allowed, a.denied)
}
