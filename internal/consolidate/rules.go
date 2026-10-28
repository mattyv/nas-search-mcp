package consolidate

import (
	"strings"
	"unicode"
)

// The functions in this file are pure decision rules that still live in Go.
// Each is marked for the verified core.

const (
	bodyExcerptChars = 400
	statementMax     = 1000
	// unscored marks a candidate no classifier has scored (owner review).
	unscored = -1
)

// TODO(core): move to Dafny. Candidate wording: the title, plus ": " and the first 400
// characters of the body when the body says more than the title; at most 1000 characters.
// Property to prove: result is non-empty when title is non-empty and never exceeds 1000.
func buildStatement(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	out := title
	if body != "" && !strings.Contains(normalizeStatement(title), normalizeStatement(body)) {
		out = title + ": " + firstRunes(body, bodyExcerptChars)
	}
	return firstRunes(out, statementMax)
}

// TODO(core): move to Dafny. Two statements are duplicates when their normal forms are
// equal: lowercase, whitespace collapsed to single spaces, surrounding punctuation trimmed.
// Property to prove: normalisation is idempotent.
func normalizeStatement(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}

// TODO(core): move to Dafny. Converts a model probability (0..1) to per-mille 0..1000,
// rounding to nearest. Property to prove: result is within [0,1000] and monotone.
func perMille(p float64) int {
	switch {
	case p != p, p <= 0:
		return 0
	case p >= 1:
		return 1000
	}
	return int(p*1000 + 0.5)
}

// TODO(core): move to Dafny. Keeps classifier scores inside the per-mille range.
func clampPerMille(pm int) int {
	if pm < 0 {
		return 0
	}
	if pm > 1000 {
		return 1000
	}
	return pm
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
