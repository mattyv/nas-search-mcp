package oauth

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Passkey decisions come from the verified core through internal/policy; the
// wrappers keep the call sites short. passkeyName only formats a label.

const (
	passkeyCapacity = 10

	stepComplete       = "complete"        // password is enough
	stepAssert         = "assert"          // a registered passkey must sign in
	stepEnrollOptional = "enroll_optional" // offer enrollment; Skip allowed
	stepEnrollRequired = "enroll_required" // enrollment required before sign-in
)

func validPasskeyMode(mode string) bool { return policy.ValidPasskeyMode(mode) }

func passkeyStep(mode string, registered int) string { return policy.PasskeyStep(mode, registered) }

func passkeyRemovalAllowed(mode string, registered int) bool {
	return policy.PasskeyRemovalAllowed(mode, registered)
}

// passkeyName (an input format, not a decision) trims a label, defaults an empty one,
// and rejects labels that are too long or contain control characters.
func passkeyName(input string) (string, bool) {
	name := strings.TrimSpace(input)
	if name == "" {
		return "Passkey", true
	}
	if utf8.RuneCountInString(name) > 64 || !utf8.ValidString(name) {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}
