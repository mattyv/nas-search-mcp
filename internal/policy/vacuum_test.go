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
