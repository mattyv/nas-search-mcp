package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSynologyPhotosRequiresWholeExplicitRootAndNoPhotoDenials(t *testing.T) {
	for _, test := range []struct {
		name        string
		configured  bool
		allow, deny []string
		want        bool
	}{
		{name: "unconfigured"},
		{name: "empty-deny-all", configured: true},
		{name: "other-source", configured: true, allow: []string{"Documents"}},
		{name: "subfolder-only", configured: true, allow: []string{"Photos/Family"}},
		{name: "similar-name", configured: true, allow: []string{"Photos2"}},
		{name: "case-sensitive-root", configured: true, allow: []string{"photos"}},
		{name: "exact-root", configured: true, allow: []string{"Photos"}, want: true},
		{name: "multiple-sources", configured: true, allow: []string{"Documents", "Photos"}, want: true},
		{name: "whole-root-denied", configured: true, allow: []string{"Photos"}, deny: []string{"Photos"}},
		{name: "nested-denial", configured: true, allow: []string{"Photos"}, deny: []string{"Photos/Family"}},
		{name: "deeper-denial", configured: true, allow: []string{"Photos"}, deny: []string{"Photos/Family/Private"}},
		{name: "unrelated-denial", configured: true, allow: []string{"Photos"}, deny: []string{"Code"}, want: true},
		{name: "similar-denial", configured: true, allow: []string{"Photos"}, deny: []string{"Photos2"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range append(append([]string{}, test.allow...), test.deny...) {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0700); err != nil {
					t.Fatal(err)
				}
			}
			a, err := New(root, t.TempDir(), "python3", "", "", false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if test.configured {
				if err := a.ConfigureAccess(test.allow, test.deny); err != nil {
					t.Fatal(err)
				}
			}
			if got := a.SynologyPhotosAllowed(); got != test.want {
				t.Fatalf("SynologyPhotosAllowed()=%v, want %v", got, test.want)
			}
		})
	}
}
