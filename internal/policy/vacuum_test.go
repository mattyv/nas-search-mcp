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
