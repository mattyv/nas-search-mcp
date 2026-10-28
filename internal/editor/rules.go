package editor

import (
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Input checks. These mirror limits the memory service enforces again; they exist so a
// person sees a friendly message before a write. They are not verified.

// Event titles are 1..200 characters (memoryapi.Event).
func validTitle(s string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	return n >= 1 && n <= 200
}

// Reasons, statements, and summaries must not be blank.
func nonBlank(s string) bool { return strings.TrimSpace(s) != "" }

// Idempotency keys: empty, or 1..128 of [A-Za-z0-9_.:-].
func validKey(s string) bool {
	if len(s) > 128 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == ':' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// noteTypes are the event types the New note form offers.
var noteTypes = []string{"manual_note", "decision", "observation", "action", "result", "exploration"}

// A note type must be offered by the form and valid for the store.
func validNoteType(t string) bool {
	for _, n := range noteTypes {
		if n == t {
			return policy.ValidEventType(t)
		}
	}
	return false
}

// Project names: empty, or 1..100 of [a-z0-9._-], the same format the store enforces.
func validProject(s string) bool {
	if len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// A note's scope must be well formed and inside the editor's roots (verified rules).
func (s *server) scopeAllowed(scope string) bool {
	return policy.ValidScope(scope) && policy.ScopeVisible(scope, s.roots)
}

// memoryAction is one status change the editor can offer.
type memoryAction struct {
	To             string
	Label          string
	NeedsStatement bool
}

var memoryActions = []memoryAction{
	{"accepted", "Accept", false},
	{"rejected", "Reject", false},
	{"retracted", "Retract", false},
	{"disputed", "Dispute", false},
	{"superseded", "Supersede with a new statement", true},
}

// actionsFor lists only the changes the verified lifecycle permits from status.
func actionsFor(status string) []memoryAction {
	var out []memoryAction
	for _, a := range memoryActions {
		if policy.ValidMemoryTransition(status, a.To) {
			out = append(out, a)
		}
	}
	return out
}

func findAction(to string) (memoryAction, bool) {
	for _, a := range memoryActions {
		if a.To == to {
			return a, true
		}
	}
	return memoryAction{}, false
}

// memoryStatuses lists every status the verified core accepts.
func memoryStatuses() []string { return policy.MemoryStatuses(policy.ValidMemoryStatus) }

// splitLines turns a textarea into one entry per non-blank line.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func joinLines(l []string) string { return strings.Join(l, "\n") }

// timeTag renders a UTC time that app.js shows in the device's local time.
func timeTag(t time.Time) template.HTML {
	if t.IsZero() {
		return "never"
	}
	u := t.UTC()
	return template.HTML(fmt.Sprintf(`<time datetime="%s">%s UTC</time>`, u.Format(time.RFC3339), html.EscapeString(u.Format("2006-01-02 15:04"))))
}

// percent shows a per-mille classifier score.
func percent(p int) string {
	if p < 0 {
		return "unscored"
	}
	return fmt.Sprintf("%d.%d%%", p/10, p%10)
}

func userCorrection(id, status, reason, statement string) memoryapi.Correction {
	return memoryapi.Correction{MemoryID: id, Status: status, Statement: statement, Reason: reason, Client: clientName, Actor: memoryapi.ActorUser}
}
