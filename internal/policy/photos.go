package policy

import (
	core "github.com/mattyv/nas-search-mcp/generated/core/Core"
	"github.com/mattyv/nas-search-mcp/generated/core/dafny"
)

// ValidPhotoPage checks a bounded Photos scan window in the verified core.
// Callers apply their protocol defaults before invoking this function.
func ValidPhotoPage(offset, limit int) bool {
	return core.Companion_Default___.ValidPhotoPage(dafny.IntOf(offset), dafny.IntOf(limit))
}

// ValidPhotoPerson restricts person identifiers to the supported positive range.
func ValidPhotoPerson(id int) bool {
	return core.Companion_Default___.ValidPhotoPerson(dafny.IntOf(id))
}

// SynologyPhotosAllowed permits the native backend only for an explicit full
// Photos grant without any denied photo subtree. Inputs are already normalized
// by the access adapter; no path or permission is changed here.
func SynologyPhotosAllowed(configured bool, allow, deny []string) bool {
	allowValues := make([]interface{}, len(allow))
	for i, path := range allow {
		allowValues[i] = dafny.UnicodeSeqOfUtf8Bytes(path)
	}
	denyValues := make([]interface{}, len(deny))
	for i, path := range deny {
		denyValues[i] = dafny.UnicodeSeqOfUtf8Bytes(path)
	}
	return core.Companion_Default___.SynologyPhotosAllowed(configured, dafny.SeqOf(allowValues...), dafny.SeqOf(denyValues...))
}
