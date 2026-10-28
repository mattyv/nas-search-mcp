package policy

import "testing"

func TestMatchesTerms(t *testing.T) {
	cases := []struct {
		text  string
		terms []string
		want  bool
	}{
		{"week 10/notes [a].pdf", []string{"notes", "week 10"}, true},
		{"week 10/notes [a].pdf", []string{"[a].", ".pdf"}, true},
		{"week 10/notes [a].pdf", []string{"notes", "absent"}, false},
		{"week 10/notes [a].pdf", []string{".*"}, false},
		{"中文🎵/notes", []string{"🎵", "中文"}, true},
		{"prepared lowercase", []string{"Lowercase"}, false},
		{"", nil, true}, {"anything", []string{""}, true},
		{"abc", []string{"abcd"}, false},
	}
	for _, c := range cases {
		if got := MatchesTerms(c.text, c.terms); got != c.want {
			t.Errorf("MatchesTerms(%q,%q)=%v want %v", c.text, c.terms, got, c.want)
		}
	}
}

func TestLinkPolicy(t *testing.T) {
	for mediaType, want := range map[string]bool{"application/pdf": true, "image/webp": true, "text/plain": true, "text/html": false, "image/svg+xml": false, "Application/PDF": false, "": false} {
		if got := ShowInline(mediaType); got != want {
			t.Errorf("ShowInline(%q)=%v want %v", mediaType, got, want)
		}
	}
	if !LinkActive(1, 2) || LinkActive(2, 2) || LinkActive(3, 2) {
		t.Fatal("link expiry boundary")
	}
}

func TestFingerprintBridge(t *testing.T) {
	if IndexedFingerprint("abc", true) != "abc;clip" || IndexedFingerprint("abc", false) != "abc" {
		t.Fatal("flavour bridge")
	}
	if !CanSkipUnchanged("1:2", "1:2", "abc;clip", true) || CanSkipUnchanged("1:2", "1:2", "abc", true) || CanSkipUnchanged("", "", "abc", false) {
		t.Fatal("skip bridge")
	}
}
