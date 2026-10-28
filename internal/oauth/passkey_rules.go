package oauth

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Pure passkey decision rules that are not yet in core/Core.dfy.
// Each is an unverified Go stub; the integrator should move them into the
// Dafny core and bridge them through internal/policy.

const (
	passkeyCapacity = 10

	stepComplete       = "complete"        // password is enough
	stepAssert         = "assert"          // a registered passkey must sign in
	stepEnrollOptional = "enroll_optional" // offer enrollment; Skip allowed
	stepEnrollRequired = "enroll_required" // enrollment required before sign-in
)

// TODO(core): move to Dafny. validPasskeyMode accepts the three documented modes.
func validPasskeyMode(mode string) bool {
	return mode == "off" || mode == "enroll" || mode == "required"
}

// TODO(core): move to Dafny. passkeyStep decides what follows a correct password.
// It builds on the verified policy.PasskeyRequired rule; the enrollment cases
// are the new part: a first passkey may be offered (enroll) or forced (required).
func passkeyStep(mode string, registered int) string {
	if !policy.PasskeyRequired(mode, registered) {
		if mode == "enroll" {
			return stepEnrollOptional
		}
		return stepComplete
	}
	if registered <= 0 {
		return stepEnrollRequired
	}
	return stepAssert
}

// TODO(core): move to Dafny. passkeyRemovalAllowed keeps required mode from
// locking its own last way in.
func passkeyRemovalAllowed(mode string, registered int) bool {
	return registered >= 1 && !(mode == "required" && registered <= 1)
}

// TODO(core): move to Dafny. passkeyName trims a label, defaults an empty one,
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
