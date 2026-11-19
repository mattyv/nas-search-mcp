package policy

import (
	core "github.com/mattyv/nas-search-mcp/generated/core/Core"
	"github.com/mattyv/nas-search-mcp/generated/core/dafny"
)

// VacuumAction reports whether the verified core allows forwarding action.
func VacuumAction(action string) bool {
	return core.Companion_Default___.VacuumAction(dafny.UnicodeSeqOfUtf8Bytes(action))
}

// VacuumNeedsRooms reports whether action targets specific rooms.
func VacuumNeedsRooms(action string) bool {
	return core.Companion_Default___.VacuumNeedsRooms(dafny.UnicodeSeqOfUtf8Bytes(action))
}

// ValidVacuumCommand checks a normalized command against the rooms the vacuum
// reported. suction -1 keeps the current setting; 0-3 is quiet..turbo.
func ValidVacuumCommand(action string, rooms, known []int, repeats, suction int) bool {
	return core.Companion_Default___.ValidVacuumCommand(dafny.UnicodeSeqOfUtf8Bytes(action), intSeq(rooms), intSeq(known), dafny.IntOf(repeats), dafny.IntOf(suction))
}

func intSeq(values []int) dafny.Sequence {
	out := make([]interface{}, len(values))
	for i, value := range values {
		out[i] = dafny.IntOf(value)
	}
	return dafny.SeqOf(out...)
}
