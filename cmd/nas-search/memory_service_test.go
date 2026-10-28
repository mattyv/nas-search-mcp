package main

import "testing"

func TestMemoryThresholds(t *testing.T) {
	for name, c := range map[string]struct {
		accept, reject string
		ok             bool
		a, r           int
	}{
		"defaults":         {"", "", true, 900, 100},
		"custom":           {"800", "200", true, 800, 200},
		"edge":             {"1000", "0", true, 1000, 0},
		"equal":            {"500", "500", false, 0, 0},
		"reject above":     {"300", "600", false, 0, 0},
		"accept too large": {"1001", "100", false, 0, 0},
		"negative reject":  {"900", "-1", false, 0, 0},
		"not a number":     {"high", "100", false, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NAS_MEMORY_ACCEPT_AT", c.accept)
			t.Setenv("NAS_MEMORY_REJECT_AT", c.reject)
			a, r, err := memoryThresholds()
			if (err == nil) != c.ok || (c.ok && (a != c.a || r != c.r)) {
				t.Fatalf("got %d %d %v", a, r, err)
			}
		})
	}
}

func TestExternalClassificationScopes(t *testing.T) {
	got, err := externalScopes("")
	if err != nil || len(got) != 1 || got[0] != "personal" {
		t.Fatalf("default = %v %v", got, err)
	}
	got, err = externalScopes("none")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("none must mean no scope is sent out: %v %v", got, err)
	}
	got, err = externalScopes("personal, work/acme")
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %v %v", got, err)
	}
	for _, bad := range []string{"Personal", "work//x"} {
		if _, err := externalScopes(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
