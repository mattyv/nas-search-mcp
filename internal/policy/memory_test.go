package policy

import (
	"reflect"
	"testing"
)

func TestMemoryBridge(t *testing.T) {
	if !ValidScope("work/acme") || ValidScope("work//acme") || ValidScope("") {
		t.Fatal("scope validation")
	}
	if !ScopeVisible("personal/x", []string{"work/acme", "personal"}) || ScopeVisible("personal2", []string{"personal"}) || ScopeVisible("personal", nil) {
		t.Fatal("scope visibility")
	}
	if !IsCurrentMemory("accepted") || IsCurrentMemory("retracted") {
		t.Fatal("current memory")
	}
	if !ValidMemoryTransition("candidate", "accepted") || ValidMemoryTransition("retracted", "accepted") {
		t.Fatal("transitions")
	}
	if !ValidEventType("decision") || ValidEventType("chat") || !ValidEventStatus("unverified") || ValidEventStatus("accepted") {
		t.Fatal("event validation")
	}
	if ok, next := CheckpointWrite(3, 3); !ok || next != 4 {
		t.Fatal("checkpoint accept", ok, next)
	}
	if ok, next := CheckpointWrite(3, 2); ok || next != 3 {
		t.Fatal("checkpoint conflict", ok, next)
	}
	if got := PackBudget([]int{3, 9, 5}, 10); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatal("pack", got)
	}
	if got := PackBudget([]int{-1, 2}, 10); !reflect.DeepEqual(got, []int{1}) {
		t.Fatal("negative costs never fit", got)
	}
	if RouteCandidate(950, 900, 100) != "accept" || RouteCandidate(500, 900, 100) != "review" || RouteCandidate(5, 900, 100) != "reject" || RouteCandidate(950, 100, 900) != "review" {
		t.Fatal("route")
	}
	if InitialMemoryStatus(true, 0, 900, 100) != "accepted" || InitialMemoryStatus(false, 500, 900, 100) != "candidate" {
		t.Fatal("initial status")
	}
	if PasskeyRequired("off", 3) || PasskeyRequired("enroll", 0) || !PasskeyRequired("enroll", 1) || !PasskeyRequired("typo", 0) {
		t.Fatal("passkey required")
	}
	if !CanRegisterPasskey(9, 10) || CanRegisterPasskey(10, 10) {
		t.Fatal("passkey capacity")
	}
	if !PasskeySignCountValid(0, 0) || PasskeySignCountValid(5, 5) || !PasskeySignCountValid(5, 6) {
		t.Fatal("sign count")
	}
	if !EditorAllowed("a@b", "a@b") || EditorAllowed("", "") || EditorAllowed("x@b", "a@b") {
		t.Fatal("editor")
	}
}

func TestMemoryRoutingBridge(t *testing.T) {
	if InitialMemoryStatus(false, -1, 900, 100) != "candidate" {
		t.Fatal("unscored proposals must wait for review")
	}
	if !AssistantMayTransition("accepted", "disputed") || AssistantMayTransition("candidate", "accepted") {
		t.Fatal("assistant transitions")
	}
	if !ProposesMemory("decision") || ProposesMemory("discussion") || DefaultMemoryCategory("decision") != "decision" || DefaultMemoryCategory("observation") != "fact" {
		t.Fatal("proposal rules")
	}
	if !ValidMemoryCategory("lesson") || ValidMemoryCategory("rumour") {
		t.Fatal("categories")
	}
}

func TestExternalClassificationBridge(t *testing.T) {
	if !ExternalClassificationAllowed(true, false, "personal/x", []string{"personal"}) || ExternalClassificationAllowed(true, true, "personal/x", []string{"personal"}) || ExternalClassificationAllowed(true, false, "work/a", []string{"personal"}) || ExternalClassificationAllowed(false, false, "personal", []string{"personal"}) {
		t.Fatal("external classification gate")
	}
}

func TestEventStatusAndForgetBridge(t *testing.T) {
	if DefaultEventStatus(true, "decision") != "confirmed" || DefaultEventStatus(false, "exploration") != "proposed" || DefaultEventStatus(false, "action") != "unverified" {
		t.Fatal("default status")
	}
	if !EventStatusAllowed(true, "confirmed") || EventStatusAllowed(false, "confirmed") || EventStatusAllowed(true, "accepted") {
		t.Fatal("status allowed")
	}
	if !IsUnconfirmedMemory("disputed") || IsUnconfirmedMemory("accepted") {
		t.Fatal("unconfirmed")
	}
	if ContextBudget(0) != 1500 || ContextBudget(9000) != 8000 || ContextBudget(10) != 200 {
		t.Fatal("budget")
	}
	if !OrphanedAfterForget([]string{"a"}, "a") || OrphanedAfterForget([]string{"a", "b"}, "a") || OrphanedAfterForget(nil, "a") {
		t.Fatal("orphaned")
	}
}

func TestPasskeyStepBridge(t *testing.T) {
	if !ValidPasskeyMode("enroll") || ValidPasskeyMode("") {
		t.Fatal("modes")
	}
	if PasskeyStep("off", 2) != "complete" || PasskeyStep("enroll", 0) != "enroll_optional" || PasskeyStep("enroll", 1) != "assert" || PasskeyStep("required", 0) != "enroll_required" {
		t.Fatal("steps")
	}
	if PasskeyRemovalAllowed("required", 1) || !PasskeyRemovalAllowed("required", 2) || !PasskeyRemovalAllowed("enroll", 1) {
		t.Fatal("removal")
	}
}

func TestReviewFixBridge(t *testing.T) {
	if !MaySupersede(true, "confirmed") || MaySupersede(false, "confirmed") || !MaySupersede(false, "proposed") {
		t.Fatal("supersede")
	}
	if !SocketMethodAllowed("AppendEvent") || SocketMethodAllowed("ForgetEvent") || SocketMethodAllowed("ForgetMemory") {
		t.Fatal("socket methods")
	}
	if !ValidMemoryStatus("retracted") || ValidMemoryStatus("deleted") {
		t.Fatal("memory status")
	}
	if !CorrectionMayCarryStatement("superseded") || CorrectionMayCarryStatement("accepted") {
		t.Fatal("correction statement")
	}
	if !AssistantMayAppendType("decision") || AssistantMayAppendType("checkpoint") {
		t.Fatal("assistant types")
	}
	if DefaultPasskeyMode(true) != "off" || DefaultPasskeyMode(false) != "enroll" {
		t.Fatal("passkey default")
	}
	if !ScopeWideContextCategory("constraint") || ScopeWideContextCategory("fact") {
		t.Fatal("scope-wide categories")
	}
	if !ValidThresholds(900, 0) || ValidThresholds(100, 900) {
		t.Fatal("thresholds")
	}
	if got := MemoryStatuses(IsCurrentMemory); len(got) != 1 || got[0] != "accepted" {
		t.Fatal("current statuses", got)
	}
	if got := MemoryStatuses(func(s string) bool { return IsCurrentMemory(s) || IsUnconfirmedMemory(s) }); len(got) != 3 {
		t.Fatal("live statuses", got)
	}
}

func TestMemoryStatusNamesAreValid(t *testing.T) {
	if got := MemoryStatuses(func(string) bool { return true }); len(got) != len(memoryStatusNames) {
		t.Fatal("every enumerated status must be valid in the core", got)
	}
}

func TestBackupRetentionBridge(t *testing.T) {
	if !KeepBackup(0, 7) || !KeepBackup(6, 7) || KeepBackup(7, 7) || KeepBackup(0, 0) {
		t.Fatal("keep backup")
	}
	if !ValidBackupRetention(7) || ValidBackupRetention(0) || ValidBackupRetention(91) {
		t.Fatal("retention bounds")
	}
}

func TestPasskeyInviteBridge(t *testing.T) {
	if PasskeyStepInvited("required", 1, true) != "enroll_required" || PasskeyStepInvited("enroll", 2, true) != "enroll_required" {
		t.Fatal("an invite turns the passkey check into enrollment")
	}
	if PasskeyStepInvited("enroll", 1, false) != "assert" || PasskeyStepInvited("off", 1, true) != "complete" || PasskeyStepInvited("enroll", 0, true) != "enroll_optional" {
		t.Fatal("an invite changes nothing else")
	}
	if !PasskeyInviteUsable(false, 100, 101) || PasskeyInviteUsable(true, 100, 101) || PasskeyInviteUsable(false, 101, 101) {
		t.Fatal("usable")
	}
	if !ValidPasskeyInviteMinutes(15) || ValidPasskeyInviteMinutes(0) || ValidPasskeyInviteMinutes(61) {
		t.Fatal("minutes")
	}
}
