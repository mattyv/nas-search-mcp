package consolidate

import (
	"strings"
	"unicode"
)

// Text and number helpers for the consolidation adapter. Statement wording, duplicate
// normalisation and probability rounding are text processing, kept in Go and covered
// by tests; every accept, route, scope and privacy decision is in the verified core.

// unscored marks a candidate no classifier has scored (owner review).
const unscored = -1

// Two statements are duplicates when their normal forms are
// equal: lowercase, whitespace collapsed to single spaces, surrounding punctuation trimmed.
func normalizeStatement(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}

// Converts a model probability (0..1) to per-mille 0..1000,
// rounding to nearest.
func perMille(p float64) int {
	switch {
	case p != p, p <= 0:
		return 0
	case p >= 1:
		return 1000
	}
	return int(p*1000 + 0.5)
}
