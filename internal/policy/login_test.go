package policy

import "testing"

func TestLoginAttemptPolicy(t *testing.T) {
	for _, count := range []int{-1, 5, 6, 1000} {
		if LoginAttemptAllowed(count) {
			t.Fatalf("allowed invalid or exhausted count %d", count)
		}
	}
	for count := 0; count < 5; count++ {
		if !LoginAttemptAllowed(count) {
			t.Fatalf("rejected permitted count %d", count)
		}
		if next := FailedLoginCount(count); next != count+1 || next > 5 {
			t.Fatalf("invalid transition %d -> %d", count, next)
		}
	}
}
