package memory

import (
	"regexp"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
)

// Pure decision rules that have no verified counterpart in internal/policy yet. Each is
// a stub with exact semantics so it can move into core/Core.dfy unchanged.

var projectPattern = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)

// TODO(core): move to Dafny.
// validProject: true iff p is empty (when allowEmpty) or 1..100 characters of [a-z0-9._-].
func validProject(p string, allowEmpty bool) bool {
	if p == "" {
		return allowEmpty
	}
	return projectPattern.MatchString(p)
}

// TODO(core): move to Dafny.
// validLength: true iff min <= number of Unicode characters in s <= max.
func validLength(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= min && n <= max
}

// TODO(core): move to Dafny.
// validActor: true iff a is "user" or "assistant".
func validActor(a string) bool { return a == memoryapi.ActorUser || a == memoryapi.ActorAssistant }

// TODO(core): move to Dafny.
// defaultEventStatus: the status given to an event whose writer left it empty.
// user -> "confirmed"; assistant -> "proposed" for type "exploration", else "unverified".
func defaultEventStatus(actor, eventType string) string {
	if actor == memoryapi.ActorUser {
		return "confirmed"
	}
	if eventType == "exploration" {
		return "proposed"
	}
	return "unverified"
}

// TODO(core): move to Dafny.
// actorMayWriteEventStatus: an assistant may never write "confirmed"; a user may write any
// valid status.
func actorMayWriteEventStatus(actor, status string) bool {
	return !(actor == memoryapi.ActorAssistant && status == "confirmed")
}

// TODO(core): move to Dafny.
// isUnconfirmedMemory: statuses that await the owner's review: "candidate" and "disputed".
func isUnconfirmedMemory(status string) bool { return status == "candidate" || status == "disputed" }

// TODO(core): move to Dafny.
// isOpenCandidate: the status counted by Projects as awaiting review: "candidate".
func isOpenCandidate(status string) bool { return status == "candidate" }

// TODO(core): move to Dafny.
// contextBudget: 0 or less -> 1500; otherwise clamp to 200..8000.
func contextBudget(requested int) int {
	if requested <= 0 {
		return 1500
	}
	if requested < 200 {
		return 200
	}
	if requested > 8000 {
		return 8000
	}
	return requested
}

// TODO(core): move to Dafny.
// itemCost: approximate token cost of an item whose JSON is n bytes: n/4 + 10.
func itemCost(n int) int { return n/4 + 10 }

// TODO(core): move to Dafny.
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

// TODO(core): move to Dafny.
// evidenceAfterForget removes id from evidence. orphaned is true iff evidence was non-empty
// and nothing remains, meaning the memory was derived only from the forgotten item.
func evidenceAfterForget(evidence []string, id string) (rest []string, orphaned bool) {
	for _, e := range evidence {
		if e != id {
			rest = append(rest, e)
		}
	}
	return rest, len(evidence) > 0 && len(rest) == 0
}

// TODO(core): move to Dafny.
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
