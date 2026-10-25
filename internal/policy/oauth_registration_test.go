package policy

import (
	"math"
	"testing"
)

func TestOAuthRegistrationRequiresEveryCheckedCondition(t *testing.T) {
	for _, count := range []int{1, 2, 4} {
		if !ValidOAuthRegistration(count, true, true, true, true, true) {
			t.Errorf("rejected valid public registration with %d redirect URI(s)", count)
		}
	}
	for _, count := range []int{math.MinInt, -1, 0, 5, math.MaxInt} {
		if ValidOAuthRegistration(count, true, true, true, true, true) {
			t.Errorf("accepted unsupported redirect count %d", count)
		}
	}
	for _, invalid := range []struct {
		name                                       string
		public, grants, response, scope, redirects bool
	}{
		{"confidential auth", false, true, true, true, true},
		{"unsupported grants", true, false, true, true, true},
		{"unsupported response", true, true, false, true, true},
		{"unsupported scope", true, true, true, false, true},
		{"unsafe redirect", true, true, true, true, false},
		{"several invalid fields", false, false, false, false, false},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if ValidOAuthRegistration(1, invalid.public, invalid.grants, invalid.response, invalid.scope, invalid.redirects) {
				t.Fatal("accepted registration with a rejected checked condition")
			}
		})
	}
}

func TestOAuthClientCapacityRejectsFullOrInvalidState(t *testing.T) {
	for _, test := range []struct {
		existing, capacity int
		want               bool
	}{
		{0, 1, true},
		{0, 128, true},
		{127, 128, true},
		{1, 1, false},
		{128, 128, false},
		{-1, 128, false},
		{0, 0, false},
		{0, -1, false},
		{0, 129, false},
		{math.MaxInt, 128, false},
		{math.MinInt, 128, false},
	} {
		if got := CanRegisterOAuthClient(test.existing, test.capacity); got != test.want {
			t.Errorf("existing=%d capacity=%d: got %v, want %v", test.existing, test.capacity, got, test.want)
		}
	}
}
