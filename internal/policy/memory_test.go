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
