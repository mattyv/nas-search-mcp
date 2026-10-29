package memory

import (
	"regexp"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Input-format checks, size bounds and list helpers for the storage adapter. Status
// defaults, who may confirm, review state, context budgets and forget orphaning are
// decided by the verified core through internal/policy.

var projectPattern = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)

// validProject: true iff p is empty (when allowEmpty) or 1..100 characters of [a-z0-9._-].
func validProject(p string, allowEmpty bool) bool {
	if p == "" {
		return allowEmpty
	}
	return projectPattern.MatchString(p)
}

// validLength: true iff min <= number of Unicode characters in s <= max.
func validLength(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= min && n <= max
}

// validActor: true iff a is "user" or "assistant".
func validActor(a string) bool { return a == memoryapi.ActorUser || a == memoryapi.ActorAssistant }

// memoryCategoryNames enumerates the categories for SQL filters; membership is decided by
// policy.ValidMemoryCategory and policy.ScopeWideContextCategory.
var memoryCategoryNames = []string{"fact", "preference", "decision", "constraint", "lesson"}

func scopeWideCategories() []string {
	out := []string{}
	for _, c := range memoryCategoryNames {
		if policy.ValidMemoryCategory(c) && policy.ScopeWideContextCategory(c) {
			out = append(out, c)
		}
	}
	return out
}

// itemCost: approximate token cost of an item whose JSON is n bytes: n/4 + 10.
func itemCost(n int) int { return n/4 + 10 }

// clampLimit: 0 or less -> def; above max -> max.
func clampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}

// withoutEvidence removes the forgotten id; whether the memory survives is decided by
// policy.OrphanedAfterForget.
func withoutEvidence(evidence []string, id string) []string {
	var rest []string
	for _, e := range evidence {
		if e != id {
			rest = append(rest, e)
		}
	}
	return rest
}

// appendNextActions merges lists in order, drops duplicates and empty strings, caps at max.
func mergeNextActions(max int, lists ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range lists {
		for _, a := range l {
			if a == "" || seen[a] {
				continue
			}
			if len(out) == max {
				return out
			}
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}
