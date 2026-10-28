package memory

import (
	"regexp"
	"strings"
	"unicode"
)

const redacted = "[redacted]"

// Best-effort removal of likely credentials before text reaches the database. It is a
// safety net, not a guarantee; it errs towards redacting.
var (
	pemBlock   = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	pemOpen    = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*`)
	tokenForms = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}|\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}|\bxox[bp]-[A-Za-z0-9-]{10,}|\bAKIA[0-9A-Z]{16}\b`)
	bearer     = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`)
	longRun    = regexp.MustCompile(`[A-Za-z0-9+/=_-]{40,}`)
)

func redact(s string) string {
	if s == "" {
		return s
	}
	s = pemBlock.ReplaceAllString(s, redacted)
	s = pemOpen.ReplaceAllString(s, redacted)
	s = tokenForms.ReplaceAllString(s, redacted)
	s = bearer.ReplaceAllString(s, redacted)
	return longRun.ReplaceAllStringFunc(s, func(run string) string {
		if looksLikeSecret(run) {
			return redacted
		}
		return run
	})
}

// looksLikeSecret is the generic rule for an unlabelled long token: at least 40 characters,
// mixed upper and lower case letters and digits, no path, URL or version punctuation, and
// not a hex digest. Commit SHAs, digests, URLs, paths, branch names and UUIDs fail it.
func looksLikeSecret(run string) bool {
	if len(run) < 40 || strings.ContainsAny(run, "/.:") {
		return false
	}
	var upper, lower, digit bool
	allHex := true
	for _, r := range run {
		switch {
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			allHex = false
		}
	}
	return upper && lower && digit && !allHex
}

func redactAll(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = redact(s)
	}
	return out
}
