package policy

import (
	"math"
	"testing"
)

func TestPhotoPagePolicy(t *testing.T) {
	tests := []struct {
		name          string
		offset, limit int
		want          bool
	}{
		{"first single item", 0, 1, true},
		{"largest page at scan bound", 100000, 100, true},
		{"normal next page", 20, 20, true},
		{"negative offset", -1, 20, false},
		{"past scan bound", 100001, 20, false},
		{"zero page", 0, 0, false},
		{"negative page", 0, -1, false},
		{"oversized page", 0, 101, false},
		{"machine minimum offset", math.MinInt, 20, false},
		{"machine maximum offset", math.MaxInt, 20, false},
		{"machine maximum limit", 0, math.MaxInt, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidPhotoPage(test.offset, test.limit); got != test.want {
				t.Fatalf("page (%d, %d): got %v, want %v", test.offset, test.limit, got, test.want)
			}
		})
	}
}

func TestPhotoPersonPolicy(t *testing.T) {
	for _, id := range []int{1, 42, 2147483647} {
		if !ValidPhotoPerson(id) {
			t.Errorf("rejected supported person identifier %d", id)
		}
	}
	for _, id := range []int{math.MinInt, -1, 0} {
		if ValidPhotoPerson(id) {
			t.Errorf("accepted invalid person identifier %d", id)
		}
	}
	if uint64(math.MaxInt) > uint64(2147483647) {
		tooLarge := int64(2147483648)
		if ValidPhotoPerson(int(tooLarge)) || ValidPhotoPerson(math.MaxInt) {
			t.Error("accepted person identifier outside the supported 32-bit range")
		}
	}
}

func TestSynologyPhotosScopePolicy(t *testing.T) {
	tests := []struct {
		name        string
		configured  bool
		allow, deny []string
		want        bool
	}{
		{name: "unconfigured even with Photos", allow: []string{"Photos"}},
		{name: "empty deny all", configured: true},
		{name: "subfolder only", configured: true, allow: []string{"Photos/Family"}},
		{name: "similar root", configured: true, allow: []string{"Photos2"}},
		{name: "case sensitive", configured: true, allow: []string{"photos"}},
		{name: "exact grant", configured: true, allow: []string{"Photos"}, want: true},
		{name: "unrelated sources and denials", configured: true, allow: []string{"Code", "Photos"}, deny: []string{"Code", "Photos2"}, want: true},
		{name: "whole Photos denied", configured: true, allow: []string{"Photos"}, deny: []string{"Photos"}},
		{name: "photo child denied", configured: true, allow: []string{"Photos"}, deny: []string{"Code", "Photos/Family"}},
		{name: "deep photo child denied", configured: true, allow: []string{"Photos"}, deny: []string{"Photos/Family/Private"}},
		{name: "bare subtree prefix denied", configured: true, allow: []string{"Photos"}, deny: []string{"Photos/"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SynologyPhotosAllowed(test.configured, test.allow, test.deny); got != test.want {
				t.Fatalf("scope policy got %v, want %v", got, test.want)
			}
		})
	}
}
