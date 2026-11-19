package policy

import "testing"

func TestVacuumPolicyBridgesCore(t *testing.T) {
	if !VacuumAction("dock") || VacuumAction("factory_reset") {
		t.Fatal("action allowlist mismatch")
	}
	if !VacuumNeedsRooms("clean_rooms") || VacuumNeedsRooms("start") {
		t.Fatal("room requirement mismatch")
	}
	if !ValidVacuumCommand("clean_rooms", []int{5, 3}, []int{3, 5}, 2, 3) {
		t.Fatal("valid room command rejected")
	}
	if ValidVacuumCommand("clean_rooms", []int{9}, []int{3, 5}, 1, -1) {
		t.Fatal("unknown room accepted")
	}
	if ValidVacuumCommand("clean_rooms", []int{3, 3}, []int{3}, 1, -1) {
		t.Fatal("duplicate room accepted")
	}
	if !ValidVacuumCommand("start", nil, nil, 0, -1) || ValidVacuumCommand("start", []int{3}, []int{3}, 0, -1) {
		t.Fatal("non-room command arguments mismatch")
	}
}

func TestVacuumZoneSpotAndSettingPolicyBridgesCore(t *testing.T) {
	bounds := [4]int{-5000, -5000, 5000, 5000}
	if !VacuumNeedsMap("clean_zones") || !VacuumNeedsMap("clean_spot") || VacuumNeedsMap("dock") {
		t.Fatal("map requirement mismatch")
	}
	if !ValidVacuumZones([][4]int{{0, 0, 1000, 1000}}, bounds, 1, -1) {
		t.Fatal("valid zone rejected")
	}
	if ValidVacuumZones([][4]int{{0, 0, 9000, 1000}}, bounds, 1, -1) || ValidVacuumZones(nil, bounds, 1, -1) {
		t.Fatal("zone outside map or empty zone list accepted")
	}
	if !ValidVacuumSpot(0, 0, bounds, 2, 3) || ValidVacuumSpot(0, 6000, bounds, 1, -1) {
		t.Fatal("spot bounds mismatch")
	}
	if !ValidVacuumSetting("volume", 40, 0, nil) || ValidVacuumSetting("volume", 400, 0, nil) {
		t.Fatal("volume range mismatch")
	}
	if !ValidVacuumSetting("room_passes", 2, 3, []int{3}) || ValidVacuumSetting("room_passes", 2, 4, []int{3}) {
		t.Fatal("room setting mismatch")
	}
	if !VacuumSettingNeedsRoom("room_mode") || VacuumSettingNeedsRoom("dnd") || ValidVacuumSetting("firmware", 1, 0, nil) {
		t.Fatal("setting allowlist mismatch")
	}
}

func TestVacuumDrivePolicyBridgesCore(t *testing.T) {
	bounds := [4]int{-5000, -5000, 5000, 5000}
	if !ValidVacuumGoTo(10, 10, bounds, 0, -1) || ValidVacuumGoTo(9000, 0, bounds, 0, -1) {
		t.Fatal("go-to bounds mismatch")
	}
	if !ValidVacuumMove(0, 200) || ValidVacuumMove(0, 0) || ValidVacuumMove(400, 0) {
		t.Fatal("move limits mismatch")
	}
	if !ValidVacuumObstacle(2, []int{2}) || ValidVacuumObstacle(3, []int{2}) {
		t.Fatal("obstacle check mismatch")
	}
}

func TestVacuumShortcutPolicyBridgesCore(t *testing.T) {
	if !ValidVacuumShortcut("start", 32, []int{32}, 0) || ValidVacuumShortcut("start", 33, []int{32}, 0) || !ValidVacuumShortcut("rename", 32, []int{32}, 5) {
		t.Fatal("shortcut rule mismatch")
	}
}

func TestVacuumOptionPolicyBridgesCore(t *testing.T) {
	options := []VacuumOption{{Name: "carpet_boost", Allowed: []int{0, 1}}, {Name: "drying", Lowest: 0, Highest: 180, Step: 15}}
	if !ValidVacuumOption("carpet_boost", 1, options) || ValidVacuumOption("carpet_boost", 5, options) {
		t.Fatal("allowed-list mismatch")
	}
	if !ValidVacuumOption("drying", 30, options) || ValidVacuumOption("drying", 31, options) || ValidVacuumOption("other", 0, options) {
		t.Fatal("range mismatch")
	}
}

func TestVacuumButtonPolicyBridgesCore(t *testing.T) {
	if !ValidVacuumButton("reset_filter", []string{"reset_filter"}) || ValidVacuumButton("x", []string{"reset_filter"}) {
		t.Fatal("button rule mismatch")
	}
}

func TestVacuumMapAndRoomPolicyBridgesCore(t *testing.T) {
	if !ValidVacuumMapAction("restore", 1, []int{1}, 0, 2, 3) || ValidVacuumMapAction("select", 9, []int{1}, 0, 0, 0) {
		t.Fatal("map rule mismatch")
	}
	if !ValidVacuumRoomAction("merge", []int{3, 5}, []int{3, 5}, 0, 0, nil, false) || ValidVacuumRoomAction("merge", []int{3}, []int{3, 5}, 0, 0, nil, false) || ValidVacuumRoomAction("split", []int{3}, []int{3}, 0, 0, nil, false) {
		t.Fatal("room rule mismatch")
	}
	if !ValidVacuumLine([4]int{0, 0, 10, 10}, [4]int{-50, -50, 50, 50}) || ValidVacuumLine([4]int{0, 0, 90, 0}, [4]int{-50, -50, 50, 50}) {
		t.Fatal("line rule mismatch")
	}
	if !ValidVacuumRoomOption(3, []int{3}, "x", 1, []VacuumOption{{Name: "x", Allowed: []int{1}}}) || ValidVacuumRoomOption(4, []int{3}, "x", 1, []VacuumOption{{Name: "x", Allowed: []int{1}}}) {
		t.Fatal("room option mismatch")
	}
}

func TestVacuumMapEditPolicyBridgesCore(t *testing.T) {
	bounds := [4]int{-50, -50, 50, 50}
	if !ValidVacuumMapEdit("no_go_zones", [][4]int{{0, 0, 10, 10}}, bounds) || ValidVacuumMapEdit("no_go_zones", [][4]int{{0, 0, 90, 10}}, bounds) {
		t.Fatal("map edit mismatch")
	}
	if !ValidVacuumPath([][2]int{{0, 0}}, bounds) || ValidVacuumPath(nil, bounds) {
		t.Fatal("path mismatch")
	}
}

func TestVacuumNoticePolicyBridgesCore(t *testing.T) {
	if !VacuumJobFinished("sweeping", "charging") || VacuumJobFinished("washing", "mopping") {
		t.Fatal("finished rule mismatch")
	}
	if !VacuumNewError("", "stuck") || VacuumNewError("stuck", "stuck") || !VacuumPartWorn(10, 9, 10) {
		t.Fatal("error or wear rule mismatch")
	}
}

func TestValidVacuumPlaceBridgesCore(t *testing.T) {
	bounds := [4]int{0, 0, 1000, 1000}
	if !ValidVacuumPlace("save", "Front door", nil, 10, 10, bounds) || ValidVacuumPlace("save", "Front door", nil, 2000, 10, bounds) {
		t.Fatal("save bounds")
	}
	if !ValidVacuumPlace("forget", "Front door", []string{"Front door"}, 0, 0, [4]int{}) || ValidVacuumPlace("forget", "Kitchen", []string{"Front door"}, 0, 0, [4]int{}) {
		t.Fatal("forget")
	}
	if ValidVacuumPlace("save", "tab\there", nil, 1, 1, bounds) || VacuumMaxPlaces != 30 {
		t.Fatal("name or limit")
	}
}

func TestVacuumPresenceActionBridgesCore(t *testing.T) {
	if VacuumPresenceAction("away", "charging", true, "", 90, false) != "start" ||
		VacuumPresenceAction("home", "sweeping", false, "", 60, true) != "dock" ||
		VacuumPresenceAction("home", "sweeping", false, "", 60, false) != "none" {
		t.Fatal("presence bridge")
	}
}
