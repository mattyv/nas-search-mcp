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

// VacuumNeedsMap reports whether action is aimed by map coordinates.
func VacuumNeedsMap(action string) bool {
	return core.Companion_Default___.VacuumNeedsMap(dafny.UnicodeSeqOfUtf8Bytes(action))
}

func box(values [4]int) dafny.Tuple {
	return dafny.TupleOf(dafny.IntOf(values[0]), dafny.IntOf(values[1]), dafny.IntOf(values[2]), dafny.IntOf(values[3]))
}

// ValidVacuumZones checks zones (x0, y0, x1, y1) against the map bounds.
func ValidVacuumZones(zones [][4]int, bounds [4]int, repeats, suction int) bool {
	values := make([]interface{}, len(zones))
	for i, zone := range zones {
		values[i] = box(zone)
	}
	return core.Companion_Default___.ValidVacuumZones(dafny.SeqOf(values...), box(bounds), dafny.IntOf(repeats), dafny.IntOf(suction))
}

// ValidVacuumSpot checks a spot-clean point against the map bounds.
func ValidVacuumSpot(x, y int, bounds [4]int, repeats, suction int) bool {
	return core.Companion_Default___.ValidVacuumSpot(dafny.IntOf(x), dafny.IntOf(y), box(bounds), dafny.IntOf(repeats), dafny.IntOf(suction))
}

// VacuumSettingNeedsRoom reports whether a setting applies to one room.
func VacuumSettingNeedsRoom(name string) bool {
	return core.Companion_Default___.VacuumSettingNeedsRoom(dafny.UnicodeSeqOfUtf8Bytes(name))
}

// ValidVacuumSetting checks a setting's value range and, for per-room
// settings, that room is one the vacuum reported (room 0 means whole vacuum).
func ValidVacuumSetting(name string, value, room int, known []int) bool {
	return core.Companion_Default___.ValidVacuumSetting(dafny.UnicodeSeqOfUtf8Bytes(name), dafny.IntOf(value), dafny.IntOf(room), intSeq(known))
}

// ValidVacuumHistoryLimit bounds how many recent cleaning runs one call returns.
func ValidVacuumHistoryLimit(limit int) bool {
	return core.Companion_Default___.ValidVacuumHistoryLimit(dafny.IntOf(limit))
}

// VacuumTargetsMatch checks that an action carries only its own kind of target.
func VacuumTargetsMatch(action string, rooms, zones int, point bool) bool {
	return core.Companion_Default___.VacuumTargetsMatch(dafny.UnicodeSeqOfUtf8Bytes(action), dafny.IntOf(rooms), dafny.IntOf(zones), point)
}

// ValidVacuumGoTo checks a drive-to point against the map bounds.
func ValidVacuumGoTo(x, y int, bounds [4]int, repeats, suction int) bool {
	return core.Companion_Default___.ValidVacuumGoTo(dafny.IntOf(x), dafny.IntOf(y), box(bounds), dafny.IntOf(repeats), dafny.IntOf(suction))
}

// ValidVacuumMove checks one remote-control step against the vendor limits.
func ValidVacuumMove(rotation, velocity int) bool {
	return core.Companion_Default___.ValidVacuumMove(dafny.IntOf(rotation), dafny.IntOf(velocity))
}

// ValidVacuumObstacle checks that a photo request names a reported obstacle.
func ValidVacuumObstacle(index int, known []int) bool {
	return core.Companion_Default___.ValidVacuumObstacle(dafny.IntOf(index), intSeq(known))
}

// ValidVacuumShortcut checks a shortcut action against the shortcuts the vacuum listed.
func ValidVacuumShortcut(action string, id int, known []int, nameLength int) bool {
	return core.Companion_Default___.ValidVacuumShortcut(dafny.UnicodeSeqOfUtf8Bytes(action), dafny.IntOf(id), intSeq(known), dafny.IntOf(nameLength))
}

// VacuumOption is one setting the vacuum reported: either a list of allowed
// values, or a range with a step.
type VacuumOption struct {
	Name                  string
	Lowest, Highest, Step int
	Allowed               []int
}

// ValidVacuumOption checks a setting change against what the vacuum reported.
func ValidVacuumOption(name string, value int, options []VacuumOption) bool {
	values := make([]interface{}, len(options))
	for i, o := range options {
		values[i] = dafny.TupleOf(dafny.UnicodeSeqOfUtf8Bytes(o.Name), dafny.IntOf(o.Lowest), dafny.IntOf(o.Highest), dafny.IntOf(o.Step), intSeq(o.Allowed))
	}
	return core.Companion_Default___.ValidVacuumOption(dafny.UnicodeSeqOfUtf8Bytes(name), dafny.IntOf(value), dafny.SeqOf(values...))
}

// ValidVacuumButton checks that a button press names a button the vacuum listed.
func ValidVacuumButton(name string, known []string) bool {
	values := make([]interface{}, len(known))
	for i, k := range known {
		values[i] = dafny.UnicodeSeqOfUtf8Bytes(k)
	}
	return core.Companion_Default___.ValidVacuumButton(dafny.UnicodeSeqOfUtf8Bytes(name), dafny.SeqOf(values...))
}

// ValidVacuumMapAction checks a map-management action against the maps the vacuum listed.
func ValidVacuumMapAction(action string, mapID int, known []int, nameLength, backup, backups int) bool {
	return core.Companion_Default___.ValidVacuumMapAction(dafny.UnicodeSeqOfUtf8Bytes(action), dafny.IntOf(mapID), intSeq(known), dafny.IntOf(nameLength), dafny.IntOf(backup), dafny.IntOf(backups))
}

// ValidVacuumRoomAction checks a room-management action against the reported rooms and room types.
func ValidVacuumRoomAction(action string, rooms, known []int, nameLength, roomType int, types []int, hasLine bool) bool {
	return core.Companion_Default___.ValidVacuumRoomAction(dafny.UnicodeSeqOfUtf8Bytes(action), intSeq(rooms), intSeq(known), dafny.IntOf(nameLength), dafny.IntOf(roomType), intSeq(types), hasLine)
}

// ValidVacuumLine checks a room-splitting line against the map bounds.
func ValidVacuumLine(line, bounds [4]int) bool {
	return core.Companion_Default___.ValidVacuumLine(box(line), box(bounds))
}

// ValidVacuumRoomOption checks a per-room setting for a reported room.
func ValidVacuumRoomOption(room int, known []int, name string, value int, options []VacuumOption) bool {
	values := make([]interface{}, len(options))
	for i, o := range options {
		values[i] = dafny.TupleOf(dafny.UnicodeSeqOfUtf8Bytes(o.Name), dafny.IntOf(o.Lowest), dafny.IntOf(o.Highest), dafny.IntOf(o.Step), intSeq(o.Allowed))
	}
	return core.Companion_Default___.ValidVacuumRoomOption(dafny.IntOf(room), intSeq(known), dafny.UnicodeSeqOfUtf8Bytes(name), dafny.IntOf(value), dafny.SeqOf(values...))
}

// ValidVacuumMapEdit checks a replacement list for one kind of map element.
func ValidVacuumMapEdit(element string, items [][4]int, bounds [4]int) bool {
	values := make([]interface{}, len(items))
	for i, item := range items {
		values[i] = box(item)
	}
	return core.Companion_Default___.ValidVacuumMapEdit(dafny.UnicodeSeqOfUtf8Bytes(element), dafny.SeqOf(values...), box(bounds))
}

// ValidVacuumPath checks a patrol path against the map bounds.
func ValidVacuumPath(points [][2]int, bounds [4]int) bool {
	values := make([]interface{}, len(points))
	for i, p := range points {
		values[i] = dafny.TupleOf(dafny.IntOf(p[0]), dafny.IntOf(p[1]))
	}
	return core.Companion_Default___.ValidVacuumPath(dafny.SeqOf(values...), box(bounds))
}

// VacuumJobFinished reports whether two readings show a cleaning job ending.
func VacuumJobFinished(previous, current string) bool {
	return core.Companion_Default___.VacuumJobFinished(dafny.UnicodeSeqOfUtf8Bytes(previous), dafny.UnicodeSeqOfUtf8Bytes(current))
}

// VacuumNewError reports whether an error appeared between two readings.
func VacuumNewError(previous, current string) bool {
	return core.Companion_Default___.VacuumNewError(dafny.UnicodeSeqOfUtf8Bytes(previous), dafny.UnicodeSeqOfUtf8Bytes(current))
}

// VacuumPartWorn reports whether a part's remaining life fell below threshold.
func VacuumPartWorn(previous, current, threshold int) bool {
	return core.Companion_Default___.VacuumPartWorn(dafny.IntOf(previous), dafny.IntOf(current), dafny.IntOf(threshold))
}
