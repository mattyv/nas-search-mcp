package policy

import (
	core "github.com/mattyv/nas-search-mcp/generated/core/Core"
	"github.com/mattyv/nas-search-mcp/generated/core/dafny"
)

func str(s string) dafny.Sequence { return dafny.UnicodeSeqOfUtf8Bytes(s) }

// ValidScope accepts slash-separated lowercase segments such as "personal" or "work/acme".
func ValidScope(scope string) bool { return core.Companion_Default___.ValidScope(str(scope)) }

// ScopeVisible denies retrieval unless scope lies within one of roots at a segment boundary.
func ScopeVisible(scope string, roots []string) bool {
	values := make([]interface{}, len(roots))
	for i, root := range roots {
		values[i] = str(root)
	}
	return core.Companion_Default___.ScopeVisible(str(scope), dafny.SeqOf(values...))
}

// IsCurrentMemory reports whether a memory status may be presented as currently believed.
func IsCurrentMemory(status string) bool {
	return core.Companion_Default___.IsCurrentMemory(str(status))
}

// ValidMemoryTransition permits only the proved status lifecycle; terminal states are final.
func ValidMemoryTransition(from, to string) bool {
	return core.Companion_Default___.ValidMemoryTransition(str(from), str(to))
}

func ValidEventType(t string) bool   { return core.Companion_Default___.ValidEventType(str(t)) }
func ValidEventStatus(s string) bool { return core.Companion_Default___.ValidEventStatus(str(s)) }

// CheckpointWrite applies optimistic concurrency: it accepts only the version the writer read.
func CheckpointWrite(current, expected int) (bool, int) {
	if current < 0 {
		return false, current
	}
	result := core.Companion_Default___.CheckpointWrite(dafny.IntOf(current), dafny.IntOf(expected))
	return (*result.IndexInt(0)).(bool), (*result.IndexInt(1)).(dafny.Int).Int()
}

// PackBudget returns the indices of items kept, in priority order, within budget.
// A negative cost is treated as never fitting.
func PackBudget(costs []int, budget int) []int {
	if budget < 0 {
		budget = 0
	}
	values := make([]interface{}, len(costs))
	for i, cost := range costs {
		if cost < 0 {
			cost = budget + 1
		}
		values[i] = dafny.IntOf(cost)
	}
	seq := core.Companion_Default___.PackBudget(dafny.SeqOf(values...), dafny.IntOf(budget))
	out := make([]int, 0, seq.Cardinality())
	for i := uint32(0); i < seq.Cardinality(); i++ {
		out = append(out, seq.Select(i).(dafny.Int).Int())
	}
	return out
}

// RouteCandidate routes a per-mille probability to "accept", "reject" or "review".
func RouteCandidate(p, acceptAt, rejectAt int) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.RouteCandidate(dafny.IntOf(p), dafny.IntOf(acceptAt), dafny.IntOf(rejectAt)), false)
}

// InitialMemoryStatus accepts what a person attests and routes model proposals by probability.
func InitialMemoryStatus(userAttested bool, p, acceptAt, rejectAt int) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.InitialMemoryStatus(userAttested, dafny.IntOf(p), dafny.IntOf(acceptAt), dafny.IntOf(rejectAt)), false)
}

// PasskeyRequired fails closed for any mode other than "off" or a first "enroll".
func PasskeyRequired(mode string, registered int) bool {
	return core.Companion_Default___.PasskeyRequired(str(mode), dafny.IntOf(registered))
}

func CanRegisterPasskey(registered, capacity int) bool {
	return core.Companion_Default___.CanRegisterPasskey(dafny.IntOf(registered), dafny.IntOf(capacity))
}

// PasskeySignCountValid rejects a non-increasing counter unless the authenticator never counts.
func PasskeySignCountValid(stored, received int64) bool {
	return core.Companion_Default___.PasskeySignCountValid(dafny.IntOfInt64(stored), dafny.IntOfInt64(received))
}

// EditorAllowed admits only the configured Tailscale owner login.
func EditorAllowed(login, owner string) bool {
	return core.Companion_Default___.EditorAllowed(str(login), str(owner))
}

// AssistantMayTransition lets an assistant only raise a dispute.
func AssistantMayTransition(from, to string) bool {
	return core.Companion_Default___.AssistantMayTransition(str(from), str(to))
}

func ValidMemoryCategory(c string) bool { return core.Companion_Default___.ValidMemoryCategory(str(c)) }

// ProposesMemory reports whether an event type yields a memory candidate.
func ProposesMemory(eventType string) bool {
	return core.Companion_Default___.ProposesMemory(str(eventType))
}

func DefaultMemoryCategory(eventType string) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.DefaultMemoryCategory(str(eventType)), false)
}

// ExternalClassificationAllowed lets an entry leave the NAS only when a classifier is
// configured, the owner did not write it, and its scope is allowed out.
func ExternalClassificationAllowed(configured, userAttested bool, scope string, external []string) bool {
	values := make([]interface{}, len(external))
	for i, root := range external {
		values[i] = str(root)
	}
	return core.Companion_Default___.ExternalClassificationAllowed(configured, userAttested, str(scope), dafny.SeqOf(values...))
}

// DefaultEventStatus is the status for an event written without one; never "confirmed"
// for an assistant.
func DefaultEventStatus(actorIsUser bool, eventType string) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.DefaultEventStatus(actorIsUser, str(eventType)), false)
}

// EventStatusAllowed lets only the owner confirm an event.
func EventStatusAllowed(actorIsUser bool, status string) bool {
	return core.Companion_Default___.EventStatusAllowed(actorIsUser, str(status))
}

// IsUnconfirmedMemory reports statuses that await the owner's review.
func IsUnconfirmedMemory(status string) bool {
	return core.Companion_Default___.IsUnconfirmedMemory(str(status))
}

// ContextBudget applies the default and bounds for a context package's token budget.
func ContextBudget(requested int) int {
	return core.Companion_Default___.ContextBudget(dafny.IntOf(requested)).Int()
}

// OrphanedAfterForget reports whether a memory's only evidence was the forgotten event.
func OrphanedAfterForget(evidence []string, forgotten string) bool {
	values := make([]interface{}, len(evidence))
	for i, id := range evidence {
		values[i] = str(id)
	}
	return core.Companion_Default___.OrphanedAfterForget(dafny.SeqOf(values...), str(forgotten))
}

func ValidPasskeyMode(mode string) bool { return core.Companion_Default___.ValidPasskeyMode(str(mode)) }

// PasskeyStep decides what follows a correct password: "complete", "assert",
// "enroll_optional" or "enroll_required".
func PasskeyStep(mode string, registered int) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.PasskeyStep(str(mode), dafny.IntOf(registered)), false)
}

// PasskeyRemovalAllowed keeps required mode from removing its last passkey.
func PasskeyRemovalAllowed(mode string, registered int) bool {
	return core.Companion_Default___.PasskeyRemovalAllowed(str(mode), dafny.IntOf(registered))
}

// MaySupersede keeps an assistant from changing the status of an entry the owner confirmed.
func MaySupersede(newActorIsUser bool, oldStatus string) bool {
	return core.Companion_Default___.MaySupersede(newActorIsUser, str(oldStatus))
}

// SocketMethodAllowed: socket callers are never the owner, so they never forget.
func SocketMethodAllowed(name string) bool {
	return core.Companion_Default___.SocketMethodAllowed(str(name))
}

func ValidMemoryStatus(status string) bool {
	return core.Companion_Default___.ValidMemoryStatus(str(status))
}

func CorrectionMayCarryStatement(to string) bool {
	return core.Companion_Default___.CorrectionMayCarryStatement(str(to))
}

// AssistantMayAppendType allows every journal type except checkpoints.
func AssistantMayAppendType(t string) bool {
	return core.Companion_Default___.AssistantMayAppendType(str(t))
}

func DefaultPasskeyMode(issuerHostIsIP bool) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.DefaultPasskeyMode(issuerHostIsIP), false)
}

func ScopeWideContextCategory(category string) bool {
	return core.Companion_Default___.ScopeWideContextCategory(str(category))
}

func ValidThresholds(acceptAt, rejectAt int) bool {
	return core.Companion_Default___.ValidThresholds(dafny.IntOf(acceptAt), dafny.IntOf(rejectAt))
}

// memoryStatusNames is every status the adapters may enumerate; each must satisfy
// ValidMemoryStatus (checked by tests), and the verified predicates decide membership.
var memoryStatusNames = []string{"candidate", "accepted", "disputed", "rejected", "superseded", "retracted"}

// MemoryStatuses lists the valid statuses selected by a verified predicate, for SQL filters.
func MemoryStatuses(selected func(string) bool) []string {
	out := []string{}
	for _, s := range memoryStatusNames {
		if ValidMemoryStatus(s) && selected(s) {
			out = append(out, s)
		}
	}
	return out
}
