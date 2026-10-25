package policy

import (
	core "github.com/mattyv/nas-search-mcp/generated/core/Core"
	"github.com/mattyv/nas-search-mcp/generated/core/dafny"
)

// ValidOAuthRegistration requires the bounded redirect list and every checked
// protocol condition. URL parsing and the checks producing these flags remain
// responsibilities of the OAuth adapter.
func ValidOAuthRegistration(redirectCount int, publicAuth, grantsOK, responseOK, scopeOK, redirectsOK bool) bool {
	return core.Companion_Default___.ValidOAuthRegistration(dafny.IntOf(redirectCount), publicAuth, grantsOK, responseOK, scopeOK, redirectsOK)
}

// CanRegisterOAuthClient checks a single admission snapshot. Database atomicity
// must keep the real count and insert within one serialized operation.
func CanRegisterOAuthClient(existing, capacity int) bool {
	return core.Companion_Default___.CanRegisterOAuthClient(dafny.IntOf(existing), dafny.IntOf(capacity))
}
