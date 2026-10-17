package main

import (
	"testing"
	"time"
)

func TestParseScanFileDelay(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		valid bool
	}{
		{"0", 0, true},
		{"2", 2 * time.Second, true},
		{"60", time.Minute, true},
		{"-1", 0, false},
		{"NaN", 0, false},
		{"Inf", 0, false},
		{"9223372036854775807", 0, false},
	} {
		got, err := parseScanFileDelay(tc.value)
		if (err == nil) != tc.valid || (tc.valid && got != tc.want) {
			t.Errorf("%q: got %v, %v; want %v, valid=%v", tc.value, got, err, tc.want, tc.valid)
		}
	}
}
